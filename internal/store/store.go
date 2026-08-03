// Package store defines the persistence contract shared by the SQLite and
// Postgres backends. All methods take an explicit `now` where time matters so
// timing behavior is fully deterministic under a fake clock.
package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound           = errors.New("not found")
	ErrAlreadyExists      = errors.New("already exists")
	ErrFailedPrecondition = errors.New("failed precondition")
)

type Store interface {
	Migrate(ctx context.Context) error
	Ping(ctx context.Context) error
	Close() error
	Dialect() string

	// Events streams dispatcher wakeup hints (task ready, queue config
	// changed, force-run). Best-effort: events may be dropped under load;
	// the dispatcher reconcile loop is the correctness backstop.
	Events() <-chan Event

	// Queues
	UpsertQueue(ctx context.Context, u QueueUpsert, now time.Time) (Queue, UpsertOutcome, error)
	CreateQueue(ctx context.Context, u QueueUpsert, now time.Time) (Queue, error)
	PatchQueue(ctx context.Context, name string, p QueuePatch, now time.Time) (Queue, error)
	GetQueue(ctx context.Context, name string) (Queue, error)
	GetQueuesByID(ctx context.Context, ids []int64) ([]Queue, error)
	ListQueues(ctx context.Context, opts ListQueuesOpts) ([]Queue, string, error)
	DeleteQueue(ctx context.Context, name string) error
	// SetQueueState is used by pause/resume; disabled is set via PatchQueue.
	SetQueueState(ctx context.Context, name string, state QueueState, now time.Time) (Queue, error)
	PurgeQueue(ctx context.Context, name string, now time.Time) (Queue, error)
	QueueStats(ctx context.Context, name, idPrefix string, now time.Time) (QueueStats, error)

	// Tasks
	CreateTask(ctx context.Context, t NewTask, now time.Time) (Task, error)
	CreateTasks(ctx context.Context, ts []NewTask, now time.Time) ([]TaskResult, error)
	GetTask(ctx context.Context, queue, taskID string, view View) (Task, error)
	ListTasks(ctx context.Context, opts ListTasksOpts) ([]Task, string, error)
	// DeleteTask tombstones the row (state=deleted) for the dedup window.
	DeleteTask(ctx context.Context, queue, taskID string, now time.Time) error
	// ForceRun moves a pending task's schedule_time to now and emits
	// EventRunTask. The dispatcher dispatches it immediately, bypassing
	// rate limits and PAUSED state (GCP parity).
	ForceRun(ctx context.Context, queue, taskID string, now time.Time) (Task, error)
	ListAttempts(ctx context.Context, queue, taskID string) ([]Attempt, error)

	// Dispatcher
	SeedDispatch(ctx context.Context) ([]QueueSeed, error)
	// ClaimTasks atomically leases up to budget.N due pending tasks per
	// queue, incrementing dispatch_count and stamping first_attempt_at.
	ClaimTasks(ctx context.Context, now time.Time, leaseToken string, leaseGrace time.Duration, budgets []ClaimBudget) ([]Task, error)
	// ClaimTaskForRun leases one specific task regardless of schedule_time
	// or queue pause state (still refuses disabled queues).
	ClaimTaskForRun(ctx context.Context, now time.Time, leaseToken string, leaseGrace time.Duration, taskPK int64) (Task, bool, error)
	// NextPending returns the min schedule_time among pending tasks.
	NextPending(ctx context.Context, queueID int64, now time.Time) (*time.Time, error)
	// FinishAttempt records the attempt and applies the outcome, fenced on
	// (taskPK, leaseToken, state=leased). Returns false when the fence
	// fails (lease expired/stolen or task deleted/purged) — the result is
	// then discarded.
	FinishAttempt(ctx context.Context, now time.Time, taskPK int64, leaseToken string, att Attempt, outcome Outcome, nextSchedule time.Time) (bool, error)
	ListExpiredLeases(ctx context.Context, now time.Time, limit int) ([]Task, error)
	// CleanupTerminal hard-deletes terminal rows older than their TTL
	// (floored by the dedup window). Returns rows deleted.
	CleanupTerminal(ctx context.Context, now time.Time, succeededTTL, failedTTL, dedupWindow time.Duration, limit int) (int64, error)
	// HardDeletePurged removes pending tasks superseded by a purge watermark.
	HardDeletePurged(ctx context.Context, limit int) (int64, error)
}

type TaskResult struct {
	Task Task
	Err  error // ErrAlreadyExists etc.; nil on success
}
