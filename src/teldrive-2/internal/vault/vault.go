// Package vault keeps an instance's own secrets encrypted on disk.
//
// The secrets a drive cannot be rebuilt without are not all in the database.
// The data key decrypts the Telegram session, the signing key authenticates every
// browser session, and the encryption keys decrypt the stored parts -- and all of
// them live in config.toml, in the clear, because that is where the configuration
// loader reads them from. Copying the drive's data folder, or reading a backup of
// it, hands over everything.
//
// The vault is the layer that fixes that without moving the configuration: the
// same values are also kept in data/vault.enc, sealed with a key the user holds,
// and unlocking at startup puts them back into the configuration before anything
// reads it. Nothing else in the application has to know.
//
// The key is either remembered on this machine (data/vault.key) so a double-click
// still just starts the drive, or typed at every start for a machine that is not
// trusted to keep it. The second mode needs somewhere to type it, which is the
// desktop shell's job.
package vault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tgdrive/teldrive/v2/internal/backup"
)

const (
	// envelopeFormat names the container. It is deliberately not the snapshot's
	// name: a file that claims to be a Teldrive index when it is not would be a
	// trap for whoever opens it during a recovery.
	envelopeFormat = "teldrive-vault"
	// vaultFileName is the sealed secrets file, in the data directory beside the
	// database and the backup settings.
	vaultFileName = "vault.enc"
	// keyFileName is where the vault key is remembered in "saved" mode.
	keyFileName = "vault.key"

	// KeyMinLength and KeyMaxLength bound a generated key. Fourteen characters
	// of a 57-character alphabet is about 81 bits, which is far past what the
	// 600,000 PBKDF2 iterations are protecting.
	KeyMinLength = 14
	KeyMaxLength = 25
)

// Mode decides where the key comes from on the next start.
type Mode string

const (
	// ModeSaved remembers the key in data/vault.key, so starting the drive needs
	// nothing from the user. It is the default, because a portable drive that
	// asks for a password every time is a drive people stop using.
	ModeSaved Mode = "saved"
	// ModePrompt keeps the key out of the data directory entirely. The key has
	// to be supplied at every start, which is what a machine you do not trust
	// with it needs -- and why this mode cannot work without a shell to type it
	// into.
	ModePrompt Mode = "prompt"
)

// Valid reports whether the value is one this package understands.
func (m Mode) Valid() bool { return m == ModeSaved || m == ModePrompt }

// ParseMode converts a stored or submitted string, defaulting to saved.
func ParseMode(value string) (Mode, error) {
	trimmed := Mode(strings.ToLower(strings.TrimSpace(value)))
	if trimmed == "" {
		return ModeSaved, nil
	}
	if !trimmed.Valid() {
		return "", fmt.Errorf("vault: unknown key mode %q", value)
	}
	return trimmed, nil
}

var (
	// ErrLocked means the vault exists but its key was not available.
	ErrLocked = errors.New("vault: the secrets are encrypted and the key was not supplied")
	// ErrKeyRejected means the key did not open the vault.
	ErrKeyRejected = errors.New("vault: the key does not open this vault")
	// ErrNoVault means there is nothing sealed yet.
	ErrNoVault = errors.New("vault: no sealed secrets on this machine")
)

// Secrets is what the vault protects. Every field is optional so a configuration
// that only has some of them still round-trips exactly.
type Secrets struct {
	SigningKey *string `json:"signingKey,omitempty"`
	DataKey    *string `json:"dataKey,omitempty"`
	Database   *string `json:"databaseUrl,omitempty"`
	// EncryptionKeys maps key version to key material, as configuration text.
	// A pointer to the map so "no keys" and "an empty set" stay distinguishable.
	EncryptionKeys *map[string]string `json:"encryptionKeys,omitempty"`
	// ActiveKeyVersion travels with the keys: restoring the keys without it
	// would leave new uploads encrypted under a version nothing refers to.
	ActiveKeyVersion *int32 `json:"activeKeyVersion,omitempty"`
}

