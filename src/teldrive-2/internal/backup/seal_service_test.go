package backup

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdrive/teldrive/v2/internal/db/sqlcgen"
)

// sealedService wires a service whose store already carries a passphrase, which
// is the state a user is in after enabling sealing on the settings page.
func sealedService(t *testing.T, password string) (*Service, *Store, *fakeStorage) {
	t.Helper()
	queries := &fakeQuerier{channels: []*sqlcgen.Channel{{ChannelID: 11, UserID: 7, Name: "mine"}}}
	queries.files = append(queries.files, testFile(t, "holiday.mkv", sqlcgen.FileKindFile))
	storage := &fakeStorage{}
	service, store := newTestService(t, queries, storage)
	if password != "" {
		if err := store.SetPassword(password); err != nil {
			t.Fatalf("SetPassword: %v", err)
		}
	}
	return service, store, storage
}

// TestCreateSealsTheSnapshotWhenAPasswordIsSet is the whole point of the
// feature: the channel only ever receives ciphertext, so the file can safely
// carry the keys that unlock everything else.
func TestCreateSealsTheSnapshotWhenAPasswordIsSet(t *testing.T) {
	service, _, storage := sealedService(t, testPassword)

	if _, err := service.Create(context.Background(), 7); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !IsSealed(storage.lastPayload) {
		t.Fatal("the uploaded snapshot is not sealed")
	}
	if strings.Contains(string(storage.lastPayload), "holiday.mkv") {
		t.Fatal("the sealed snapshot leaks a filename")
	}
	// The filename is the only thing a channel listing shows, so it has to say
	// what the file is.
	if !strings.HasSuffix(storage.lastName, ".sealed.json") {
		t.Fatalf("document name = %q, want a .sealed.json suffix", storage.lastName)
	}
}

func TestCreateLeavesTheSnapshotReadableWithoutAPassword(t *testing.T) {
	service, _, storage := sealedService(t, "")

	if _, err := service.Create(context.Background(), 7); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if IsSealed(storage.lastPayload) {
		t.Fatal("a snapshot without a passphrase must be stored as plain JSON")
	}
	var decoded Index
	if err := json.Unmarshal(storage.lastPayload, &decoded); err != nil {
		t.Fatalf("a plain snapshot must stay a plain index: %v", err)
	}
	if !strings.HasSuffix(storage.lastName, ".json") || strings.HasSuffix(storage.lastName, ".sealed.json") {
		t.Fatalf("document name = %q, want a plain .json suffix", storage.lastName)
	}
}

// TestLoadSnapshotUnsealsWhatCreateSealed closes the loop through the real code
// path rather than through Seal/Unseal directly: what a scheduled backup wrote
// is what a disaster recovery reads.
func TestLoadSnapshotUnsealsWhatCreateSealed(t *testing.T) {
	service, _, _ := sealedService(t, testPassword)
	ctx := context.Background()

	created, err := service.Create(ctx, 7)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	index, err := service.LoadSnapshot(ctx, 7, created.MessageID)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if len(index.Files) != 1 || index.Files[0].Name != "holiday.mkv" {
		t.Fatalf("recovered index = %+v", index.Files)
	}
	if !index.Sealed {
		t.Fatal("the summary should report that the snapshot was sealed")
	}
	if err := index.Validate(7); err != nil {
		t.Fatalf("an unsealed snapshot must still validate: %v", err)
	}
}

// TestLoadSnapshotNeedsThePasswordForASealedSnapshot covers the failure a user
// actually hits: the passphrase was cleared, or this is a fresh install reading
// an old snapshot.
func TestLoadSnapshotNeedsThePasswordForASealedSnapshot(t *testing.T) {
	service, _, _ := sealedService(t, testPassword)
	ctx := context.Background()
	created, err := service.Create(ctx, 7)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Stand in for a user who cleared the passphrase after a backup was written.
	if err := service.store.SetPassword(""); err != nil {
		t.Fatalf("clear passphrase: %v", err)
	}
	if _, err := service.LoadSnapshot(ctx, 7, created.MessageID); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("err = %v, want ErrPasswordRequired", err)
	}

	// And the supplied passphrase still opens it, which is what the settings
	// page offers when the stored one no longer works.
	index, err := service.LoadSnapshotWithPassword(ctx, 7, created.MessageID, testPassword)
	if err != nil {
		t.Fatalf("LoadSnapshotWithPassword: %v", err)
	}
	if len(index.Files) != 1 {
		t.Fatalf("recovered %d files, want 1", len(index.Files))
	}
	if _, err := service.LoadSnapshotWithPassword(ctx, 7, created.MessageID, "wrong password here"); !errors.Is(err, ErrPasswordRejected) {
		t.Fatalf("err = %v, want ErrPasswordRejected", err)
	}
}

func TestInspectDescribesWithoutTouchingAnything(t *testing.T) {
	service, _, _ := sealedService(t, testPassword)
	ctx := context.Background()
	created, err := service.Create(ctx, 7)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	summary, err := service.Inspect(ctx, 7, created.MessageID, testPassword)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !summary.Sealed || summary.Format != IndexFormat || summary.Counts.Files != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	// A summary must not carry the document itself.
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("encode summary: %v", err)
	}
	if strings.Contains(string(encoded), "holiday.mkv") {
		t.Fatalf("the summary leaked the catalog: %s", encoded)
	}
}

func TestStorePasswordRoundTripsAndValidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.json")
	store := NewStore(path, nil)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if store.Settings().PasswordSet {
		t.Fatal("a fresh store must not claim a passphrase")
	}
	if err := store.SetPassword(testPassword); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if !store.Settings().PasswordSet || store.Password() != testPassword {
		t.Fatal("the passphrase was not stored")
	}
	reloaded := NewStore(path, nil)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Password() != testPassword {
		t.Fatalf("reloaded passphrase = %q", reloaded.Password())
	}
	// A passphrase the sealer would refuse must be refused here instead, where
	// the user can still see why.
	if err := store.SetPassword("short"); err == nil {
		t.Fatal("SetPassword accepted a passphrase Seal would reject")
	}
	if store.Password() != testPassword {
		t.Fatal("a rejected passphrase changed the stored one")
	}
	// Clearing is allowed: it is how sealing is turned off.
	if err := store.SetPassword(""); err != nil {
		t.Fatalf("SetPassword(\"\"): %v", err)
	}
	if store.Settings().PasswordSet {
		t.Fatal("clearing the passphrase did not turn sealing off")
	}
}

// TestSettingsNeverHandsOutThePassphrase is a guard against a future refactor
// adding the passphrase to the struct the API serialises.
func TestSettingsNeverHandsOutThePassphrase(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "backup.json"), nil)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.SetPassword(testPassword); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	encoded, err := json.Marshal(store.Settings())
	if err != nil {
		t.Fatalf("encode settings: %v", err)
	}
	if strings.Contains(string(encoded), testPassword) {
		t.Fatalf("the settings view contains the passphrase: %s", encoded)
	}
}
