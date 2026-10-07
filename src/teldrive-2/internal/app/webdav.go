package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/tgdrive/teldrive/v2/internal/authn"
	"github.com/tgdrive/teldrive/v2/internal/catalog"
	"github.com/tgdrive/teldrive/v2/internal/transfer"
	"github.com/tgdrive/teldrive/v2/internal/uploads"
	"github.com/tgdrive/teldrive/v2/internal/webdav"
)

// webDAVConfig is the slice of the application the DAV layer borrows. It reuses
// the same catalog, upload and download services as the HTTP API so DAV clients
// inherit identical authorization, conflict and caching rules.
type webDAVConfig struct {
	catalogService *catalog.Service
	uploadService  *uploads.Service
	pipeline       *transfer.Pipeline
	downloader     *transfer.Downloader
	authService    *authn.Service
	httpAddress    string
	logger         *slog.Logger
}

// newWebDAVHandler builds the DAV handler and restores the persisted enable
// flag. The toggle lives in a JSON file next to the config rather than in the
// database, so enabling WebDAV needs no migration.
func newWebDAVHandler(config webDAVConfig) (*webDAVHandler, error) {
	configPath := webdav.DefaultConfigPath()
	store := webdav.NewConfigStore(configPath, config.logger)
	if err := store.Load(); err != nil {
		return nil, fmt.Errorf("load webdav settings: %w", err)
	}
	handler, err := webdav.New(webdav.Options{
		Catalog:    config.catalogService,
		Uploads:    config.uploadService,
		Pipeline:   config.pipeline,
		Downloader: config.downloader,
		Config:     store,
		APIKeys:    config.authService,
		Browser:    config.authService,
		// Spooled PUT bodies live beside the database, not in the OS temp dir, so
		// a large upload cannot fill the system drive.
		TempDir:     filepath.Join(filepath.Dir(configPath), "tmp"),
		HTTPAddress: config.httpAddress,
		Logger:      config.logger,
	})
	if err != nil {
		return nil, err
	}
	return &webDAVHandler{
		dav:      http.HandlerFunc(handler.ServeDAV),
		settings: http.HandlerFunc(handler.ServeConfig),
	}, nil
}

type webDAVHandler struct {
	dav      http.Handler
	settings http.Handler
}

// webDAVSettingsPath is where the settings UI reads and writes the toggle. It
// lives under /api so the SPA can use its usual fetch helper.
const webDAVSettingsPath = "/api/webdav-config"

// wrap dispatches the DAV surface ahead of the chi mux.
//
// It cannot use chi routes: chi keeps one routing tree per *known* HTTP method
// and answers every other method with 405, so WebDAV's own verbs (PROPFIND,
// MKCOL, MOVE, COPY, LOCK) would never reach a route. Dispatching ahead of the
// mux is the only way to serve them.
func (h *webDAVHandler) wrap(next http.Handler, security *requestSecurity, logger *slog.Logger) http.Handler {
	dav := security.middleware(h.dav)
	// The settings call is made by the browser with the session cookie, so it
	// gets exactly the same CSRF treatment as the rest of the API.
	settings := security.middleware(browserCSRFMiddleware(h.settings))

	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == webDAVSettingsPath:
			settings.ServeHTTP(w, r)
		case isDAVPath(r.URL.Path):
			dav.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
	// The mux no longer sees DAV requests, so it cannot log them or assign a
	// request id; those middlewares wrap the whole dispatch instead.
	return requestIDMiddleware(httpRequestLogger(logger)(dispatch))
}

// isDAVPath reports whether a request belongs to the DAV surface. Matching "/webdav"
// exactly and "/webdav/" as a prefix keeps a sibling such as "/webdavfoo" out.
func isDAVPath(path string) bool {
	return path == "/webdav" || strings.HasPrefix(path, "/webdav/")
}
