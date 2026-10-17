package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// settingsFileName is where the shell's own preferences live: beside the
// database and the vault, inside the folder a person copies to another machine.
const settingsFileName = "gui.json"

// Settings is what the shell remembers about this computer.
type Settings struct {
	// DownloadDir is where "save to this computer" writes files.
	DownloadDir string `json:"downloadDir"`
	// AskEveryTime makes every save show the folder picker first. It is the
	// setting somebody wants when they keep files in different places.
	AskEveryTime bool `json:"askEveryTime"`
}

// DefaultDownloadDir is where a person expects a downloaded file to land.
func DefaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Downloads")
}

// SettingsStore owns gui.json. It is read and written by the HTTP handlers while
// the shell is running, so every access goes through the mutex.
type SettingsStore struct {
	path string

	mu       sync.Mutex
	loaded   bool
	settings Settings
}

// NewSettingsStore points the store at a data directory.
func NewSettingsStore(dataDir string) *SettingsStore {
	return &SettingsStore{path: filepath.Join(dataDir, settingsFileName)}
}

// Path is the file the settings are kept in.
func (s *SettingsStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Load reads the file, falling back to defaults for anything missing. A file
// that cannot be parsed is reported but does not take the drive down: a
// preference is not worth refusing to start over.
func (s *SettingsStore) Load() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = Settings{DownloadDir: DefaultDownloadDir()}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.loaded = true
			return nil
		}
		return err
	}
	var stored Settings
	if err := json.Unmarshal(raw, &stored); err != nil {
		s.loaded = true
		return err
	}
	if strings.TrimSpace(stored.DownloadDir) != "" {
		s.settings.DownloadDir = stored.DownloadDir
	}
	s.settings.AskEveryTime = stored.AskEveryTime
	s.loaded = true
	return nil
}

// Get returns the current settings.
func (s *SettingsStore) Get() Settings {
	if s == nil {
		return Settings{DownloadDir: DefaultDownloadDir()}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// Set validates and records new settings. The directory has to exist or be
// creatable: a save that fails after the file has been fetched from Telegram is
// the worst possible moment to discover a typo in a path.
func (s *SettingsStore) Set(settings Settings) error {
	if s == nil {
		return errors.New("no settings to store")
	}
	settings.DownloadDir = strings.TrimSpace(settings.DownloadDir)
	if settings.DownloadDir != "" {
		if err := os.MkdirAll(settings.DownloadDir, 0o755); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.settings = settings
	s.mu.Unlock()
	return s.write()
}

func (s *SettingsStore) write() error {
	s.mu.Lock()
	payload, err := json.MarshalIndent(s.settings, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, s.path)
}

// TargetDir answers "where does this save go": the configured folder, unless the
// person asked to be asked.
func (s *SettingsStore) TargetDir(choose func(title, initial string) (string, bool)) (string, bool) {
	settings := s.Get()
	if settings.AskEveryTime && choose != nil {
		if picked, ok := choose("选择保存位置", settings.DownloadDir); ok {
			return picked, true
		}
		return "", false
	}
	return settings.DownloadDir, true
}