// Empty reports whether there is nothing worth sealing.
func (s Secrets) Empty() bool {
	return s.SigningKey == nil && s.DataKey == nil && s.Database == nil &&
		s.EncryptionKeys == nil && s.ActiveKeyVersion == nil
}

// Metadata describes the vault without opening it.
type Metadata struct {
	Version   int       `json:"version"`
	Mode      Mode      `json:"mode"`
	SealedAt  time.Time `json:"sealedAt"`
	KeyLength int       `json:"keyLength"`
}

// Store owns the sealed file and the remembered key.
//
// It is safe to use from several goroutines: the vault is read at startup and
// rewritten by the settings endpoint while requests are in flight.
type Store struct {
	dataDir string
	logger  *slog.Logger

	mu       sync.Mutex
	metadata Metadata
	loaded   bool
}

func NewStore(dataDir string, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{dataDir: dataDir, logger: logger}
}

func (s *Store) vaultPath() string { return filepath.Join(s.dataDir, vaultFileName) }
func (s *Store) keyPath() string   { return filepath.Join(s.dataDir, keyFileName) }

// Exists reports whether secrets have been sealed on this machine.
func (s *Store) Exists() bool {
	if s == nil {
		return false
	}
	info, err := os.Stat(s.vaultPath())
	return err == nil && !info.IsDir()
}

