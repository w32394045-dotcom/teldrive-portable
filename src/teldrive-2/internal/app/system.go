package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tgdrive/teldrive/v2/internal/authn"
	"github.com/tgdrive/teldrive/v2/internal/sysintegration"
	"github.com/tgdrive/teldrive/v2/internal/webdav"
)

const (
	// autostartPath and mountPath are the OS integration endpoints.
	autostartPath = systemPathPrefix + "autostart"
	mountPath     = systemPathPrefix + "webdav-mount"

	// mountKeyName labels the API key this server generates for the mapping.
	mountKeyName = "Windows 映射盘（自动创建）"

	// mountUser is sent as the Basic user name; the DAV layer accepts the API key
	// in either field, so the name itself is arbitrary.
	mountUser = "teldrive"

	// elevationWait bounds how long we wait for the user to accept the UAC prompt
	// and for the redirector settings to land.
	elevationWait = 20 * time.Second
)

type systemConfig struct {
	authService *authn.Service
	store       *webdav.ConfigStore
	httpAddress string
	executable  string
	logger      *slog.Logger
}

// systemHandler exposes the operating-system integration: start Teldrive at
// login, and mount the DAV tree as a system drive.
type systemHandler struct {
	manager     *sysintegration.Manager
	auth        *authn.Service
	store       *webdav.ConfigStore
	httpAddress string
	logger      *slog.Logger
}

func newSystemHandler(config systemConfig) (*systemHandler, error) {
	if config.authService == nil || config.store == nil {
		return nil, errors.New("system handler requires the auth service and the webdav config store")
	}
	if config.logger == nil {
		config.logger = slog.Default()
	}
	return &systemHandler{
		manager:     sysintegration.New(config.executable),
		auth:        config.authService,
		store:       config.store,
		httpAddress: config.httpAddress,
		logger:      config.logger,
	}, nil
}

func (h *systemHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	switch r.URL.Path {
	case autostartPath:
		h.serveAutostart(w, r)
	case mountPath:
		h.serveMount(w, r, userID)
	default:
		writeJSONMessage(w, http.StatusNotFound, "not_found", "Unknown system endpoint.")
	}
}

// authorize resolves the browser session and requires an owner or administrator,
// matching the WebDAV toggle itself.
func (h *systemHandler) authorize(w http.ResponseWriter, r *http.Request) (int64, bool) {
	cookie, err := r.Cookie(accessCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		writeJSONMessage(w, http.StatusUnauthorized, "unauthenticated", "Sign in to change this setting.")
		return 0, false
	}
	identity, err := h.auth.AuthenticateBearer(r.Context(), cookie.Value)
	if err != nil {
		writeJSONMessage(w, http.StatusUnauthorized, "unauthenticated", "Sign in to change this setting.")
		return 0, false
	}
	manager := false
	for _, role := range identity.Roles {
		switch strings.ToLower(role) {
		case "owner", "admin":
			manager = true
		}
	}
	if !manager {
		writeJSONMessage(w, http.StatusForbidden, "forbidden",
			"Only an instance owner or administrator can change system integration.")
		return 0, false
	}
	return identity.UserID, true
}

