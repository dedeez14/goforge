package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
)

// stubQueue is a goroutine-safe in-memory Queue used to exercise
// Runner. It is sufficient for behaviour tests; real Postgres
// integration is exercised in cmd/pentest and in the runtime.
type stubQueue struct {
	mu      sync.Mutex
	jobs    []*Job
	failed  []error
	doneIDs []uuid.UUID
}

func (s *stubQueue) push(j *Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = append(s.jobs, j)
}

func (s *stubQueue) Enqueue(_ context.Context, kind string, payload any, _ EnqueueOptions) (*Job, error) {
	body, _ := json.Marshal(payload)
	j := &Job{ID: uuid.New(), Kind: kind, Payload: body, MaxAttempts: 3, RunAt: time.Now()}
	s.push(j)
	return j, nil
}

// EnqueueTx satisfies TxEnqueuer so Scheduler sees stubQueue as a
// transactional Queue in tests. The stub ignores the tx argument
// because it is in-memory.
func (s *stubQueue) EnqueueTx(_ context.Context, _ pgx.Tx, kind string, payload any, _ EnqueueOptions) (*Job, error) {
	body, _ := json.Marshal(payload)
	j := &Job{ID: uuid.New(), Kind: kind, Payload: body, MaxAttempts: 3, RunAt: time.Now()}
	s.push(j)
	return j, nil
}

func (s *stubQueue) Claim(_ context.Context, _, _ string, _ time.Duration) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, j := range s.jobs {
		if j.Status != StatusDone && j.Status != StatusDead && j.Attempts < j.MaxAttempts && j.RunAt.Before(time.Now().Add(time.Second)) {
			j.Attempts++
			j.Status = StatusRunning
			s.jobs[i] = j
			return j, nil
		}
	}
	return nil, nil
}

func (s *stubQueue) Complete(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ID == id {
			j.Status = StatusDone
			s.doneIDs = append(s.doneIDs, id)
		}
	}
	return nil
}

func (s *stubQueue) Fail(_ context.Context, id uuid.UUID, msg string, retryAt time.Time, dead bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = append(s.failed, errors.New(msg))
	for _, j := range s.jobs {
		if j.ID == id {
			if dead {
				j.Status = StatusDead
			} else {
				j.Status = StatusFailed
				j.RunAt = retryAt
			}
		}
	}
	return nil
}

func (s *stubQueue) Stats(context.Context) (Stats, error) { return Stats{}, nil }

func TestRunner_HandlerCompletesJob(t *testing.T) {
	t.Parallel()
	q := &stubQueue{}
	q.push(&Job{ID: uuid.New(), Kind: "say-hi", Payload: []byte(`{"name":"d"}`), MaxAttempts: 3, RunAt: time.Now()})
	var ran atomic.Int32
	r := &Runner{
		Queue:       q,
		Queues:      []string{"default"},
		Handlers:    map[string]Handler{"say-hi": func(_ context.Context, _ json.RawMessage) error { ran.Add(1); return nil }},
		Concurrency: 1,
		Poll:        10 * time.Millisecond,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  10 * time.Millisecond,
		Logger:      zerolog.Nop(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)
	if ran.Load() == 0 {
		t.Fatal("handler never executed")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.doneIDs) != 1 {
		t.Fatalf("expected 1 done, got %d", len(q.doneIDs))
	}
}

func TestRunner_PanicCountsAsFailure(t *testing.T) {
	t.Parallel()
	q := &stubQueue{}
	q.push(&Job{ID: uuid.New(), Kind: "boom", Payload: []byte(`{}`), MaxAttempts: 1, RunAt: time.Now()})
	r := &Runner{
		Queue:       q,
		Queues:      []string{"default"},
		Handlers:    map[string]Handler{"boom": func(context.Context, json.RawMessage) error { panic("kaboom") }},
		Concurrency: 1,
		Poll:        10 * time.Millisecond,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  10 * time.Millisecond,
		Logger:      zerolog.Nop(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.failed) == 0 {
		t.Fatal("panic should be reported as a failure")
	}
}

func TestRunner_UnknownKindGoesToDLQ(t *testing.T) {
	t.Parallel()
	q := &stubQueue{}
	q.push(&Job{ID: uuid.New(), Kind: "ghost", Payload: []byte(`{}`), MaxAttempts: 1, RunAt: time.Now()})
	r := &Runner{
		Queue:       q,
		Queues:      []string{"default"},
		Handlers:    map[string]Handler{},
		Concurrency: 1,
		Poll:        10 * time.Millisecond,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  10 * time.Millisecond,
		Logger:      zerolog.Nop(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.failed) == 0 {
		t.Fatal("unknown kind should hit DLQ")
	}
	if q.jobs[0].Status != StatusDead {
		t.Fatalf("status = %s, want dead", q.jobs[0].Status)
	}
}
