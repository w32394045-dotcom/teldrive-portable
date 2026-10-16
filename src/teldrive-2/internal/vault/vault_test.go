package vault

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testKey = "correct-horse-battery"

func testStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir(), nil)
}

func sampleSecrets() Secrets {
	signing := "signing-key-material"
	data := "data-key-material"
	database := "postgres://user:secret@127.0.0.1:5433/teldrive"
	keys := map[string]string{"1": "part-key-one", "2": "part-key-two"}
	active := int32(2)
	return Secrets{
		SigningKey:       &signing,
		DataKey:          &data,
		Database:         &database,
		EncryptionKeys:   &keys,
		ActiveKeyVersion: &active,
	}
}

func TestSealAndOpenRoundTrip(t *testing.T) {
	store := testStore(t)
	if store.Exists() {
		t.Fatal("a fresh store should have no vault")
	}
	if err := store.Seal(sampleSecrets(), testKey, ModeSaved); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !store.Exists() {
		t.Fatal("Exists should report the sealed file")
	}

	// Opening with no key at all is the double-click path: it must find the
	// remembered one.
	opened, err := store.Open("")
	if err != nil {
		t.Fatalf("Open with the remembered key: %v", err)
	}
	if opened.SigningKey == nil || *opened.SigningKey != *sampleSecrets().SigningKey {
		t.Fatalf("signing key = %v", opened.SigningKey)
	}
	if opened.EncryptionKeys == nil || (*opened.EncryptionKeys)["2"] != "part-key-two" {
		t.Fatalf("encryption keys = %v", opened.EncryptionKeys)
	}
	if opened.ActiveKeyVersion == nil || *opened.ActiveKeyVersion != 2 {
		t.Fatalf("active key version = %v", opened.ActiveKeyVersion)
	}
	if opened.Database == nil || !strings.Contains(*opened.Database, "secret") {
		t.Fatalf("database url = %v", opened.Database)
	}
}

// TestSealedFileDoesNotLeakTheSecrets is the whole point: the data directory is
// the thing people copy to another machine.
func TestSealedFileDoesNotLeakTheSecrets(t *testing.T) {
	store := testStore(t)
	if err := store.Seal(sampleSecrets(), testKey, ModeSaved); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	document, err := os.ReadFile(filepath.Join(store.dataDir, vaultFileName))
	if err != nil {
		t.Fatalf("read the vault: %v", err)
	}
	for _, secret := range []string{"signing-key-material", "data-key-material", "part-key-one", "postgres://"} {
		if strings.Contains(string(document), secret) {
			t.Fatalf("the sealed file contains %q", secret)
		}
	}
}

func TestOpenRejectsTheWrongKey(t *testing.T) {
	store := testStore(t)
	if err := store.Seal(sampleSecrets(), testKey, ModeSaved); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := store.Open("not the key at all"); !errors.Is(err, ErrKeyRejected) {
		t.Fatalf("err = %v, want ErrKeyRejected", err)
	}
}

// TestPromptModeKeepsNoKeyOnDisk is the mode for a machine that is not trusted to
// remember the key: after sealing, the data directory must not contain it.
func TestPromptModeKeepsNoKeyOnDisk(t *testing.T) {
	store := testStore(t)
	if err := store.Seal(sampleSecrets(), testKey, ModePrompt); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.dataDir, keyFileName)); !os.IsNotExist(err) {
		t.Fatalf("a key file was left behind in prompt mode: %v", err)
	}
	// Without the key the vault cannot be opened, and it says so rather than
	// returning empty secrets.
	if _, err := store.Open(""); !errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	// With the key it opens, which is what the shell will collect at startup.
	if _, err := store.Open(testKey); err != nil {
		t.Fatalf("Open with the typed key: %v", err)
	}

	entries, err := os.ReadDir(store.dataDir)
	if err != nil {
		t.Fatalf("read the data directory: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(names) != 1 || names[0] != vaultFileName {
		t.Fatalf("prompt mode left %v, want only %s", names, vaultFileName)
	}
}

