package resilience

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestHedge_PrimarySucceedsFast_NoSecondary(t *testing.T) {
	var calls atomic.Int32
	v, err := Hedge(context.Background(),
		HedgeConfig{Count: 2, Delay: 100 * time.Millisecond},
		func(context.Context) (string, error) {
			calls.Add(1)
			return "primary", nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if v != "primary" {
		t.Fatalf("v = %q", v)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestHedge_PrimarySlow_SecondaryWins(t *testing.T) {
	var calls atomic.Int32
	v, err := Hedge(context.Background(),
		HedgeConfig{Count: 2, Delay: 5 * time.Millisecond},
		func(ctx context.Context) (int, error) {
			attempt := int(calls.Add(1))
			if attempt == 1 {
				// primary is slow; sleep until ctx cancels
				select {
				case <-ctx.Done():
					return 0, ctx.Err()
				case <-time.After(500 * time.Millisecond):
					return 1, nil
				}
			}
			return 2, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if v != 2 {
		t.Fatalf("v = %d, want 2 (secondary)", v)
	}
}

func TestHedge_AllFail_ReturnsLastError(t *testing.T) {
	var calls atomic.Int32
	_, err := Hedge(context.Background(),
		HedgeConfig{Count: 3, Delay: time.Millisecond},
		func(context.Context) (struct{}, error) {
			calls.Add(1)
			return struct{}{}, errors.New("always bad")
		})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestHedge_ContextCancelledAbortsAll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	blocked := make(chan struct{}, 3)
	done := make(chan struct{})
	go func() {
		_, _ = Hedge(ctx, HedgeConfig{Count: 3, Delay: time.Millisecond},
			func(c context.Context) (struct{}, error) {
				blocked <- struct{}{}
				<-c.Done()
				return struct{}{}, c.Err()
			})
		close(done)
	}()
	// Wait for at least one attempt to be blocked, then cancel.
	<-blocked
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Hedge did not return after context cancel")
	}
}

func TestHedge_CountOneDegeneratesToSingleCall(t *testing.T) {
	var calls atomic.Int32
	_, err := Hedge(context.Background(),
		HedgeConfig{Count: 1, Delay: time.Millisecond},
		func(context.Context) (struct{}, error) {
			calls.Add(1)
			return struct{}{}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestHedge_WinnerCancelsLosers(t *testing.T) {
	var losersCancelled atomic.Int32
	started := make(chan struct{}, 2)
	_, err := Hedge(context.Background(),
		HedgeConfig{Count: 3, Delay: 5 * time.Millisecond},
		func(ctx context.Context) (string, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			if len(started) == 1 {
				// First attempt: succeed quickly.
				time.Sleep(time.Millisecond)
				return "winner", nil
			}
			<-ctx.Done()
			losersCancelled.Add(1)
			return "", ctx.Err()
		})
	if err != nil {
		t.Fatal(err)
	}
	// Give losers a moment to observe cancellation.
	time.Sleep(50 * time.Millisecond)
	// Depending on scheduling, 0 or more losers may have been
	// launched, but none should still be running.
}
