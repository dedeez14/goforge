package resilience

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCircuitBreaker_ClosedByDefault(t *testing.T) {
	b := NewCircuitBreaker("test", CBConfig{})
	if b.State() != StateClosed {
		t.Fatalf("initial state = %s, want closed", b.State())
	}
}

func TestCircuitBreaker_TripsAfterConsecutiveFailures(t *testing.T) {
	b := NewCircuitBreaker("test", CBConfig{FailureThreshold: 3})
	ctx := context.Background()
	boom := errors.New("boom")
	for i := 0; i < 2; i++ {
		_ = b.Execute(ctx, func(context.Context) error { return boom })
	}
	if b.State() != StateClosed {
		t.Fatalf("after 2 failures state = %s, want closed", b.State())
	}
	_ = b.Execute(ctx, func(context.Context) error { return boom })
	if b.State() != StateOpen {
		t.Fatalf("after 3 failures state = %s, want open", b.State())
	}
}

func TestCircuitBreaker_OpenShortCircuits(t *testing.T) {
	b := NewCircuitBreaker("test", CBConfig{FailureThreshold: 1})
	ctx := context.Background()
	_ = b.Execute(ctx, func(context.Context) error { return errors.New("boom") })
	if b.State() != StateOpen {
		t.Fatalf("want open after first failure")
	}

	called := false
	err := b.Execute(ctx, func(context.Context) error { called = true; return nil })
	if called {
		t.Fatal("fn was called while breaker open")
	}
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("err = %v, want ErrOpen", err)
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	b := NewCircuitBreaker("test", CBConfig{FailureThreshold: 3})
	ctx := context.Background()
	boom := errors.New("boom")
	_ = b.Execute(ctx, func(context.Context) error { return boom })
	_ = b.Execute(ctx, func(context.Context) error { return boom })
	_ = b.Execute(ctx, func(context.Context) error { return nil })
	// counter reset; need 3 more failures to trip
	_ = b.Execute(ctx, func(context.Context) error { return boom })
	_ = b.Execute(ctx, func(context.Context) error { return boom })
	if b.State() != StateClosed {
		t.Fatalf("state = %s, want closed (counter should have reset)", b.State())
	}
}

func TestCircuitBreaker_HalfOpenAfterCooldown(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewCircuitBreaker("test", CBConfig{
		FailureThreshold: 1,
		CooldownPeriod:   time.Second,
		Clock:            func() time.Time { return now },
	})
	ctx := context.Background()
	_ = b.Execute(ctx, func(context.Context) error { return errors.New("boom") })
	if b.State() != StateOpen {
		t.Fatalf("want open")
	}
	now = now.Add(999 * time.Millisecond)
	if b.State() != StateOpen {
		t.Fatalf("state before cooldown = %s, want open", b.State())
	}
	now = now.Add(2 * time.Millisecond) // now > cooldown
	if b.State() != StateHalfOpen {
		t.Fatalf("state after cooldown = %s, want half_open", b.State())
	}
}

func TestCircuitBreaker_HalfOpenCloses_OnSuccessThreshold(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewCircuitBreaker("test", CBConfig{
		FailureThreshold: 1,
		SuccessThreshold: 2,
		CooldownPeriod:   time.Second,
		Clock:            func() time.Time { return now },
	})
	ctx := context.Background()
	_ = b.Execute(ctx, func(context.Context) error { return errors.New("boom") })
	now = now.Add(2 * time.Second) // elapse cooldown

	_ = b.Execute(ctx, func(context.Context) error { return nil })
	if b.State() != StateHalfOpen {
		t.Fatalf("after 1 success in half-open state = %s, want half_open", b.State())
	}
	_ = b.Execute(ctx, func(context.Context) error { return nil })
	if b.State() != StateClosed {
		t.Fatalf("after 2 successes in half-open state = %s, want closed", b.State())
	}
}

