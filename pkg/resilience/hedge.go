package resilience

import (
	"context"
	"errors"
	"sync"
	"time"
)

// HedgeConfig configures Hedge.
//
// Hedging is the latency-reduction pattern from Google's "The Tail
// at Scale" paper: issue a primary request, and if it hasn't
// responded within HedgeDelay, fire a secondary. Return the first
// to succeed; cancel the other. Useful for read-only or naturally
// idempotent calls where duplicate work is cheap.
//
// Do NOT hedge non-idempotent writes: you'll duplicate the side
// effect roughly Count times.
type HedgeConfig struct {
	// Count is the maximum number of concurrent attempts, including
	// the primary. Must be >= 1; values <= 1 disable hedging (Hedge
	// degenerates to a single call). Default: 2.
	Count int

	// Delay is how long to wait before firing each additional
	// attempt. Default: 50ms.
	//
	// A common tuning starting point is the downstream's p95 latency:
	// if the primary is past p95 without responding, it's probably
	// going to be a tail-latency outlier, and a fresh attempt is
	// likely to return first.
	Delay time.Duration
}

// Hedge runs up to cfg.Count concurrent attempts staggered by
// cfg.Delay and returns whichever attempt succeeds first (or the
// last error if they all fail).
//
// Each attempt gets its own derived context; when one attempt
// succeeds, the contexts of the others are cancelled so callers can
// bail early (e.g. close an HTTP response body).
//
// Hedge respects the parent ctx: cancellation aborts all in-flight
// attempts.
func Hedge[T any](parent context.Context, cfg HedgeConfig, fn func(context.Context) (T, error)) (T, error) {
	if cfg.Count <= 0 {
		cfg.Count = 2
	}
	if cfg.Delay <= 0 {
		cfg.Delay = 50 * time.Millisecond
	}
	var zero T
	if cfg.Count == 1 {
		return fn(parent)
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	type outcome struct {
		val T
		err error
	}
	out := make(chan outcome, cfg.Count)

	var wg sync.WaitGroup
	launch := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := fn(ctx)
			// Deliver even if we're about to be cancelled — the
			// reader side drains the channel.
			select {
			case out <- outcome{v, err}:
			case <-ctx.Done():
			}
		}()
	}

	// Primary
	launch()

	// Staggered secondaries. Each waits cfg.Delay from the previous
	// unless the winning result has already arrived or ctx cancels.
	timer := time.NewTimer(cfg.Delay)
	defer timer.Stop()

	var lastErr error
	received := 0
	launched := 1
	for {
		if received >= launched && launched >= cfg.Count {
			// All in-flight have completed without success.
			if lastErr == nil {
				lastErr = errors.New("resilience: hedge exhausted without error — bug")
			}
			wg.Wait()
			return zero, lastErr
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			if lastErr != nil {
				return zero, lastErr
			}
			return zero, ctx.Err()
		case <-timer.C:
			if launched < cfg.Count {
				launch()
				launched++
				if launched < cfg.Count {
					timer.Reset(cfg.Delay)
				}
			}
		case o := <-out:
			received++
			if o.err == nil {
				// Winner — cancel peers and return.
				cancel()
				// Don't wait: let losers clean up asynchronously;
				// they observe ctx cancellation.
				return o.val, nil
			}
			lastErr = o.err
		}
	}
}
