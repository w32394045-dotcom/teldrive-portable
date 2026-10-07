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

	channelID, channelName, err := s.ensureChannel(ctx, userID)
	if err != nil {
		return Result{}, err
	}

	name := documentName(started)
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
		"bytes", len(payload), "files", index.Counts.Files, "parts", index.Counts.Parts)

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
func documentName(at time.Time) string {
	return fmt.Sprintf("%s-%s.json", documentPrefix, at.UTC().Format("20060102-150405"))
}
