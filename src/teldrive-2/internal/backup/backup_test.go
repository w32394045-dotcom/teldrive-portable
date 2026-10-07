package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tgdrive/teldrive/v2/internal/db/sqlcgen"
	"github.com/tgdrive/teldrive/v2/internal/telegramstore"
)

// --------------------------------------------------------------------------
// fakes
// --------------------------------------------------------------------------

type fakeQuerier struct {
	channels  []*sqlcgen.Channel
	files     []*sqlcgen.File
	parts     []*sqlcgen.FilePart
	fileCalls int
	partCalls int
}

func (f *fakeQuerier) ListChannelsForOrphanCleanup(context.Context) ([]*sqlcgen.Channel, error) {
	return f.channels, nil
}

// ListFilesAdvanced mirrors the id-cursor paging the real query performs, so a
// caller that forgets to advance the cursor loops forever and the test hangs
// rather than passing by accident.
func (f *fakeQuerier) ListFilesAdvanced(_ context.Context, arg sqlcgen.ListFilesAdvancedParams) ([]*sqlcgen.File, error) {
	f.fileCalls++
	ordered := append([]*sqlcgen.File(nil), f.files...)
	sort.Slice(ordered, func(i, j int) bool {
		return uuid.UUID(ordered[i].ID.Bytes).String() < uuid.UUID(ordered[j].ID.Bytes).String()
	})
	var after string
	if arg.AfterID.Valid {
		after = uuid.UUID(arg.AfterID.Bytes).String()
	}
	var out []*sqlcgen.File
	for _, file := range ordered {
		id := uuid.UUID(file.ID.Bytes).String()
		if after != "" && id <= after {
			continue
		}
		out = append(out, file)
		if int32(len(out)) >= arg.PageSize {
			break
		}
	}
	return out, nil
}

func (f *fakeQuerier) ListFilePartsByFileIDs(_ context.Context, ids []pgtype.UUID) ([]*sqlcgen.FilePart, error) {
	f.partCalls++
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[uuid.UUID(id.Bytes).String()] = true
	}
	var out []*sqlcgen.FilePart
	for _, part := range f.parts {
		if wanted[uuid.UUID(part.FileID.Bytes).String()] {
			out = append(out, part)
		}
	}
	return out, nil
}

type fakeStorage struct {
	createdChannels int
	channelID       int64
	uploads         int
	lastChannel     int64
	lastName        string
	lastPayload     []byte
	listCalls       int
	messages        []telegramstore.DocumentMessage
}

func (f *fakeStorage) Upload(_ context.Context, request telegramstore.UploadRequest) (telegramstore.StoredPart, error) {
	f.uploads++
	f.lastChannel = request.ChannelID
	f.lastName = request.Name
	payload, err := io.ReadAll(request.Reader)
	if err != nil {
		return telegramstore.StoredPart{}, err
	}
	f.lastPayload = payload
	return telegramstore.StoredPart{ChannelID: request.ChannelID, MessageID: int64(100 + f.uploads), Size: int64(len(payload))}, nil
}

func (f *fakeStorage) OpenRange(context.Context, telegramstore.RangeRequest) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(f.lastPayload)), nil
}

func (f *fakeStorage) DeleteMessages(context.Context, int64, int64, []int64) error { return nil }

func (f *fakeStorage) CopyPart(context.Context, int64, int64, int64, int64) (telegramstore.StoredPart, error) {
	return telegramstore.StoredPart{}, nil
}

func (f *fakeStorage) CreateChannel(_ context.Context, _ int64, _ string) (telegramstore.Channel, error) {
	f.createdChannels++
	f.channelID = 900 + int64(f.createdChannels)
	return telegramstore.Channel{ID: f.channelID, Name: ChannelName}, nil
}

func (f *fakeStorage) DeleteChannel(context.Context, int64, int64) error { return nil }

func (f *fakeStorage) ListDocumentMessages(_ context.Context, request telegramstore.ListDocumentMessagesRequest) (telegramstore.DocumentMessagePage, error) {
	f.listCalls++
	return telegramstore.DocumentMessagePage{Messages: f.messages, BeforeID: request.BeforeID, Exhausted: true}, nil
}

// --------------------------------------------------------------------------
// helpers
// --------------------------------------------------------------------------

func uuidValue(value uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: value, Valid: true} }

func testFile(t *testing.T, name string, kind sqlcgen.FileKind) *sqlcgen.File {
	t.Helper()
	return &sqlcgen.File{
		ID:        uuidValue(uuid.New()),
		UserID:    7,
		Name:      name,
		Kind:      kind,
		Status:    sqlcgen.FileStatusActive,
		Size:      pgtype.Int8{Int64: 10, Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: time.Unix(0, 0), Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: time.Unix(0, 0), Valid: true},
	}
}

// --------------------------------------------------------------------------
// store
// --------------------------------------------------------------------------

