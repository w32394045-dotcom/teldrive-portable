package app

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tgdrive/teldrive/v2/internal/authn"
	"github.com/tgdrive/teldrive/v2/internal/config"
	"github.com/tgdrive/teldrive/v2/internal/vault"
)

// vaultPath is where the settings page reads and changes the sealed secrets.
const vaultPath = "/api/vault"

// vaultForgetPath drops the remembered key without touching the sealed file,
// which is how the page moves a machine from "remember it" to "ask me".
const vaultForgetPath = "/api/vault/forget"

// generatedKeyLength is what the page's generate button asks for. It sits in the
// middle of the accepted range: long enough that the 600,000 PBKDF2 iterations
// are protecting something worth protecting, short enough to write down.
const generatedKeyLength = 20

type vaultConfig struct {
	store       *vault.Store
	cfg         config.Config
	authService *authn.Service
	logger      *slog.Logger
}

// vaultHandler manages the sealed copy of the instance's own secrets.
//
// It never hands the key back except once, at the moment it is created, because
// that is the only moment the person is in a position to write it down. Every
// later read reports the length and nothing else: a page that can display the
// key is a page that can leak it.
type vaultHandler struct {
	store  *vault.Store
	cfg    config.Config
	auth   *authn.Service
	logger *slog.Logger
}

func newVaultHandler(cfg vaultConfig) (*vaultHandler, error) {
	if cfg.store == nil || cfg.authService == nil {
		return nil, errors.New("vault handler requires the vault store and the auth service")
	}
	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}
	return &vaultHandler{store: cfg.store, cfg: cfg.cfg, auth: cfg.authService, logger: cfg.logger}, nil
}

func (h *vaultHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, ok := authorizeManager(w, r, h.auth,
		"Only an instance owner or administrator can change the settings vault."); !ok {
		return
	}
	switch r.URL.Path {
	case vaultPath:
		h.serveVault(w, r)
	case vaultForgetPath:
		h.serveForget(w, r)
	default:
		writeJSONMessage(w, http.StatusNotFound, "not_found", "Unknown vault endpoint.")
	}
}

type vaultStatusResponse struct {
	// Exists reports whether secrets have been sealed on this machine.
	Exists bool `json:"exists"`
	// Mode is where the key will come from at the next start, as it actually is
	// rather than as it was asked for: a vault whose key was forgotten reports
	// "prompt" whatever was chosen.
	Mode string `json:"mode"`
	// SealedAt and KeyLength describe the vault without revealing it.
	SealedAt  *time.Time `json:"sealedAt,omitempty"`
	KeyLength int        `json:"keyLength,omitempty"`
	// Secrets names what is sealed, so the page can say what is protected
	// without handing over any of it.
	Secrets []string `json:"secrets"`
	// The key bounds travel with the payload so the page cannot offer a length
	// the server would refuse.
	MinimumKeyLength int `json:"minimumKeyLength"`
	MaximumKeyLength int `json:"maximumKeyLength"`
}

func (h *vaultHandler) serveVault(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.status())
	case http.MethodPost:
		h.create(w, r)
	case http.MethodDelete:
		if err := h.store.Destroy(); err != nil {
			h.logger.Error("vault.destroy_failed", "error", err)
			writeJSONMessage(w, http.StatusInternalServerError, "destroy_failed", err.Error())
			return
		}
		h.logger.Warn("vault.destroyed",
			"detail", "the sealed copy was removed; the configuration still holds the secrets in the clear")
		writeJSON(w, http.StatusOK, h.status())
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET, POST or DELETE.")
	}
}

type createVaultRequest struct {
	// Key is the passphrase to seal with. Empty means "generate one".
	Key string `json:"key"`
	// Mode decides whether the key is remembered on this machine.
	Mode string `json:"mode"`
}

type createVaultResponse struct {
	Status vaultStatusResponse `json:"status"`
	// Key is returned exactly once, at creation, because that is the only moment
	// the person can write it down. It is never readable again through the API.
	Key string `json:"key,omitempty"`
	// Remembered says whether this machine now holds the key, so the page can
	// tell the user whether the next start will ask for it.
	Remembered bool `json:"remembered"`
}

func (h *vaultHandler) create(w http.ResponseWriter, r *http.Request) {
	var body createVaultRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, configBodyLimit)).Decode(&body); err != nil {
		writeJSONMessage(w, http.StatusBadRequest, "invalid_request",
			`Expected a JSON body of the form {"key": "...", "mode": "saved"}.`)
		return
	}
	mode, err := vault.ParseMode(body.Mode)
	if err != nil {
		writeJSONMessage(w, http.StatusBadRequest, "invalid_key_mode", err.Error())
		return
	}
	// The secrets come from the running configuration, which already holds the
	// unlocked values when this machine had a vault.
	secrets := SecretsOf(h.cfg)
	if secrets.Empty() {
		writeJSONMessage(w, http.StatusConflict, "nothing_to_seal",
			"This instance has no secrets to seal: its signing key, data key and encryption keys are all unset.")
		return
	}

	key := strings.TrimSpace(body.Key)
	generated := false
	if key == "" {
		key, err = vault.GenerateKey(generatedKeyLength)
		if err != nil {
			h.logger.Error("vault.generate_failed", "error", err)
			writeJSONMessage(w, http.StatusInternalServerError, "generate_failed", err.Error())
			return
		}
		generated = true
	}
	if err := h.store.Seal(secrets, key, mode); err != nil {
		writeJSONMessage(w, http.StatusBadRequest, "invalid_key", err.Error())
		return
	}
	h.logger.Warn("vault.created",
		"mode", mode, "generated", generated, "secrets", describeVaultSecrets(&h.cfg))

	response := createVaultResponse{Status: h.status(), Remembered: mode == vault.ModeSaved}
	// A key the user chose is one they already know; only a generated one has to
	// be shown, and only once.
	if generated {
		response.Key = key
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *vaultHandler) serveForget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONMessage(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.")
		return
	}
	if !h.store.Exists() {
		writeJSONMessage(w, http.StatusConflict, "no_vault", "There is no vault on this machine.")
		return
	}
	if err := h.store.Forget(); err != nil {
		h.logger.Error("vault.forget_failed", "error", err)
		writeJSONMessage(w, http.StatusInternalServerError, "forget_failed", err.Error())
		return
	}
	h.logger.Info("vault.key_forgotten", "detail", "the next start will ask for the key")
	writeJSON(w, http.StatusOK, h.status())
}

// status describes the vault without opening it.
func (h *vaultHandler) status() vaultStatusResponse {
	response := vaultStatusResponse{
		Secrets:          describeVaultSecrets(&h.cfg),
		MinimumKeyLength: vault.KeyMinLength,
		MaximumKeyLength: vault.KeyMaxLength,
	}
	metadata, ok := h.store.Status()
	if !ok {
		// No vault, or one that cannot even be read: the page shows "not set up"
		// and the create path is the only thing it offers.
		return response
	}
	response.Exists = true
	response.Mode = string(metadata.Mode)
	response.KeyLength = metadata.KeyLength
	if !metadata.SealedAt.IsZero() {
		sealedAt := metadata.SealedAt
		response.SealedAt = &sealedAt
	}
	return response
}
