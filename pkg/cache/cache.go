// Package cache is the goforge cache abstraction.
//
// Application code talks to the Cache interface; the framework wires
// either a process-local in-memory implementation (zero infra) or a
// Redis-backed one (shared across replicas, durable beyond restarts).
// Switching between them is a one-line config change — no call site
// changes.
//
// Use cases:
//
//   - Cache fan-out reads (a "user-by-id" lookup that 50 handlers do)
//   - Coordinate one-shot work via SetNX (locks, leader election)
//   - Hold short-lived state that doesn't deserve a database table
//     (login attempt counters, OTP codes, idempotency hints)
//
// The interface is intentionally minimal: anything fancier (pub/sub,
// streams) belongs in a dedicated package, not here.
package cache

import (
	"context"
	"errors"
	"time"
)

// ErrMiss is returned by Get when the key is absent. Callers compare
// with errors.Is so the underlying implementation can wrap it without
// breaking switch-case logic.
var ErrMiss = errors.New("cache: miss")

// Cache is the contract every backend implements. Implementations must
// be safe for concurrent use from multiple goroutines.
type Cache interface {
	// Get returns the bytes stored at key, or ErrMiss when absent.
	Get(ctx context.Context, key string) ([]byte, error)

	// Set stores the value at key with the supplied TTL. ttl=0 means
	// "no expiry"; backends that don't support that (some Redis
	// configs) should fall back to a long expiry (e.g. 30 days) and
	// document the substitution.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// SetNX is "Set if Not eXists". The boolean reports whether the
	// value was actually stored (false means a value was already
	// present and was kept). Used for leader election and locks.
	SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)

	// Del removes the key. Missing keys are not an error.
	Del(ctx context.Context, keys ...string) error

	// Incr atomically increments an integer counter and returns the
	// new value. Used for rate limiters, attempt counters, etc.
	// When key was absent, the counter starts at 1 and the supplied
	// ttl is applied.
	Incr(ctx context.Context, key string, ttl time.Duration) (int64, error)

	// Ping is a health check.
	Ping(ctx context.Context) error

	// Close releases any background resources (Redis pool, etc.).
	Close() error
}
