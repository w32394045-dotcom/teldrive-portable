package app

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/tgdrive/teldrive/v2/internal/lanaccess"
	"github.com/tgdrive/teldrive/v2/internal/principal"
)

// deviceHeader carries a network device's credential.
//
// It travels as an ordinary bearer token so it flows through the generated API's
// existing authentication rather than beside it: one path that decides who a
// caller is, not two that can disagree.
const deviceHeader = "X-Teldrive-Device"

// deviceAuthenticator lets an approved device act as the machine's own session.
//
// This is the companion model the feature is for: the phone on the sofa does not
// sign in to Telegram again -- it borrows the session the computer already has,
// because the person is standing next to that computer. That is also why
// approval is a strong act and why the credential is bound to one device rather
// than being a shared password.
//
// It wraps the real authenticator rather than replacing it: a credential this
// does not recognise is passed straight through, so the browser session and the
// API keys keep working exactly as before.
type deviceAuthenticator struct {
	inner   Authenticator
	devices *lanaccess.Store
	logger  *slog.Logger
}

// Authenticator is the slice of the generated API's authenticator this wraps.
type Authenticator interface {
	AuthenticateBearer(context.Context, string) (principal.Identity, error)
	AuthenticateAPIKey(context.Context, string) (principal.Identity, error)
}

func (a *deviceAuthenticator) AuthenticateBearer(ctx context.Context, token string) (principal.Identity, error) {
	if identity, ok := a.deviceIdentity(ctx, token); ok {
		return identity, nil
	}
	return a.inner.AuthenticateBearer(ctx, token)
}

func (a *deviceAuthenticator) AuthenticateAPIKey(ctx context.Context, token string) (principal.Identity, error) {
	// A device credential is accepted here too, so a client that sends it in the
	// API-key header is not silently refused for choosing the other door.
	if identity, ok := a.deviceIdentity(ctx, token); ok {
		return identity, nil
	}
	return a.inner.AuthenticateAPIKey(ctx, token)
}

func (a *deviceAuthenticator) deviceIdentity(_ context.Context, token string) (principal.Identity, bool) {
	if a == nil || a.devices == nil {
		return principal.Identity{}, false
	}
	owner := a.devices.OwnerUserID()
	if owner <= 0 {
		// Nothing has been approved, so there is nobody to borrow a session
		// from. This is the state before the first approval, not an error.
		return principal.Identity{}, false
	}
	device, ok := a.devices.Authenticate(token)
	if !ok {
		return principal.Identity{}, false
	}
	// The owner's roles, not the device's: the device has no roles of its own,
	// and the whole point is that it works as the machine does.
	identity := principal.Identity{
		UserID: owner,
		Roles:  []string{"owner"},
		Source: "lan-device:" + device.ID,
	}
	a.log().Info("lan.device_authenticated", "device", device.ID, "name", device.Name)
	return identity, true
}

// log never returns nil, for the same reason as the guard's.
func (a *deviceAuthenticator) log() *slog.Logger {
	if a == nil || a.logger == nil {
		return slog.Default()
	}
	return a.logger
}

// networkGuard refuses requests from other machines until their device is
// approved.
//
// Only the API surface is gated. The page itself is served, because a device
// that cannot load the page cannot ask for access -- and an access control with
// no way in is a control people turn off.
type networkGuard struct {
	devices *lanaccess.Store
	next    http.Handler
	logger  *slog.Logger
}

func (g *networkGuard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if g == nil || g.devices == nil {
		http.NotFound(w, r)
		return
	}
	// The machine's own browser is always allowed: it is not a device on the
	// network, and requiring it to hold a credential would lock the owner out of
	// their own drive the moment they turned the feature on.
	if isLoopbackRequest(r) {
		g.next.ServeHTTP(w, r)
		return
	}
	if !isGuardedPath(r.URL.Path) {
		g.next.ServeHTTP(w, r)
		return
	}
	if _, ok := g.devices.Authenticate(deviceCredential(r)); ok {
		g.next.ServeHTTP(w, r)
		return
	}
	g.log().Info("lan.request_refused", "path", r.URL.Path, "address", remoteHost(r))
	writeJSONMessage(w, http.StatusForbidden, "device_not_approved",
		"This drive is reachable on the network, but this device has not been approved. "+
			"Ask the person at the drive to approve it.")
}

// log never returns nil: the guard is the one place a request from the network
// is refused, and a guard that panics on a missing logger would refuse
// everything with a stack trace instead of an explanation.
func (g *networkGuard) log() *slog.Logger {
	if g == nil || g.logger == nil {
		return slog.Default()
	}
	return g.logger
}

// isGuardedPath reports whether a path needs an approved device.
//
// The access endpoints themselves are exempt: asking for access cannot require
// access, and a device that is already approved still has to be able to check
// whether it is.
func isGuardedPath(path string) bool {
	if strings.HasPrefix(path, lanPathPrefix) {
		return false
	}
	switch {
	case strings.HasPrefix(path, "/api/"), strings.HasPrefix(path, "/v1/"), strings.HasPrefix(path, "/webdav"):
		return true
	default:
		// Everything else is the page and its assets.
		return false
	}
}

// deviceCredential reads the credential from wherever the client put it.
//
// Both headers are accepted because the two doors into this application are a
// bearer token and an API key, and a device that picks the wrong one should not
// be told to try again with no explanation.
func deviceCredential(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get(deviceHeader)); value != "" {
		return value
	}
	if value := strings.TrimSpace(r.Header.Get("X-Api-Key")); value != "" {
		return value
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authorization) > 7 && strings.EqualFold(authorization[:7], "Bearer ") {
		return strings.TrimSpace(authorization[7:])
	}
	return ""
}

// isLoopbackRequest reports whether the request came from this machine.
//
// It reads the connection, not a forwarded header: the guard is what stands
// between the network and the drive, so a client must not be able to claim it is
// local. A reverse proxy in front of the drive is a different deployment, and
// the trusted-proxy list is what would make forwarded headers meaningful.
func isLoopbackRequest(r *http.Request) bool {
	host := remoteHost(r)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	if address.IsLoopback() {
		return true
	}
	// An IPv4-mapped IPv6 address is still the IPv4 address.
	return address.Is4In6() && address.Unmap().IsLoopback()
}

// normaliseRemoteHost strips the port and any IPv6 brackets.
func normaliseRemoteHost(remote string) string {
	remote = strings.TrimSpace(remote)
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return strings.Trim(remote, "[]")
	}
	return host
}

var _ Authenticator = (*deviceAuthenticator)(nil)
