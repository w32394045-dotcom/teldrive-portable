package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdrive/teldrive/v2/internal/authn"
	"github.com/tgdrive/teldrive/v2/internal/backup"
	"github.com/tgdrive/teldrive/v2/internal/jobs"
)

// fakeBackupScheduler records what the settings endpoint asked the job runtime to
// do. The schedule itself is River's business and is covered in internal/jobs;
// what matters here is that the HTTP layer asks for the right thing.
type fakeBackupScheduler struct {
	synced   []syncCall
	inserted []int64
	err      error
}

type syncCall struct {
	enabled  bool
	interval time.Duration
	userID   int64
}

func (f *fakeBackupScheduler) SyncBackupPeriodicJob(_ context.Context, enabled bool, interval time.Duration, userID int64) error {
	f.synced = append(f.synced, syncCall{enabled: enabled, interval: interval, userID: userID})
	return f.err
}

func (f *fakeBackupScheduler) InsertBackup(_ context.Context, userID int64) (jobs.Job, error) {
	f.inserted = append(f.inserted, userID)
	return jobs.Job{ID: 99}, f.err
}

// newTestBackupHandler builds the handler with a real settings file and stub
// collaborators. The service and restorer are never reached by the paths these
// tests exercise; the tests that do reach them would need a database, and those
// cases live in the integration suite.
func newTestBackupHandler(t *testing.T, configure func(*backup.Store)) (*backupHandler, *backup.Store, *fakeBackupScheduler) {
	t.Helper()
	store := backup.NewStore(filepath.Join(t.TempDir(), "backup.json"), nil)
	if err := store.Load(); err != nil {
		t.Fatalf("load store: %v", err)
	}
	if configure != nil {
		configure(store)
	}
	scheduler := &fakeBackupScheduler{}
	handler, err := newBackupHandler(backupConfig{
		service:     backup.NewService(nil, nil, store, nil),
		restorer:    backup.NewRestorer(nil, nil),
		store:       store,
		scheduler:   scheduler,
		authService: &authn.Service{},
		logger:      nil,
	})
	if err != nil {
		t.Fatalf("newBackupHandler: %v", err)
	}
	return handler, store, scheduler
}

func postRestore(t *testing.T, handler *backupHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, backupRestorePath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.serveRestore(recorder, request, 42)
	return recorder
}

func errorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error body %q: %v", recorder.Body.String(), err)
	}
	return payload.Error.Code
}

// TestRestoreRequiresTheConfirmationToken is the guard that makes the button the
// only way in: a request that does not name what it is doing cannot delete a
// drive, however well-formed it is otherwise.
func TestRestoreRequiresTheConfirmationToken(t *testing.T) {
	handler, _, _ := newTestBackupHandler(t, nil)
	cases := map[string]string{
		"absent":  `{"messageId": 1, "mode": "overwrite"}`,
		"null":    `{"messageId": 1, "mode": "overwrite", "confirm": null}`,
		"wrong":   `{"messageId": 1, "mode": "overwrite", "confirm": "yes"}`,
		"shouted": `{"messageId": 1, "mode": "overwrite", "confirm": "RESTORE"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := postRestore(t, handler, body)
			if recorder.Code != http.StatusPreconditionRequired {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, http.StatusPreconditionRequired, recorder.Body)
			}
			if code := errorCode(t, recorder); code != "confirmation_required" {
				t.Fatalf("code = %q, want confirmation_required", code)
			}
			if !strings.Contains(recorder.Body.String(), restoreConfirm) {
				t.Fatalf("the refusal should name the token to send: %s", recorder.Body)
			}
		})
	}
}

// TestRestoreRefusesABooleanConfirmation pins the reason the token is a word and
// not a flag: `"confirm": true` is exactly what a careless client sends, and it
// must not be mistaken for consent.
func TestRestoreRefusesABooleanConfirmation(t *testing.T) {
	handler, _, _ := newTestBackupHandler(t, nil)
	recorder := postRestore(t, handler, `{"messageId": 1, "mode": "overwrite", "confirm": true}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", recorder.Code, recorder.Body)
	}
	if code := errorCode(t, recorder); code != "invalid_request" {
		t.Fatalf("code = %q, want invalid_request", code)
	}
}