func TestStoreDefaultsToDisabledWeekly(t *testing.T) {
	t.Parallel()
	store := NewStore(filepath.Join(t.TempDir(), configFileName), nil)
	if err := store.Load(); err != nil {
		t.Fatalf("Load() on a missing file returned %v", err)
	}
	settings := store.Settings()
	if settings.Enabled {
		t.Error("backups should start disabled")
	}
	if settings.Interval != DefaultInterval.String() {
		t.Errorf("interval = %q, want %q", settings.Interval, DefaultInterval)
	}
}

func TestStoreRejectsIntervalBelowTheMinimum(t *testing.T) {
	t.Parallel()
	store := NewStore(filepath.Join(t.TempDir(), configFileName), nil)
	if err := store.Configure(true, "5s", 0); err != nil {
		t.Fatalf("Configure() returned %v", err)
	}
	// A five-second schedule would hammer Telegram; the store falls back rather
	// than leaving backups silently unscheduled.
	if got := store.Settings().Interval; got != DefaultInterval.String() {
		t.Errorf("interval = %q, want the default %q", got, DefaultInterval)
	}
	if store.Interval() != DefaultInterval {
		t.Errorf("Interval() = %v, want %v", store.Interval(), DefaultInterval)
	}
}

func TestStoreRoundTripsThroughDisk(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), configFileName)
	store := NewStore(path, nil)
	if err := store.Configure(true, "24h", 7); err != nil {
		t.Fatalf("Configure() returned %v", err)
	}
	// A later toggle without a user must not orphan the schedule.
	if err := store.Configure(false, "24h", 0); err != nil {
		t.Fatalf("Configure() returned %v", err)
	}
	if err := store.Configure(true, "24h", 0); err != nil {
		t.Fatalf("Configure() returned %v", err)
	}
	when := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if err := store.RecordSnapshot(555, ChannelName, 42, 1234, when); err != nil {
		t.Fatalf("RecordSnapshot() returned %v", err)
	}

	reloaded := NewStore(path, nil)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load() returned %v", err)
	}
	settings := reloaded.Settings()
	if !settings.Enabled || settings.Interval != "24h" {
		t.Errorf("settings = %+v, want enabled at 24h", settings)
	}
	if settings.OwnerUserID != 7 {
		t.Errorf("OwnerUserID = %d, want 7: a toggle without a user must keep the owner", settings.OwnerUserID)
	}
	if settings.ChannelID != 555 || settings.LastMessageID != 42 || settings.LastSize != 1234 {
		t.Errorf("snapshot record lost: %+v", settings)
	}
	if !settings.LastBackupAt.Equal(when) {
		t.Errorf("LastBackupAt = %v, want %v", settings.LastBackupAt, when)
	}
}

func TestStoreDegradesOnGarbageInsteadOfFailing(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), configFileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(path, nil)
	if err := store.Load(); err != nil {
		t.Fatalf("Load() should tolerate a corrupt file, got %v", err)
	}
	if store.Settings().Enabled {
		t.Error("a corrupt file must fall back to disabled")
	}
}

// --------------------------------------------------------------------------
// index
// --------------------------------------------------------------------------

func TestBuildIndexPagesTheWholeDriveAndCarriesParts(t *testing.T) {
	t.Parallel()
	queries := &fakeQuerier{
		channels: []*sqlcgen.Channel{
			{ChannelID: 11, UserID: 7, Name: "mine", Selected: true, Health: sqlcgen.ChannelHealthHealthy},
			{ChannelID: 12, UserID: 99, Name: "someone else"},
		},
	}
	const total = indexPageSize + 250
	for range total {
		queries.files = append(queries.files, testFile(t, "file", sqlcgen.FileKindFile))
	}
	queries.files[0].Kind = sqlcgen.FileKindFolder
	queries.parts = append(queries.parts, &sqlcgen.FilePart{
		FileID: queries.files[1].ID, PartNo: 1, ChannelID: 11, MessageID: 777,
		PlainSize: pgtype.Int8{Int64: 512, Valid: true},
	})

	index, err := BuildIndex(context.Background(), queries, 7, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("BuildIndex() returned %v", err)
	}
	if queries.fileCalls < 2 {
		t.Errorf("file query ran %d time(s); paging did not advance", queries.fileCalls)
	}
	if index.Counts.Files != int(total) {
		t.Errorf("files = %d, want %d", index.Counts.Files, total)
	}
	if index.Counts.Folders != 1 {
		t.Errorf("folders = %d, want 1", index.Counts.Folders)
	}
	if index.Counts.Parts != 1 {
		t.Errorf("parts = %d, want 1", index.Counts.Parts)
	}
	// Only this user's channel belongs in the document.
	if index.Counts.Channels != 1 || index.Channels[0].ChannelID != 11 {
		t.Errorf("channels = %+v, want only channel 11", index.Channels)
	}
	if index.Format != IndexFormat || index.Version != IndexVersion {
		t.Errorf("format/version = %q/%d", index.Format, index.Version)
	}
	// The mapping is the whole point of the file, so it must survive encoding.
	encoded, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("Marshal() returned %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"messageId":777`)) {
		t.Error("encoded index lost the part-to-message mapping")
	}
}

