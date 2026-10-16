package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tgdrive/teldrive/v2/internal/authn"
	"github.com/tgdrive/teldrive/v2/internal/backup"
	"github.com/tgdrive/teldrive/v2/internal/jobs"
)

const (
	// backupSettingsPath is where the settings UI reads and writes the schedule.
	backupSettingsPath = "/api/backup-config"
	// backupRunPath queues a snapshot now.
	backupRunPath = "/api/backup/run"
	// backupScanPath enumerates the snapshots already in the backup channel. It
	// is the only call here that walks Telegram history, which is why it is a
	// POST behind an explicit button rather than something a page load triggers.
	backupScanPath = "/api/backup/scan"
	// backupSnapshotPath streams one stored snapshot back out.
	backupSnapshotPath = "/api/backup/snapshot"
	// backupRestorePath applies a snapshot over the live catalog. It is the only
	// endpoint in the application that deletes a user's files in bulk, so it
	// requires an explicit confirmation and is never reachable from a timer.
	backupRestorePath = "/api/backup/restore"
	// backupInspectPath describes a snapshot without applying it. It is what
	// lets the page ask for a passphrase instead of failing at the last moment,
	// and what lets someone look before they leap.
	backupInspectPath = "/api/backup/inspect"
)

// restoreConfirm is the exact token the caller must echo to apply a restore.
// A bare boolean would be too easy to send by accident from a script or a
// mis-wired form; a word makes the request say what it is about to do.
const restoreConfirm = "restore"

// backupScheduler is the slice of the job runtime this handler drives. The
// handler never touches River directly: the interval is a setting, and turning it
// into a periodic job is the runtime's business.
type backupScheduler interface {
	SyncBackupPeriodicJob(ctx context.Context, enabled bool, interval time.Duration, userID int64) error
	InsertBackup(ctx context.Context, userID int64) (jobs.Job, error)
}

type backupConfig struct {
	service     *backup.Service
	restorer    *backup.Restorer
	store       *backup.Store
	scheduler   backupScheduler
	authService *authn.Service
	logger      *slog.Logger
}

// backupHandler serves the drive-index snapshot settings. Like the WebDAV toggle
// it sits outside the generated ogen contract, so its shape can follow the UI
// instead of a code-generation round trip.
//
// Creating snapshots is automatic; applying one is not, and that asymmetry is the
// point. Nothing scheduled ever calls Restore, and the endpoint refuses a request
// that does not spell out the confirmation token, so the only way to go backwards
// is for a person to decide to.
type backupHandler struct {
	service   *backup.Service
	restorer  *backup.Restorer
	store     *backup.Store
	scheduler backupScheduler
	auth      *authn.Service
	logger    *slog.Logger
}

func newBackupHandler(config backupConfig) (*backupHandler, error) {
	if config.service == nil || config.restorer == nil || config.store == nil || config.scheduler == nil || config.authService == nil {
		return nil, errors.New("backup handler requires the backup service, the restorer, its store, the job runtime and the auth service")
	}
	if config.logger == nil {
		config.logger = slog.Default()
	}
	return &backupHandler{
		service:   config.service,
		restorer:  config.restorer,
		store:     config.store,
		scheduler: config.scheduler,
		auth:      config.authService,
		logger:    config.logger,
	}, nil
}

func (h *backupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID, ok := authorizeManager(w, r, h.auth,
		"Only an instance owner or administrator can change backups.")
	if !ok {
		return
	}
	switch r.URL.Path {
	case backupSettingsPath:
		h.serveSettings(w, r, userID)
	case backupRunPath:
		h.serveRun(w, r, userID)
	case backupScanPath:
		h.serveScan(w, r, userID)
	case backupSnapshotPath:
		h.serveSnapshot(w, r, userID)
	case backupRestorePath:
		h.serveRestore(w, r, userID)
	case backupInspectPath:
		h.serveInspect(w, r, userID)
	default:
		writeJSONMessage(w, http.StatusNotFound, "not_found", "Unknown backup endpoint.")
	}
}