func TestForgetRemovesTheKeyButKeepsTheVault(t *testing.T) {
	store := testStore(t)
	if err := store.Seal(sampleSecrets(), testKey, ModeSaved); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := store.Forget(); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if !store.Exists() {
		t.Fatal("Forget removed the vault itself")
	}
	if _, err := store.Open(""); !errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	if _, err := store.Open(testKey); err != nil {
		t.Fatalf("the vault must still open with its key: %v", err)
	}
}

func TestDestroyRemovesEverything(t *testing.T) {
	store := testStore(t)
	if err := store.Seal(sampleSecrets(), testKey, ModeSaved); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := store.Destroy(); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if store.Exists() {
		t.Fatal("Destroy left the vault behind")
	}
	if _, err := store.Open(testKey); !errors.Is(err, ErrNoVault) {
		t.Fatalf("err = %v, want ErrNoVault", err)
	}
	// Destroying twice is not an error: it is how the feature is turned off.
	if err := store.Destroy(); err != nil {
		t.Fatalf("second Destroy: %v", err)
	}
}

func TestOpenWithoutAVaultIsNotAnError(t *testing.T) {
	store := testStore(t)
	if _, err := store.Open(testKey); !errors.Is(err, ErrNoVault) {
		t.Fatalf("err = %v, want ErrNoVault", err)
	}
}

func TestSealRefusesUnusableInput(t *testing.T) {
	store := testStore(t)
	for name, testCase := range map[string]struct {
		secrets Secrets
		key     string
		mode    Mode
	}{
		"nothing to seal": {Secrets{}, testKey, ModeSaved},
		"empty key":       {sampleSecrets(), "", ModeSaved},
		"short key":       {sampleSecrets(), "short", ModeSaved},
		"unknown mode":    {sampleSecrets(), testKey, Mode("maybe")},
	} {
		t.Run(name, func(t *testing.T) {
			if err := store.Seal(testCase.secrets, testCase.key, testCase.mode); err == nil {
				t.Fatal("Seal accepted input it should have refused")
			}
			if store.Exists() {
				t.Fatal("a refused seal left a vault behind")
			}
		})
	}
}

// TestSealReplacesAtomically covers re-sealing, which is what changing the key
// does: the old vault must be readable until the new one is complete.
func TestSealReplacesAtomically(t *testing.T) {
	store := testStore(t)
	if err := store.Seal(sampleSecrets(), testKey, ModeSaved); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	const newKey = "a-completely-different-key"
	if err := store.Seal(sampleSecrets(), newKey, ModeSaved); err != nil {
		t.Fatalf("re-Seal: %v", err)
	}
	if _, err := store.Open(""); err != nil {
		t.Fatalf("the new key should open the vault: %v", err)
	}
	if _, err := store.Open(testKey); !errors.Is(err, ErrKeyRejected) {
		t.Fatalf("the old key still opens the vault: %v", err)
	}
	// No temporary files may be left in the data directory.
	entries, err := os.ReadDir(store.dataDir)
	if err != nil {
		t.Fatalf("read the data directory: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp") {
			t.Fatalf("a temporary file was left behind: %s", entry.Name())
		}
	}
}

func TestStatusDescribesWithoutOpening(t *testing.T) {
	store := testStore(t)
	if _, ok := store.Status(); ok {
		t.Fatal("a fresh store should report no vault")
	}
	if err := store.Seal(sampleSecrets(), testKey, ModeSaved); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	status, ok := store.Status()
	if !ok {
		t.Fatal("Status should report the vault")
	}
	if status.Mode != ModeSaved {
		t.Fatalf("mode = %q, want saved", status.Mode)
	}
	if status.KeyLength != len(testKey) {
		t.Fatalf("key length = %d, want %d", status.KeyLength, len(testKey))
	}
	if status.SealedAt.IsZero() {
		t.Fatal("SealedAt should be set")
	}
	// The status is what the page renders, so it must not carry the key itself.
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("encode status: %v", err)
	}
	if strings.Contains(string(encoded), testKey) {
		t.Fatalf("the status leaked the key: %s", encoded)
	}
}