// --------------------------------------------------------------------------
// service
// --------------------------------------------------------------------------

func newTestService(t *testing.T, queries *fakeQuerier, storage *fakeStorage) (*Service, *Store) {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), configFileName), nil)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	return NewService(queries, storage, store, nil), store
}

func TestCreateUploadsTheIndexAndRemembersTheChannel(t *testing.T) {
	t.Parallel()
	queries := &fakeQuerier{channels: []*sqlcgen.Channel{{ChannelID: 11, UserID: 7, Name: "mine"}}}
	queries.files = append(queries.files, testFile(t, "holiday.mkv", sqlcgen.FileKindFile))
	storage := &fakeStorage{}
	service, store := newTestService(t, queries, storage)

	result, err := service.Create(context.Background(), 7)
	if err != nil {
		t.Fatalf("Create() returned %v", err)
	}
	if storage.uploads != 1 {
		t.Fatalf("uploads = %d, want 1", storage.uploads)
	}
	if storage.createdChannels != 1 {
		t.Errorf("channels created = %d, want 1", storage.createdChannels)
	}
	if !bytes.HasPrefix([]byte(storage.lastName), []byte(documentPrefix)) {
		t.Errorf("document name %q does not identify the backup", storage.lastName)
	}
	if store.ChannelID() == 0 || store.Settings().LastMessageID != result.MessageID {
		t.Errorf("snapshot was not recorded: %+v", store.Settings())
	}

	var decoded Index
	if err := json.Unmarshal(storage.lastPayload, &decoded); err != nil {
		t.Fatalf("uploaded payload is not the index: %v", err)
	}
	if decoded.Counts.Files != 1 || decoded.UserID != 7 {
		t.Errorf("uploaded index = %+v", decoded.Counts)
	}
}

// The scheduled path must not enumerate Telegram: that is the expensive call,
// and running it on a timer is what invites FLOOD_WAIT. Only the explicit scan
// action may do it.
func TestCreateNeverScansTheChannel(t *testing.T) {
	t.Parallel()
	queries := &fakeQuerier{}
	queries.files = append(queries.files, testFile(t, "a", sqlcgen.FileKindFile))
	storage := &fakeStorage{}
	service, _ := newTestService(t, queries, storage)

	if _, err := service.Create(context.Background(), 7); err != nil {
		t.Fatalf("first Create() returned %v", err)
	}
	if _, err := service.Create(context.Background(), 7); err != nil {
		t.Fatalf("second Create() returned %v", err)
	}

	if storage.listCalls != 0 {
		t.Errorf("Create() listed channel documents %d time(s); the scheduled path must never scan", storage.listCalls)
	}
	if storage.createdChannels != 1 {
		t.Errorf("channels created = %d, want 1: a remembered channel must be reused", storage.createdChannels)
	}
	if storage.lastChannel != storage.channelID {
		t.Errorf("upload went to channel %d, want %d", storage.lastChannel, storage.channelID)
	}
}

func TestScanWalksTheChannelOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	storage := &fakeStorage{messages: []telegramstore.DocumentMessage{
		{ID: 5, CreatedAt: time.Unix(100, 0)},
		{ID: 4, CreatedAt: time.Unix(50, 0)},
	}}
	store := NewStore(filepath.Join(t.TempDir(), configFileName), nil)
	if err := store.RecordSnapshot(900, ChannelName, 5, 10, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	service := NewService(&fakeQuerier{}, storage, store, nil)

	entries, err := service.Scan(context.Background(), 7)
	if err != nil {
		t.Fatalf("Scan() returned %v", err)
	}
	if storage.listCalls != 1 {
		t.Errorf("list calls = %d, want 1", storage.listCalls)
	}
	if len(entries) != 2 || entries[0].ChannelID != 900 {
		t.Errorf("entries = %+v", entries)
	}
}

func TestScanWithoutAChannelReportsNothing(t *testing.T) {
	t.Parallel()
	storage := &fakeStorage{}
	service, _ := newTestService(t, &fakeQuerier{}, storage)

	entries, err := service.Scan(context.Background(), 7)
	if err != nil {
		t.Fatalf("Scan() returned %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %+v, want none", entries)
	}
	if storage.listCalls != 0 {
		t.Error("Scan() called Telegram even though no backup channel exists yet")
	}
}

func TestCreateWithoutDependenciesIsRejected(t *testing.T) {
	t.Parallel()
	if _, err := (&Service{}).Create(context.Background(), 7); err == nil {
		t.Error("Create() on an unconfigured service must fail")
	}
	if _, err := (&Service{}).Create(context.Background(), 0); err == nil {
		t.Error("Create() with an invalid user must fail")
	}
}
