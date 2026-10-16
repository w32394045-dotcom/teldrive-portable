package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tgdrive/teldrive/v2/internal/db/sqlcgen"
)

// RestoreMode selects how an index is applied over the live catalog.
//
// The two modes answer different questions. Overwrite asks "make the drive be
// what this snapshot says it was", so it deletes rows the snapshot does not
// mention -- including trash. Merge asks "give me back what I lost", so it only
// ever adds. Neither is safe to run on a timer, which is why nothing but an
// explicit, confirmed request reaches this code.
type RestoreMode string

const (
	// RestoreOverwrite rebuilds the owner's catalog from the index.
	RestoreOverwrite RestoreMode = "overwrite"
	// RestoreMerge adds what the catalog lacks and touches nothing else.
	RestoreMerge RestoreMode = "merge"
)

// RestoreModes lists the accepted values, in the order the UI should offer them.
var RestoreModes = []RestoreMode{RestoreOverwrite, RestoreMerge}

// Valid reports whether the value is one this package understands.
func (m RestoreMode) Valid() bool {
	return m == RestoreOverwrite || m == RestoreMerge
}

// ParseRestoreMode converts a stored or submitted string. An empty value falls
// back to overwrite, which is the mode that actually reproduces a snapshot;
// anything else unrecognised is rejected rather than guessed at, because a typo
// must not silently become "delete my drive" or "quietly do nothing".
func ParseRestoreMode(value string) (RestoreMode, error) {
	trimmed := RestoreMode(strings.ToLower(strings.TrimSpace(value)))
	if trimmed == "" {
		return RestoreOverwrite, nil
	}
	if !trimmed.Valid() {
		return "", fmt.Errorf("backup: unknown restore mode %q", value)
	}
	return trimmed, nil
}

var (
	// ErrRestoreUnavailable means the restorer was never wired up.
	ErrRestoreUnavailable = errors.New("backup: restore is not available")
	// ErrUploadsInProgress refuses an overwrite while uploads are still running.
	ErrUploadsInProgress = errors.New("backup: uploads are still in progress")
	// ErrCatalogCycle means the leaf-first delete could not make progress.
	ErrCatalogCycle = errors.New("backup: the catalog contains a parent cycle")
	// ErrInvalidIndex marks a snapshot that must not be applied. It is separated
	// from the rest so the API can answer 400 instead of 500: a bad document is
	// the caller's problem, not the server's.
	ErrInvalidIndex = errors.New("invalid snapshot")
)

const (
	// restoreBatchSize bounds one pipelined batch. Local PostgreSQL handles far
	// more per round trip, but a bounded batch keeps peak memory predictable.
	restoreBatchSize = 500
	// maxDeleteRounds bounds the leaf-first delete loop. A tree deeper than this
	// is pathological; a cycle would never terminate at all, so the loop stops
	// and rolls back instead of spinning.
	maxDeleteRounds = 512
	// maxRestoreFiles and maxRestoreParts reject an index so large that applying
	// it would exhaust memory or hang the request.
	maxRestoreFiles = 5_000_000
	maxRestoreParts = 20_000_000
)

// RestoreResult reports exactly what an apply did. It is returned to the UI so
// the user can see what changed rather than being told "done".
type RestoreResult struct {
	Mode         RestoreMode `json:"mode"`
	Channels     int         `json:"channels"`
	Files        int         `json:"files"`
	Folders      int         `json:"folders"`
	Parts        int         `json:"parts"`
	SkippedFiles int         `json:"skippedFiles"`
	SkippedParts int         `json:"skippedParts"`
	DeletedFiles int         `json:"deletedFiles,omitempty"`
	// Orphans are files whose parent exists neither in the catalog nor in the
	// index, so they could not be attached to anything. They are reported, not
	// inserted: a file with a dangling parent is invisible either way.
	Orphans []string `json:"orphans,omitempty"`
	// DurationMS is how long the apply took, for the settings page.
	DurationMS int64 `json:"durationMs"`
}

// Restorer applies an index to the catalog.
//
// It talks to PostgreSQL through pgx directly rather than through the generated
// query set: this is a bulk load of rows that already exist elsewhere, it needs
// explicit control over statement order to satisfy the self-referencing foreign
// key, and adding it to `db/queries` would mean running sqlc for one feature.
// Table names still come from sqlcgen.QualifiedName so a custom schema keeps
// working exactly like the rest of the application.
type Restorer struct {
	pool    *pgxpool.Pool
	logger  *slog.Logger
	timeout time.Duration
}

func NewRestorer(pool *pgxpool.Pool, logger *slog.Logger) *Restorer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Restorer{pool: pool, logger: logger, timeout: 30 * time.Minute}
}