// settingsResponse is the whole state of the settings page in one payload: the
// stored knobs plus what the last run produced, so the page never needs a second
// request to render.
type settingsResponse struct {
	Enabled       bool   `json:"enabled"`
	Interval      string `json:"interval"`
	ChannelID     int64  `json:"channelId,omitempty"`
	LastMessageID int64  `json:"lastMessageId,omitempty"`
	LastSize      int64  `json:"lastSize,omitempty"`
	// LastBackupAt is a pointer so "never" is distinguishable from the zero time.
	LastBackupAt *time.Time `json:"lastBackupAt,omitempty"`
	// RestoreMode is the stored apply strategy; RestoreModes lists the values the
	// UI may offer, so the two can never disagree about what is valid.
	RestoreMode  string   `json:"restoreMode"`
	RestoreModes []string `json:"restoreModes"`
	// PasswordSet says whether snapshots are sealed. The passphrase itself is
	// deliberately absent: the page only needs to know whether one is set, and a
	// secret that travels is a secret that leaks.
	PasswordSet bool `json:"passwordSet"`
	// The bounds travel with the payload rather than being reimplemented in the
	// UI, so the two can never drift apart.
	DefaultInterval string `json:"defaultInterval"`
	MinimumInterval string `json:"minimumInterval"`
	// MinimumPasswordLength travels for the same reason: the page validates a
	// typed passphrase before sending it, and hard-coding the rule there would
	// let the two drift.
	MinimumPasswordLength int `json:"minimumPasswordLength"`
}

