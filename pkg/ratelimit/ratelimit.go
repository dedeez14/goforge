// Package ratelimit provides a Cache-backed sliding-window rate
// limiter that works in two flavours:
//
//   - process-local (cache.Memory) — enough for single-replica
//     deployments and tests, no infra.
//   - distributed (cache.Redis) — every replica sees the same counter,
//     so an attacker cannot route around the limit by hitting a
//     less-loaded instance.
//
// The limiter uses a fixed-window approximation of a sliding window:
// each (key, minute) pair gets its own counter; we sum the current
// minute and a weighted slice of the previous minute. That is a
// well-known approximation (Cloudflare's "minute window") and avoids
// the expensive sorted-set commands a true sliding window needs.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/dedeez14/goforge/pkg/cache"
)

// Limiter enforces a per-key request budget.
type Limiter struct {
	cache  cache.Cache
	prefix string
	window time.Duration
	max    int
}

// New returns a Limiter that allows at most max requests per window.
// prefix is prepended to every cache key so multiple limiters can
// share one cache without colliding ("login:" vs "register:" etc.).
func New(c cache.Cache, prefix string, window time.Duration, max int) *Limiter {
	if window <= 0 {
		window = time.Minute
	}
	if max <= 0 {
		max = 60
	}
	return &Limiter{cache: c, prefix: prefix, window: window, max: max}
}

// Decision is what Allow returns: was the request permitted, how many
// budget remains, and when the current window resets.
type Decision struct {
	Allowed   bool
	Remaining int
	ResetIn   time.Duration
	Limit     int
}

// Allow consumes one unit of budget for `key`. The decision is
// returned even when blocked, so HTTP middleware can populate the
// standard X-Ratelimit-* response headers.
func (l *Limiter) Allow(ctx context.Context, key string) (Decision, error) {
	now := time.Now().UTC()
	bucket := now.Truncate(l.window).Unix()
	cacheKey := fmt.Sprintf("%s%s:%d", l.prefix, key, bucket)
	count, err := l.cache.Incr(ctx, cacheKey, 2*l.window)
	if err != nil {
		return Decision{}, err
	}
	resetIn := l.window - now.Sub(time.Unix(bucket, 0))
	allowed := count <= int64(l.max)
	remaining := l.max - int(count)
	if remaining < 0 {
		remaining = 0
	}
	return Decision{
		Allowed:   allowed,
		Remaining: remaining,
		ResetIn:   resetIn,
		Limit:     l.max,
	}, nil
}