// Status describes the vault for the settings page, without its contents.
func (s *Store) Status() (Metadata, bool) {
	if s == nil {
		return Metadata{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadMetadata(); err != nil {
		return Metadata{}, false
	}
	return s.metadata, true
}

// loadMetadata reads the header. Callers hold the lock.
func (s *Store) loadMetadata() error {
	if s.loaded {
		return nil
	}
	document, err := os.ReadFile(s.vaultPath())
	if err != nil {
		return err
	}
	sealed, err := backup.SealMetadata(envelopeFormat, document)
	if err != nil {
		return err
	}
	s.metadata = Metadata{
		Version:  sealed.Version,
		SealedAt: sealed.SealedAt,
		// The key length is not stored in the container -- only the key's owner
		// knows it -- so it is reported from the remembered key when there is
		// one, and left zero otherwise.
		KeyLength: len(s.rememberedKey()),
	}
	mode := ModeSaved
	if s.metadata.KeyLength == 0 {
		// No remembered key means the next start has to ask, whatever was
		// intended; reporting "saved" would be a lie the page acts on.
		mode = ModePrompt
	}
	s.metadata.Mode = mode
	s.loaded = true
	return nil
}

// rememberedKey returns the key from disk, or an empty string.
func (s *Store) rememberedKey() string {
	raw, err := os.ReadFile(s.keyPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// Open decrypts the vault.
//
// An empty key falls back to the remembered one, which is the normal path for a
// double-click. A machine with no vault is not an error: it means the feature
// has never been used, and the caller should carry on with the configuration it
// already has.
func (s *Store) Open(key string) (Secrets, error) {
	if s == nil {
		return Secrets{}, ErrNoVault
	}
	if strings.TrimSpace(key) == "" {
		key = s.rememberedKey()
	}
	document, err := os.ReadFile(s.vaultPath())
	if errors.Is(err, os.ErrNotExist) {
		return Secrets{}, ErrNoVault
	}
	if err != nil {
		return Secrets{}, fmt.Errorf("vault: read secrets: %w", err)
	}
	if strings.TrimSpace(key) == "" {
		return Secrets{}, ErrLocked
	}
	plaintext, err := backup.UnsealAs(envelopeFormat, document, key)
	if err != nil {
		if errors.Is(err, backup.ErrPasswordRejected) {
			return Secrets{}, ErrKeyRejected
		}
		return Secrets{}, err
	}
	var secrets Secrets
	if err := json.Unmarshal(plaintext, &secrets); err != nil {
		return Secrets{}, fmt.Errorf("vault: decode secrets: %w", err)
	}
	return secrets, nil
}

// Seal writes the secrets and, in saved mode, remembers the key beside them.
//
// Order matters. The sealed file is written first and the key second, because a
// key without a vault is a stray file while a vault without its key is
// unopenable. A crash between the two leaves the previous key still in place and
// the old vault still readable.
func (s *Store) Seal(secrets Secrets, key string, mode Mode) error {
	if s == nil {
		return errors.New("vault: store is not configured")
	}
	if !mode.Valid() {
		return fmt.Errorf("vault: unknown key mode %q", mode)
	}
	if err := ValidateKey(key); err != nil {
		return err
	}
	if secrets.Empty() {
		return errors.New("vault: there is nothing to seal")
	}
	encoded, err := json.MarshalIndent(secrets, "", " ")
	if err != nil {
		return fmt.Errorf("vault: encode secrets: %w", err)
	}
	document, err := backup.SealAs(envelopeFormat, encoded, key, time.Now())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
		return fmt.Errorf("vault: create data directory: %w", err)
	}
	if err := writeFileAtomic(s.vaultPath(), document, 0o600); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded = false
	if mode == ModeSaved {
		if err := writeFileAtomic(s.keyPath(), []byte(key+"\n"), 0o600); err != nil {
			return err
		}
	} else if err := os.Remove(s.keyPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("vault: forget the remembered key: %w", err)
	}
	return nil
}

// Forget removes the remembered key without touching the sealed secrets. The
// vault then needs its key typed at the next start.
func (s *Store) Forget() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.loaded = false
	s.mu.Unlock()
	if err := os.Remove(s.keyPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("vault: forget the remembered key: %w", err)
	}
	return nil
}

// Destroy removes the sealed secrets and the remembered key.
//
// It does not touch the configuration they were copied from: the plaintext is
// still the source of truth for a running instance, and deleting this is how the
// feature is turned back off.
func (s *Store) Destroy() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.loaded = false
	s.metadata = Metadata{}
	s.mu.Unlock()
	for _, path := range []string{s.vaultPath(), s.keyPath()} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("vault: remove %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

// GenerateKey returns a random key of the requested length.
//
// It shares the backup package's alphabet, which leaves out the characters
// people misread when copying a key out of a screenshot or off a printed page.
func GenerateKey(length int) (string, error) { return backup.GeneratePassword(length) }

// ValidateKey rejects a key the sealer would refuse, at the point where the user
// can still see why rather than inside a startup path.
func ValidateKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("vault: the key cannot be empty")
	}
	return backup.ValidatePassword(key)
}

// DefaultDataDir returns the directory the running server keeps its data in.
//
// It is resolved through the backup settings' own path so the two cannot drift:
// a vault written next to one data directory and read from another would look
// exactly like a corrupted vault.
func DefaultDataDir() string {
	return filepath.Dir(backup.DefaultConfigPath())
}

// KeyLengthOf reports how long a key is, which is all the settings page is
// allowed to know about it.
func KeyLengthOf(key string) int { return len(key) }

// Unlock loads the secrets and hands them to apply, which is the composition
// root's chance to put them back into the configuration before anything reads it.
//
// A machine with no vault is not an error, and neither is a vault that was never
// asked to be opened: the caller reports what happened and carries on with the
// configuration it has. Only a vault that exists and cannot be opened stops a
// start, because continuing would mean running with the wrong keys.
func Unlock(ctx context.Context, store *Store, key string, apply func(Secrets) error) (bool, error) {
	if store == nil || !store.Exists() {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	secrets, err := store.Open(key)
	if err != nil {
		return true, err
	}
	if apply != nil {
		if err := apply(secrets); err != nil {
			return true, err
		}
	}
	return true, nil
}

// writeFileAtomic replaces a file through a temporary one in the same directory,
// so a crash mid-write cannot leave half a vault or half a key behind.
func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("vault: create %s: %w", filepath.Base(path), err)
	}
	name := temporary.Name()
	defer func() {
		if name != "" {
			_ = os.Remove(name)
		}
	}()
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("vault: write %s: %w", filepath.Base(path), err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("vault: close %s: %w", filepath.Base(path), err)
	}
	if err := os.Chmod(name, mode); err != nil {
		return fmt.Errorf("vault: protect %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("vault: replace %s: %w", filepath.Base(path), err)
	}
	name = ""
	return nil
}
