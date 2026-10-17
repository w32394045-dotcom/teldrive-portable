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
	"github.com/tgdrive/teldrive/v2/internal/lanaccess"
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
	backupHandler  *backupHandler
	vaultHandler   *vaultHandler
	lanHandler     *lanHandler
	// lan is the same registry the handler above uses, read directly by the
	// status endpoint: "is anybody waiting to be let in" needs the store, not
	// the page's own view of it.
	lan            *lanaccess.Store
	desktopHandler *desktopHandler
	// desktop reports whether a native shell is present, which is what decides
	// whether the page offers "save to this computer" at all.
	desktop        bool
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
	backup   http.Handler
	vault    http.Handler
	lan      http.Handler
	desktop  http.Handler
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
		lan:         config.lan,
		desktop:     config.desktop,
	})
	if err != nil {
		return nil, err
	}

	return &extraAPI{
		dav:      http.HandlerFunc(handler.ServeDAV),
		settings: http.HandlerFunc(handler.ServeConfig),
		system:   system,
		backup:   config.backupHandler,
		vault:    config.vaultHandler,
		lan:      config.lanHandler,
		desktop:  config.desktopHandler,
	}, nil
}

// wrap dispatches the extra surface ahead of the chi mux.
func (h *extraAPI) wrap(next http.Handler, security *requestSecurity, logger *slog.Logger) http.Handler {
	dav := security.middleware(h.dav)
	// These are called by the browser with the session cookie, so they get exactly
	// the same CSRF treatment as the rest of the API.
	settings := security.middleware(browserCSRFMiddleware(h.settings))
	system := security.middleware(browserCSRFMiddleware(h.system))
	var backup http.Handler
	if h.backup != nil {
		backup = security.middleware(browserCSRFMiddleware(h.backup))
	}
	var vaultHandler http.Handler
	if h.vault != nil {
		vaultHandler = security.middleware(browserCSRFMiddleware(h.vault))
	}
	// The local-network surface is the one place a request may arrive with no
	// session at all -- a device asking for access has none yet -- so it is
	// dispatched without the browser-session treatment and decides for itself
	// which of its routes need an owner.
	var lan http.Handler
	if h.lan != nil {
		lan = security.middleware(h.lan)
	}
	// The desktop routes write files outside the data directory and open native
	// dialogs, so they get the same browser-session treatment as the rest.
	var desktopHost http.Handler
	if h.desktop != nil {
		desktopHost = security.middleware(browserCSRFMiddleware(h.desktop))
	}

	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == webDAVSettingsPath:
			settings.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, systemPathPrefix):
			system.ServeHTTP(w, r)
		case backup != nil && isBackupPath(r.URL.Path):
			backup.ServeHTTP(w, r)
		case vaultHandler != nil && isVaultPath(r.URL.Path):
			vaultHandler.ServeHTTP(w, r)
		case desktopHost != nil && isDesktopPath(r.URL.Path):
			desktopHost.ServeHTTP(w, r)
		case lan != nil && strings.HasPrefix(r.URL.Path, lanPathPrefix):
			lan.ServeHTTP(w, r)
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

// isBackupPath matches the snapshot settings API. Every path here is an exact
// literal, so there is no prefix to get wrong.
func isBackupPath(path string) bool {
	switch path {
	case backupSettingsPath, backupRunPath, backupScanPath, backupSnapshotPath, backupRestorePath, backupInspectPath:
		return true
	default:
		return false
	}
}

// isVaultPath matches the settings-vault API, with the same exact-literal rule.
func isVaultPath(path string) bool {
	switch path {
	case vaultPath, vaultForgetPath:
		return true
	default:
		return false
	}
}

// isDesktopPath matches the native-shell API, again by exact literal.
func isDesktopPath(path string) bool {
	switch path {
	case desktopDownloadsPath, desktopFolderPath, desktopSavePath:
		return true
	default:
		return false
	}
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
