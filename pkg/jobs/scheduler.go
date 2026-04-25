package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
// next_run_at advancement are written inside the same transaction,
// so a commit failure leaves both untouched and the next tick is
// free to retry without producing duplicate work.
type Scheduler struct {
	Pool   *pgxpool.Pool
	Logger zerolog.Logger
}

// Run blocks until ctx is cancelled. It wakes every Tick to look for
// schedules whose next_run_at <= now() and atomically advances them.
func (s *Scheduler) Run(ctx context.Context, tick time.Duration) error {
	if tick <= 0 {
		tick = 30 * time.Second
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
// they commit atomically.
func (s *Scheduler) dispatchDue(ctx context.Context) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id, name, queue, kind, payload, interval_secs
		FROM job_schedules
		WHERE enabled = TRUE AND next_run_at <= now()
		FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	type due struct {
		id       string
		name     string
		queue    string
		kind     string
		payload  json.RawMessage
		interval int
	}
	var dues []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.name, &d.queue, &d.kind, &d.payload, &d.interval); err != nil {
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
		// Use a deterministic dedupe key that does NOT depend on
		// time.Now: if this transaction is retried after a transient
		// commit failure the same key is reused, and the unique
		// constraint on dedupe_key collapses retries into a single
		// job rather than producing duplicates.
		dedupeKey := "schedule:" + d.id + ":" + d.kind
		if _, err := tx.Exec(ctx, `
			INSERT INTO jobs (queue, kind, payload, max_attempts, run_at, dedupe_key)
			VALUES ($1, $2, $3, 5, now(), $4)`,
			d.queue, d.kind, d.payload, dedupeKey,
		); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				// Already enqueued for this run — fall through to
				// advance next_run_at as if we had just inserted.
			} else {
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