func TestRestoreRejectsAnUnknownMode(t *testing.T) {
	handler, _, _ := newTestBackupHandler(t, nil)
	recorder := postRestore(t, handler, `{"messageId": 1, "mode": "wipe", "confirm": "restore"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", recorder.Code, recorder.Body)
	}
	if code := errorCode(t, recorder); code != "invalid_restore_mode" {
		t.Fatalf("code = %q, want invalid_restore_mode", code)
	}
}

func TestRestoreRequiresASnapshotToApply(t *testing.T) {
	handler, _, _ := newTestBackupHandler(t, nil)
	for name, body := range map[string]string{
		"no message id": `{"mode": "overwrite", "confirm": "restore"}`,
		"negative":      `{"messageId": -1, "mode": "overwrite", "confirm": "restore"}`,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := postRestore(t, handler, body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", recorder.Code, recorder.Body)
			}
		})
	}
}

func TestRestoreRejectsAMalformedBody(t *testing.T) {
	handler, _, _ := newTestBackupHandler(t, nil)
	recorder := postRestore(t, handler, `{"messageId":`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", recorder.Code, recorder.Body)
	}
	if code := errorCode(t, recorder); code != "invalid_request" {
		t.Fatalf("code = %q, want invalid_request", code)
	}
}

func TestRestoreIsPostOnly(t *testing.T) {
	handler, _, _ := newTestBackupHandler(t, nil)
	request := httptest.NewRequest(http.MethodGet, backupRestorePath, nil)
	recorder := httptest.NewRecorder()
	handler.serveRestore(recorder, request, 42)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
	if allow := recorder.Header().Get("Allow"); allow != "POST" {
		t.Fatalf("Allow = %q, want POST", allow)
	}
}

// TestSettingsUpdateKeepsWhatItWasNotGiven matters because the page has three
// controls and each one saves on its own: changing the restore mode must not
// quietly turn backups off or reset the interval.
func TestSettingsUpdateKeepsWhatItWasNotGiven(t *testing.T) {
	handler, store, scheduler := newTestBackupHandler(t, func(store *backup.Store) {
		if err := store.Configure(true, "24h", 0); err != nil {
			t.Fatalf("seed settings: %v", err)
		}
	})

	request := httptest.NewRequest(http.MethodPut, backupSettingsPath, strings.NewReader(`{"restoreMode": "merge"}`))
	recorder := httptest.NewRecorder()
	handler.serveSettings(recorder, request, 42)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body)
	}

	settings := store.Settings()
	if !settings.Enabled {
		t.Fatal("saving the restore mode turned backups off")
	}
	if settings.Interval != "24h" {
		t.Fatalf("interval = %q, want 24h", settings.Interval)
	}
	if settings.RestoreMode != string(backup.RestoreMerge) {
		t.Fatalf("restore mode = %q, want merge", settings.RestoreMode)
	}
	if settings.OwnerUserID != 42 {
		t.Fatalf("owner = %d, want the caller", settings.OwnerUserID)
	}
	if len(scheduler.synced) != 1 {
		t.Fatalf("schedule syncs = %d, want 1", len(scheduler.synced))
	}
	if got := scheduler.synced[0]; !got.enabled || got.interval != 24*time.Hour || got.userID != 42 {
		t.Fatalf("synced %+v, want an enabled 24h schedule for user 42", got)
	}
}

func TestSettingsRejectsAnUnknownRestoreMode(t *testing.T) {
	handler, store, scheduler := newTestBackupHandler(t, nil)
	request := httptest.NewRequest(http.MethodPut, backupSettingsPath, strings.NewReader(`{"restoreMode": "destroy"}`))
	recorder := httptest.NewRecorder()
	handler.serveSettings(recorder, request, 42)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", recorder.Code, recorder.Body)
	}
	if code := errorCode(t, recorder); code != "invalid_restore_mode" {
		t.Fatalf("code = %q, want invalid_restore_mode", code)
	}
	if store.Settings().RestoreMode != string(backup.RestoreOverwrite) {
		t.Fatal("a rejected mode changed the stored setting")
	}
	if len(scheduler.synced) != 0 {
		t.Fatal("a rejected update should not have touched the schedule")
	}
}

func TestSettingsRejectsAnEmptyBody(t *testing.T) {
	handler, _, _ := newTestBackupHandler(t, nil)
	request := httptest.NewRequest(http.MethodPut, backupSettingsPath, strings.NewReader(`{}`))
	recorder := httptest.NewRecorder()
	handler.serveSettings(recorder, request, 42)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", recorder.Code, recorder.Body)
	}
}

// TestSettingsPublishesTheRestoreChoices keeps the documented list and the
// parser in step: the UI renders whatever it is given, so an entry the parser
// rejects would be a button that always fails.
func TestSettingsPublishesTheRestoreChoices(t *testing.T) {
	handler, _, _ := newTestBackupHandler(t, nil)
	request := httptest.NewRequest(http.MethodGet, backupSettingsPath, nil)
	recorder := httptest.NewRecorder()
	handler.serveSettings(recorder, request, 42)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var payload settingsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if len(payload.RestoreModes) != len(backup.RestoreModes) {
		t.Fatalf("restoreModes = %v, want %v", payload.RestoreModes, backup.RestoreModes)
	}
	for _, name := range payload.RestoreModes {
		if _, err := backup.ParseRestoreMode(name); err != nil {
			t.Fatalf("published mode %q is not accepted by the parser: %v", name, err)
		}
	}
	if payload.RestoreMode == "" {
		t.Fatal("the current restore mode should never be blank")
	}
	if payload.MinimumInterval != backup.MinInterval.String() || payload.DefaultInterval != backup.DefaultInterval.String() {
		t.Fatalf("interval bounds = %q/%q", payload.MinimumInterval, payload.DefaultInterval)
	}
}

// TestSettingsJSONKeysMatchTheUIContract pins the wire names, not the Go field
// names. The page validates this payload with a schema it cannot check at
// compile time, so a renamed field would surface as "the response is malformed"
// in the browser rather than as a build failure.
func TestSettingsJSONKeysMatchTheUIContract(t *testing.T) {
	handler, _, _ := newTestBackupHandler(t, nil)
	request := httptest.NewRequest(http.MethodGet, backupSettingsPath, nil)
	recorder := httptest.NewRecorder()
	handler.serveSettings(recorder, request, 42)

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	for _, key := range []string{
		"enabled", "interval", "restoreMode", "restoreModes", "defaultInterval", "minimumInterval",
	} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("settings payload is missing %q: %v", key, payload)
		}
	}
	// These are omitempty, so they must be absent rather than zero when unset:
	// the UI schema declares them optional and reads undefined as "never".
	for _, key := range []string{"channelId", "lastMessageId", "lastSize", "lastBackupAt"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("%q should be omitted while unset, got %v", key, payload[key])
		}
	}
}

// TestSettingsJSONCarriesTheLastBackup covers the other half of the contract:
// once a snapshot has run, the UI reads these fields to render the last-backup
// line without asking the channel.
func TestSettingsJSONCarriesTheLastBackup(t *testing.T) {
	handler, store, _ := newTestBackupHandler(t, nil)
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := store.RecordSnapshot(900, "teldrive-backup", 42, 2048, at); err != nil {
		t.Fatalf("record snapshot: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, backupSettingsPath, nil)
	recorder := httptest.NewRecorder()
	handler.serveSettings(recorder, request, 42)

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if payload["lastMessageId"] != float64(42) {
		t.Fatalf("lastMessageId = %v, want 42", payload["lastMessageId"])
	}
	if payload["lastSize"] != float64(2048) {
		t.Fatalf("lastSize = %v, want 2048", payload["lastSize"])
	}
	// The timestamp has to be a string the browser can parse.
	stamp, ok := payload["lastBackupAt"].(string)
	if !ok {
		t.Fatalf("lastBackupAt = %#v, want a string", payload["lastBackupAt"])
	}
	if _, err := time.Parse(time.RFC3339, stamp); err != nil {
		t.Fatalf("lastBackupAt %q is not RFC3339: %v", stamp, err)
	}
}

// TestIsBackupPathMatchesExactly keeps a lookalike path from being routed into
// the backup handler, where the prefix would decide which endpoint runs.
func TestIsBackupPathMatchesExactly(t *testing.T) {
	for _, path := range []string{backupSettingsPath, backupRunPath, backupScanPath, backupSnapshotPath, backupRestorePath} {
		if !isBackupPath(path) {
			t.Fatalf("isBackupPath(%q) = false, want true", path)
		}
	}
	for _, path := range []string{
		"/api/backup", "/api/backup/", "/api/backup/restore/", "/api/backup-config/extra",
		"/api/backup/restores", "/api/backups/run", "/webdav", "/",
	} {
		if isBackupPath(path) {
			t.Fatalf("isBackupPath(%q) = true, want false", path)
		}
	}
}