// TestStatusReportsPromptWhenTheKeyIsGone keeps the page honest: a vault whose
// key was forgotten will ask at the next start, whatever mode was chosen.
func TestStatusReportsPromptWhenTheKeyIsGone(t *testing.T) {
	store := testStore(t)
	if err := store.Seal(sampleSecrets(), testKey, ModeSaved); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// A new store, as after Forgetting or after copying the vault alone.
	fresh := NewStore(store.dataDir, nil)
	if err := fresh.Forget(); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	status, ok := NewStore(store.dataDir, nil).Status()
	if !ok {
		t.Fatal("the vault is still there")
	}
	if status.Mode != ModePrompt {
		t.Fatalf("mode = %q, want prompt", status.Mode)
	}
	if status.KeyLength != 0 {
		t.Fatalf("key length = %d, want 0", status.KeyLength)
	}
}

func TestUnlockAppliesTheSecrets(t *testing.T) {
	store := testStore(t)
	if err := store.Seal(sampleSecrets(), testKey, ModeSaved); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	var applied Secrets
	found, err := Unlock(context.Background(), store, "", func(secrets Secrets) error {
		applied = secrets
		return nil
	})
	if err != nil || !found {
		t.Fatalf("Unlock = %v, %v", found, err)
	}
	if applied.SigningKey == nil || *applied.SigningKey != *sampleSecrets().SigningKey {
		t.Fatalf("applied = %+v", applied)
	}
}

func TestUnlockWithoutAVaultDoesNothing(t *testing.T) {
	store := testStore(t)
	called := false
	found, err := Unlock(context.Background(), store, testKey, func(Secrets) error {
		called = true
		return nil
	})
	if err != nil || found || called {
		t.Fatalf("Unlock on an empty store = %v, %v, called=%v", found, err, called)
	}
}

// TestUnlockStopsWhenTheVaultCannotBeOpened is the safety property: continuing
// would start the drive with the configuration's plaintext values, which after a
// key change are the wrong ones -- and the drive would come up looking fine.
func TestUnlockStopsWhenTheVaultCannotBeOpened(t *testing.T) {
	store := testStore(t)
	if err := store.Seal(sampleSecrets(), testKey, ModePrompt); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	found, err := Unlock(context.Background(), store, "wrong key entirely", nil)
	if !found || !errors.Is(err, ErrKeyRejected) {
		t.Fatalf("Unlock = %v, %v; want found with ErrKeyRejected", found, err)
	}
}

func TestParseMode(t *testing.T) {
	for name, testCase := range map[string]struct {
		input string
		want  Mode
		fail  bool
	}{
		"empty is saved":   {"", ModeSaved, false},
		"padded":           {"  PROMPT ", ModePrompt, false},
		"saved":            {"saved", ModeSaved, false},
		"unknown":          {"never", "", true},
		"near miss":        {"save", "", true},
		"another language": {"gespeichert", "", true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseMode(testCase.input)
			if testCase.fail {
				if err == nil {
					t.Fatalf("ParseMode(%q) = %q, want an error", testCase.input, got)
				}
				return
			}
			if err != nil || got != testCase.want {
				t.Fatalf("ParseMode(%q) = %q, %v", testCase.input, got, err)
			}
		})
	}
}

func TestGenerateKeyIsUsable(t *testing.T) {
	for _, length := range []int{KeyMinLength, 20, KeyMaxLength} {
		key, err := GenerateKey(length)
		if err != nil {
			t.Fatalf("GenerateKey(%d): %v", length, err)
		}
		if len(key) != length {
			t.Fatalf("generated %d characters, want %d", len(key), length)
		}
		if err := ValidateKey(key); err != nil {
			t.Fatalf("a generated key must be accepted: %v", err)
		}
		store := testStore(t)
		if err := store.Seal(sampleSecrets(), key, ModeSaved); err != nil {
			t.Fatalf("Seal with a generated key: %v", err)
		}
		if _, err := store.Open(""); err != nil {
			t.Fatalf("Open with a generated key: %v", err)
		}
	}
}

func TestEmptySecrets(t *testing.T) {
	if !(Secrets{}).Empty() {
		t.Fatal("the zero Secrets is empty")
	}
	empty := map[string]string{}
	if (Secrets{EncryptionKeys: &empty}).Empty() {
		t.Fatal("an explicit empty key set is not the zero value")
	}
}
