package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/tgdrive/teldrive/v2/internal/telegramstore"
)

// ChannelName is the Telegram channel that holds snapshots. It is created on the
// first backup and reused afterwards; the id is remembered in the settings file
// so later runs never have to look for it.
const ChannelName = "teldrive-backup"

// documentPrefix marks the uploaded documents so a scan can recognise its own
// files rather than treating every document in the channel as a backup.
const documentPrefix = "teldrive-index"

// scanPageSize bounds one ListDocumentMessages call.
const scanPageSize = 100

var (
	ErrNotConfigured    = errors.New("backup: service is not configured")
	ErrScanUnsupported  = errors.New("backup: storage cannot list channel documents")
	ErrSnapshotNotFound = errors.New("backup: snapshot not found")
)

// Service builds and stores drive-index snapshots.
type Service struct {
	queries Querier
	storage telegramstore.Storage
	store   *Store
	logger  *slog.Logger
	now     func() time.Time
}

func NewService(queries Querier, storage telegramstore.Storage, store *Store, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{queries: queries, storage: storage, store: store, logger: logger, now: time.Now}
}

// Entry describes one snapshot stored in Telegram.
type Entry struct {
	MessageID int64     `json:"messageId"`
	ChannelID int64     `json:"channelId"`
	CreatedAt time.Time `json:"createdAt"`
	Name      string    `json:"name,omitempty"`
	Size      int64     `json:"size,omitempty"`
}

// Result reports what a finished backup contained, for the caller's log or UI.
type Result struct {
	Entry
	Counts Counts `json:"counts"`
}

// Create builds the index and uploads it.
//
// It never enumerates the channel: the destination is remembered from the last
// run, so the only Telegram traffic is the upload itself. That is what keeps a
// scheduled backup cheap enough to leave enabled.
func (s *Service) Create(ctx context.Context, userID int64) (Result, error) {
	if s == nil || s.queries == nil || s.storage == nil || s.store == nil {
		return Result{}, ErrNotConfigured
	}
	if userID <= 0 {
		return Result{}, fmt.Errorf("backup: invalid user %d", userID)
	}

	started := s.now().UTC()
	index, err := BuildIndex(ctx, s.queries, userID, started)
	if err != nil {
		return Result{}, err
	}
	payload, err := json.Marshal(index)
	if err != nil {
		return Result{}, fmt.Errorf("backup: encode index: %w", err)
	}
	// Sealing happens before the channel is touched, so a passphrase problem
	// cannot leave a half-finished backup behind. An empty passphrase is not a
	// problem: it means the snapshot is stored as plain JSON.
	sealed := false
	if password := s.store.Password(); password != "" {
		payload, err = Seal(payload, password, started)
		if err != nil {
			return Result{}, err
		}
		sealed = true
	}

	channelID, channelName, err := s.ensureChannel(ctx, userID)
	if err != nil {
		return Result{}, err
	}

	name := documentName(started, sealed)
	stored, err := s.storage.Upload(ctx, telegramstore.UploadRequest{
		UserID:    userID,
		ChannelID: channelID,
		Name:      name,
		Reader:    bytes.NewReader(payload),
		Size:      int64(len(payload)),
		Threads:   1,
	})
	if err != nil {
		return Result{}, fmt.Errorf("backup: upload index: %w", err)
	}
	if err := s.store.RecordSnapshot(channelID, channelName, stored.MessageID, int64(len(payload)), started); err != nil {
		// The snapshot exists; only the record of it failed. Report it, because
		// the next run would otherwise create a second channel.
		return Result{}, fmt.Errorf("backup: record snapshot: %w", err)
	}
	s.logger.Info("backup.created",
		"user", userID, "channel", channelID, "message", stored.MessageID,
		"bytes", len(payload), "sealed", sealed,
		"files", index.Counts.Files, "parts", index.Counts.Parts)

	return Result{
		Entry: Entry{
			MessageID: stored.MessageID,
			ChannelID: channelID,
			CreatedAt: started,
			Name:      name,
			Size:      int64(len(payload)),
		},
		Counts: index.Counts,
	}, nil
}

// Scan lists the snapshots currently in the backup channel.
//
// This is the one operation that walks Telegram history, which is why it is only
// ever called from an explicit user action: it costs one API round trip per page
// and is exactly the kind of traffic that provokes FLOOD_WAIT when repeated.
func (s *Service) Scan(ctx context.Context, userID int64) ([]Entry, error) {
	if s == nil || s.storage == nil || s.store == nil {
		return nil, ErrNotConfigured
	}
	if userID <= 0 {
		return nil, fmt.Errorf("backup: invalid user %d", userID)
	}
	lister, ok := s.storage.(telegramstore.DocumentMessageLister)
	if !ok {
		return nil, ErrScanUnsupported
	}
	channelID := s.store.ChannelID()
	if channelID == 0 {
		// No backup has run yet, so there is no channel to enumerate. Not an
		// error: the answer genuinely is "nothing stored".
		return nil, nil
	}

	var entries []Entry
	before := int64(0)
	for {
		page, err := lister.ListDocumentMessages(ctx, telegramstore.ListDocumentMessagesRequest{
			UserID:    userID,
			ChannelID: channelID,
			BeforeID:  before,
			Limit:     scanPageSize,
		})
		if err != nil {
			return nil, fmt.Errorf("backup: list channel documents: %w", err)
		}
		for _, message := range page.Messages {
			entries = append(entries, Entry{
				MessageID: message.ID,
				ChannelID: channelID,
				CreatedAt: message.CreatedAt,
			})
		}
		if page.Exhausted || len(page.Messages) == 0 {
			return entries, nil
		}
		// Guard against a pager that stops advancing instead of looping forever.
		if page.BeforeID == before && page.BeforeID != 0 {
			return entries, nil
		}
		before = page.BeforeID
	}
}

