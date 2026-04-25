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
type Scheduler struct {
	Pool   *pgxpool.Pool
	Queue  Queue
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

// dispatchDue is exposed for tests.
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

	for _, d := range dues {
		_, err := s.Queue.Enqueue(ctx, d.kind, d.payload, EnqueueOptions{
			Queue:     d.queue,
			DedupeKey: "schedule:" + d.id + ":" + time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil && !errors.Is(err, ErrDuplicate) {
			s.Logger.Warn().Err(err).Str("schedule", d.name).Msg("enqueue from schedule failed")
			continue
		}
		if _, err := tx.Exec(ctx,
			`UPDATE job_schedules SET next_run_at = now() + ($2 || ' seconds')::interval, updated_at = now() WHERE id = $1`,
			d.id, d.interval); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
