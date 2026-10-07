package webdav

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	xwebdav "golang.org/x/net/webdav"

	"github.com/tgdrive/teldrive/v2/internal/principal"
)

const (
	// accessCookieName is the browser session cookie the SPA already holds.
	accessCookieName = "teldrive_access"
	// configBodyLimit caps the settings request body; it only ever carries a bool.
	configBodyLimit = 4 << 10
)

// APIKeyAuthenticator resolves an API key into an identity. Satisfied by
// *authn.Service.
type APIKeyAuthenticator interface {
	AuthenticateAPIKey(ctx context.Context, raw string) (principal.Identity, error)
}

// BrowserAuthenticator resolves a browser access token into an identity.
// Satisfied by *authn.Service.
type BrowserAuthenticator interface {
	AuthenticateBearer(ctx context.Context, raw string) (principal.Identity, error)
}

// Options are the application services the DAV layer borrows.
type Options struct {
	Catalog    Catalog
	Uploads    UploadService
	Pipeline   PartUploader
	Downloader Downloader
	Config     *ConfigStore
	APIKeys    APIKeyAuthenticator
	Browser    BrowserAuthenticator
	TempDir    string
	PartSize   int64
	Logger     *slog.Logger
	// HTTPAddress is the address the server listens on; the settings UI is given
	// a copyable URL derived from it instead of guessing one.
	HTTPAddress string
}

// Handler serves the DAV tree at /webdav and the enable/disable endpoint at
// /webdav-config. The DAV filesystem is built per request so the authenticated
// principal never leaks into shared state.
type Handler struct {
	options Options
	locks   xwebdav.LockSystem
}

func New(options Options) (*Handler, error) {
	if options.Catalog == nil || options.Config == nil || options.APIKeys == nil {
		return nil, errors.New("webdav handler requires a catalog, a config store and an API key authenticator")
	}
	if options.Browser == nil {
		return nil, errors.New("webdav handler requires a browser authenticator")
	}
	if options.PartSize <= 0 {
		options.PartSize = defaultPartSize
	}
	if strings.TrimSpace(options.TempDir) == "" {
		options.TempDir = os.TempDir()
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Handler{options: options, locks: xwebdav.NewMemLS()}, nil
}

// ServeDAV answers the DAV methods. It is mounted at /webdav and /webdav/*.
func (h *Handler) ServeDAV(w http.ResponseWriter, r *http.Request) {
	if !h.options.Config.Enabled() {
		// A clear refusal, not a 404: the client's credentials are not the issue.
		writeDAVMessage(w, http.StatusForbidden,
			"WebDAV is turned off. Enable it in Teldrive under Settings, WebDAV.")
		return
	}

	username, password, ok := r.BasicAuth()
	if !ok {
		challengeDAV(w)
		return
	}
	identity, err := h.authenticateAPIKey(r.Context(), username, password)
	if err != nil {
		h.options.Logger.Warn("webdav.auth_failed", "remote", r.RemoteAddr, "user", username)
		challengeDAV(w)
		return
	}

	if r.URL.Path == "/webdav" {
		http.Redirect(w, r, "/webdav/", http.StatusMovedPermanently)
		return
	}

	// The webdav.FileSystem interface only receives a context, so the declared
	// body size and a clean-EOF flag travel through it; PUT needs both to avoid
	// publishing a truncated file.
	tracker := &bodyTracker{}
	meta := requestMeta{contentLength: r.ContentLength, body: tracker}
	if r.Body != nil {
		r.Body = &trackingBody{ReadCloser: r.Body, tracker: tracker}
	}
	ctx := withRequestMeta(r.Context(), meta)
	ctx = principal.WithIdentity(ctx, identity)
	request := r.WithContext(ctx)

	failures := &failureHolder{}
	dav := &xwebdav.Handler{
		Prefix:     "/webdav",
		FileSystem: h.newFileSystem(identity, meta, failures),
		LockSystem: h.locks,
		Logger: func(req *http.Request, err error) {
			failures.record(err)
			h.options.Logger.Warn("webdav.request_failed",
				"method", req.Method, "path", req.URL.Path, "user", identity.UserID, "error", err)
		},
	}

	// webdav.Handler flattens most write failures onto 405/404. Buffering the
	// small write responses lets a recorded domain failure answer with a status
	// the client can act on; reads are streamed straight through so a large GET
	// is never held in memory.
	if bufferWriteResponse(request.Method) {
		buffered := newBufferedResponse()
		dav.ServeHTTP(buffered, request)
		// webdav.Handler answers 405 for most write failures, which tells the
		// client nothing. Only that uninformative status is replaced: a 404, 409
		// or 412 coming out of the DAV layer already names the real problem (for
		// example MKCOL into a missing parent must stay 409 per RFC 4918).
		if err := failures.Err(); err != nil && buffered.status == http.StatusMethodNotAllowed {
			if status, message := davStatusFor(err); status != buffered.status {
				writeDAVMessage(w, status, message)
				return
			}
		}
		buffered.flush(w)
		return
	}
	dav.ServeHTTP(w, request)
}

// ServeConfig reads and updates the enable/disable setting. It is mounted at
// /api/webdav-config, alongside the other calls the browser UI makes, and
// expects the session cookie.
func (h *Handler) ServeConfig(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.browserIdentity(r)
	if !ok {
		writeJSONMessage(w, http.StatusUnauthorized, "unauthenticated", "Sign in to change this setting.")
		return
	}
	if !canManageWebDAV(identity) {
		writeJSONMessage(w, http.StatusForbidden, "forbidden",
			"Only an instance owner or administrator can change WebDAV access.")
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Reading the state is safe and needs no further checks.
	case http.MethodPut:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, configBodyLimit)).Decode(&body); err != nil || body.Enabled == nil {
			writeJSONMessage(w, http.StatusBadRequest, "invalid_request",
				`Expected a JSON body of the form {"enabled": true|false}.`)
			return
		}
		if err := h.options.Config.SetEnabled(*body.Enabled); err != nil {
			h.options.Logger.Error("webdav.toggle_failed", "error", err)
			writeJSONMessage(w, http.StatusInternalServerError, "write_failed",
				"The setting could not be saved.")
			return
		}
		h.options.Logger.Info("webdav.toggled", "enabled", *body.Enabled, "user", identity.UserID)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or PUT.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": h.options.Config.Enabled(),
		"url":     h.publicURL(),
	})
}