// Download streams a stored snapshot, so it can be inspected or handed to a
// restore. length < 0 reads to the end.
func (s *Service) Download(ctx context.Context, userID, messageID, offset, length int64) (io.ReadCloser, error) {
	if s == nil || s.storage == nil || s.store == nil {
		return nil, ErrNotConfigured
	}
	channelID := s.store.ChannelID()
	if channelID == 0 {
		return nil, ErrSnapshotNotFound
	}
	return s.storage.OpenRange(ctx, telegramstore.RangeRequest{
		UserID:    userID,
		ChannelID: channelID,
		MessageID: messageID,
		Offset:    offset,
		Length:    length,
	})
}

// maxIndexBytes caps how much of a snapshot is read into memory. A drive index is
// a few megabytes even for a large drive; the bound exists so a corrupt or
// hostile document in the channel cannot exhaust the server.
const maxIndexBytes = 256 << 20

// LoadSnapshot downloads one stored snapshot and parses it, unsealing it with
// the passphrase from the settings.
func (s *Service) LoadSnapshot(ctx context.Context, userID, messageID int64) (*Index, error) {
	if s == nil || s.store == nil {
		return nil, ErrNotConfigured
	}
	return s.LoadSnapshotWithPassword(ctx, userID, messageID, s.store.Password())
}

// LoadSnapshotWithPassword is LoadSnapshot with the passphrase supplied by the
// caller, for a snapshot that was sealed with a different one than the settings
// currently hold. An empty passphrase is fine for an unsealed snapshot and is
// refused, with ErrPasswordRequired, for a sealed one.
//
// Reading is the only part of a restore that touches Telegram, and it happens
// before anything is written, so a snapshot that cannot be fetched, cannot be
// unsealed or does not parse fails without having altered the catalog.
func (s *Service) LoadSnapshotWithPassword(ctx context.Context, userID, messageID int64, password string) (*Index, error) {
	if s == nil || s.storage == nil || s.store == nil {
		return nil, ErrNotConfigured
	}
	if userID <= 0 {
		return nil, fmt.Errorf("backup: invalid user %d", userID)
	}
	reader, err := s.Download(ctx, userID, messageID, 0, -1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()

	// Read one byte past the limit so hitting it is an error rather than a
	// silently truncated document that then fails to parse for an unrelated
	// reason.
	payload, err := io.ReadAll(io.LimitReader(reader, maxIndexBytes+1))
	if err != nil {
		return nil, fmt.Errorf("backup: read snapshot: %w", err)
	}
	if len(payload) > maxIndexBytes {
		return nil, fmt.Errorf("backup: snapshot is larger than %d MiB", maxIndexBytes>>20)
	}
	// Note whether the stored bytes were encrypted before they are opened, so a
	// summary can report it; the plaintext no longer says.
	sealed := IsSealed(payload)
	payload, err = Unseal(payload, password)
	if err != nil {
		return nil, err
	}
	var index Index
	if err := json.Unmarshal(payload, &index); err != nil {
		return nil, fmt.Errorf("backup: decode snapshot: %w", err)
	}
	index.Sealed = sealed
	return &index, nil
}

// Inspect describes a stored snapshot without applying anything.
//
// It exists so the settings page can show what is inside before someone commits
// to a restore -- and, more importantly, so it can ask for the passphrase of a
// sealed snapshot instead of failing at the last moment. Nothing here writes.
func (s *Service) Inspect(ctx context.Context, userID, messageID int64, password string) (IndexSummary, error) {
	index, err := s.LoadSnapshotWithPassword(ctx, userID, messageID, password)
	if err != nil {
		return IndexSummary{}, err
	}
	return index.Summary(), nil
}

// ensureChannel returns the snapshot channel, creating it on first use.
func (s *Service) ensureChannel(ctx context.Context, userID int64) (int64, string, error) {
	if existing := s.store.ChannelID(); existing != 0 {
		return existing, ChannelName, nil
	}
	created, err := s.storage.CreateChannel(ctx, userID, ChannelName)
	if err != nil {
		return 0, "", fmt.Errorf("backup: create backup channel: %w", err)
	}
	if created.ID == 0 {
		return 0, "", errors.New("backup: Telegram returned an empty channel id")
	}
	name := strings.TrimSpace(created.Name)
	if name == "" {
		name = ChannelName
	}
	s.logger.Info("backup.channel_created", "user", userID, "channel", created.ID, "name", name)
	return created.ID, name, nil
}

// documentName sorts lexicographically by time so the channel reads in order.
//
// A sealed snapshot says so in its own filename. That is the only place the
// channel listing can tell the difference -- the scanner never reads message
// bodies -- and it is what a person recovering a drive years later will see
// before they have any tooling at hand.
func documentName(at time.Time, sealed bool) string {
	suffix := ".json"
	if sealed {
		suffix = ".sealed.json"
	}
	return fmt.Sprintf("%s-%s%s", documentPrefix, at.UTC().Format("20060102-150405"), suffix)
}
