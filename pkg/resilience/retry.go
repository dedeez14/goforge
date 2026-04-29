package resilience

import (
	"context"
	"errors"
	"math/rand"
	"time"
)

// RetryConfig configures Retry.
//
// A retry is only helpful when the underlying operation is idempotent
// or is known to be safe to repeat. For HTTP, that's generally GET /
// HEAD / PUT / DELETE, or POST with an Idempotency-Key header.
type RetryConfig struct {
	// MaxAttempts is the total number of attempts (including the
	// first). Must be >= 1. Default: 3.
	MaxAttempts int

	// BaseDelay is the starting delay before the second attempt.
	// Default: 100ms.
	BaseDelay time.Duration

	// MaxDelay caps the exponential backoff. Default: 10s.
	MaxDelay time.Duration

	// Multiplier is the exponential base. Default: 2.0.
	Multiplier float64

	// JitterFraction controls randomisation of the delay in the
	// range (0, 1]. The default (zero value) is 1.0 — full jitter,
	// uniform on [0, delay] — which is the AWS-recommended default
	// and almost always the right choice. To disable jitter
	// entirely, set DisableJitter=true.
	JitterFraction float64

	// DisableJitter turns jitter off and uses the raw exponential
	// delay. Rarely useful in production (retries from many clients
	// synchronise) but helpful in tests that want deterministic
	// timings.
	DisableJitter bool

	// ShouldRetry decides whether a given error is worth another
	// attempt. If nil, DefaultShouldRetry is used: any error except
	// context cancellation and ErrOpen is retried.
	ShouldRetry func(error) bool

	// OnRetry is called before each sleep, with the 1-based attempt
	// that just failed and the delay about to elapse. Useful for
	// structured logging / metrics. nil = no-op.
	OnRetry func(attempt int, delay time.Duration, err error)

	// Rand lets tests seed determinism. nil uses math/rand's
	// goroutine-safe top-level source. Callers that pass their own
	// *rand.Rand are responsible for serialising access to it — we
	// call Float64() without a mutex.
	Rand *rand.Rand
}

// DefaultShouldRetry is the default retry classifier.
//
// It retries everything EXCEPT:
//   - context.Canceled / context.DeadlineExceeded (the caller gave up)
//   - ErrOpen / ErrTooManyProbes (the circuit breaker said no —
//     retrying inside the same process just burns budget)
//
// Callers that need finer granularity (e.g. retry 5xx but not 4xx)
// should pass their own ShouldRetry.
func DefaultShouldRetry(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, ErrOpen) || errors.Is(err, ErrTooManyProbes) {
		return false
	}
	return true
}

// Retry runs fn up to MaxAttempts times with exponential backoff and
// (by default) full jitter between attempts. It returns the result
// and error from the last attempt; on success, the intermediate
// errors are discarded.
//
// Retry respects ctx: if the context is cancelled or the deadline is
// hit during a sleep, Retry aborts immediately with the context
// error (not the last attempt error). fn itself is also expected to
// honour ctx.
func Retry[T any](ctx context.Context, cfg RetryConfig, fn func(context.Context) (T, error)) (T, error) {
	cfg = normaliseRetryConfig(cfg)
	var zero T

	var lastErr error
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return zero, lastErr
			}
			return zero, err
		}
		result, err := fn(ctx)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt == cfg.MaxAttempts || !cfg.ShouldRetry(err) {
			return zero, err
		}
		delay := backoffDelay(cfg, attempt)
		if cfg.OnRetry != nil {
			cfg.OnRetry(attempt, delay, err)
		}
		if err := sleep(ctx, delay); err != nil {
			// ctx cancelled during sleep — surface the underlying
			// downstream error, not the context error, so callers
			// see why they actually failed.
			return zero, lastErr
		}
	}
	return zero, lastErr
}

// RetryVoid is a convenience for operations that don't return a
// value (e.g. Publish, Send). It's a thin wrapper over Retry.
func RetryVoid(ctx context.Context, cfg RetryConfig, fn func(context.Context) error) error {
	_, err := Retry(ctx, cfg, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return err
}

// backoffDelay computes the delay before the (attempt+1)-th attempt.
// attempt is 1-based; after attempt 1 we schedule the second attempt.
func backoffDelay(cfg RetryConfig, attempt int) time.Duration {
	// base * multiplier^(attempt-1)
	d := float64(cfg.BaseDelay)
	for i := 1; i < attempt; i++ {
		d *= cfg.Multiplier
		if d > float64(cfg.MaxDelay) {
			d = float64(cfg.MaxDelay)
			break
		}
	}
	if d > float64(cfg.MaxDelay) {
		d = float64(cfg.MaxDelay)
	}
	if cfg.DisableJitter {
		return time.Duration(d)
	}
	// full jitter = uniform [0, d*jitterFraction] added on top of the
	// residual portion (d * (1 - jitterFraction)). With
	// JitterFraction=1 this is uniform [0, d], which is the
	// AWS-recommended default.
	min := d * (1 - cfg.JitterFraction)
	if min < 0 {
		min = 0
	}
	jitter := d - min
	return time.Duration(min + randFloat64(cfg.Rand)*jitter)
}

// randFloat64 hides the thread-safety distinction between a user-
// supplied *rand.Rand (not safe for concurrent use per docs) and
// rand.Float64() (safe, uses the internal locked source).
func randFloat64(r *rand.Rand) float64 {
	if r == nil {
		return rand.Float64() //nolint:gosec // non-crypto jitter
	}
	return r.Float64()
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func normaliseRetryConfig(cfg RetryConfig) RetryConfig {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 100 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 10 * time.Second
	}
	if cfg.Multiplier <= 1.0 {
		cfg.Multiplier = 2.0
	}
	if cfg.JitterFraction < 0 {
		cfg.JitterFraction = 0
	}
	if cfg.JitterFraction > 1 {
		cfg.JitterFraction = 1
	}
	// Zero value = caller didn't set it = use full jitter (the
	// overwhelmingly common case). Callers who genuinely want no
	// jitter set DisableJitter=true explicitly.
	if cfg.JitterFraction == 0 {
		cfg.JitterFraction = 1.0
	}
	if cfg.ShouldRetry == nil {
		cfg.ShouldRetry = DefaultShouldRetry
	}
	return cfg
}