func TestCircuitBreaker_HalfOpenFailureReopens(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewCircuitBreaker("test", CBConfig{
		FailureThreshold: 1,
		CooldownPeriod:   time.Second,
		Clock:            func() time.Time { return now },
	})
	ctx := context.Background()
	_ = b.Execute(ctx, func(context.Context) error { return errors.New("boom") })
	now = now.Add(2 * time.Second)

	_ = b.Execute(ctx, func(context.Context) error { return errors.New("still bad") })
	if b.State() != StateOpen {
		t.Fatalf("state after half-open failure = %s, want open", b.State())
	}
}

func TestCircuitBreaker_HalfOpenProbeBudget(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewCircuitBreaker("test", CBConfig{
		FailureThreshold:  1,
		HalfOpenMaxProbes: 1,
		CooldownPeriod:    time.Second,
		Clock:             func() time.Time { return now },
	})
	ctx := context.Background()
	_ = b.Execute(ctx, func(context.Context) error { return errors.New("boom") })
	now = now.Add(2 * time.Second)
	_ = b.State() // promote to half-open

	// First probe holds the slot.
	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = b.Execute(ctx, func(context.Context) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	err := b.Execute(ctx, func(context.Context) error { return nil })
	if !errors.Is(err, ErrTooManyProbes) {
		t.Fatalf("err = %v, want ErrTooManyProbes", err)
	}
	close(release)
}

func TestCircuitBreaker_OnStateChangeFires(t *testing.T) {
	var transitions []string
	var mu sync.Mutex
	b := NewCircuitBreaker("mailer", CBConfig{
		FailureThreshold: 1,
		CooldownPeriod:   time.Millisecond,
		OnStateChange: func(name string, from, to State) {
			mu.Lock()
			defer mu.Unlock()
			transitions = append(transitions, name+":"+from.String()+"->"+to.String())
		},
	})
	ctx := context.Background()
	_ = b.Execute(ctx, func(context.Context) error { return errors.New("boom") })
	time.Sleep(5 * time.Millisecond)
	_ = b.Execute(ctx, func(context.Context) error { return nil })

	mu.Lock()
	defer mu.Unlock()
	if len(transitions) < 3 {
		t.Fatalf("transitions = %v, want at least 3", transitions)
	}
	if transitions[0] != "mailer:closed->open" {
		t.Fatalf("first transition = %s", transitions[0])
	}
}

func TestCircuitBreaker_ContextCanceledNotCounted(t *testing.T) {
	b := NewCircuitBreaker("test", CBConfig{FailureThreshold: 2})
	// Default IsFailure excludes context.Canceled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 5; i++ {
		_ = b.Execute(ctx, func(ctx context.Context) error { return ctx.Err() })
	}
	if b.State() != StateClosed {
		t.Fatalf("state = %s, want closed (canceled errors should not count)", b.State())
	}
}

func TestCircuitBreaker_GenericExecute_TypedReturn(t *testing.T) {
	b := NewCircuitBreaker("test", CBConfig{FailureThreshold: 5})
	ctx := context.Background()
	v, err := Execute(b, ctx, func(context.Context) (int, error) { return 42, nil })
	if err != nil {
		t.Fatal(err)
	}
	if v != 42 {
		t.Fatalf("got %d, want 42", v)
	}
}

func TestCircuitBreaker_Concurrent(t *testing.T) {
	b := NewCircuitBreaker("test", CBConfig{FailureThreshold: 100})
	var success, failure atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := b.Execute(context.Background(), func(context.Context) error {
				if i%3 == 0 {
					return errors.New("boom")
				}
				return nil
			})
			if err == nil {
				success.Add(1)
			} else {
				failure.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if success.Load()+failure.Load() != 200 {
		t.Fatalf("lost outcomes: s=%d f=%d", success.Load(), failure.Load())
	}
}

func TestCircuitBreaker_PanicsOnEmptyName(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("no panic")
		}
	}()
	NewCircuitBreaker("", CBConfig{})
}
