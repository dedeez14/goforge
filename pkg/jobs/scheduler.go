package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// Scheduler enqueues fresh job rows from the recurring schedule
// table at their configured interval. It is intentionally simpler
// than a full cron-expression evaluator: most "send weekly digest"
// or "purge old refresh tokens every hour" use-cases are happy with
// an integer interval, and storing one is much easier to reason
// about than a 5-field expression.
//
// Atomicity contract: the new job row and the schedule's
// next_run_at advancement are written inside the same transaction
// whenever the configured Queue supports a transactional enqueue
// (implements the TxEnqueuer interface — *Postgres does). A commit
// failure then leaves both untouched and the next tick is free to
// retry without producing duplicate work. When the Queue does not
// support transactional enqueue the Scheduler falls back to a
// non-atomic path and logs a warning on startup.
type Scheduler struct {
	Pool *pgxpool.Pool

	// Queue is the enqueue target. Its Enqueue method is always
	// honoured; if it additionally implements TxEnqueuer the
	// Scheduler will use the transactional variant so the new job
	// row and the schedule advancement commit atomically.
	//
	// Leaving Queue nil makes the Scheduler default to a
	// (*Postgres){Pool: s.Pool} behind the scenes — convenient for
	// apps that only want the built-in Postgres backend.
	Queue Queue

	Logger zerolog.Logger
}

// TxEnqueuer is the optional interface a Queue may implement to
// enqueue a job inside a caller-supplied pgx.Tx. When it is absent
// Scheduler falls back to the Queue.Enqueue method, which does not
// share the transaction.
//
// Implementations MUST accept an empty opts.DedupeKey (meaning "no
// dedupe") and MUST translate a dedupe-key collision into
// ErrDuplicate without aborting the transaction — on Postgres that
// means ON CONFLICT DO NOTHING rather than catching 23505.
type TxEnqueuer interface {
	EnqueueTx(ctx context.Context, tx pgx.Tx, kind string, payload any, opts EnqueueOptions) (*Job, error)
}

// Run blocks until ctx is cancelled. It wakes every Tick to look for
// schedules whose next_run_at <= now() and atomically advances them.
func (s *Scheduler) Run(ctx context.Context, tick time.Duration) error {
	if tick <= 0 {
		tick = 30 * time.Second
	}
	if s.Queue == nil {
		// Default the Queue to the shipped Postgres implementation
		// so Scheduler stays plug-and-play. The Postgres backend
		// satisfies TxEnqueuer, so atomicity is preserved.
		s.Queue = NewPostgres(s.Pool)
	}
	if _, ok := s.Queue.(TxEnqueuer); !ok {
		s.Logger.Warn().Msg("Scheduler.Queue does not implement TxEnqueuer; enqueue and schedule advancement are NOT atomic. Consider switching to *jobs.Postgres or implementing EnqueueTx to avoid duplicate executions on transient commit failures.")
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := s.dispatchDue(ctx); err != nil {
				s.Logger.Warn().Err(err).Msg("schedule dispatch")
			}
		}
	}
}

// dispatchDue is exposed for tests. Both the INSERT into jobs and
// the UPDATE of job_schedules.next_run_at run on the same tx so
// they commit atomically whenever the Queue implements TxEnqueuer.
func (s *Scheduler) dispatchDue(ctx context.Context) error {
	q := s.Queue
	if q == nil {
		q = NewPostgres(s.Pool)
	}
	txq, _ := q.(TxEnqueuer)

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id, name, queue, kind, payload, interval_secs, next_run_at
		FROM job_schedules
		WHERE enabled = TRUE AND next_run_at <= now()
		FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	type due struct {
		id        string
		name      string
		queue     string
		kind      string
		payload   json.RawMessage
		interval  int
		nextRunAt time.Time
	}
	var dues []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.name, &d.queue, &d.kind, &d.payload, &d.interval, &d.nextRunAt); err != nil {
			rows.Close()
			return err
		}
		dues = append(dues, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, d := range dues {
		// Dedupe key ties each invocation to the schedule's current
		// next_run_at: stable across transaction retries (the row is
		// locked with FOR UPDATE SKIP LOCKED, so next_run_at cannot
		// move underneath us) but different across successive ticks,
		// so overlapping invocations are never silently dropped. A
		// time.Now()-based key would change on every retry and defeat
		// the unique index; a purely static key would collide with a
		// still-active previous invocation and lose work.
		opts := EnqueueOptions{
			Queue:     d.queue,
			DedupeKey: "schedule:" + d.id + ":" + d.nextRunAt.UTC().Format(time.RFC3339Nano),
		}

		if txq != nil {
			if _, err := txq.EnqueueTx(ctx, tx, d.kind, d.payload, opts); err != nil && !errors.Is(err, ErrDuplicate) {
				s.Logger.Warn().Err(err).Str("schedule", d.name).Msg("enqueue from schedule failed")
				return err
			}
		} else {
			// Non-atomic fallback: the Queue does not offer an
			// in-tx variant, so call Enqueue on its own connection.
			// We logged a warning about this in Run.
			if _, err := q.Enqueue(ctx, d.kind, d.payload, opts); err != nil && !errors.Is(err, ErrDuplicate) {
				s.Logger.Warn().Err(err).Str("schedule", d.name).Msg("enqueue from schedule failed")
				continue
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE job_schedules SET next_run_at = now() + ($2 || ' seconds')::interval, updated_at = now() WHERE id = $1`,
			d.id, d.interval); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
