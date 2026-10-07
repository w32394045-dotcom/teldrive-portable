package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tgdrive/teldrive/v2/internal/authn"
	"github.com/tgdrive/teldrive/v2/internal/catalog"
	"github.com/tgdrive/teldrive/v2/internal/transfer"
	"github.com/tgdrive/teldrive/v2/internal/uploads"
	"github.com/tgdrive/teldrive/v2/internal/webdav"
)

// extraAPIConfig is the slice of the application the extra HTTP surface borrows.
// It reuses the same catalog, upload and download services as the generated API
// so DAV clients inherit identical authorization, conflict and caching rules.
type extraAPIConfig struct {
	catalogService *catalog.Service
	uploadService  *uploads.Service
	pipeline       *transfer.Pipeline
	downloader     *transfer.Downloader
	authService    *authn.Service
	configStore    *webdav.ConfigStore
	httpAddress    string
	executablePath string
	logger         *slog.Logger
}

// extraAPI serves the HTTP surface that deliberately sits outside the generated
// ogen contract: the DAV tree, the WebDAV toggle, and the operating-system
// integration (start at login, mount the DAV tree as a drive).
//
// It cannot use chi routes: chi keeps one routing tree per *known* HTTP method
// and answers every other method with 405, so WebDAV's own verbs (PROPFIND,
// MKCOL, MOVE, COPY, LOCK) would never reach a route. Dispatching ahead of the
// mux is the only way to serve them.
type extraAPI struct {
	dav      http.Handler
	settings http.Handler
	system   http.Handler
}

const (
	// webDAVSettingsPath is where the settings UI reads and writes the toggle.
	webDAVSettingsPath = "/api/webdav-config"
	// systemPathPrefix owns the OS integration endpoints.
	systemPathPrefix = "/api/system/"
)

// newExtraAPI builds the DAV handler and the OS integration handler, and
// restores the persisted WebDAV flag. The toggle lives in a JSON file next to
// the config rather than in the database, so enabling WebDAV needs no migration.
func newExtraAPI(config extraAPIConfig) (*extraAPI, error) {
	handler, err := webdav.New(webdav.Options{
		Catalog:    config.catalogService,
		Uploads:    config.uploadService,
		Pipeline:   config.pipeline,
		Downloader: config.downloader,
		Config:     config.configStore,
		APIKeys:    config.authService,
		Browser:    config.authService,
		// Spooled PUT bodies live beside the database, not in the OS temp dir, so
		// a large upload cannot fill the system drive.
		TempDir:     filepath.Join(filepath.Dir(config.configStore.Path()), "tmp"),
		HTTPAddress: config.httpAddress,
		Logger:      config.logger,
	})
	if err != nil {
		return nil, err
	}

	executable := config.executablePath
	if executable == "" {
		if resolved, err := os.Executable(); err == nil {
			executable = resolved
		}
	}
	system, err := newSystemHandler(systemConfig{
		authService: config.authService,
		store:       config.configStore,
		httpAddress: config.httpAddress,
		executable:  executable,
		logger:      config.logger,
	})
	if err != nil {
		return nil, err
	}

	return &extraAPI{
		dav:      http.HandlerFunc(handler.ServeDAV),
		settings: http.HandlerFunc(handler.ServeConfig),
		system:   system,
	}, nil
}

// wrap dispatches the extra surface ahead of the chi mux.
func (h *extraAPI) wrap(next http.Handler, security *requestSecurity, logger *slog.Logger) http.Handler {
	dav := security.middleware(h.dav)
	// These are called by the browser with the session cookie, so they get exactly
	// the same CSRF treatment as the rest of the API.
	settings := security.middleware(browserCSRFMiddleware(h.settings))
	system := security.middleware(browserCSRFMiddleware(h.system))

	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == webDAVSettingsPath:
			settings.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, systemPathPrefix):
			system.ServeHTTP(w, r)
		case isDAVPath(r.URL.Path):
			dav.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
	// The mux no longer sees these requests, so it cannot log them or assign a
	// request id; those middlewares wrap the whole dispatch instead.
	return requestIDMiddleware(httpRequestLogger(logger)(dispatch))
}

// isDAVPath reports whether a request belongs to the DAV surface. Matching "/webdav"
// exactly and "/webdav/" as a prefix keeps a sibling such as "/webdavfoo" out.
func isDAVPath(path string) bool {
	return path == "/webdav" || strings.HasPrefix(path, "/webdav/")
}

// newConfigStore loads the WebDAV settings file. A missing file is the documented
// default (disabled), so a first run is silent.
func newConfigStore(logger *slog.Logger) (*webdav.ConfigStore, error) {
	store := webdav.NewConfigStore(webdav.DefaultConfigPath(), logger)
	if err := store.Load(); err != nil {
		return nil, fmt.Errorf("load webdav settings: %w", err)
	}
	return store, nil
}
