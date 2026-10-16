package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/tgdrive/teldrive/v2/internal/api"
	"github.com/tgdrive/teldrive/v2/internal/authn"
)

const (
	accessCookieName  = "teldrive_access"
	refreshCookieName = "teldrive_refresh"
)

const (
	accessRefreshSkew = 10 * time.Second
	refreshTimeout    = 5 * time.Second
)

type sessionRefresher interface {
	RenewAccess(context.Context, string) (*authn.AccessRenewal, error)
}

type sessionRenewer struct {
	auth sessionRefresher
	now  func() time.Time
}

func sessionRenewalMiddleware(auth sessionRefresher, next http.Handler) http.Handler {
	if auth == nil {
		return next
	}
	return (&sessionRenewer{auth: auth, now: time.Now}).middleware(next)
}

func (m *sessionRenewer) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sessionRenewalSkipped(r) {
			next.ServeHTTP(w, r)
			return
		}
		refreshCookie, err := r.Cookie(refreshCookieName)
		if err != nil || strings.TrimSpace(refreshCookie.Value) == "" {
			next.ServeHTTP(w, r)
			return
		}
		accessCookie, accessErr := r.Cookie(accessCookieName)
		if accessErr == nil && !accessCookieNeedsRefresh(accessCookie.Value, m.now().UTC()) {
			next.ServeHTTP(w, r)
			return
		}

		renewal, err := m.renew(r.Context(), refreshCookie.Value)
		if err != nil {
			if errors.Is(err, authn.ErrInvalidCredential) || errors.Is(err, authn.ErrSessionNotFound) {
				clearSessionCookies(w, r)
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"code":    "session_refresh_unavailable",
					"message": "The session could not be renewed.",
				},
			})
			return
		}

		accessTTL := time.Duration(renewal.ExpiresIn) * time.Second
		access := sessionCookie(r, accessCookieName, renewal.AccessToken, accessTTL)
		http.SetCookie(w, access)
		replaceRequestCookie(r, access)
		next.ServeHTTP(w, r)
	})
}

func sessionRenewalSkipped(r *http.Request) bool {
	if r == nil || hasExplicitAPICredential(r) || !strings.HasPrefix(r.URL.Path, "/v1/") {
		return true
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/auth/") {
		return false
	}
	return r.URL.Path != "/v1/auth/logout" && r.URL.Path != "/v1/auth/cookie/logout"
}

func (m *sessionRenewer) renew(ctx context.Context, refreshToken string) (*authn.AccessRenewal, error) {
	refreshCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	return m.auth.RenewAccess(refreshCtx, refreshToken)
}

func accessCookieNeedsRefresh(raw string, now time.Time) bool {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 3 {
		return true
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return true
	}
	var claims struct {
		ExpiresAt int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.ExpiresAt <= 0 {
		return true
	}
	return !time.Unix(claims.ExpiresAt, 0).After(now.Add(accessRefreshSkew))
}

// sessionCookie builds the renewed access cookie.
//
// It honours the same "keep me signed in" choice the login did. Without that, a
// session-only login would quietly become a persistent one fifteen minutes
// later, when the access token is first renewed -- which is exactly the kind of
// silent reversal a security preference must not have.
func sessionCookie(r *http.Request, name, value string, ttl time.Duration) *http.Cookie {
	cookie := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	}
	if sessionPersistenceRequested(r) {
		cookie.MaxAge = int(ttl / time.Second)
		cookie.Expires = time.Now().UTC().Add(ttl)
	}
	return cookie
}

func clearSessionCookies(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{accessCookieName, refreshCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Path:     "/",
			MaxAge:   -1,
			Expires:  time.Unix(1, 0).UTC(),
			HttpOnly: true,
			Secure:   requestIsSecure(r),
			SameSite: http.SameSiteLaxMode,
		})
	}
}

func replaceRequestCookie(r *http.Request, replacement *http.Cookie) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	replaced := false
	for _, cookie := range cookies {
		if cookie.Name == replacement.Name {
			if replaced {
				continue
			}
			cookie.Value = replacement.Value
			replaced = true
		}
		r.AddCookie(cookie)
	}
	if !replaced {
		r.AddCookie(&http.Cookie{Name: replacement.Name, Value: replacement.Value})
	}
}

type requestSecurity struct {
	trustedProxies []netip.Prefix
}

func newRequestSecurity(values []string) (*requestSecurity, error) {
	security := &requestSecurity{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			address, addressErr := netip.ParseAddr(value)
			if addressErr != nil {
				return nil, err
			}
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		security.trustedProxies = append(security.trustedProxies, prefix)
	}
	return security, nil
}

func (s *requestSecurity) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secure := r.TLS != nil || (s.isTrustedProxy(r) && forwardedProto(r) == "https")
		ctx := context.WithValue(r.Context(), requestSecureContextKey{}, secure)
		ctx = api.WithCookieSecure(ctx, secure)
		ctx = api.WithSessionPersistence(ctx, sessionPersistenceRequested(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// sessionPersistenceHeader is how the login page says whether the session should
// outlive the browser. A header rather than a body field because the login
// handlers are generated from the TypeSpec contract, and one checkbox is not
// worth regenerating the API surface for.
const sessionPersistenceHeader = "X-Teldrive-Persist"

// sessionPersistenceRequested reads the "keep me signed in" choice.
//
// Only an explicit "session" turns persistence off. Anything else -- an absent
// header, a different client, a typo -- keeps the persistent default, because
// the failure mode of guessing wrong here is a user who has to sign in again
// with their phone, and the failure mode of the other guess is a cookie that
// outlives a browser they closed on purpose.
func sessionPersistenceRequested(r *http.Request) bool {
	value, _, _ := strings.Cut(r.Header.Get(sessionPersistenceHeader), ",")
	return !strings.EqualFold(strings.TrimSpace(value), "session")
}

type requestSecureContextKey struct{}

func requestIsSecure(r *http.Request) bool {
	secure, _ := r.Context().Value(requestSecureContextKey{}).(bool)
	return secure
}

func (s *requestSecurity) isTrustedProxy(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	address, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return false
	}
	for _, prefix := range s.trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func forwardedProto(r *http.Request) string {
	value, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.ToLower(strings.TrimSpace(value))
}

func browserCSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) || !hasSessionCookie(r) || hasExplicitAPICredential(r) || isSameOriginBrowserRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "csrf_rejected",
				"message": "The browser request did not originate from this Teldrive server.",
			},
		})
	})
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func hasSessionCookie(r *http.Request) bool {
	if r == nil {
		return false
	}
	if cookie, err := r.Cookie(accessCookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
		return true
	}
	if cookie, err := r.Cookie(refreshCookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
		return true
	}
	return false
}

func hasExplicitAPICredential(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.Header.Get("Authorization")) != "" || strings.TrimSpace(r.Header.Get("X-API-Key")) != ""
}

func isSameOriginBrowserRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "same-origin", "same-site", "none":
		return true
	case "cross-site":
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Host, r.Host) {
		return false
	}
	expectedScheme := "http"
	if requestIsSecure(r) {
		expectedScheme = "https"
	}
	return strings.EqualFold(parsed.Scheme, expectedScheme)
}
