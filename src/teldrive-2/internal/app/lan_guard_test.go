package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tgdrive/teldrive/v2/internal/lanaccess"
	"github.com/tgdrive/teldrive/v2/internal/principal"
)

func guardWithDevice(t *testing.T) (*networkGuard, *lanaccess.Store, string) {
	t.Helper()
	store := lanaccess.NewStore(lanaccess.DefaultPath(t.TempDir()), nil)
	if err := store.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	request, err := store.Request("Tablet", "192.168.1.9")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if _, err := store.Approve(request.ID, 42); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	_, secret, claimed, err := store.Claim(request.ID)
	if err != nil || !claimed {
		t.Fatalf("Claim: %v (claimed=%v)", err, claimed)
	}
	reached := false
	guard := &networkGuard{
		devices: store,
		logger:  nil,
		next: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusOK)
		}),
	}
	_ = reached
	return guard, store, secret
}

func guardRequest(guard *networkGuard, method, path, remote string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.RemoteAddr = remote
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	guard.ServeHTTP(recorder, request)
	return recorder
}

// TestGuardLetsTheMachineThrough: the owner's own browser is not a device on the
// network, and requiring it to hold a credential would lock them out of their own
// drive the moment they turned the feature on.
func TestGuardLetsTheMachineThrough(t *testing.T) {
	guard, _, _ := guardWithDevice(t)
	for _, remote := range []string{"127.0.0.1:5000", "[::1]:5000", "localhost:5000"} {
		recorder := guardRequest(guard, http.MethodGet, "/api/v1/me", remote, nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s -> %d, want 200", remote, recorder.Code)
		}
	}
}

// TestGuardRefusesAnUnapprovedDevice is the whole point of the feature.
func TestGuardRefusesAnUnapprovedDevice(t *testing.T) {
	guard, _, _ := guardWithDevice(t)
	for _, path := range []string{"/api/v1/me", "/api/v1/files", "/v1/me", "/webdav/folder/file.txt"} {
		recorder := guardRequest(guard, http.MethodGet, path, "192.168.1.50:4000", nil)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s -> %d, want 403", path, recorder.Code)
		}
		if code := errorCode(t, recorder); code != "device_not_approved" {
			t.Fatalf("%s: code = %q", path, code)
		}
	}
}

func TestGuardLetsAnApprovedDeviceThrough(t *testing.T) {
	guard, _, secret := guardWithDevice(t)
	// Every door a client might choose is accepted, so a device that picked the
	// API-key header is not refused for choosing the other one.
	for name, headers := range map[string]map[string]string{
		"device header":  {deviceHeader: secret},
		"api key header": {"X-Api-Key": secret},
		"bearer":         {"Authorization": "Bearer " + secret},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := guardRequest(guard, http.MethodGet, "/api/v1/me", "192.168.1.50:4000", headers)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body)
			}
		})
	}
}

func TestGuardRefusesAWrongCredential(t *testing.T) {
	guard, _, secret := guardWithDevice(t)
	for name, value := range map[string]string{
		"empty":     "",
		"nonsense":  "tdl_nope",
		"near miss": secret[:len(secret)-1] + "X",
	} {
		t.Run(name, func(t *testing.T) {
			recorder := guardRequest(guard, http.MethodGet, "/api/v1/me", "192.168.1.50:4000",
				map[string]string{deviceHeader: value})
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", recorder.Code)
			}
		})
	}
}

// TestGuardServesThePageToAnyone: a device that cannot load the page cannot ask
// for access, and an access control with no way in is one people turn off.
func TestGuardServesThePageToAnyone(t *testing.T) {
	guard, _, _ := guardWithDevice(t)
	for _, path := range []string{"/", "/login", "/assets/index.js", "/settings"} {
		recorder := guardRequest(guard, http.MethodGet, path, "192.168.1.50:4000", nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s -> %d, want 200", path, recorder.Code)
		}
	}
}

// TestGuardExemptsTheAccessEndpoints: asking for access cannot require access,
// and a device that is already approved still has to check whether it is.
func TestGuardExemptsTheAccessEndpoints(t *testing.T) {
	guard, _, _ := guardWithDevice(t)
	for _, path := range []string{lanRequestPath, lanRequestPrefix + "some-id"} {
		recorder := guardRequest(guard, http.MethodPost, path, "192.168.1.50:4000", nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s -> %d, want 200", path, recorder.Code)
		}
	}
}

