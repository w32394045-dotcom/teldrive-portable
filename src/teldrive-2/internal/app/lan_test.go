package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tgdrive/teldrive/v2/internal/authn"
	"github.com/tgdrive/teldrive/v2/internal/lanaccess"
)

func newTestLANHandler(t *testing.T) *lanHandler {
	t.Helper()
	store := lanaccess.NewStore(lanaccess.DefaultPath(t.TempDir()), nil)
	if err := store.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	handler, err := newLANHandler(lanConfig{store: store, authService: &authn.Service{}})
	if err != nil {
		t.Fatalf("newLANHandler: %v", err)
	}
	return handler
}

func callLAN(t *testing.T, handler *lanHandler, method, path, body, remote string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if remote != "" {
		request.RemoteAddr = remote
	}
	recorder := httptest.NewRecorder()
	switch {
	case path == lanRequestPath:
		handler.serveRequest(recorder, request)
	case strings.HasPrefix(path, lanRequestPrefix):
		handler.serveClaim(recorder, request)
	case strings.HasPrefix(path, lanDevicePrefix):
		// The owner gate needs a session, so the decision methods are called
		// through the store in the tests below; here only the routing is checked.
		handler.serveDevice(recorder, request)
	default:
		handler.serveDevices(recorder, request)
	}
	return recorder
}

// TestLANRequestTakesTheAddressFromTheConnection is a small but load-bearing
// detail: the approval screen shows where the request came from, and a client
// must not be able to describe itself as something else.
func TestLANRequestTakesTheAddressFromTheConnection(t *testing.T) {
	handler := newTestLANHandler(t)
	recorder := callLAN(t, handler, http.MethodPost, lanRequestPath,
		`{"name":"TV","address":"10.0.0.1"}`, "192.168.1.44:51234")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s)", recorder.Code, recorder.Body)
	}
	var response lanRequestResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.State != string(lanaccess.StatePending) {
		t.Fatalf("state = %q", response.State)
	}
	if response.ID == "" || response.Message == "" {
		t.Fatalf("response = %+v", response)
	}
	var payload lanDevicesResponse
	devices := handler.store.Devices()
	if len(devices) != 1 {
		t.Fatalf("devices = %+v", devices)
	}
	if devices[0].Address != "192.168.1.44" {
		t.Fatalf("address = %q, want the connection's address", devices[0].Address)
	}
	_ = payload
}

func TestLANRequestRejectsOtherMethods(t *testing.T) {
	handler := newTestLANHandler(t)
	recorder := callLAN(t, handler, http.MethodGet, lanRequestPath, "", "192.168.1.1:1")
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
}

// TestLANClaimIsTheOnlyWayToGetACredential walks the flow a device actually
// walks: ask, poll while waiting, then collect.
func TestLANClaimIsTheOnlyWayToGetACredential(t *testing.T) {
	handler := newTestLANHandler(t)
	created := callLAN(t, handler, http.MethodPost, lanRequestPath, `{"name":"Tablet"}`, "192.168.1.9:4000")
	var request lanRequestResponse
	if err := json.Unmarshal(created.Body.Bytes(), &request); err != nil {
		t.Fatalf("decode: %v", err)
	}
	pollPath := lanRequestPrefix + request.ID

	// While it waits, the poll says so and hands over nothing.
	waiting := callLAN(t, handler, http.MethodGet, pollPath, "", "")
	var waitingResponse lanClaimResponse
	if err := json.Unmarshal(waiting.Body.Bytes(), &waitingResponse); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if waitingResponse.State != string(lanaccess.StatePending) || waitingResponse.Token != "" {
		t.Fatalf("waiting = %+v", waitingResponse)
	}

	// The person approves, and the device collects exactly once.
	if _, err := handler.store.Approve(request.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	first := callLAN(t, handler, http.MethodGet, pollPath, "", "")
	var claimed lanClaimResponse
	if err := json.Unmarshal(first.Body.Bytes(), &claimed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if claimed.State != string(lanaccess.StateApproved) || claimed.Token == "" {
		t.Fatalf("claimed = %+v", claimed)
	}
	if _, ok := handler.store.Authenticate(claimed.Token); !ok {
		t.Fatal("the collected credential does not authenticate")
	}

	second := callLAN(t, handler, http.MethodGet, pollPath, "", "")
	var again lanClaimResponse
	if err := json.Unmarshal(second.Body.Bytes(), &again); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if again.Token != "" {
		t.Fatal("the credential was handed over twice")
	}
	if again.State != string(lanaccess.StateApproved) {
		t.Fatalf("state = %q", again.State)
	}
}

func TestLANClaimReportsARefusal(t *testing.T) {
	handler := newTestLANHandler(t)
	created := callLAN(t, handler, http.MethodPost, lanRequestPath, `{"name":"Unknown"}`, "192.168.1.9:4000")
	var request lanRequestResponse
	if err := json.Unmarshal(created.Body.Bytes(), &request); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, err := handler.store.Deny(request.ID); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	recorder := callLAN(t, handler, http.MethodGet, lanRequestPrefix+request.ID, "", "")
	var response lanClaimResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.State != string(lanaccess.StateDenied) || response.Token != "" {
		t.Fatalf("response = %+v", response)
	}
}

// TestLANClaimOnAnUnknownRequestTellsTheDeviceToStartAgain: an expired request
// must not leave a device polling forever.
func TestLANClaimOnAnUnknownRequestTellsTheDeviceToStartAgain(t *testing.T) {
	handler := newTestLANHandler(t)
	recorder := callLAN(t, handler, http.MethodGet, lanRequestPrefix+"11111111-2222-3333-4444-555555555555", "", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", recorder.Code, recorder.Body)
	}
}

func TestLANUnknownPaths(t *testing.T) {
	handler := newTestLANHandler(t)
	for _, path := range []string{"/api/lan/", "/api/lan/elsewhere"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s -> %d, want 404", path, recorder.Code)
		}
	}
}

// TestLANOwnerEndpointsRequireASession: the endpoints that decide must not be
// reachable by the devices asking.
func TestLANOwnerEndpointsRequireASession(t *testing.T) {
	handler := newTestLANHandler(t)
	for _, path := range []string{lanDevicesPath, lanDevicePrefix + "some-id/approve"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.RemoteAddr = "192.168.1.9:4000"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s -> %d, want 401", path, recorder.Code)
		}
	}
}

func TestRemoteHost(t *testing.T) {
	for name, testCase := range map[string]struct{ remote, want string }{
		"with port": {"192.168.1.44:51234", "192.168.1.44"},
		"ipv6":      {"[fe80::1]:51234", "fe80::1"},
		"no port":   {"192.168.1.44", "192.168.1.44"},
		"empty":     {"", ""},
		"padded":    {" 192.168.1.44:1 ", "192.168.1.44"},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = testCase.remote
			if got := remoteHost(request); got != testCase.want {
				t.Fatalf("remoteHost(%q) = %q, want %q", testCase.remote, got, testCase.want)
			}
		})
	}
	if got := remoteHost(nil); got != "" {
		t.Fatalf("remoteHost(nil) = %q", got)
	}
}
