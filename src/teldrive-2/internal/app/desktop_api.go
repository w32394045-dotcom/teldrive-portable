package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/tgdrive/teldrive/v2/internal/authn"
	"github.com/tgdrive/teldrive/v2/internal/desktop"
	"github.com/tgdrive/teldrive/v2/internal/transfer"
)

// The desktop endpoints are the part of the drive that only exists on the
// machine running it: where a downloaded file goes, and the native dialogs a web
// page cannot open for itself. They sit outside the generated contract for the
// same reason the OS integration does - they change this computer, not the
// database.
const (
	desktopDownloadsPath = "/api/desktop/downloads"
	desktopFolderPath    = "/api/desktop/folder"
	desktopSavePath      = "/api/desktop/save"

	// maxSaveName bounds a filename taken from the database: filesystems cap
	// names at 255 bytes, and one that long is a bug somewhere else.
	maxSaveName = 200
	// maxSaveAttempts is how many numbered names are tried before giving up.
	maxSaveAttempts = 500
	// fallbackSaveName is used when the stored name has nothing usable left.
	fallbackSaveName = "downloaded-file"
)

// DesktopBridge is the native shell, when the drive is running inside one. It is
// the only way a page can have a question answered that needs a person sitting
// at this computer.
type DesktopBridge interface {
	// ChooseFolder asks the person at this computer for a directory, returning
	// ok=false when they cancel.
	ChooseFolder(title, initial string) (string, bool)
}

// desktopHandler serves the shell's preferences and the save-to-this-computer
// action. Every route is owner/administrator only: this writes outside the data
// directory, which is a different kind of power from changing a setting.
type desktopHandler struct {
	auth       *authn.Service
	store      *desktop.SettingsStore
	shell      DesktopBridge
	downloader *transfer.Downloader
	logger     *slog.Logger
}

type desktopConfig struct {
	store       *desktop.SettingsStore
	shell       DesktopBridge
	downloader  *transfer.Downloader
	authService *authn.Service
	logger      *slog.Logger
}

func newDesktopHandler(config desktopConfig) (*desktopHandler, error) {
	if config.authService == nil || config.store == nil {
		return nil, errors.New("the desktop handler requires the auth service and the settings store")
	}
	if config.logger == nil {
		config.logger = slog.Default()
	}
	return &desktopHandler{
		auth:       config.authService,
		store:      config.store,
		shell:      config.shell,
		downloader: config.downloader,
		logger:     config.logger,
	}, nil
}

func (h *desktopHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case desktopDownloadsPath:
		h.serveSettings(w, r)
	case desktopFolderPath:
		h.serveFolder(w, r)
	case desktopSavePath:
		h.serveSave(w, r)
	default:
		writeJSONMessage(w, http.StatusNotFound, "not_found", "Unknown desktop endpoint.")
	}
}

// authorize is the same owner/administrator gate the other machine-changing
// endpoints use.
func (h *desktopHandler) authorize(w http.ResponseWriter, r *http.Request) (int64, bool) {
	return authorizeManager(w, r, h.auth,
		"只有实例所有者或管理员可以把文件保存到这台电脑。")
}

type desktopSettingsResponse struct {
	DownloadDir  string `json:"downloadDir"`
	AskEveryTime bool   `json:"askEveryTime"`
	// CanChoose says whether a native folder picker is available at all, so the
	// page can hide a button that would do nothing.
	CanChoose bool `json:"canChoose"`
	// DefaultDir is where saves land when nothing has been chosen.
	DefaultDir string `json:"defaultDir,omitempty"`
}

func (h *desktopHandler) settingsResponse() desktopSettingsResponse {
	settings := h.store.Get()
	return desktopSettingsResponse{
		DownloadDir:  settings.DownloadDir,
		AskEveryTime: settings.AskEveryTime,
		CanChoose:    h.shell != nil,
		DefaultDir:   desktop.DefaultDownloadDir(),
	}
}

