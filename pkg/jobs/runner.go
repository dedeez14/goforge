package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Handler is what application code registers per job kind. Returning
// nil completes the job; returning an error reschedules it (or drops
// it to DLQ when attempts are exhausted).
type Handler func(ctx context.Context, payload json.RawMessage) error

// HandlerFunc is sugar so you can pass a literal func.
type HandlerFunc func(ctx context.Context, payload json.RawMessage) error

// Runner pulls jobs from a Queue and dispatches them to Handlers.
// Concurrency is bounded by the number of workers; each worker has
// its own goroutine and an isolated context.
type Runner struct {
	Queue       Queue
	Queues      []string
	Handlers    map[string]Handler
	Concurrency int
	Poll        time.Duration // how often to poll when the queue is empty
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	Logger      zerolog.Logger
	WorkerID    string
}

// Run blocks until ctx is cancelled. It spawns Concurrency workers,
// each polling the configured queues round-robin.
func (r *Runner) Run(ctx context.Context) error {
	if r.Concurrency <= 0 {
		r.Concurrency = 4
	}
	if len(r.Queues) == 0 {
		r.Queues = []string{"default"}
	}
	if r.Poll <= 0 {
		r.Poll = time.Second
	}
	if r.BaseBackoff <= 0 {
		r.BaseBackoff = 5 * time.Second
	}
	if r.MaxBackoff <= 0 {
		r.MaxBackoff = 10 * time.Minute
	}
	if r.WorkerID == "" {
		r.WorkerID = fmt.Sprintf("worker-%d", time.Now().UnixNano())
	}

	var wg sync.WaitGroup
	for i := 0; i < r.Concurrency; i++ {
		wg.Add(1)
		go func(workerN int) {
			defer wg.Done()
			r.workerLoop(ctx, workerN)
		}(i)
	}
	wg.Wait()
	return nil
}

func (r *Runner) workerLoop(ctx context.Context, n int) {
	id := fmt.Sprintf("%s-%d", r.WorkerID, n)
	tick := time.NewTimer(0)
	defer tick.Stop()
	queueIdx := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		// Round-robin across queues so one busy queue doesn't
		// starve the others.
		queue := r.Queues[queueIdx%len(r.Queues)]
		queueIdx++

		job, err := r.Queue.Claim(ctx, queue, id, time.Minute)
		if err != nil {
			r.Logger.Warn().Err(err).Str("queue", queue).Msg("claim failed")
			tick.Reset(r.Poll)
			continue
		}
		if job == nil {
			tick.Reset(r.Poll)
			continue
		}
		r.run(ctx, job)
		tick.Reset(0) // try the next job immediately
	}
}

func (r *Runner) run(ctx context.Context, job *Job) {
	h, ok := r.Handlers[job.Kind]
	if !ok {
		r.Logger.Warn().Str("kind", job.Kind).Msg("no handler registered; dropping to DLQ")
		_ = r.Queue.Fail(ctx, job.ID, "no handler", time.Time{}, true)
		return
	}

	// Hand the handler a child context with a generous default
	// timeout. Handlers that need more should set their own.
	hctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	err := safeRun(hctx, h, job.Payload)
	if err == nil {
		if err := r.Queue.Complete(ctx, job.ID); err != nil {
			r.Logger.Warn().Err(err).Str("job_id", job.ID.String()).Msg("complete failed")
		}
		return
	}

	dead := job.Attempts >= job.MaxAttempts
	retryAt := time.Now().Add(r.backoff(job.Attempts))
	if ferr := r.Queue.Fail(ctx, job.ID, err.Error(), retryAt, dead); ferr != nil {
		r.Logger.Error().Err(ferr).Str("job_id", job.ID.String()).Msg("fail update failed")
	}
	r.Logger.Warn().
		Err(err).
		Str("kind", job.Kind).
		Int("attempt", job.Attempts).
		Bool("dead", dead).
		Msg("job failed")
}

// backoff returns an exponentially-increasing duration with full
// jitter, capped at MaxBackoff.
func (r *Runner) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	expo := time.Duration(math.Pow(2, float64(attempt-1))) * r.BaseBackoff
	if expo > r.MaxBackoff {
		expo = r.MaxBackoff
	}
	// Full jitter — picking a uniform value within [0, expo)
	// avoids the thundering-herd that plain exponential causes.
	//nolint:gosec // not used for cryptography
	return time.Duration(rand.Int63n(int64(expo)) + int64(r.BaseBackoff))
}

// safeRun isolates handler panics so a bad payload cannot kill a
// worker forever.
func safeRun(ctx context.Context, h Handler, payload json.RawMessage) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	if h == nil {
		return errors.New("nil handler")
	}
	return h(ctx, payload)
}
