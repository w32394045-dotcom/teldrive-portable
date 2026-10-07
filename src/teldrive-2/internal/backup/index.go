package backup

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tgdrive/teldrive/v2/internal/db/sqlcgen"
)

// Querier is the slice of the generated query set this package needs. Taking an
// interface rather than a pool is what lets the index builder be tested without
// a live PostgreSQL; *sqlcgen.Queries satisfies it.
type Querier interface {
	ListChannelsForOrphanCleanup(context.Context) ([]*sqlcgen.Channel, error)
	ListFilesAdvanced(context.Context, sqlcgen.ListFilesAdvancedParams) ([]*sqlcgen.File, error)
	ListFilePartsByFileIDs(context.Context, []pgtype.UUID) ([]*sqlcgen.FilePart, error)
}

const (
	// IndexFormat marks the file so a future reader can tell a Teldrive index
	// from any other JSON that happens to be in the channel.
	IndexFormat = "teldrive-index"
	// IndexVersion is the schema of the document below. It changes only when a
	// reader would have to interpret the file differently.
	IndexVersion = 1
	// indexPageSize bounds one ListFilesAdvanced call. The dump pages through
	// the whole drive, so this trades round trips against memory.
	indexPageSize int32 = 1000
	// partBatchSize bounds one ListFilePartsByFileIDs call.
	partBatchSize = 500
)

// Index is the backup document itself: everything needed to reattach a logical
// drive to the Telegram messages that hold its bytes.
type Index struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UserID    int64     `json:"userId"`
	Counts    Counts    `json:"counts"`
	Channels  []Channel `json:"channels"`
	Files     []File    `json:"files"`
	Parts     []Part    `json:"parts"`
}

type Counts struct {
	Files    int `json:"files"`
	Folders  int `json:"folders"`
	Parts    int `json:"parts"`
	Channels int `json:"channels"`
}

// Channel records a Telegram channel that holds data, so a restore knows which
// channels to expect.
type Channel struct {
	ChannelID int64  `json:"channelId"`
	Name      string `json:"name"`
	Selected  bool   `json:"selected"`
	Health    string `json:"health"`
}

