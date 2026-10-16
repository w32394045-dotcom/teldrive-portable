package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdrive/teldrive/v2/internal/authn"
	"github.com/tgdrive/teldrive/v2/internal/config"
	"github.com/tgdrive/teldrive/v2/internal/vault"
)

func newTestVaultHandler(t *testing.T, cfg config.Config) (*vaultHandler, *vault.Store, string) {
	t.Helper()
	dir := t.TempDir()
	store := vault.NewStore(dir, nil)
	handler, err := newVaultHandler(vaultConfig{
		store:       store,
		cfg:         cfg,
		authService: &authn.Service{},
	})
	if err != nil {
		t.Fatalf("newVaultHandler: %v", err)
	}
	return handler, store, dir
}

func callVault(t *testing.T, handler *vaultHandler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	recorder := httptest.NewRecorder()
	if path == vaultForgetPath {
		handler.serveForget(recorder, request)
	} else {
		handler.serveVault(recorder, request)
	}
	return recorder
}

func decodeVaultStatus(t *testing.T, recorder *httptest.ResponseRecorder) vaultStatusResponse {
	t.Helper()
	var status vaultStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status %q: %v", recorder.Body.String(), err)
	}
	return status
}

// TestVaultStatusOnAFreshMachine has to be useful before anything is sealed: it
// is how the page knows what there is to protect.
func TestVaultStatusOnAFreshMachine(t *testing.T) {
	handler, _, _ := newTestVaultHandler(t, sampleConfig())
	recorder := callVault(t, handler, http.MethodGet, vaultPath, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	status := decodeVaultStatus(t, recorder)
	if status.Exists {
		t.Fatal("a fresh machine reported a vault")
	}
	if status.Mode != "" {
		t.Fatalf("mode = %q, want empty", status.Mode)
	}
	if len(status.Secrets) == 0 {
		t.Fatal("the page cannot say what would be protected")
	}
	if status.MinimumKeyLength != vault.KeyMinLength || status.MaximumKeyLength != vault.KeyMaxLength {
		t.Fatalf("key bounds = %d..%d", status.MinimumKeyLength, status.MaximumKeyLength)
	}
}

// TestVaultCreateGeneratesAndShowsTheKeyOnce is the only moment the key is
// readable through the API, and it exists because that is the only moment the
// person can write it down.
func TestVaultCreateGeneratesAndShowsTheKeyOnce(t *testing.T) {
	handler, store, _ := newTestVaultHandler(t, sampleConfig())
	recorder := callVault(t, handler, http.MethodPost, vaultPath, `{}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", recorder.Code, recorder.Body)
	}
	var created createVaultResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(created.Key) != generatedKeyLength {
		t.Fatalf("generated key length = %d, want %d", len(created.Key), generatedKeyLength)
	}
	if !created.Remembered {
		t.Fatal("the default mode should remember the key")
	}
	if !created.Status.Exists || created.Status.Mode != string(vault.ModeSaved) {
		t.Fatalf("status = %+v", created.Status)
	}
	if created.Status.KeyLength != len(created.Key) {
		t.Fatalf("reported key length = %d, want %d", created.Status.KeyLength, len(created.Key))
	}
	// The key that was generated is the key that opens it.
	if _, err := store.Open(created.Key); err != nil {
		t.Fatalf("the returned key does not open the vault: %v", err)
	}

	// And it is never readable again.
	status := callVault(t, handler, http.MethodGet, vaultPath, "")
	if strings.Contains(status.Body.String(), created.Key) {
		t.Fatalf("a later read returned the key: %s", status.Body)
	}
	if decoded := decodeVaultStatus(t, status); !decoded.Exists || decoded.KeyLength == 0 {
		t.Fatalf("status = %+v", decoded)
	}
}

// TestVaultCreateKeepsAChosenKeySecret: a key the user typed is one they already
// know, so there is no reason to send it back over the wire again.
func TestVaultCreateKeepsAChosenKeySecret(t *testing.T) {
	handler, store, _ := newTestVaultHandler(t, sampleConfig())
	const chosen = "my own long key"
	recorder := callVault(t, handler, http.MethodPost, vaultPath, `{"key": "`+chosen+`"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", recorder.Code, recorder.Body)
	}
	if strings.Contains(recorder.Body.String(), chosen) {
		t.Fatalf("the response echoed the chosen key: %s", recorder.Body)
	}
	if _, err := store.Open(chosen); err != nil {
		t.Fatalf("the chosen key does not open the vault: %v", err)
	}
}

// TestVaultCreateInPromptModeLeavesNoKeyBehind is the mode for a machine that is
// not trusted to remember it.
func TestVaultCreateInPromptModeLeavesNoKeyBehind(t *testing.T) {
	handler, _, dir := newTestVaultHandler(t, sampleConfig())
	recorder := callVault(t, handler, http.MethodPost, vaultPath, `{"mode": "prompt"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", recorder.Code, recorder.Body)
	}
	var created createVaultResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if created.Remembered {
		t.Fatal("prompt mode reported that it remembered the key")
	}
	if created.Status.Mode != string(vault.ModePrompt) {
		t.Fatalf("mode = %q, want prompt", created.Status.Mode)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the data directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "vault.enc" {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("the data directory holds %v, want only the sealed file", names)
	}
}

func TestVaultCreateRefusesUnusableInput(t *testing.T) {
	handler, store, _ := newTestVaultHandler(t, sampleConfig())
	for name, body := range map[string]string{
		"unknown mode": `{"mode": "whenever"}`,
		"short key":    `{"key": "tiny"}`,
		"malformed":    `{"key":`,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := callVault(t, handler, http.MethodPost, vaultPath, body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", recorder.Code, recorder.Body)
			}
			if store.Exists() {
				t.Fatal("a refused request sealed something")
			}
		})
	}
}

// TestVaultCreateSaysWhenThereIsNothingToSeal covers an instance whose secrets
// are unset: sealing an empty set would produce a vault that protects nothing
// while looking like it protects something.
func TestVaultCreateSaysWhenThereIsNothingToSeal(t *testing.T) {
	var empty config.Config
	handler, store, _ := newTestVaultHandler(t, empty)
	recorder := callVault(t, handler, http.MethodPost, vaultPath, `{}`)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", recorder.Code, recorder.Body)
	}
	if store.Exists() {
		t.Fatal("an empty vault was written")
	}
}

func TestVaultForgetSwitchesToPrompt(t *testing.T) {
	handler, _, dir := newTestVaultHandler(t, sampleConfig())
	if recorder := callVault(t, handler, http.MethodPost, vaultPath, `{}`); recorder.Code != http.StatusOK {
		t.Fatalf("create status = %d", recorder.Code)
	}
	recorder := callVault(t, handler, http.MethodPost, vaultForgetPath, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", recorder.Code, recorder.Body)
	}
	status := decodeVaultStatus(t, recorder)
	if !status.Exists || status.Mode != string(vault.ModePrompt) {
		t.Fatalf("status = %+v, want an existing vault in prompt mode", status)
	}
	if _, err := os.Stat(filepath.Join(dir, "vault.key")); !os.IsNotExist(err) {
		t.Fatalf("the key was not removed: %v", err)
	}
}

func TestVaultForgetWithoutAVault(t *testing.T) {
	handler, _, _ := newTestVaultHandler(t, sampleConfig())
	recorder := callVault(t, handler, http.MethodPost, vaultForgetPath, "")
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
}

func TestVaultDestroy(t *testing.T) {
	handler, store, _ := newTestVaultHandler(t, sampleConfig())
	callVault(t, handler, http.MethodPost, vaultPath, `{}`)
	recorder := callVault(t, handler, http.MethodDelete, vaultPath, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", recorder.Code, recorder.Body)
	}
	if store.Exists() {
		t.Fatal("Destroy left the vault behind")
	}
	if status := decodeVaultStatus(t, recorder); status.Exists {
		t.Fatalf("status = %+v, want no vault", status)
	}
}

func TestVaultRejectsUnsupportedMethods(t *testing.T) {
	handler, _, _ := newTestVaultHandler(t, sampleConfig())
	recorder := callVault(t, handler, http.MethodPut, vaultPath, "{}")
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
	if allow := recorder.Header().Get("Allow"); allow != "GET, POST, DELETE" {
		t.Fatalf("Allow = %q", allow)
	}
	recorder = callVault(t, handler, http.MethodGet, vaultForgetPath, "")
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
}

// TestVaultStatusNeverCarriesAKey is the guard against a future refactor adding
// the key to the struct the API serialises.
func TestVaultStatusNeverCarriesAKey(t *testing.T) {
	handler, store, _ := newTestVaultHandler(t, sampleConfig())
	const key = "a-remembered-vault-key"
	if err := store.Seal(SecretsOf(sampleConfig()), key, vault.ModeSaved); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	recorder := callVault(t, handler, http.MethodGet, vaultPath, "")
	if strings.Contains(recorder.Body.String(), key) {
		t.Fatalf("the status leaked the key: %s", recorder.Body)
	}
	// And the values it protects must not appear either.
	for _, secret := range []string{"plaintext-signing", "plaintext-data", "plaintext-key-one"} {
		if strings.Contains(recorder.Body.String(), secret) {
			t.Fatalf("the status leaked %q: %s", secret, recorder.Body)
		}
	}
}

// TestIsVaultPathMatchesExactly keeps a lookalike path from reaching the handler.
func TestIsVaultPathMatchesExactly(t *testing.T) {
	for _, path := range []string{vaultPath, vaultForgetPath} {
		if !isVaultPath(path) {
			t.Fatalf("isVaultPath(%q) = false", path)
		}
	}
	for _, path := range []string{"/api/vault/", "/api/vaults", "/api/vault/key", "/vault", "/api/backup/run"} {
		if isVaultPath(path) {
			t.Fatalf("isVaultPath(%q) = true", path)
		}
	}
}
