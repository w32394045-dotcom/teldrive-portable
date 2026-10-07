package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river"

	"github.com/tgdrive/teldrive/v2/internal/backup"
)

type stubBackupRunner struct {
	calls   []int64
	result  backup.Result
	failure error
}

func (s *stubBackupRunner) Create(_ context.Context, userID int64) (backup.Result, error) {
	s.calls = append(s.calls, userID)
	return s.result, s.failure
}

func TestBackupWorkerRunsTheConfiguredUser(t *testing.T) {
	t.Parallel()
	runner := &stubBackupRunner{result: backup.Result{
		Entry:  backup.Entry{MessageID: 5, ChannelID: 9, Size: 128},
		Counts: backup.Counts{Files: 3, Parts: 4, Channels: 1},
	}}
	worker := NewBackupWorker(runner)

	// A nil JobRow is what River passes for a plain run; it also exercises the
	// branch that skips RecordOutput.
	if err := worker.Work(context.Background(), &river.Job[BackupArgs]{Args: BackupArgs{UserID: 42}}); err != nil {
		t.Fatalf("Work() returned %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0] != 42 {
		t.Fatalf("runner calls = %v, want [42]", runner.calls)
	}
}

func TestBackupWorkerRejectsAMissingUser(t *testing.T) {
	t.Parallel()
	runner := &stubBackupRunner{}
	worker := NewBackupWorker(runner)
	if err := worker.Work(context.Background(), &river.Job[BackupArgs]{}); err == nil {
		t.Error("a job without a user must fail rather than back up user 0")
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner was called with %v", runner.calls)
	}
}

func TestBackupWorkerPropagatesFailure(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("telegram is unhappy")
	worker := NewBackupWorker(&stubBackupRunner{failure: sentinel})
	err := worker.Work(context.Background(), &river.Job[BackupArgs]{Args: BackupArgs{UserID: 1}})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Work() = %v, want it to wrap %v so River retries", err, sentinel)
	}
}

func TestBackupWorkerWithoutARunnerIsRejected(t *testing.T) {
	t.Parallel()
	if err := (&BackupWorker{}).Work(context.Background(), &river.Job[BackupArgs]{Args: BackupArgs{UserID: 1}}); !errors.Is(err, ErrBackupNotConfigured) {
		t.Fatalf("Work() = %v, want ErrBackupNotConfigured", err)
	}
}

func TestBackupArgsIdentifyTheJobKind(t *testing.T) {
	t.Parallel()
	if got := (BackupArgs{}).Kind(); got != BackupKind {
		t.Errorf("Kind() = %q, want %q", got, BackupKind)
	}
	// The periodic job and the manual insert must land on the same queue, or a
	// scheduled run would be served by a worker pool that never picks it up.
	insert := (BackupArgs{}).InsertOpts()
	periodic := BackupArgs{}.InsertOpts()
	if insert.Queue != periodic.Queue {
		t.Errorf("queues differ: %q vs %q", insert.Queue, periodic.Queue)
	}
	if insert.Queue != CleanupQueue {
		t.Errorf("queue = %q, want %q", insert.Queue, CleanupQueue)
	}
}
