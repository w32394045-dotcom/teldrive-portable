package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// chi keeps one routing tree per *known* HTTP method and answers every other
// method with 405, so a WebDAV verb can never reach a chi route. This pins the
// dispatch that works around it: every DAV method must reach the DAV handler,
// the settings endpoint must be intercepted, and everything else must fall
// through to the mux untouched.
func TestWebDAVDispatchHandlesDAVMethodsAheadOfTheMux(t *testing.T) {
	t.Parallel()
	security, err := newRequestSecurity(nil)
	if err != nil {
		t.Fatalf("newRequestSecurity: %v", err)
	}
	handler := &extraAPI{
		dav: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "dav:"+r.Method)
		}),
		settings: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "settings")
		}),
		system: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "system:"+r.URL.Path)
		}),
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "mux:"+r.URL.Path)
	})
	root := handler.wrap(next, security, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Every DAV verb, including the ones chi has no tree for.
	for _, method := range []string{
		http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions,
		"PROPFIND", "PROPPATCH", "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK",
	} {
		for _, path := range []string{"/webdav", "/webdav/", "/webdav/folder/file.txt"} {
			response := httptest.NewRecorder()
			root.ServeHTTP(response, httptest.NewRequest(method, path, nil))
			if got, want := response.Body.String(), "dav:"+method; got != want {
				t.Errorf("%s %s = %q, want %q", method, path, got, want)
			}
		}
	}

	// The settings endpoint is intercepted for the methods the UI uses.
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		response := httptest.NewRecorder()
		root.ServeHTTP(response, httptest.NewRequest(method, webDAVSettingsPath, nil))
		if got := response.Body.String(); got != "settings" {
			t.Errorf("%s %s = %q, want %q", method, webDAVSettingsPath, got, "settings")
		}
	}

	// The OS integration endpoints are intercepted too, for the methods the UI uses.
	for _, path := range []string{autostartPath, mountPath} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodPost, http.MethodDelete} {
			response := httptest.NewRecorder()
			root.ServeHTTP(response, httptest.NewRequest(method, path, nil))
			if got, want := response.Body.String(), "system:"+path; got != want {
				t.Errorf("%s %s = %q, want %q", method, path, got, want)
			}
		}
	}

	// Anything else keeps going to the mux, and near-miss paths are not captured
	// by the prefix checks.
	for path, want := range map[string]string{
		"/":                "mux:/",
		"/files":           "mux:/files",
		"/webdavfoo":       "mux:/webdavfoo",
		"/webdav-config":   "mux:/webdav-config",
		"/api/v1/files":    "mux:/api/v1/files",
		"/api/systemfoo":   "mux:/api/systemfoo",
		"/api/webdav-conf": "mux:/api/webdav-conf",
	} {
		response := httptest.NewRecorder()
		root.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if got := response.Body.String(); got != want {
			t.Errorf("GET %s = %q, want %q", path, got, want)
		}
	}
}

// The mux must keep serving the API and the SPA unchanged now that the DAV
// dispatch wraps it.
func TestMuxStillServesAPIAndSPAAfterWrapping(t *testing.T) {
	t.Parallel()
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "api:"+r.URL.Path)
	})
	ui := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ui:"+r.URL.Path)
	})
	mux := chi.NewRouter()
	routeApplication(mux, api, ui)

	for path, want := range map[string]string{
		"/api/v1/files": "api:/v1/files",
		"/v1/files":     "api:/v1/files",
		"/files":        "ui:/files",
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if got := response.Body.String(); got != want {
			t.Errorf("GET %s = %q, want %q", path, got, want)
		}
	}
}

func TestIsDAVPath(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		"/webdav":            true,
		"/webdav/":           true,
		"/webdav/a/b.txt":    true,
		"/webdavfoo":         false,
		"/webdav-config":     false,
		"/api/webdav-config": false,
		"/":                  false,
		"/files":             false,
	} {
		if got := isDAVPath(path); got != want {
			t.Errorf("isDAVPath(%q) = %v, want %v", path, got, want)
		}
	}
}