type autostartResponse struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Command   string `json:"command,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Message   string `json:"message,omitempty"`
}

func (h *systemHandler) serveAutostart(w http.ResponseWriter, r *http.Request) {
	status := h.manager.Autostart(r.Context())

	switch r.Method {
	case http.MethodGet:
		// Reading is free.
	case http.MethodPut:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, configBodyLimit)).Decode(&body); err != nil || body.Enabled == nil {
			writeJSONMessage(w, http.StatusBadRequest, "invalid_request",
				`Expected a JSON body of the form {"enabled": true|false}.`)
			return
		}
		if !status.Supported {
			writeJSONMessage(w, http.StatusNotImplemented, "unsupported",
				"Starting at login is only implemented for Windows.")
			return
		}
		if err := h.manager.SetAutostart(r.Context(), *body.Enabled); err != nil {
			h.logger.Error("system.autostart_failed", "error", err)
			writeJSONMessage(w, http.StatusInternalServerError, "write_failed",
				"The start-at-login setting could not be saved: "+err.Error())
			return
		}
		h.logger.Info("system.autostart", "enabled", *body.Enabled)
		status = h.manager.Autostart(r.Context())
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or PUT.")
		return
	}

	writeJSON(w, http.StatusOK, autostartResponse{
		Supported: status.Supported,
		Enabled:   status.Enabled,
		Command:   status.Command,
		Detail:    status.Detail,
	})
}

type mountResponse struct {
	Supported     bool                         `json:"supported"`
	Mounted       bool                         `json:"mounted"`
	Drive         string                       `json:"drive,omitempty"`
	URL           string                       `json:"url,omitempty"`
	Detail        string                       `json:"detail,omitempty"`
	Message       string                       `json:"message,omitempty"`
	Prerequisites sysintegration.Prerequisites `json:"prerequisites"`
}

// serveMount reports, creates and removes the mapped drive.
func (h *systemHandler) serveMount(w http.ResponseWriter, r *http.Request, userID int64) {
	switch r.Method {
	case http.MethodGet:
		h.writeMountStatus(w, r.Context(), "")
	case http.MethodPost:
		h.mount(w, r, userID)
	case http.MethodDelete:
		_, _, drive := h.store.MountCredentials()
		if drive == "" {
			drive = strings.TrimSpace(r.URL.Query().Get("drive"))
		}
		if err := h.manager.Unmount(r.Context(), drive); err != nil {
			writeJSONMessage(w, http.StatusConflict, "unmount_failed", err.Error())
			return
		}
		h.logger.Info("system.mount.removed", "drive", drive)
		h.writeMountStatus(w, r.Context(), "映射已删除。")
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET, POST or DELETE.")
	}
}

func (h *systemHandler) mount(w http.ResponseWriter, r *http.Request, userID int64) {
	var body struct {
		Elevate bool `json:"elevate"`
	}
	// An empty body is fine: it means "mount, do not try to fix anything".
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, configBodyLimit)).Decode(&body)
	}

	// Windows will not talk Basic auth over plain HTTP until the redirector is
	// reconfigured, and that needs administrator rights.
	prerequisites := h.manager.CheckPrerequisites(r.Context())
	if !prerequisites.Ready {
		if !body.Elevate {
			h.writeMountStatus(w, r.Context(),
				"需要先修复前置条件（会弹出 UAC 提权对话框）："+firstBrokenItem(prerequisites))
			return
		}
		if err := h.manager.Elevate(r.Context()); err != nil {
			writeJSONMessage(w, http.StatusConflict, "elevation_failed", err.Error())
			return
		}
		if !waitForPrerequisites(r.Context(), h.manager) {
			h.writeMountStatus(w, r.Context(), "已请求提权，但设置尚未生效——请在弹出的 UAC 对话框中确认后重试。")
			return
		}
	}

	if !h.store.Enabled() {
		writeJSONMessage(w, http.StatusConflict, "webdav_disabled",
			"请先在设置里开启 WebDAV，再挂载。")
		return
	}

	keyID, secret, err := h.ensureMountKey(r.Context(), userID)
	if err != nil {
		writeJSONMessage(w, http.StatusInternalServerError, "key_failed", err.Error())
		return
	}

	drive := h.mountDrive()
	urlValue := h.mountURL()
	drive, err = h.manager.Mount(r.Context(), urlValue, mountUser, secret, drive)
	if err != nil && keyID != "" {
		// A stored key may have been revoked; regenerate once before giving up.
		if revokeErr := h.auth.RevokeAPIKey(r.Context(), userID, uuid.MustParse(keyID)); revokeErr == nil {
			_ = h.store.ClearMountCredentials()
			if _, fresh, keyErr := h.ensureMountKey(r.Context(), userID); keyErr == nil {
				if retryDrive, retryErr := h.manager.Mount(r.Context(), urlValue, mountUser, fresh, drive); retryErr == nil {
					drive, secret, err = retryDrive, fresh, nil
				}
			}
		}
	}
	if err != nil {
		h.logger.Error("system.mount.failed", "error", err)
		writeJSONMessage(w, http.StatusConflict, "mount_failed", err.Error())
		return
	}

	// Remember the credential that actually worked, plus where it landed, so a
	// re-mount after a reboot needs no user input.
	if err := h.store.SetMountCredentials(keyID, secret, drive); err != nil {
		h.logger.Warn("system.mount.remember_failed", "error", err)
	}
	h.logger.Info("system.mount.created", "drive", drive, "url", urlValue)
	h.writeMountStatus(w, r.Context(), fmt.Sprintf("已挂载到 %s。", drive))
}

func (h *systemHandler) writeMountStatus(w http.ResponseWriter, ctx context.Context, message string) {
	urlValue := h.mountURL()
	status := h.manager.MountStatus(ctx, urlValue)
	writeJSON(w, http.StatusOK, mountResponse{
		Supported:     status.Supported,
		Mounted:       status.Mounted,
		Drive:         status.Drive,
		URL:           urlValue,
		Detail:        status.Detail,
		Message:       message,
		Prerequisites: status.Prerequisites,
	})
}

// ensureMountKey returns the API key used for the mapping, creating one on first
// use so mounting is genuinely one click.
func (h *systemHandler) ensureMountKey(ctx context.Context, userID int64) (keyID, secret string, err error) {
	if id, stored, _ := h.store.MountCredentials(); strings.TrimSpace(stored) != "" {
		return id, stored, nil
	}
	created, err := h.auth.CreateAPIKey(ctx, userID, mountKeyName, nil)
	if err != nil {
		return "", "", fmt.Errorf("创建挂载专用 API 密钥失败: %w", err)
	}
	id := ""
	if created.Row != nil {
		id = uuid.UUID(created.Row.ID.Bytes).String()
	}
	if err := h.store.SetMountCredentials(id, created.Secret, ""); err != nil {
		return "", "", fmt.Errorf("保存挂载密钥失败: %w", err)
	}
	return id, created.Secret, nil
}

func (h *systemHandler) mountDrive() string {
	if _, _, drive := h.store.MountCredentials(); strings.TrimSpace(drive) != "" {
		return drive
	}
	return ""
}

func (h *systemHandler) mountURL() string {
	address := strings.TrimSpace(h.httpAddress)
	if address == "" {
		address = "127.0.0.1:8080"
	}
	// A wildcard bind is not something a client can connect to.
	if parsed, err := url.Parse("//" + address); err == nil {
		host := parsed.Hostname()
		if host == "" || host == "0.0.0.0" || host == "::" {
			address = "127.0.0.1:" + parsed.Port()
		}
	}
	return "http://" + address + "/webdav"
}

// waitForPrerequisites polls until the redirector settings land, which happens
// as soon as the user accepts the UAC prompt.
func waitForPrerequisites(ctx context.Context, manager *sysintegration.Manager) bool {
	deadline := time.Now().Add(elevationWait)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		if manager.CheckPrerequisites(ctx).Ready {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
	return false
}

func firstBrokenItem(prerequisites sysintegration.Prerequisites) string {
	for _, item := range prerequisites.Items {
		if !item.OK {
			return item.Description
		}
	}
	return ""
}

// configBodyLimit caps the request bodies on these endpoints; they only ever
// carry a boolean.
const configBodyLimit = 4 << 10

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