// File is one catalog row. ParentID is the directory link; an empty value means
// the file sits at the root.
type File struct {
	ID                   string     `json:"id"`
	ParentID             string     `json:"parentId,omitempty"`
	Name                 string     `json:"name"`
	Kind                 string     `json:"kind"`
	Status               string     `json:"status"`
	Size                 *int64     `json:"size,omitempty"`
	MIMEType             string     `json:"mimeType,omitempty"`
	HashAlgorithm        string     `json:"hashAlgorithm,omitempty"`
	HashValue            string     `json:"hashValue,omitempty"`
	Encryption           bool       `json:"encryption"`
	EncryptionKeyVersion *int32     `json:"encryptionKeyVersion,omitempty"`
	Generation           int64      `json:"generation"`
	ModTime              *time.Time `json:"modTime,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

// Part is the mapping that makes the backup worth having: which Telegram
// message holds which slice of which file.
type Part struct {
	FileID     string `json:"fileId"`
	PartNo     int32  `json:"partNo"`
	ChannelID  int64  `json:"channelId"`
	MessageID  int64  `json:"messageId"`
	PlainSize  *int64 `json:"plainSize,omitempty"`
	StoredSize *int64 `json:"storedSize,omitempty"`
	Checksum   string `json:"checksum,omitempty"`
	Salt       string `json:"salt,omitempty"`
}

// BuildIndex reads the drive index for one user.
//
// Files come from ListFilesAdvanced with scope "drive", which walks the whole
// tree from the roots in one query rather than one round trip per folder, and
// pages on the id cursor so a long dump stays consistent while it runs.
// Trashed files are deliberately excluded: they are already reachable through
// the trash and would roughly double the document for no restore value.
func BuildIndex(ctx context.Context, queries Querier, userID int64, now time.Time) (*Index, error) {
	if queries == nil || userID <= 0 {
		return nil, fmt.Errorf("backup: index needs a query set and a user")
	}

	channels, err := listChannels(ctx, queries, userID)
	if err != nil {
		return nil, err
	}
	files, err := listFiles(ctx, queries, userID)
	if err != nil {
		return nil, err
	}
	parts, err := listParts(ctx, queries, files)
	if err != nil {
		return nil, err
	}

	index := &Index{
		Format:    IndexFormat,
		Version:   IndexVersion,
		CreatedAt: now.UTC(),
		UserID:    userID,
		Channels:  channels,
		Files:     files,
		Parts:     parts,
	}
	index.Counts.Channels = len(channels)
	index.Counts.Files = len(files)
	index.Counts.Parts = len(parts)
	for _, file := range files {
		if file.Kind == string(sqlcgen.FileKindFolder) {
			index.Counts.Folders++
		}
	}
	return index, nil
}

func listChannels(ctx context.Context, queries Querier, userID int64) ([]Channel, error) {
	rows, err := queries.ListChannelsForOrphanCleanup(ctx)
	if err != nil {
		return nil, fmt.Errorf("backup: list channels: %w", err)
	}
	channels := make([]Channel, 0, len(rows))
	for _, row := range rows {
		if row.UserID != userID {
			continue
		}
		channels = append(channels, Channel{
			ChannelID: row.ChannelID,
			Name:      row.Name,
			Selected:  row.Selected,
			Health:    string(row.Health),
		})
	}
	return channels, nil
}

func listFiles(ctx context.Context, queries Querier, userID int64) ([]File, error) {
	files := make([]File, 0, indexPageSize)
	var afterID pgtype.UUID
	for {
		rows, err := queries.ListFilesAdvanced(ctx, sqlcgen.ListFilesAdvancedParams{
			UserID:     userID,
			Scope:      "drive",
			Status:     sqlcgen.FileStatusActive,
			SearchType: "text",
			Categories: []string{},
			SortBy:     "id",
			SortOrder:  "asc",
			AfterID:    afterID,
			PageSize:   indexPageSize,
		})
		if err != nil {
			return nil, fmt.Errorf("backup: list files: %w", err)
		}
		if len(rows) == 0 {
			return files, nil
		}
		for _, row := range rows {
			files = append(files, fileFromRow(row))
			afterID = row.ID
		}
		// A short page means the cursor reached the end; stopping here avoids one
		// more query per dump.
		if len(rows) < int(indexPageSize) {
			return files, nil
		}
	}
}

func listParts(ctx context.Context, queries Querier, files []File) ([]Part, error) {
	var parts []Part
	batch := make([]pgtype.UUID, 0, partBatchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		rows, err := queries.ListFilePartsByFileIDs(ctx, batch)
		if err != nil {
			return fmt.Errorf("backup: list parts: %w", err)
		}
		for _, row := range rows {
			parts = append(parts, partFromRow(row))
		}
		batch = batch[:0]
		return nil
	}
	for _, file := range files {
		parsed, err := uuid.Parse(file.ID)
		if err != nil {
			continue
		}
		batch = append(batch, pgtype.UUID{Bytes: parsed, Valid: true})
		if len(batch) == partBatchSize {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return parts, nil
}

func fileFromRow(row *sqlcgen.File) File {
	return File{
		ID:                   uuidString(row.ID),
		ParentID:             uuidString(row.ParentID),
		Name:                 row.Name,
		Kind:                 string(row.Kind),
		Status:               string(row.Status),
		Size:                 optInt64(row.Size),
		MIMEType:             optText(row.MimeType),
		HashAlgorithm:        optText(row.HashAlgorithm),
		HashValue:            optText(row.HashValue),
		Encryption:           row.Encryption,
		EncryptionKeyVersion: optInt32(row.EncryptionKeyVersion),
		Generation:           row.Generation,
		ModTime:              optTime(row.ModTime),
		CreatedAt:            timeValue(row.CreatedAt),
		UpdatedAt:            timeValue(row.UpdatedAt),
	}
}

func partFromRow(row *sqlcgen.FilePart) Part {
	return Part{
		FileID:     uuidString(row.FileID),
		PartNo:     row.PartNo,
		ChannelID:  row.ChannelID,
		MessageID:  row.MessageID,
		PlainSize:  optInt64(row.PlainSize),
		StoredSize: optInt64(row.StoredSize),
		Checksum:   optText(row.Checksum),
		Salt:       optText(row.Salt),
	}
}

func uuidString(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return uuid.UUID(value.Bytes).String()
}

func optText(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func optInt64(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	converted := value.Int64
	return &converted
}

func optInt32(value pgtype.Int4) *int32 {
	if !value.Valid {
		return nil
	}
	converted := value.Int32
	return &converted
}

func optTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	converted := value.Time
	return &converted
}

func timeValue(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time
}
