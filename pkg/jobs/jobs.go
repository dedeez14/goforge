// Package jobs is goforge's background-work primitive.
//
// Why it exists: the outbox publishes domain events; jobs do *work*
// (send email, resize an image, deliver a webhook, run a nightly
// rollup). The two are complementary.
//
// Design highlights:
//
//   - Postgres-backed (no extra infra) using `FOR UPDATE SKIP LOCKED`,
//     so any number of replicas can run dispatchers safely.
//   - Idempotent enqueue via dedupe_key — a webhook delivery enqueued
//     twice for the same (event_id, endpoint) collapses to one row.
//   - Exponential-jittered backoff with a per-job MaxAttempts cap.
//     Exhausted jobs land in status='dead' and are NEVER deleted, so
//     operators can inspect and replay them.
//   - Cron-style recurring schedules in a sibling table; the runner
//     picks them up alongside ad-hoc work.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Status is the job lifecycle state. Strings are the canonical wire
// format and match the column directly.
type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	StatusDead    Status = "dead"
)

// Job is the in-memory shape of a queue row.
type Job struct {
	ID          uuid.UUID
	Queue       string
	Kind        string
	Payload     json.RawMessage
	Status      Status
	Attempts    int
	MaxAttempts int
	LastError   string
	RunAt       time.Time
	LockedAt    *time.Time
	LockedBy    *string
	CompletedAt *time.Time
	DedupeKey   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// EnqueueOptions configures a single Enqueue call. The zero value is
// fine: queue=default, max_attempts=5, run immediately, no dedupe.
type EnqueueOptions struct {
	Queue       string
	MaxAttempts int
	RunAt       time.Time // zero = now
	DedupeKey   string    // empty = no dedupe
}

// ErrDuplicate is returned by Enqueue when DedupeKey collides with an
// open job in the same queue. Callers typically swallow it.
var ErrDuplicate = errors.New("jobs: duplicate dedupe_key")

// Queue is the storage abstraction. Implementations live alongside the
// concrete database (Postgres for goforge); the interface is what the
// Runner consumes.
type Queue interface {
	Enqueue(ctx context.Context, kind string, payload any, opts EnqueueOptions) (*Job, error)
	Claim(ctx context.Context, queue string, workerID string, lease time.Duration) (*Job, error)
	Complete(ctx context.Context, id uuid.UUID) error
	Fail(ctx context.Context, id uuid.UUID, errMsg string, retryAt time.Time, dead bool) error
	Stats(ctx context.Context) (Stats, error)
}

// Stats is a small dashboard view returned by Queue.Stats. Useful for
// /admin/jobs and Prometheus scrape.
type Stats struct {
	Pending int
	Running int
	Failed  int
	Dead    int
	Done24h int
}
