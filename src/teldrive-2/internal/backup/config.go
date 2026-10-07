// Package backup snapshots the drive index and stores it inside Telegram.
//
// The bytes of every file already live in Telegram, but which message belongs to
// which file lives only in PostgreSQL. Losing the database therefore loses the
// drive even though nothing was actually deleted, and no amount of Telegram
// archiving brings it back. This package writes that mapping somewhere that
// survives the database, in the same place the data itself is kept.
//
// Three operations, deliberately separated by cost:
//
//	Create  build the index and upload it. Never lists Telegram, so it can run on
//	        a schedule without touching the API beyond the upload itself.
//	Scan    list the documents already sitting in the backup channel. Enumerating
//	        Telegram history is the expensive call, so it is kept off the
//	        scheduled path.
//	Download stream a stored backup back out.
//
// Restore is not implemented here yet, and when it is it must stay a manual,
// explicitly confirmed action: applying an old index over a live one rewrites
// the entire catalog, so an automatic or scheduled restore could destroy a
// working drive without anyone asking for it. Backups may be automatic; the
// decision to go backwards is always a human one.
package backup

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
	"time"
)

// configFileName is the persisted settings file, stored as JSON in the runtime
// data directory next to the server's config.toml. Like the WebDAV toggle it
// stays outside the database, so enabling backups needs no migration.
const configFileName = "backup.json"

// DefaultInterval backs up weekly. A drive index is small and changes slowly;
// more often mostly costs Telegram uploads.
const DefaultInterval = 168 * time.Hour

// MinInterval guards against a schedule that would hammer Telegram.
const MinInterval = time.Hour

// Store owns the backup settings and the record of where the last snapshot went.
// It is read on the scheduling path and written by the settings API, so a write
// persists first and only then swaps the in-memory value: a failed write must
// not report success.
type Store struct {
	path   string
	logger *slog.Logger

	mu    sync.RWMutex
	value configFile
}

type configFile struct {
	Enabled bool `json:"enabled"`
	// Interval is a Go duration string ("168h"). Stored as text rather than a
	// number so the file stays readable and hand-editable, which is the whole
	// point of a settings file next to config.toml.
	Interval string `json:"interval"`
	// ChannelID is the Telegram channel holding the snapshots. Persisting it is
	// what lets Create run without ever scanning: without it every backup would
	// have to search for its own destination first.
	ChannelID   int64  `json:"channelId,omitempty"`
	ChannelName string `json:"channelName,omitempty"`
	// OwnerUserID is whose index gets written. The scheduled job carries it
	// rather than enumerating users, so a periodic run costs nothing extra.
	OwnerUserID int64 `json:"ownerUserId,omitempty"`
	// LastBackupAt and LastMessageID describe the newest snapshot, for the
	// settings page and for finding the document after a restart.
	LastBackupAt  string `json:"lastBackupAt,omitempty"`
	LastMessageID int64  `json:"lastMessageId,omitempty"`
	LastSize      int64  `json:"lastSize,omitempty"`
}

// Settings is the caller-facing view of the stored configuration.
type Settings struct {
	Enabled       bool      `json:"enabled"`
	Interval      string    `json:"interval"`
	OwnerUserID   int64     `json:"ownerUserId,omitempty"`
	ChannelID     int64     `json:"channelId"`
	LastBackupAt  time.Time `json:"lastBackupAt,omitempty"`
	LastMessageID int64     `json:"lastMessageId,omitempty"`
	LastSize      int64     `json:"lastSize,omitempty"`
}

func NewStore(path string, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{path: path, logger: logger}
}

// Load reads the persisted settings. A missing file is the documented default
// (disabled), and an unreadable one degrades to the default rather than
// refusing to start the server over an optional feature.
func (s *Store) Load() error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}
	content, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read backup config: %w", err)
	}
	var parsed configFile
	if err := json.Unmarshal(content, &parsed); err != nil {
		s.logger.Warn("backup.config.unreadable", "path", s.path, "error", err)
		return nil
	}
	parsed.Interval = normalizeInterval(parsed.Interval)
	s.mu.Lock()
	s.value = parsed
	s.mu.Unlock()
	return nil
}