func (h *desktopHandler) serveSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.settingsResponse())
	case http.MethodPut:
		var body struct {
			DownloadDir  *string `json:"downloadDir"`
			AskEveryTime *bool   `json:"askEveryTime"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, configBodyLimit)).Decode(&body); err != nil {
			writeJSONMessage(w, http.StatusBadRequest, "invalid_request",
				`Expected {"downloadDir": "...", "askEveryTime": true|false}.`)
			return
		}
		settings := h.store.Get()
		if body.DownloadDir != nil {
			settings.DownloadDir = strings.TrimSpace(*body.DownloadDir)
		}
		if body.AskEveryTime != nil {
			settings.AskEveryTime = *body.AskEveryTime
		}
		if err := h.store.Set(settings); err != nil {
			h.logger.Error("desktop.settings_write", "error", err)
			writeJSONMessage(w, http.StatusInternalServerError, "write_failed",
				"The download folder could not be saved: "+err.Error())
			return
		}
		h.logger.Info("desktop.settings", "downloadDir", settings.DownloadDir, "askEveryTime", settings.AskEveryTime)
		writeJSON(w, http.StatusOK, h.settingsResponse())
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or PUT.")
	}
}

// serveFolder opens the native folder picker on the screen of this computer and
// returns what was chosen. It is a POST because it changes nothing but asks the
// person a question, and GET is not supposed to do that.
func (h *desktopHandler) serveFolder(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.")
		return
	}
	if h.shell == nil {
		writeJSONMessage(w, http.StatusNotImplemented, "unsupported",
			"这个服务不是在桌面壳里启动的，因此没有可以弹出的文件夹选择框。")
		return
	}
	initial := h.store.Get().DownloadDir
	path, ok := h.shell.ChooseFolder("选择保存位置", initial)
	if !ok {
		writeJSONMessage(w, http.StatusConflict, "cancelled", "没有选择文件夹。")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

type desktopSaveResponse struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// serveSave writes one file from the drive into the configured folder on this
// computer. It is deliberately not a browser download: the browser saves where
// the browser's own settings say, and this endpoint exists so "save to this
// computer" can mean the folder the person chose here.
func (h *desktopHandler) serveSave(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.")
		return
	}
	var body struct {
		FileID string `json:"fileId"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, configBodyLimit)).Decode(&body); err != nil ||
		strings.TrimSpace(body.FileID) == "" {
		writeJSONMessage(w, http.StatusBadRequest, "invalid_request", `Expected {"fileId": "..."}.`)
		return
	}
	fileID, err := uuid.Parse(strings.TrimSpace(body.FileID))
	if err != nil {
		writeJSONMessage(w, http.StatusBadRequest, "invalid_request", "That file id is not a file id.")
		return
	}
	if h.downloader == nil {
		writeJSONMessage(w, http.StatusNotImplemented, "unsupported",
			"这台服务器没有配置存储，无法把文件取下来。")
		return
	}
	dir, ok := h.targetDir()
	if !ok {
		writeJSONMessage(w, http.StatusConflict, "cancelled", "没有选择保存位置。")
		return
	}

	download, err := h.downloader.Open(r.Context(), transfer.DownloadRequest{
		UserID: userID,
		FileID: fileID,
		Offset: 0,
		Length: -1,
	})
	if err != nil {
		h.logger.Error("desktop.save_open", "fileId", fileID, "error", err)
		writeJSONMessage(w, http.StatusConflict, "download_failed", err.Error())
		return
	}
	defer download.Reader.Close()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSONMessage(w, http.StatusInternalServerError, "write_failed",
			"保存目录无法创建: "+err.Error())
		return
	}
	path, err := uniquePath(dir, safeFileName(download.File.Name))
	if err != nil {
		writeJSONMessage(w, http.StatusInternalServerError, "write_failed", err.Error())
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		writeJSONMessage(w, http.StatusInternalServerError, "write_failed",
			"文件无法创建: "+err.Error())
		return
	}
	written, copyErr := io.Copy(file, download.Reader)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		// A half-written file is worse than no file: the next attempt would then
		// have to explain a truncated one.
		_ = os.Remove(path)
		message := "文件写入失败"
		if copyErr != nil {
			message += ": " + copyErr.Error()
		}
		writeJSONMessage(w, http.StatusInternalServerError, "write_failed", message)
		return
	}
	h.logger.Info("desktop.saved", "fileId", fileID, "path", path, "bytes", written)
	writeJSON(w, http.StatusOK, desktopSaveResponse{
		Path: path,
		Name: filepath.Base(path),
		Size: written,
	})
}

// targetDir answers where a save goes: the configured folder, unless the person
// asked to be asked - in which case this computer's picker appears.
func (h *desktopHandler) targetDir() (string, bool) {
	settings := h.store.Get()
	if settings.AskEveryTime && h.shell != nil {
		if picked, ok := h.shell.ChooseFolder("选择保存位置", settings.DownloadDir); ok {
			return picked, true
		}
		return "", false
	}
	dir := strings.TrimSpace(settings.DownloadDir)
	if dir == "" {
		dir = desktop.DefaultDownloadDir()
	}
	return dir, dir != ""
}

// safeFileName turns a stored name into a name that can only land inside the
// download folder: no separators, no control characters, no dot-only names.
func safeFileName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			return '-'
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	name = strings.Trim(name, ".")
	if name == "" {
		return fallbackSaveName
	}
	if len(name) > maxSaveName {
		name = name[:maxSaveName]
	}
	return name
}

// uniquePath finds a name that does not exist yet, numbering it the way a
// browser does: "report.pdf", "report (1).pdf".
func uniquePath(dir, name string) (string, error) {
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	for attempt := 0; attempt < maxSaveAttempts; attempt++ {
		candidate := name
		if attempt > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, attempt, extension)
		}
		path := filepath.Join(dir, candidate)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
	}
	return "", fmt.Errorf("没有空闲的文件名可用：%s", name)
}
