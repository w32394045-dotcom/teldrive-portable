package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tgdrive/teldrive/v2/internal/config"
	"github.com/tgdrive/teldrive/v2/internal/vault"
)

func sampleConfig() config.Config {
	var cfg config.Config
	cfg.Security.SigningKey = "plaintext-signing"
	cfg.Security.DataKey = "plaintext-data"
	cfg.Database.URL = "postgres://plaintext@127.0.0.1:5433/teldrive"
	cfg.Encryption.Keys = map[int32]string{1: "plaintext-key-one"}
	cfg.Encryption.ActiveKeyVersion = 1
	return cfg
}

// TestApplyVaultSecretsReplacesEverySecret is the property the whole feature
// rests on: after this, nothing downstream can see the plaintext values.
func TestApplyVaultSecretsReplacesEverySecret(t *testing.T) {
	signing := "sealed-signing"
	data := "sealed-data"
	database := "postgres://sealed@db.example:5432/teldrive"
	keys := map[string]string{"2": "sealed-key-two", "3": "sealed-key-three"}
	active := int32(3)

	cfg := sampleConfig()
	applyVaultSecrets(&cfg, vault.Secrets{
		SigningKey:       &signing,
		DataKey:          &data,
		Database:         &database,
		EncryptionKeys:   &keys,
		ActiveKeyVersion: &active,
	})

	if cfg.Security.SigningKey != signing || cfg.Security.DataKey != data {
		t.Fatalf("security secrets = %q/%q", cfg.Security.SigningKey, cfg.Security.DataKey)
	}
	if cfg.Database.URL != database {
		t.Fatalf("database url = %q", cfg.Database.URL)
	}
	if len(cfg.Encryption.Keys) != 2 || cfg.Encryption.Keys[3] != "sealed-key-three" {
		t.Fatalf("encryption keys = %v", cfg.Encryption.Keys)
	}
	if cfg.Encryption.ActiveKeyVersion != 3 {
		t.Fatalf("active key version = %d", cfg.Encryption.ActiveKeyVersion)
	}
}

// TestApplyVaultSecretsLeavesWhatTheVaultDoesNotCarry covers a configuration that
// only sealed some of its secrets: the rest must stay exactly as written.
func TestApplyVaultSecretsLeavesWhatTheVaultDoesNotCarry(t *testing.T) {
	signing := "sealed-signing"
	cfg := sampleConfig()
	before := cfg

	applyVaultSecrets(&cfg, vault.Secrets{SigningKey: &signing})

	if cfg.Security.SigningKey != signing {
		t.Fatal("the sealed value was not applied")
	}
	if cfg.Security.DataKey != before.Security.DataKey ||
		cfg.Database.URL != before.Database.URL ||
		cfg.Encryption.ActiveKeyVersion != before.Encryption.ActiveKeyVersion {
		t.Fatalf("an unsealed field was touched: %+v", cfg)
	}
	if len(cfg.Encryption.Keys) != 1 || cfg.Encryption.Keys[1] != "plaintext-key-one" {
		t.Fatalf("encryption keys = %v", cfg.Encryption.Keys)
	}
}

// TestApplyVaultSecretsIgnoresAnUnparsableKeyVersion keeps one stray entry from
// making the whole key set unusable at startup.
func TestApplyVaultSecretsIgnoresAnUnparsableKeyVersion(t *testing.T) {
	keys := map[string]string{"1": "one", "two": "two", "3": "three"}
	cfg := sampleConfig()
	applyVaultSecrets(&cfg, vault.Secrets{EncryptionKeys: &keys})

	if len(cfg.Encryption.Keys) != 2 {
		t.Fatalf("keys = %v, want the two numeric versions", cfg.Encryption.Keys)
	}
	if cfg.Encryption.Keys[1] != "one" || cfg.Encryption.Keys[3] != "three" {
		t.Fatalf("keys = %v", cfg.Encryption.Keys)
	}
}

// TestSecretsOfCollectsEverythingWorthSealing is the other direction, used when
// the settings endpoint creates a vault.
func TestSecretsOfCollectsEverythingWorthSealing(t *testing.T) {
	cfg := sampleConfig()
	secrets := SecretsOf(cfg)

	if secrets.Empty() {
		t.Fatal("a configuration with four secrets produced an empty vault")
	}
	if secrets.SigningKey == nil || *secrets.SigningKey != cfg.Security.SigningKey {
		t.Fatalf("signing key = %v", secrets.SigningKey)
	}
	if secrets.EncryptionKeys == nil || (*secrets.EncryptionKeys)["1"] != "plaintext-key-one" {
		t.Fatalf("encryption keys = %v", secrets.EncryptionKeys)
	}
	if secrets.ActiveKeyVersion == nil || *secrets.ActiveKeyVersion != 1 {
		t.Fatalf("active key version = %v", secrets.ActiveKeyVersion)
	}
	// The collected map is a copy: a vault must not be able to mutate the
	// configuration it was taken from.
	(*secrets.EncryptionKeys)["1"] = "changed"
	if cfg.Encryption.Keys[1] != "plaintext-key-one" {
		t.Fatal("SecretsOf handed out the configuration's own map")
	}
}

func TestSecretsOfAnEmptyConfigurationIsEmpty(t *testing.T) {
	var cfg config.Config
	if !SecretsOf(cfg).Empty() {
		t.Fatal("an empty configuration has nothing to seal")
	}
}

// TestUnlockVaultWithoutAVaultDoesNothing is the normal case on every machine
// that has never used the feature, so it must be silent and cheap.
func TestUnlockVaultWithoutAVaultDoesNothing(t *testing.T) {
	// DefaultDataDir is resolved from the process arguments, so an empty
	// temporary working directory is enough to guarantee no vault exists.
	t.Chdir(t.TempDir())
	cfg := sampleConfig()
	before := cfg

	found, err := UnlockVault(context.Background(), &cfg, nil)
	if err != nil || found {
		t.Fatalf("UnlockVault = %v, %v; want nothing found and no error", found, err)
	}
	if cfg.Security.SigningKey != before.Security.SigningKey {
		t.Fatal("the configuration was changed with no vault present")
	}
}

// TestUnlockVaultRoundTripsThroughTheRealPaths is the end-to-end shape: a vault
// written where the running server looks for it is applied at the next start.
func TestUnlockVaultRoundTripsThroughTheRealPaths(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	// DefaultDataDir keys off a config file in the working directory, so putting
	// one there is what tells the vault where the data directory is.
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte("[http]\naddress = \"127.0.0.1:18787\"\n"), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	store := vault.NewStore(vault.DefaultDataDir(), nil)
	const key = "a-generated-vault-key"
	if err := store.Seal(SecretsOf(sampleConfig()), key, vault.ModeSaved); err != nil {
		t.Fatalf("seal the vault: %v", err)
	}

	cfg := sampleConfig()
	found, err := UnlockVault(context.Background(), &cfg, nil)
	if err != nil || !found {
		t.Fatalf("UnlockVault = %v, %v", found, err)
	}
	if cfg.Security.SigningKey != "plaintext-signing" {
		// The sealed values equal the plaintext ones here, which is what makes
		// this test about the plumbing rather than about the values.
		t.Fatalf("signing key = %q", cfg.Security.SigningKey)
	}
	if cfg.Encryption.ActiveKeyVersion != 1 || len(cfg.Encryption.Keys) != 1 {
		t.Fatalf("the keys did not survive the round trip: %+v", cfg.Encryption)
	}
}
