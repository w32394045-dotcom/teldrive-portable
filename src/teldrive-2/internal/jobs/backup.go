package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/riverqueue/river"

	"github.com/tgdrive/teldrive/v2/internal/backup"
)

// BackupKind is the River job kind for a drive-index snapshot.
const BackupKind = "teldrive_backup"

// BackupPeriodicID is the periodic job that keeps snapshots on a schedule. One
// per instance: the backup settings are instance-level, exactly like the WebDAV
// toggle, and the owner recorded there is whose index gets written.
const BackupPeriodicID = "teldrive-backup"

// backupTimeout bounds one snapshot. Building the index of a large drive means
// paging through every row, so this is generous on purpose.
const backupTimeout = 30 * time.Minute

var ErrBackupNotConfigured = errors.New("backup worker is not configured")

// BackupArgs carries the user whose index is written. The periodic job stores it
// rather than enumerating users, so a scheduled run costs nothing beyond the
// write itself.
type BackupArgs struct {
	UserID int64 `json:"userId"`
}

func (BackupArgs) Kind() string { return BackupKind }

func (BackupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: CleanupQueue, MaxAttempts: 3, Priority: 3}
}

// BackupRunner is the part of the backup service this worker needs. It is an
// interface so the worker can be tested without a database or Telegram.
type BackupRunner interface {
	Create(ctx context.Context, userID int64) (backup.Result, error)
}

type BackupWorker struct {
	river.WorkerDefaults[BackupArgs]
	runner BackupRunner
}

func NewBackupWorker(runner BackupRunner) *BackupWorker {
	return &BackupWorker{runner: runner}
}

func (w *BackupWorker) Timeout(*river.Job[BackupArgs]) time.Duration { return backupTimeout }

func (w *BackupWorker) Work(ctx context.Context, job *river.Job[BackupArgs]) error {
	if w == nil || w.runner == nil {
		return ErrBackupNotConfigured
	}
	if job.Args.UserID <= 0 {
		return errors.New("backup job is missing a user")
	}
	result, err := w.runner.Create(ctx, job.Args.UserID)
	if err != nil {
		return err
	}
	if job.JobRow != nil {
		return river.RecordOutput(ctx, struct {
			MessageID int64         `json:"messageId"`
			ChannelID int64         `json:"channelId"`
			Size      int64         `json:"size"`
			Counts    backup.Counts `json:"counts"`
		}{
			MessageID: result.MessageID,
			ChannelID: result.ChannelID,
			Size:      result.Size,
			Counts:    result.Counts,
		})
	}
	return nil
}