// publicURL is the address a DAV client should connect to. A wildcard bind is
// reported as loopback, because "0.0.0.0" is not something a user can paste.
func (h *Handler) publicURL() string {
	address := strings.TrimSpace(h.options.HTTPAddress)
	if address == "" {
		address = "127.0.0.1:8080"
	}
	if host, port, err := net.SplitHostPort(address); err == nil {
		if host == "" || host == "0.0.0.0" || host == "::" {
			address = net.JoinHostPort("127.0.0.1", port)
		}
	}
	return "http://" + address + "/webdav"
}

func (h *Handler) newFileSystem(identity principal.Identity, meta requestMeta, failures *failureHolder) *fileSystem {
	return &fileSystem{
		meta:       meta,
		userID:     identity.UserID,
		catalog:    h.options.Catalog,
		uploads:    h.options.Uploads,
		pipeline:   h.options.Pipeline,
		downloader: h.options.Downloader,
		partSize:   h.options.PartSize,
		tempDir:    h.options.TempDir,
		failures:   failures,
	}
}

// authenticateAPIKey accepts the key as either the username or the password,
// because DAV clients disagree about which field a token belongs in.
func (h *Handler) authenticateAPIKey(ctx context.Context, username, password string) (principal.Identity, error) {
	var lastErr error
	for _, candidate := range []string{username, password} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		identity, err := h.options.APIKeys.AuthenticateAPIKey(ctx, candidate)
		if err == nil {
			return identity, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no credentials supplied")
	}
	return principal.Identity{}, lastErr
}

func (h *Handler) browserIdentity(r *http.Request) (principal.Identity, bool) {
	cookie, err := r.Cookie(accessCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return principal.Identity{}, false
	}
	identity, err := h.options.Browser.AuthenticateBearer(r.Context(), cookie.Value)
	if err != nil {
		return principal.Identity{}, false
	}
	return identity, true
}

// canManageWebDAV requires the owner/admin roles that rolesForUser always
// populates for a privileged account.
func canManageWebDAV(identity principal.Identity) bool {
	for _, role := range identity.Roles {
		switch strings.ToLower(role) {
		case "owner", "admin":
			return true
		}
	}
	return false
}

// bufferWriteResponse reports whether a response is small enough to hold in
// memory so a failure can be re-mapped to a better status.
func bufferWriteResponse(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, "PROPFIND":
		return false
	default:
		return true
	}
}

// davStatusFor maps a recorded domain failure onto an HTTP status that tells the
// client what actually went wrong.
func davStatusFor(err error) (int, string) {
	switch {
	case err == nil:
		return 0, ""
	case errors.Is(err, os.ErrNotExist):
		return http.StatusNotFound, "The item does not exist."
	case errors.Is(err, os.ErrExist):
		return http.StatusConflict, "An item with that name already exists."
	case errors.Is(err, os.ErrPermission):
		return http.StatusForbidden, "The server refused to change that item."
	case errors.Is(err, ErrRootOperation), errors.Is(err, ErrInvalidPath):
		return http.StatusForbidden, "That operation is not allowed on this path."
	}
	return http.StatusInternalServerError, "The server could not complete the request: " + err.Error()
}

func challengeDAV(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Teldrive", charset="UTF-8"`)
	writeDAVMessage(w, http.StatusUnauthorized, "Authentication is required.")
}

func writeDAVMessage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintln(w, message)
}

func writeJSONMessage(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// bufferedResponse collects a small write response so the handler can choose the
// final status after the DAV layer has run.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header), status: http.StatusOK}
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) WriteHeader(status int) { b.status = status }

func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }

func (b *bufferedResponse) flush(w http.ResponseWriter) {
	for key, values := range b.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(b.status)
	if b.body.Len() > 0 {
		_, _ = w.Write(b.body.Bytes())
	}
}