// Apply writes the index into the catalog inside a single transaction.
//
// One transaction is the whole safety story: a restore that fails halfway would
// otherwise leave a drive that is neither the old one nor the snapshot, which is
// worse than either. Either every row lands or nothing does.
func (r *Restorer) Apply(ctx context.Context, userID int64, index *Index, mode RestoreMode) (RestoreResult, error) {
	if r == nil || r.pool == nil {
		return RestoreResult{}, ErrRestoreUnavailable
	}
	if !mode.Valid() {
		return RestoreResult{}, fmt.Errorf("backup: unknown restore mode %q", mode)
	}
	if err := index.Validate(userID); err != nil {
		return RestoreResult{}, err
	}
	if r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}

	started := time.Now()
	result := RestoreResult{Mode: mode, Orphans: nil}
	now := started.UTC()

	// The leaf-first delete has to see the real thing, and the enum casts below
	// have to name the configured schema.
	filesTable := sqlcgen.QualifiedName("files")
	partsTable := sqlcgen.QualifiedName("file_parts")
	channelsTable := sqlcgen.QualifiedName("channels")
	sessionsTable := sqlcgen.QualifiedName("upload_sessions")
	kindEnum := sqlcgen.QualifiedName("file_kind")
	statusEnum := sqlcgen.QualifiedName("file_status")
	healthEnum := sqlcgen.QualifiedName("channel_health")

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RestoreResult{}, fmt.Errorf("backup: begin restore: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := restoreChannels(ctx, tx, channelsTable, healthEnum, userID, index.Channels); err != nil {
		return RestoreResult{}, err
	}
	result.Channels = len(index.Channels)

	existing, err := existingFileIDs(ctx, tx, filesTable, userID)
	if err != nil {
		return RestoreResult{}, err
	}

	if mode == RestoreOverwrite {
		deleted, err := wipeCatalog(ctx, tx, filesTable, sessionsTable, userID)
		if err != nil {
			return RestoreResult{}, err
		}
		result.DeletedFiles = deleted
		existing = map[string]bool{}
	}

	present := make(map[string]bool, len(existing)+len(index.Files))
	for id := range existing {
		present[id] = true
	}

	inserted, skipped, orphans, err := insertFiles(ctx, tx, filesTable, kindEnum, statusEnum, userID, index.Files, present, now)
	if err != nil {
		return RestoreResult{}, err
	}
	result.Files = inserted
	result.SkippedFiles = skipped
	result.Orphans = orphans
	result.Folders = countFolders(index, present, orphans)

	parts, skippedParts, err := insertParts(ctx, tx, partsTable, index.Parts, present)
	if err != nil {
		return RestoreResult{}, err
	}
	result.Parts = parts
	result.SkippedParts = skippedParts

	if err := tx.Commit(ctx); err != nil {
		return RestoreResult{}, fmt.Errorf("backup: commit restore: %w", err)
	}
	result.DurationMS = time.Since(started).Milliseconds()
	r.logger.Info("backup.restored",
		"user", userID, "mode", mode, "files", result.Files, "folders", result.Folders,
		"parts", result.Parts, "deleted", result.DeletedFiles, "orphans", len(result.Orphans))
	return result, nil
}

// countFolders reports how many folders the restore left in place, which is the
// number a user actually recognises as "my directory tree".
func countFolders(index *Index, present map[string]bool, orphans []string) int {
	if index == nil {
		return 0
	}
	dangling := make(map[string]bool, len(orphans))
	for _, id := range orphans {
		dangling[id] = true
	}
	folders := 0
	for _, file := range index.Files {
		if file.Kind != string(sqlcgen.FileKindFolder) || dangling[file.ID] {
			continue
		}
		if present[file.ID] {
			folders++
		}
	}
	return folders
}

// restoreChannels upserts the channel rows the index refers to. Channels are
// never deleted here: a channel id is also referenced by uploads and by parts
// that may belong to files the index does not mention.
func restoreChannels(ctx context.Context, tx pgx.Tx, table, healthEnum string, userID int64, channels []Channel) error {
	if len(channels) == 0 {
		return nil
	}
	statement := fmt.Sprintf(`INSERT INTO %s (channel_id, user_id, name, selected, health, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5::text::%s, now(), now())
ON CONFLICT (user_id, channel_id) DO UPDATE
SET name = EXCLUDED.name, selected = EXCLUDED.selected, health = EXCLUDED.health, updated_at = now()`, table, healthEnum)

	rows := make([][]any, 0, len(channels))
	for _, channel := range channels {
		if channel.ChannelID == 0 {
			continue
		}
		name := strings.TrimSpace(channel.Name)
		if name == "" {
			name = fmt.Sprintf("channel-%d", channel.ChannelID)
		}
		health := channel.Health
		switch health {
		case "unknown", "healthy", "degraded", "unavailable":
		default:
			// An unknown health value would fail the enum cast; degrade to
			// "unknown" instead of refusing an otherwise valid restore.
			health = "unknown"
		}
		rows = append(rows, []any{channel.ChannelID, userID, name, channel.Selected, health})
	}
	if _, err := execBatches(ctx, tx, statement, rows); err != nil {
		return fmt.Errorf("backup: restore channels: %w", err)
	}
	return nil
}

