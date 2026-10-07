package webdav

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// configFileName is the persisted toggle, stored as JSON in the runtime data
// directory next to the server's config.toml. It deliberately stays outside the
// database so enabling WebDAV needs no migration.
const configFileName = "webdav.json"

// ConfigStore owns the WebDAV toggle. It is read on the request path and written
// by the settings API, so the write must apply immediately without a restart:
// SetEnabled persists first and only then flips the in-memory value, which keeps
// a failed write from reporting success.
type ConfigStore struct {
	path   string
	logger *slog.Logger

	mu      sync.RWMutex
	enabled bool
}

type configFile struct {
	Enabled bool `json:"enabled"`
}

// NewConfigStore returns a store backed by path. A missing file means disabled,
// and the value is not read from disk until Load is called.
func NewConfigStore(path string, logger *slog.Logger) *ConfigStore {
	if logger == nil {
		logger = slog.Default()
	}
	return &ConfigStore{path: path, logger: logger}
}

// Load reads the persisted toggle. A missing file is not an error: it is the
// documented default (disabled). An unreadable or malformed file also degrades
// to disabled, because refusing to start the whole server over an optional
// feature would be worse than starting it with WebDAV off.
func (s *ConfigStore) Load() error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	content, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read webdav config: %w", err)
	}
	var parsed configFile
	if err := json.Unmarshal(content, &parsed); err != nil {
		s.logger.Warn("webdav.config.unreadable", "path", s.path, "error", err)
		return nil
	}
	s.mu.Lock()
	s.enabled = parsed.Enabled
	s.mu.Unlock()
	return nil
}

// Enabled reports the current toggle value.
func (s *ConfigStore) Enabled() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

// Path is the JSON file backing the toggle; it is reported in diagnostics.
func (s *ConfigStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// SetEnabled persists the toggle atomically and applies it to this process.
func (s *ConfigStore) SetEnabled(enabled bool) error {
	if s == nil {
		return errors.New("webdav: config store is not configured")
	}
	if strings.TrimSpace(s.path) == "" {
		return errors.New("webdav: config store path is empty")
	}
	if err := writeConfigFile(s.path, configFile{Enabled: enabled}); err != nil {
		return err
	}
	s.mu.Lock()
	s.enabled = enabled
	s.mu.Unlock()
	return nil
}

// writeConfigFile writes JSON through a temporary file in the destination
// directory so a crash mid-write cannot leave a half-written toggle behind.
func writeConfigFile(path string, value configFile) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create webdav config directory: %w", err)
	}
	encoded, err := json.MarshalIndent(value, "", " ")
	if err != nil {
		return fmt.Errorf("encode webdav config: %w", err)
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(directory, configFileName+".tmp*")
	if err != nil {
		return fmt.Errorf("create webdav config temporary file: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() {
		if temporaryName != "" {
			_ = os.Remove(temporaryName)
		}
	}()
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write webdav config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close webdav config: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace webdav config: %w", err)
	}
	temporaryName = ""
	return nil
}

// DataDir resolves the runtime data directory that sits next to the server's
// config file. The configuration loader owns config file discovery, but it does
// not expose the resolved path, so the same candidates are searched here in the
// same order: an explicit -c/--config flag, then a config file in the working
// directory, then the default home location.
func DataDir(args []string, homeDir func() (string, error)) string {
	if flagPath := configFlagPath(args); flagPath != "" {
		return filepath.Join(filepath.Dir(flagPath), "data")
	}
	for _, name := range []string{"config.toml", "config.yaml", "config.yml"} {
		info, err := os.Stat(name)
		if err != nil || info.IsDir() {
			continue
		}
		absolute, err := filepath.Abs(name)
		if err != nil {
			continue
		}
		return filepath.Join(filepath.Dir(absolute), "data")
	}
	if homeDir != nil {
		if home, err := homeDir(); err == nil && strings.TrimSpace(home) != "" {
			return filepath.Join(home, ".teldrive", "data")
		}
	}
	return "data"
}

// DefaultConfigPath returns the JSON file used by the running server, resolving
// the data directory the same way DataDir does.
func DefaultConfigPath() string {
	return filepath.Join(DataDir(os.Args, os.UserHomeDir), configFileName)
}

// configFlagPath extracts the configuration file path from the process
// arguments. Both "-c value" and "-c=value" (and the long form) are supported
// because the CLI accepts either.
func configFlagPath(args []string) string {
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "-c" || argument == "--config":
			if index+1 < len(args) {
				return strings.TrimSpace(args[index+1])
			}
			return ""
		case strings.HasPrefix(argument, "-c="):
			return strings.TrimSpace(strings.TrimPrefix(argument, "-c="))
		case strings.HasPrefix(argument, "--config="):
			return strings.TrimSpace(strings.TrimPrefix(argument, "--config="))
		}
	}
	return ""
}