// Path is the JSON file backing the settings; it is reported in diagnostics.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Settings returns the current configuration, with the interval already
// normalised so callers never have to re-validate it.
func (s *Store) Settings() Settings {
	if s == nil {
		return Settings{Interval: DefaultInterval.String()}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Settings{
		Enabled:       s.value.Enabled,
		Interval:      normalizeInterval(s.value.Interval),
		OwnerUserID:   s.value.OwnerUserID,
		ChannelID:     s.value.ChannelID,
		LastBackupAt:  parseTime(s.value.LastBackupAt),
		LastMessageID: s.value.LastMessageID,
		LastSize:      s.value.LastSize,
	}
}

// Interval returns the configured period, falling back to the default.
func (s *Store) Interval() time.Duration {
	parsed, err := time.ParseDuration(s.Settings().Interval)
	if err != nil || parsed < MinInterval {
		return DefaultInterval
	}
	return parsed
}

// ChannelID returns the channel holding the snapshots, or zero when no backup
// has run yet.
func (s *Store) ChannelID() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value.ChannelID
}

// Configure applies the user-facing knobs. An empty interval, or one below
// MinInterval, falls back to the default instead of being rejected outright:
// the settings endpoint is a small surface and a bad value there should not
// leave backups silently unscheduled.
//
// ownerUserID records whose index the schedule should write. A non-positive
// value keeps the existing owner, so toggling the checkbox cannot silently
// orphan the schedule.
func (s *Store) Configure(enabled bool, interval string, ownerUserID int64) error {
	return s.update(func(value *configFile) {
		value.Enabled = enabled
		value.Interval = normalizeInterval(interval)
		if ownerUserID > 0 {
			value.OwnerUserID = ownerUserID
		}
	})
}

// RecordSnapshot remembers where a finished backup landed. The channel id is
// what lets the next run skip scanning entirely.
func (s *Store) RecordSnapshot(channelID int64, channelName string, messageID, size int64, at time.Time) error {
	return s.update(func(value *configFile) {
		value.ChannelID = channelID
		value.ChannelName = channelName
		value.LastMessageID = messageID
		value.LastSize = size
		value.LastBackupAt = at.UTC().Format(time.RFC3339)
	})
}

func (s *Store) update(mutate func(*configFile)) error {
	if s == nil {
		return errors.New("backup: store is not configured")
	}
	if strings.TrimSpace(s.path) == "" {
		return errors.New("backup: store path is empty")
	}
	s.mu.Lock()
	next := s.value
	mutate(&next)
	s.mu.Unlock()

	if err := writeConfigFile(s.path, next); err != nil {
		return err
	}
	s.mu.Lock()
	s.value = next
	s.mu.Unlock()
	return nil
}

// normalizeInterval validates the configured period and keeps the user's own
// spelling when it is usable. Round-tripping through time.Duration.String() would
// turn "24h" into "24h0m0s", which is technically the same value but makes the
// settings file -- a file people are meant to be able to read and edit -- worse.
func normalizeInterval(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return DefaultInterval.String()
	}
	parsed, err := time.ParseDuration(trimmed)
	if err != nil || parsed < MinInterval {
		return DefaultInterval.String()
	}
	return trimmed
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// writeConfigFile writes JSON through a temporary file in the destination
// directory, so a crash mid-write cannot leave a half-written file behind.
func writeConfigFile(path string, value configFile) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create backup config directory: %w", err)
	}
	encoded, err := json.MarshalIndent(value, "", " ")
	if err != nil {
		return fmt.Errorf("encode backup config: %w", err)
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(directory, configFileName+".tmp*")
	if err != nil {
		return fmt.Errorf("create backup config temporary file: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() {
		if temporaryName != "" {
			_ = os.Remove(temporaryName)
		}
	}()
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write backup config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close backup config: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace backup config: %w", err)
	}
	temporaryName = ""
	return nil
}

// DefaultConfigPath returns the JSON file used by the running server. The data
// directory resolution matches the WebDAV toggle's so both settings live side by
// side; it is duplicated rather than imported so this package does not depend on
// the WebDAV one.
func DefaultConfigPath() string {
	return filepath.Join(dataDir(os.Args, os.UserHomeDir), configFileName)
}

// dataDir searches the same candidates, in the same order, as the WebDAV
// toggle: an explicit -c/--config flag, then a config file in the working
// directory, then the default home location.
func dataDir(args []string, homeDir func() (string, error)) string {
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
