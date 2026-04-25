package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is the production-grade Queue implementation. It is the
// only Queue goforge ships, but the interface allows swapping (for
// example, a SQLite version for tests or a Redis Streams version for
// extreme throughput).
type Postgres struct{ Pool *pgxpool.Pool }

// NewPostgres wraps a pgxpool.
func NewPostgres(p *pgxpool.Pool) *Postgres { return &Postgres{Pool: p} }

// Enqueue inserts a new job. When opts.DedupeKey is set and another
// non-terminal job in the same queue already holds it, ErrDuplicate
// is returned.
func (q *Postgres) Enqueue(ctx context.Context, kind string, payload any, opts EnqueueOptions) (*Job, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	queue := opts.Queue
	if queue == "" {
		queue = "default"
	}
	max := opts.MaxAttempts
	if max <= 0 {
		max = 5
	}
	runAt := opts.RunAt
	if runAt.IsZero() {
		runAt = time.Now().UTC()
	}
	var dedupe any
	if opts.DedupeKey != "" {
		dedupe = opts.DedupeKey
	}

	row := q.Pool.QueryRow(ctx, `
		INSERT INTO jobs (queue, kind, payload, max_attempts, run_at, dedupe_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, queue, kind, payload, status, attempts, max_attempts,
		          run_at, locked_at, locked_by, completed_at, created_at, updated_at, dedupe_key`,
		queue, kind, body, max, runAt, dedupe,
	)
	job, err := scanJob(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return job, nil
}

// EnqueueTx is the transactional variant of Enqueue: the caller owns
// the pgx.Tx and the INSERT shares its lifetime, so the enqueue can
// be rolled back atomically with whatever the caller did alongside
// it (for example Scheduler advancing next_run_at). When DedupeKey
// collides with an open row the method uses ON CONFLICT DO NOTHING
// and returns ErrDuplicate — this keeps the transaction in a valid
// state (catching error 23505 from a plain INSERT would abort the
// tx and wedge later statements with "current transaction is aborted,
// commands ignored").
//
// Postgres satisfies TxEnqueuer.
func (q *Postgres) EnqueueTx(ctx context.Context, tx pgx.Tx, kind string, payload any, opts EnqueueOptions) (*Job, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	queue := opts.Queue
	if queue == "" {
		queue = "default"
	}
	max := opts.MaxAttempts
	if max <= 0 {
		max = 5
	}
	runAt := opts.RunAt
	if runAt.IsZero() {
		runAt = time.Now().UTC()
	}
	var dedupe any
	if opts.DedupeKey != "" {
		dedupe = opts.DedupeKey
	}

	sql := `
		INSERT INTO jobs (queue, kind, payload, max_attempts, run_at, dedupe_key)
		VALUES ($1, $2, $3, $4, $5, $6)`
	if opts.DedupeKey != "" {
		sql += `
		ON CONFLICT (queue, dedupe_key)
		  WHERE dedupe_key IS NOT NULL AND status IN ('pending', 'running', 'failed')
		DO NOTHING`
	}
	sql += `
		RETURNING id, queue, kind, payload, status, attempts, max_attempts,
		          run_at, locked_at, locked_by, completed_at, created_at, updated_at, dedupe_key`

	row := tx.QueryRow(ctx, sql, queue, kind, body, max, runAt, dedupe)
	job, err := scanJob(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// ON CONFLICT DO NOTHING skipped the insert.
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return job, nil
}

// Claim atomically locks one pending or retry-ready job for the
// caller. It returns nil, nil when the queue is empty so the runner
// loop can sleep without distinguishing between "no work" and "real
// error".
func (q *Postgres) Claim(ctx context.Context, queue, workerID string, lease time.Duration) (*Job, error) {
	tx, err := q.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx, `
		WITH ready AS (
			SELECT id FROM jobs
			WHERE queue = $1
			  AND status IN ('pending', 'failed')
			  AND run_at <= now()
			ORDER BY run_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE jobs SET
			status     = 'running',
			locked_at  = now(),
			locked_by  = $2,
			attempts   = attempts + 1,
			updated_at = now()
		WHERE id IN (SELECT id FROM ready)
		RETURNING id, queue, kind, payload, status, attempts, max_attempts,
		          run_at, locked_at, locked_by, completed_at, created_at, updated_at, dedupe_key`,
		queue, workerID,
	)
	job, err := scanJob(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			return nil, nil
		}
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	_ = lease // lease is enforced by the runner via context deadline
	return job, nil
}

// Complete marks the job done and clears the lock.
func (q *Postgres) Complete(ctx context.Context, id uuid.UUID) error {
	_, err := q.Pool.Exec(ctx, `
		UPDATE jobs SET
			status       = 'done',
			locked_at    = NULL,
			locked_by    = NULL,
			completed_at = now(),
			updated_at   = now()
		WHERE id = $1`, id)
	return err
}

// Fail marks a job retry-able (or dead). The runner decides between
// retry and dead by looking at attempts vs max_attempts.
func (q *Postgres) Fail(ctx context.Context, id uuid.UUID, errMsg string, retryAt time.Time, dead bool) error {
	if dead {
		_, err := q.Pool.Exec(ctx, `
			UPDATE jobs SET
				status     = 'dead',
				last_error = $2,
				locked_at  = NULL,
				locked_by  = NULL,
				updated_at = now()
			WHERE id = $1`, id, errMsg)
		return err
	}
	_, err := q.Pool.Exec(ctx, `
		UPDATE jobs SET
			status     = 'failed',
			last_error = $2,
			locked_at  = NULL,
			locked_by  = NULL,
			run_at     = $3,
			updated_at = now()
		WHERE id = $1`, id, errMsg, retryAt)
	return err
}

// Stats returns counts per status for dashboards.
func (q *Postgres) Stats(ctx context.Context) (Stats, error) {
	var s Stats
	err := q.Pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'pending'),
			COUNT(*) FILTER (WHERE status = 'running'),
			COUNT(*) FILTER (WHERE status = 'failed'),
			COUNT(*) FILTER (WHERE status = 'dead'),
			COUNT(*) FILTER (WHERE status = 'done' AND completed_at >= now() - interval '24 hours')
		FROM jobs`).Scan(&s.Pending, &s.Running, &s.Failed, &s.Dead, &s.Done24h)
	return s, err
}

func scanJob(row pgx.Row) (*Job, error) {
	var j Job
	if err := row.Scan(
		&j.ID, &j.Queue, &j.Kind, &j.Payload, &j.Status,
		&j.Attempts, &j.MaxAttempts,
		&j.RunAt, &j.LockedAt, &j.LockedBy, &j.CompletedAt,
		&j.CreatedAt, &j.UpdatedAt, &j.DedupeKey,
	); err != nil {
		return nil, err
	}
	return &j, nil
}
