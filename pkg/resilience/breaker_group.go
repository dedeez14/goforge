package resilience

import "sync"

// BreakerGroup memoises one CircuitBreaker per identifier so that a
// long-lived service (e.g. webhooks.Dispatcher, mailer) can lazily
// materialise state per downstream without maintaining a second map
// itself.
//
// The breaker returned for a given key is reused across Get calls,
// so breaker state (consecutive failures, open/half-open) persists
// across invocations, which is the whole point.
type BreakerGroup struct {
	factory func(key string) CBConfig
	mu      sync.Mutex
	m       map[string]*CircuitBreaker
}

// NewBreakerGroup returns an empty group. factory is called once per
// key the first time a breaker is requested, and should return the
// CBConfig to use for that key. Common patterns:
//
//   - All breakers share the same config → factory ignores the key
//     and returns a constant.
//   - Per-tenant / per-endpoint tuning → factory inspects the key
//     and returns specific thresholds.
//
// factory must be safe to call concurrently from multiple goroutines.
func NewBreakerGroup(factory func(key string) CBConfig) *BreakerGroup {
	if factory == nil {
		panic("resilience: NewBreakerGroup requires a factory")
	}
	return &BreakerGroup{
		factory: factory,
		m:       make(map[string]*CircuitBreaker),
	}
}

// Get returns the breaker for key, creating it on first use.
func (g *BreakerGroup) Get(key string) *CircuitBreaker {
	g.mu.Lock()
	defer g.mu.Unlock()
	if b, ok := g.m[key]; ok {
		return b
	}
	b := NewCircuitBreaker(key, g.factory(key))
	g.m[key] = b
	return b
}

// Len returns the number of breakers currently tracked. Mostly
// useful for tests and metrics.
func (g *BreakerGroup) Len() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.m)
}