// TestDeviceAuthenticatorBorrowsTheOwnersSession is the companion model: the
// device does not sign in to Telegram, it works as the machine does.
func TestDeviceAuthenticatorBorrowsTheOwnersSession(t *testing.T) {
	_, store, secret := guardWithDevice(t)
	wrapper := &deviceAuthenticator{inner: stubAuthenticator{}, devices: store}

	identity, err := wrapper.AuthenticateBearer(context.Background(), secret)
	if err != nil {
		t.Fatalf("AuthenticateBearer: %v", err)
	}
	if identity.UserID != 42 {
		t.Fatalf("user = %d, want the owner who approved", identity.UserID)
	}
	if len(identity.Roles) == 0 {
		t.Fatal("the borrowed identity has no roles")
	}
	// The API-key door accepts it too, so a client that picks the other header is
	// not silently refused.
	if _, err := wrapper.AuthenticateAPIKey(context.Background(), secret); err != nil {
		t.Fatalf("AuthenticateAPIKey: %v", err)
	}
}

// TestDeviceAuthenticatorPassesEverythingElseThrough: the browser session and
// the API keys must keep working exactly as before.
func TestDeviceAuthenticatorPassesEverythingElseThrough(t *testing.T) {
	_, store, _ := guardWithDevice(t)
	wrapper := &deviceAuthenticator{inner: stubAuthenticator{}, devices: store}

	identity, err := wrapper.AuthenticateBearer(context.Background(), "a-real-session-token")
	if err != nil {
		t.Fatalf("AuthenticateBearer: %v", err)
	}
	if identity.Source != "stub" {
		t.Fatalf("the inner authenticator was not used: %+v", identity)
	}
}

// TestDeviceAuthenticatorWithoutAnOwner: before anything is approved there is
// nobody to borrow a session from, which is a state rather than an error.
func TestDeviceAuthenticatorWithoutAnOwner(t *testing.T) {
	store := lanaccess.NewStore(lanaccess.DefaultPath(t.TempDir()), nil)
	if err := store.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	wrapper := &deviceAuthenticator{inner: stubAuthenticator{}, devices: store}
	if _, err := wrapper.AuthenticateBearer(context.Background(), "tdl_anything"); err != nil {
		t.Fatalf("a device credential with no owner must fall through, got %v", err)
	}
}

func TestIsGuardedPath(t *testing.T) {
	for _, path := range []string{"/api/v1/me", "/api/backup-config", "/v1/files", "/webdav", "/webdav/a"} {
		if !isGuardedPath(path) {
			t.Fatalf("isGuardedPath(%q) = false, want true", path)
		}
	}
	for _, path := range []string{"/", "/login", "/assets/x.js", lanRequestPath, lanPathPrefix + "devices"} {
		if isGuardedPath(path) {
			t.Fatalf("isGuardedPath(%q) = true, want false", path)
		}
	}
}

func TestIsLoopbackRequest(t *testing.T) {
	for name, remote := range map[string]string{
		"ipv4":            "127.0.0.1:1234",
		"ipv6":            "[::1]:1234",
		"localhost":       "localhost:1234",
		"ipv4 in ipv6":    "[::ffff:127.0.0.1]:1234",
		"lan not loopbac": "192.168.1.9:1234",
		"public":          "203.0.113.7:1234",
		"empty":           "",
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = remote
			want := name != "lan not loopbac" && name != "public" && name != "empty"
			if got := isLoopbackRequest(request); got != want {
				t.Fatalf("isLoopbackRequest(%q) = %v, want %v", remote, got, want)
			}
		})
	}
}

func TestWidenToNetwork(t *testing.T) {
	for name, testCase := range map[string]struct{ address, want string }{
		"loopback":       {"127.0.0.1:18787", "0.0.0.0:18787"},
		"localhost":      {"localhost:18787", "0.0.0.0:18787"},
		"ipv6 loopback":  {"[::1]:18787", "0.0.0.0:18787"},
		"wildcard":       {"0.0.0.0:18787", "0.0.0.0:18787"},
		"chosen address": {"192.168.1.5:18787", "192.168.1.5:18787"},
		"no port":        {"127.0.0.1", "127.0.0.1"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := widenToNetwork(testCase.address); got != testCase.want {
				t.Fatalf("widenToNetwork(%q) = %q, want %q", testCase.address, got, testCase.want)
			}
		})
	}
}

// stubAuthenticator stands in for the real one so the wrapper can be tested
// without a database.
type stubAuthenticator struct{}

func (stubAuthenticator) AuthenticateBearer(context.Context, string) (principal.Identity, error) {
	return principal.Identity{UserID: 7, Source: "stub"}, nil
}

func (stubAuthenticator) AuthenticateAPIKey(context.Context, string) (principal.Identity, error) {
	return principal.Identity{UserID: 7, Source: "stub"}, nil
}