// existingFileIDs reads the ids already in the catalog, so a merge knows what to
// skip and every mode knows which parents can be attached to.
func existingFileIDs(ctx context.Context, tx pgx.Tx, table string, userID int64) (map[string]bool, error) {
	rows, err := tx.Query(ctx, fmt.Sprintf("SELECT id::text FROM %s WHERE user_id = $1", table), userID)
	if err != nil {
		return nil, fmt.Errorf("backup: list existing files: %w", err)
	}
	defer rows.Close()
	ids := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("backup: scan existing file: %w", err)
		}
		ids[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("backup: list existing files: %w", err)
	}
	return ids, nil
}

// wipeCatalog removes the owner's catalog for an overwrite.
//
// files.parent_id carries a self-referencing foreign key with ON DELETE
// RESTRICT, which is checked per row rather than at the end of the statement, so
// a single DELETE of the whole table fails as soon as it reaches a parent. The
// loop removes one generation of leaves at a time, which is the only ordering
// that is guaranteed to satisfy it. Trash goes too: overwrite means "be exactly
// this snapshot", and the bytes remain in Telegram regardless.
func wipeCatalog(ctx context.Context, tx pgx.Tx, filesTable, sessionsTable string, userID int64) (int, error) {
	// Open uploads hold a parent_id reference into the catalog. Deleting their
	// folders under a running upload would corrupt it, so refuse instead of
	// guessing.
	var active int
	if err := tx.QueryRow(ctx, fmt.Sprintf(
		`SELECT count(*) FROM %s WHERE user_id = $1 AND state IN ('open', 'completing') AND expires_at > now()`,
		sessionsTable), userID).Scan(&active); err != nil {
		return 0, fmt.Errorf("backup: count active uploads: %w", err)
	}
	if active > 0 {
		return 0, fmt.Errorf("%w: %d upload(s) are still running, finish or cancel them first", ErrUploadsInProgress, active)
	}
	// Everything left is finished or abandoned; its folder link is meaningless
	// and it would otherwise block the delete below.
	if _, err := tx.Exec(ctx, fmt.Sprintf(
		`UPDATE %s SET parent_id = NULL WHERE user_id = $1 AND parent_id IS NOT NULL`,
		sessionsTable), userID); err != nil {
		return 0, fmt.Errorf("backup: detach upload sessions: %w", err)
	}

	deleteLeaves := fmt.Sprintf(`DELETE FROM %s f
WHERE f.user_id = $1
  AND NOT EXISTS (
      SELECT 1 FROM %s c WHERE c.parent_id = f.id AND c.user_id = f.user_id
  )`, filesTable, filesTable)

	deleted := 0
	for round := 0; round < maxDeleteRounds; round++ {
		tag, err := tx.Exec(ctx, deleteLeaves, userID)
		if err != nil {
			return 0, fmt.Errorf("backup: clear catalog: %w", err)
		}
		affected := int(tag.RowsAffected())
		deleted += affected
		if affected == 0 {
			// Nothing was removable: either the catalog is empty, which is the
			// good case, or a cycle is holding rows back.
			var remaining int
			if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE user_id = $1`, filesTable), userID).Scan(&remaining); err != nil {
				return 0, fmt.Errorf("backup: count remaining files: %w", err)
			}
			if remaining > 0 {
				return 0, fmt.Errorf("%w: %d row(s) could not be removed", ErrCatalogCycle, remaining)
			}
			return deleted, nil
		}
	}
	return 0, fmt.Errorf("%w: more than %d levels deep", ErrCatalogCycle, maxDeleteRounds)
}

// insertFiles writes the files in dependency waves, returning how many were
// inserted, how many already existed, and the ids that had nowhere to go.
//
// A row can be inserted as soon as its parent is known to exist, so each wave is
// a set of rows with no dependencies between them and a single batched statement
// is enough. Rows left over after the waves stop are orphans: their parent is in
// neither the catalog nor the index, so the foreign key would reject them.
func insertFiles(ctx context.Context, tx pgx.Tx, table, kindEnum, statusEnum string, userID int64, files []File, present map[string]bool, now time.Time) (inserted, skipped int, orphans []string, err error) {
	statement := fmt.Sprintf(`INSERT INTO %s (
    id, user_id, parent_id, name, kind, mime_type, size, hash_algorithm, hash_value,
    encryption, encryption_key_version, status, mod_time, generation, created_at, updated_at, deleted_at
) VALUES ($1, $2, $3, $4, $5::text::%s, $6, $7, $8, $9, $10, $11, $12::text::%s, $13, $14, $15, $16, NULL)
ON CONFLICT (id) DO NOTHING`, table, kindEnum, statusEnum)

	pending := make([]File, 0, len(files))
	for _, file := range files {
		if present[file.ID] {
			skipped++
			continue
		}
		pending = append(pending, file)
	}

	for len(pending) > 0 {
		ready := make([]File, 0, len(pending))
		rest := make([]File, 0, len(pending))
		for _, file := range pending {
			if file.ParentID == "" || present[file.ParentID] {
				ready = append(ready, file)
				continue
			}
			rest = append(rest, file)
		}
		if len(ready) == 0 {
			break
		}
		rows := make([][]any, 0, len(ready))
		for _, file := range ready {
			rows = append(rows, fileInsertArgs(file, userID, now))
		}
		affected, err := execBatches(ctx, tx, statement, rows)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("backup: restore files: %w", err)
		}
		inserted += int(affected)
		for _, file := range ready {
			present[file.ID] = true
		}
		skipped += len(ready) - int(affected)
		pending = rest
	}

	if len(pending) > 0 {
		orphans = make([]string, 0, len(pending))
		for _, file := range pending {
			orphans = append(orphans, file.ID)
		}
	}
	return inserted, skipped, orphans, nil
}

func fileInsertArgs(file File, userID int64, now time.Time) []any {
	createdAt := file.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := file.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	// mod_time is NOT NULL in the schema; the index always carries it, but a
	// hand-written index should still restore rather than fail on a null.
	modTime := createdAt
	if file.ModTime != nil && !file.ModTime.IsZero() {
		modTime = *file.ModTime
	}
	generation := file.Generation
	if generation <= 0 {
		generation = 1
	}
	return []any{
		uuidParam(file.ID),
		userID,
		uuidParam(file.ParentID),
		file.Name,
		file.Kind,
		textValue(file.MIMEType),
		int8Value(file.Size),
		textValue(file.HashAlgorithm),
		textValue(file.HashValue),
		file.Encryption,
		int4Value(file.EncryptionKeyVersion),
		file.Status,
		modTime,
		generation,
		createdAt,
		updatedAt,
	}
}

// insertParts writes the message mapping. A part whose file did not make it into
// the catalog is counted and dropped: file_parts references files, so keeping it
// is not an option.
func insertParts(ctx context.Context, tx pgx.Tx, table string, parts []Part, present map[string]bool) (inserted, skipped int, err error) {
	statement := fmt.Sprintf(`INSERT INTO %s (file_id, part_no, channel_id, message_id, plain_size, stored_size, checksum, salt, block_hashes, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT DO NOTHING`, table)

	rows := make([][]any, 0, len(parts))
	for _, part := range parts {
		if !present[part.FileID] {
			skipped++
			continue
		}
		rows = append(rows, []any{
			uuidParam(part.FileID),
			part.PartNo,
			part.ChannelID,
			part.MessageID,
			int8Value(part.PlainSize),
			int8Value(part.StoredSize),
			textValue(part.Checksum),
			textValue(part.Salt),
			part.BlockHashes,
		})
	}
	affected, err := execBatches(ctx, tx, statement, rows)
	if err != nil {
		return 0, 0, fmt.Errorf("backup: restore parts: %w", err)
	}
	// ON CONFLICT DO NOTHING makes an existing mapping cheap to re-apply; the
	// rows it skipped are reported rather than counted as new.
	return int(affected), skipped + len(rows) - int(affected), nil
}

// execBatches runs one statement for many argument rows, pipelined in bounded
// batches so each batch costs a single round trip.
func execBatches(ctx context.Context, tx pgx.Tx, statement string, rows [][]any) (int64, error) {
	var affected int64
	for start := 0; start < len(rows); start += restoreBatchSize {
		end := min(start+restoreBatchSize, len(rows))
		batch := &pgx.Batch{}
		for _, row := range rows[start:end] {
			batch.Queue(statement, row...)
		}
		results := tx.SendBatch(ctx, batch)
		for index := start; index < end; index++ {
			tag, err := results.Exec()
			if err != nil {
				_ = results.Close()
				return affected, err
			}
			affected += tag.RowsAffected()
		}
		if err := results.Close(); err != nil {
			return affected, err
		}
	}
	return affected, nil
}

// Validate rejects an index that would produce a broken catalog. The document
// arrives as a file in Telegram, so it is untrusted input: it may have been
// truncated, hand-edited, or replaced by something else entirely, and every one
// of those has to fail here rather than halfway through a restore.
//
// Everything it rejects is wrapped in ErrInvalidIndex, which is what lets the API
// distinguish "you sent me a bad document" from "the server broke".
func (i *Index) Validate(userID int64) error {
	if err := i.validate(userID); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidIndex, err)
	}
	return nil
}

func (i *Index) validate(userID int64) error {
	if i == nil {
		return errors.New("the index is empty")
	}
	if i.Format != IndexFormat {
		return fmt.Errorf("not a Teldrive index (format %q)", i.Format)
	}
	if i.Version < 1 {
		return fmt.Errorf("index version %d is not valid", i.Version)
	}
	if i.Version > IndexVersion {
		return fmt.Errorf("index version %d was written by a newer Teldrive", i.Version)
	}
	if i.UserID != userID {
		return fmt.Errorf("the index belongs to user %d, not %d", i.UserID, userID)
	}
	if len(i.Files) > maxRestoreFiles {
		return fmt.Errorf("the index has %d files, more than this build restores", len(i.Files))
	}
	if len(i.Parts) > maxRestoreParts {
		return fmt.Errorf("the index has %d parts, more than this build restores", len(i.Parts))
	}

	known := make(map[string]bool, len(i.Files))
	for _, file := range i.Files {
		if _, err := uuid.Parse(file.ID); err != nil {
			return fmt.Errorf("file id %q is not a UUID", file.ID)
		}
		if known[file.ID] {
			return fmt.Errorf("file id %s appears twice", file.ID)
		}
		known[file.ID] = true
		if strings.TrimSpace(file.Name) == "" {
			return fmt.Errorf("file %s has an empty name", file.ID)
		}
		switch file.Kind {
		case string(sqlcgen.FileKindFolder):
			if file.Size != nil {
				return fmt.Errorf("folder %s carries a size", file.ID)
			}
		case string(sqlcgen.FileKindFile):
			if file.Size == nil || *file.Size < 0 {
				return fmt.Errorf("file %s has no usable size", file.ID)
			}
		default:
			return fmt.Errorf("file %s has unknown kind %q", file.ID, file.Kind)
		}
		if file.Encryption && file.EncryptionKeyVersion == nil {
			return fmt.Errorf("encrypted file %s has no key version", file.ID)
		}
		if !file.Encryption && file.EncryptionKeyVersion != nil {
			return fmt.Errorf("unencrypted file %s carries a key version", file.ID)
		}
		if (file.HashAlgorithm == "") != (file.HashValue == "") {
			return fmt.Errorf("file %s has half a hash", file.ID)
		}
	}
	for _, file := range i.Files {
		if file.ParentID == "" {
			continue
		}
		if _, err := uuid.Parse(file.ParentID); err != nil {
			return fmt.Errorf("parent id %q is not a UUID", file.ParentID)
		}
		if file.ParentID == file.ID {
			return fmt.Errorf("file %s is its own parent", file.ID)
		}
	}
	for _, part := range i.Parts {
		if !known[part.FileID] {
			return fmt.Errorf("part references unknown file %s", part.FileID)
		}
		if part.PartNo <= 0 {
			return fmt.Errorf("part %d of file %s has a non-positive number", part.PartNo, part.FileID)
		}
		if part.ChannelID == 0 || part.MessageID == 0 {
			return errors.New("a part is missing its channel or message id")
		}
		if part.PlainSize != nil && *part.PlainSize < 0 {
			return fmt.Errorf("part %d of file %s has a negative size", part.PartNo, part.FileID)
		}
		if part.StoredSize != nil && *part.StoredSize < 0 {
			return fmt.Errorf("part %d of file %s has a negative stored size", part.PartNo, part.FileID)
		}
	}
	return nil
}

func uuidParam(value string) pgtype.UUID {
	if strings.TrimSpace(value) == "" {
		return pgtype.UUID{}
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

func textValue(value string) pgtype.Text {
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}

func int8Value(value *int64) pgtype.Int8 {
	if value == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *value, Valid: true}
}

func int4Value(value *int32) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *value, Valid: true}
}
