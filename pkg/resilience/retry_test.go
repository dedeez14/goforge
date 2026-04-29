package resilience

import (
	"context"
	"errors"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetry_SucceedsFirstTry(t *testing.T) {
	var calls atomic.Int32
	v, err := Retry(context.Background(), RetryConfig{MaxAttempts: 3},
		func(context.Context) (int, error) {
			calls.Add(1)
			return 7, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if v != 7 || calls.Load() != 1 {
		t.Fatalf("v=%d calls=%d", v, calls.Load())
	}
}

func TestRetry_SucceedsAfterTransient(t *testing.T) {
	var calls atomic.Int32
	v, err := Retry(context.Background(),
		RetryConfig{MaxAttempts: 5, BaseDelay: time.Millisecond, DisableJitter: true},
		func(context.Context) (string, error) {
			if calls.Add(1) < 3 {
				return "", errors.New("transient")
			}
			return "ok", nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if v != "ok" || calls.Load() != 3 {
		t.Fatalf("v=%q calls=%d", v, calls.Load())
	}
}

func TestRetry_ExhaustsAndReturnsLastError(t *testing.T) {
	var calls atomic.Int32
	want := errors.New("always bad")
	_, err := Retry(context.Background(),
		RetryConfig{MaxAttempts: 3, BaseDelay: time.Microsecond, DisableJitter: true},
		func(context.Context) (struct{}, error) {
			calls.Add(1)
			return struct{}{}, want
		})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestRetry_ShouldRetryFalseStopsImmediately(t *testing.T) {
	var calls atomic.Int32
	permanent := errors.New("permanent")
	_, err := Retry(context.Background(),
		RetryConfig{
			MaxAttempts: 5,
			BaseDelay:   time.Microsecond,
			ShouldRetry: func(err error) bool { return !errors.Is(err, permanent) },
		},
		func(context.Context) (struct{}, error) {
			calls.Add(1)
			return struct{}{}, permanent
		})
	if !errors.Is(err, permanent) {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (permanent error should stop retry)", calls.Load())
	}
}

func TestRetry_ContextCancelledDuringSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	boom := errors.New("boom")
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err := Retry(ctx,
		RetryConfig{MaxAttempts: 10, BaseDelay: 100 * time.Millisecond, DisableJitter: true},
		func(context.Context) (struct{}, error) {
			calls.Add(1)
			return struct{}{}, boom
		})
	// Should surface the downstream error, not ctx.Canceled.
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if calls.Load() > 3 {
		t.Fatalf("too many calls: %d", calls.Load())
	}
}

func TestRetry_DefaultShouldRetry_BailsOnErrOpen(t *testing.T) {
	var calls atomic.Int32
	_, err := Retry(context.Background(),
		RetryConfig{MaxAttempts: 5, BaseDelay: time.Microsecond, DisableJitter: true},
		func(context.Context) (struct{}, error) {
			calls.Add(1)
			return struct{}{}, ErrOpen
		})
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (ErrOpen should short-circuit)", calls.Load())
	}
}

func TestRetry_OnRetryFires(t *testing.T) {
	var events []int
	_, err := Retry(context.Background(),
		RetryConfig{
			MaxAttempts:   3,
			BaseDelay:     time.Microsecond,
			DisableJitter: true,
			OnRetry: func(attempt int, d time.Duration, err error) {
				events = append(events, attempt)
			},
		},
		func(context.Context) (struct{}, error) {
			return struct{}{}, errors.New("boom")
		})
	if err == nil {
		t.Fatal("expected error")
	}
	// OnRetry fires before each sleep → called 2 times for MaxAttempts=3.
	if len(events) != 2 {
		t.Fatalf("OnRetry called %d times, want 2", len(events))
	}
}

func TestRetry_VoidWrapper(t *testing.T) {
	var calls atomic.Int32
	err := RetryVoid(context.Background(),
		RetryConfig{MaxAttempts: 2, BaseDelay: time.Microsecond, DisableJitter: true},
		func(context.Context) error {
			if calls.Add(1) == 1 {
				return errors.New("transient")
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestBackoffDelay_Monotonic(t *testing.T) {
	cfg := normaliseRetryConfig(RetryConfig{
		BaseDelay:     10 * time.Millisecond,
		Multiplier:    2.0,
		MaxDelay:      time.Second,
		DisableJitter: true,
	})
	d1 := backoffDelay(cfg, 1)
	d2 := backoffDelay(cfg, 2)
	d3 := backoffDelay(cfg, 3)
	if !(d1 < d2 && d2 < d3) {
		t.Fatalf("delays not monotonic: %v %v %v", d1, d2, d3)
	}
}

func TestBackoffDelay_CapsAtMax(t *testing.T) {
	cfg := normaliseRetryConfig(RetryConfig{
		BaseDelay:     time.Second,
		Multiplier:    10.0,
		MaxDelay:      2 * time.Second,
		DisableJitter: true,
	})
	if d := backoffDelay(cfg, 10); d > 2*time.Second {
		t.Fatalf("delay %v exceeded max", d)
	}
}

func TestBackoffDelay_JitterInRange(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	cfg := normaliseRetryConfig(RetryConfig{
		BaseDelay:      100 * time.Millisecond,
		Multiplier:     2.0,
		MaxDelay:       time.Second,
		JitterFraction: 1.0,
		Rand:           r,
	})
	for i := 0; i < 100; i++ {
		d := backoffDelay(cfg, 2)
		// With full jitter and base=100ms at attempt 2, raw delay is 200ms.
		// Jittered delay is uniform in [0, 200ms].
		if d < 0 || d > 200*time.Millisecond {
			t.Fatalf("jittered delay %v out of range", d)
		}
	}
}
