package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/tgdrive/teldrive/v2/internal/config"
	"github.com/tgdrive/teldrive/v2/internal/vault"
)

// Unlocked describes what a start found on this machine.
type Unlocked struct {
	// Found reports whether a vault exists and was opened.
	Found bool
	// Key is the key that opened it, so a snapshot can carry it and a rebuilt
	// machine can read the secrets that snapshot also carries. It is empty when
	// there is no vault.
	Key string
}

// UnlockVault replaces the configuration's secrets with the sealed ones, if this
// machine has a vault.
//
// It is called by the entry points rather than inside New, because the bundled
// launcher reads the database URL to start PostgreSQL *before* the application
// exists: a vault that held the URL would otherwise point the database at one
// place and the server at another.
//
// A machine with no vault is the normal case and costs one stat. A vault that
// exists but cannot be opened is fatal on purpose: carrying on would start the
// drive with the plaintext configuration, which after a key change holds values
// that are wrong in a way nothing else would notice.
func UnlockVault(ctx context.Context, cfg *config.Config, logger *slog.Logger) (Unlocked, error) {
	return unlockVault(ctx, cfg, "", logger)
}

// VaultNeedsKey reports whether this machine holds sealed secrets and no key to
// open them. It is the question a shell asks before it can start anything: the
// answer decides whether a person has to be asked for the passphrase first.
func VaultNeedsKey() bool {
	store := vault.NewStore(vault.DefaultDataDir(), nil)
	if !store.Exists() {
		return false
	}
	_, err := store.AvailableKey()
	return errors.Is(err, vault.ErrLocked)
}

// UnlockVaultWithKey opens the vault with a key the caller collected from a
// person, which is the only thing a shell can do about ModePrompt. It reports
// the same errors as UnlockVault, so a rejected key can be told apart from a
// broken file and the prompt can be shown again.
func UnlockVaultWithKey(ctx context.Context, cfg *config.Config, key string, logger *slog.Logger) (Unlocked, error) {
	return unlockVault(ctx, cfg, key, logger)
}

// unlockVault applies the sealed secrets to the configuration. An empty key
// means "use the one this machine remembers".
func unlockVault(ctx context.Context, cfg *config.Config, key string, logger *slog.Logger) (Unlocked, error) {
	if cfg == nil {
		return Unlocked{}, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	store := vault.NewStore(vault.DefaultDataDir(), logger)
	if !store.Exists() {
		return Unlocked{}, nil
	}
	if strings.TrimSpace(key) == "" {
		available, err := store.AvailableKey()
		if err != nil {
			// The vault exists and its key does not. Only a shell can ask for it,
			// so a headless start has to say so rather than run on the plaintext.
			return Unlocked{Found: true}, fmt.Errorf("unlock the settings vault: %w", err)
		}
		key = available
	}
	found, err := vault.Unlock(ctx, store, key, func(secrets vault.Secrets) error {
		applyVaultSecrets(cfg, secrets)
		return nil
	})
	if err != nil {
		return Unlocked{Found: found}, fmt.Errorf("unlock the settings vault: %w", err)
	}
	logger.Info("vault.unlocked", "secrets", describeVaultSecrets(cfg))
	return Unlocked{Found: found, Key: key}, nil
}

// applyVaultSecrets copies the sealed values over the plaintext ones.
//
// A field the vault does not carry is left alone, so a configuration that only
// sealed some of its secrets keeps the rest exactly as written.
func applyVaultSecrets(cfg *config.Config, secrets vault.Secrets) {
	if secrets.SigningKey != nil {
		cfg.Security.SigningKey = *secrets.SigningKey
	}
	if secrets.DataKey != nil {
		cfg.Security.DataKey = *secrets.DataKey
	}
	if secrets.Database != nil {
		cfg.Database.URL = *secrets.Database
	}
	if secrets.EncryptionKeys != nil {
		keys := make(map[int32]string, len(*secrets.EncryptionKeys))
		for version, key := range *secrets.EncryptionKeys {
			parsed, err := strconv.Atoi(version)
			if err != nil {
				// A version that is not a number cannot be a key version, and the
				// configuration validates the rest; dropping it keeps one bad
				// entry from making the whole set unusable.
				continue
			}
			keys[int32(parsed)] = key
		}
		cfg.Encryption.Keys = keys
	}
	if secrets.ActiveKeyVersion != nil {
		cfg.Encryption.ActiveKeyVersion = *secrets.ActiveKeyVersion
	}
}

// SecretsOf collects the values a vault should hold, for the settings endpoint
// that creates one. It never returns an empty map for a configuration that has
// something worth sealing.
func SecretsOf(cfg config.Config) vault.Secrets {
	secrets := vault.Secrets{}
	if value := cfg.Security.SigningKey; value != "" {
		secrets.SigningKey = &value
	}
	if value := cfg.Security.DataKey; value != "" {
		secrets.DataKey = &value
	}
	if value := cfg.Database.URL; value != "" {
		secrets.Database = &value
	}
	if len(cfg.Encryption.Keys) > 0 {
		keys := make(map[string]string, len(cfg.Encryption.Keys))
		for version, key := range cfg.Encryption.Keys {
			keys[strconv.Itoa(int(version))] = key
		}
		secrets.EncryptionKeys = &keys
	}
	if cfg.Encryption.ActiveKeyVersion > 0 {
		active := cfg.Encryption.ActiveKeyVersion
		secrets.ActiveKeyVersion = &active
	}
	return secrets
}

// describeVaultSecrets names what was unlocked, for the log line. It never
// reports a value.
func describeVaultSecrets(cfg *config.Config) []string {
	names := make([]string, 0, 4)
	if cfg.Security.SigningKey != "" {
		names = append(names, "signingKey")
	}
	if cfg.Security.DataKey != "" {
		names = append(names, "dataKey")
	}
	if cfg.Database.URL != "" {
		names = append(names, "databaseUrl")
	}
	if len(cfg.Encryption.Keys) > 0 {
		names = append(names, "encryptionKeys")
	}
	return names
}