func (h *backupHandler) serveSettings(w http.ResponseWriter, r *http.Request, userID int64) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.snapshot())
	case http.MethodPut:
		var body struct {
			Enabled     *bool   `json:"enabled"`
			Interval    *string `json:"interval"`
			RestoreMode *string `json:"restoreMode"`
			// Password is the sealing passphrase. An empty string clears it and
			// stores snapshots as plain JSON, which is a legitimate choice for an
			// unattended schedule rather than a mistake to be rejected.
			Password *string `json:"password"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, configBodyLimit)).Decode(&body); err != nil {
			writeJSONMessage(w, http.StatusBadRequest, "invalid_request",
				`Expected a JSON body of the form {"enabled": true|false, "interval": "168h", "restoreMode": "overwrite", "password": "..."}.`)
			return
		}
		if body.Enabled == nil && body.Interval == nil && body.RestoreMode == nil && body.Password == nil {
			writeJSONMessage(w, http.StatusBadRequest, "invalid_request",
				`Expected at least one of "enabled", "interval", "restoreMode" or "password".`)
			return
		}
		// Omitted fields keep their current value, so saving one switch cannot
		// silently reset the others.
		current := h.store.Settings()
		enabled := current.Enabled
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		interval := current.Interval
		if body.Interval != nil {
			interval = *body.Interval
		}
		if body.RestoreMode != nil {
			mode, err := backup.ParseRestoreMode(*body.RestoreMode)
			if err != nil {
				writeJSONMessage(w, http.StatusBadRequest, "invalid_restore_mode", err.Error())
				return
			}
			if err := h.store.SetRestoreMode(mode); err != nil {
				h.logger.Error("backup.settings.restore_mode_failed", "error", err)
				writeJSONMessage(w, http.StatusInternalServerError, "write_failed",
					"The restore mode could not be saved: "+err.Error())
				return
			}
		}
		if body.Password != nil {
			if err := h.store.SetPassword(*body.Password); err != nil {
				writeJSONMessage(w, http.StatusBadRequest, "invalid_password", err.Error())
				return
			}
		}
		// The caller becomes the recorded owner, which is whose index the schedule
		// writes. Without it a periodic job would have no user to act as.
		if err := h.store.Configure(enabled, interval, userID); err != nil {
			h.logger.Error("backup.settings.write_failed", "error", err)
			writeJSONMessage(w, http.StatusInternalServerError, "write_failed",
				"The backup settings could not be saved: "+err.Error())
			return
		}
		settings := h.store.Settings()
		// Turning the switch on has to actually schedule something; if the
		// schedule cannot be written, say so rather than reporting success on a
		// toggle that does nothing.
		if err := h.scheduler.SyncBackupPeriodicJob(r.Context(), settings.Enabled, h.store.Interval(), settings.OwnerUserID); err != nil {
			h.logger.Error("backup.schedule.failed", "enabled", settings.Enabled, "error", err)
			writeJSONMessage(w, http.StatusInternalServerError, "schedule_failed",
				"The settings were saved, but the schedule could not be applied: "+err.Error())
			return
		}
		h.logger.Info("backup.settings.updated",
			"enabled", settings.Enabled, "interval", settings.Interval,
			"restore_mode", settings.RestoreMode, "owner", settings.OwnerUserID)
		writeJSON(w, http.StatusOK, h.snapshot())
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or PUT.")
	}
}

// serveRun queues a snapshot on the maintenance queue. It answers immediately
// with the job id instead of building the index inline: the work is durable, it
// belongs to the worker that already knows how to retry it, and a large drive
// must not hold an HTTP request open.
func (h *backupHandler) serveRun(w http.ResponseWriter, r *http.Request, userID int64) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.")
		return
	}
	target := h.ownerOrDefault(userID)
	if target <= 0 {
		writeJSONMessage(w, http.StatusBadRequest, "no_owner", "No user is recorded for backups yet.")
		return
	}
	job, err := h.scheduler.InsertBackup(r.Context(), target)
	if err != nil {
		if errors.Is(err, jobs.ErrRuntimeNotConfigured) {
			writeJSONMessage(w, http.StatusConflict, "workers_disabled",
				"Backups need the job worker, which is disabled on this instance.")
			return
		}
		h.logger.Error("backup.run.failed", "user", target, "error", err)
		writeJSONMessage(w, http.StatusInternalServerError, "enqueue_failed", err.Error())
		return
	}
	h.logger.Info("backup.run.queued", "user", target, "job", job.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"queued": true,
		"jobId":  job.ID,
		"userId": target,
	})
}

// serveScan lists what is actually sitting in the backup channel. It is the only
// endpoint that enumerates Telegram history, so it is deliberately a manual,
// user-triggered call.
func (h *backupHandler) serveScan(w http.ResponseWriter, r *http.Request, userID int64) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.")
		return
	}
	target := h.ownerOrDefault(userID)
	entries, err := h.service.Scan(r.Context(), target)
	if err != nil {
		switch {
		case errors.Is(err, backup.ErrScanUnsupported):
			writeJSONMessage(w, http.StatusNotImplemented, "unsupported",
				"This storage backend cannot list channel documents.")
		case errors.Is(err, backup.ErrNotConfigured):
			writeJSONMessage(w, http.StatusConflict, "not_configured", "Backups are not configured yet.")
		default:
			h.logger.Error("backup.scan.failed", "error", err)
			writeJSONMessage(w, http.StatusBadGateway, "scan_failed", err.Error())
		}
		return
	}
	if entries == nil {
		entries = []backup.Entry{}
	}
	h.logger.Info("backup.scan.completed", "snapshots", len(entries))
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": entries, "count": len(entries)})
}

// serveSnapshot streams one stored snapshot back out so it can be inspected or
// archived. Reading is manual by design: there is no endpoint that applies one.
func (h *backupHandler) serveSnapshot(w http.ResponseWriter, r *http.Request, userID int64) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET.")
		return
	}
	messageID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("messageId")), 10, 64)
	if err != nil || messageID <= 0 {
		writeJSONMessage(w, http.StatusBadRequest, "invalid_request", "Pass ?messageId=<id>.")
		return
	}
	target := h.ownerOrDefault(userID)
	reader, err := h.service.Download(r.Context(), target, messageID, 0, -1)
	if err != nil {
		if errors.Is(err, backup.ErrSnapshotNotFound) {
			writeJSONMessage(w, http.StatusNotFound, "not_found", "No snapshot has been recorded yet.")
			return
		}
		h.logger.Error("backup.snapshot.failed", "message", messageID, "error", err)
		writeJSONMessage(w, http.StatusBadGateway, "download_failed", err.Error())
		return
	}
	defer func() { _ = reader.Close() }()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("teldrive-index-%d.json", messageID)))
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, reader); err != nil {
		// The status line is already out; all that is left is the log.
		h.logger.Warn("backup.snapshot.interrupted", "message", messageID, "error", err)
	}
}

// serveInspect reports what a stored snapshot contains.
//
// It is deliberately the inverse of the restore endpoint: read-only, no
// confirmation token, no mode, and nothing in the catalog is touched. Someone
// staring at a disaster needs to be able to look inside a snapshot before they
// decide, and they need to find out that it is encrypted while they still have
// the passphrase at hand.
func (h *backupHandler) serveInspect(w http.ResponseWriter, r *http.Request, userID int64) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.")
		return
	}
	var body struct {
		MessageID int64  `json:"messageId"`
		Password  string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, configBodyLimit)).Decode(&body); err != nil || body.MessageID <= 0 {
		writeJSONMessage(w, http.StatusBadRequest, "invalid_request",
			`Expected a JSON body of the form {"messageId": 1, "password": ""}.`)
		return
	}
	passphrase := body.Password
	if passphrase == "" {
		passphrase = h.store.Password()
	}
	target := h.ownerOrDefault(userID)

	summary, err := h.service.Inspect(r.Context(), target, body.MessageID, passphrase)
	if err != nil {
		switch {
		case errors.Is(err, backup.ErrSnapshotNotFound):
			writeJSONMessage(w, http.StatusNotFound, "not_found", "No snapshot has been recorded yet.")
		case errors.Is(err, backup.ErrPasswordRequired):
			writeJSONMessage(w, http.StatusPreconditionRequired, "password_required", err.Error())
		case errors.Is(err, backup.ErrPasswordRejected):
			writeJSONMessage(w, http.StatusConflict, "password_rejected", err.Error())
		case errors.Is(err, backup.ErrEnvelopeInvalid):
			writeJSONMessage(w, http.StatusBadRequest, "invalid_snapshot", err.Error())
		default:
			h.logger.Error("backup.inspect.failed", "message", body.MessageID, "error", err)
			writeJSONMessage(w, http.StatusBadGateway, "load_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// serveRestore applies a snapshot over the live catalog.
//
// This is the one endpoint that can destroy a drive, so it demands three things
// that no accident supplies: the confirmation token, an explicit mode, and an
// authenticated owner. It also runs inline rather than on the job queue, because
// the person clicking the button needs to see what actually happened -- a queued
// restore whose result nobody reads is how a surprise becomes permanent.
func (h *backupHandler) serveRestore(w http.ResponseWriter, r *http.Request, userID int64) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.")
		return
	}
	var body struct {
		MessageID int64  `json:"messageId"`
		Mode      string `json:"mode"`
		Confirm   string `json:"confirm"`
		// Password unseals a snapshot that was written with a different
		// passphrase than the settings now hold. Empty falls back to the stored
		// one, which is the normal case.
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, configBodyLimit)).Decode(&body); err != nil {
		writeJSONMessage(w, http.StatusBadRequest, "invalid_request",
			fmt.Sprintf(`Expected a JSON body of the form {"messageId": 1, "mode": "overwrite", "confirm": %q}.`, restoreConfirm))
		return
	}
	if strings.TrimSpace(body.Confirm) != restoreConfirm {
		writeJSONMessage(w, http.StatusPreconditionRequired, "confirmation_required",
			fmt.Sprintf("Restoring rewrites the drive. Send %q in the confirm field to proceed.", restoreConfirm))
		return
	}
	if body.MessageID <= 0 {
		writeJSONMessage(w, http.StatusBadRequest, "invalid_request", "Pass the messageId of the snapshot to apply.")
		return
	}
	// An omitted mode means "use the one saved in the settings", which is the
	// choice the user already made deliberately; an explicit mode is honoured so
	// a one-off merge does not have to change the setting.
	mode := h.store.RestoreMode()
	if strings.TrimSpace(body.Mode) != "" {
		parsed, err := backup.ParseRestoreMode(body.Mode)
		if err != nil {
			writeJSONMessage(w, http.StatusBadRequest, "invalid_restore_mode", err.Error())
			return
		}
		mode = parsed
	}
	target := h.ownerOrDefault(userID)

	// Read and validate first: a snapshot that cannot be fetched, cannot be
	// unsealed or does not parse must fail before anything has been deleted.
	passphrase := body.Password
	if passphrase == "" {
		passphrase = h.store.Password()
	}
	index, err := h.service.LoadSnapshotWithPassword(r.Context(), target, body.MessageID, passphrase)
	if err != nil {
		switch {
		case errors.Is(err, backup.ErrSnapshotNotFound):
			writeJSONMessage(w, http.StatusNotFound, "not_found", "No snapshot has been recorded yet.")
		case errors.Is(err, backup.ErrPasswordRequired):
			writeJSONMessage(w, http.StatusPreconditionRequired, "password_required", err.Error())
		case errors.Is(err, backup.ErrPasswordRejected):
			writeJSONMessage(w, http.StatusConflict, "password_rejected", err.Error())
		case errors.Is(err, backup.ErrEnvelopeInvalid),
			errors.Is(err, backup.ErrPasswordTooShort),
			errors.Is(err, backup.ErrPasswordTooLong),
			errors.Is(err, backup.ErrPasswordBlank):
			writeJSONMessage(w, http.StatusBadRequest, "invalid_snapshot", err.Error())
		default:
			h.logger.Error("backup.restore.load_failed", "message", body.MessageID, "error", err)
			writeJSONMessage(w, http.StatusBadGateway, "load_failed", err.Error())
		}
		return
	}
	if err := index.Validate(target); err != nil {
		writeJSONMessage(w, http.StatusBadRequest, "invalid_snapshot", err.Error())
		return
	}

	h.logger.Warn("backup.restore.starting",
		"user", target, "message", body.MessageID, "mode", mode,
		"files", len(index.Files), "parts", len(index.Parts))

	result, err := h.restorer.Apply(r.Context(), target, index, mode)
	if err != nil {
		switch {
		case errors.Is(err, backup.ErrUploadsInProgress):
			writeJSONMessage(w, http.StatusConflict, "uploads_in_progress", err.Error())
		case errors.Is(err, backup.ErrInvalidIndex):
			writeJSONMessage(w, http.StatusBadRequest, "invalid_snapshot", err.Error())
		case errors.Is(err, backup.ErrCatalogCycle):
			writeJSONMessage(w, http.StatusConflict, "catalog_cycle", err.Error())
		case errors.Is(err, backup.ErrRestoreUnavailable):
			writeJSONMessage(w, http.StatusServiceUnavailable, "unavailable", err.Error())
		default:
			h.logger.Error("backup.restore.failed", "error", err)
			writeJSONMessage(w, http.StatusInternalServerError, "restore_failed", err.Error())
		}
		return
	}
	h.logger.Warn("backup.restore.completed",
		"user", target, "mode", mode, "files", result.Files, "parts", result.Parts,
		"deleted", result.DeletedFiles, "orphans", len(result.Orphans))
	writeJSON(w, http.StatusOK, result)
}

// snapshot renders the current settings. Settings() recomputes the interval and
// the restore mode, so a direct struct build is the only place they could drift.
func (h *backupHandler) snapshot() settingsResponse {
	settings := h.store.Settings()
	response := settingsResponse{
		Enabled:         settings.Enabled,
		Interval:        settings.Interval,
		ChannelID:       settings.ChannelID,
		LastMessageID:   settings.LastMessageID,
		LastSize:        settings.LastSize,
		RestoreMode:     settings.RestoreMode,
		RestoreModes:    restoreModeNames(),
		PasswordSet:     settings.PasswordSet,
		DefaultInterval: backup.DefaultInterval.String(),
		MinimumInterval: backup.MinInterval.String(),

		MinimumPasswordLength: backup.MinimumPasswordLength,
	}
	if !settings.LastBackupAt.IsZero() {
		at := settings.LastBackupAt
		response.LastBackupAt = &at
	}
	return response
}

func restoreModeNames() []string {
	names := make([]string, 0, len(backup.RestoreModes))
	for _, mode := range backup.RestoreModes {
		names = append(names, string(mode))
	}
	return names
}

// newBackupStore loads the snapshot settings file. A missing file is the
// documented default (disabled), so a first run is silent.
func newBackupStore(logger *slog.Logger) (*backup.Store, error) {
	store := backup.NewStore(backup.DefaultConfigPath(), logger)
	if err := store.Load(); err != nil {
		return nil, fmt.Errorf("load backup settings: %w", err)
	}
	return store, nil
}

// ownerOrDefault picks whose index a manual action writes. The recorded owner
// wins, because that is the account whose Telegram session holds the snapshot
// channel; a caller is only used before the settings page has ever been saved.
func (h *backupHandler) ownerOrDefault(userID int64) int64 {
	if owner := h.store.Settings().OwnerUserID; owner > 0 {
		return owner
	}
	return userID
}
