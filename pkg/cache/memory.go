package cache

import (
	"context"
	"sync"
	"time"
)

// Memory is an in-process Cache implementation. It is safe for
// concurrent use, has no external dependencies, and is the natural
// choice for single-replica deployments and tests.
//
// Memory is NOT a substitute for Redis when scaling horizontally:
// each replica has an independent copy. Use NewMemory for "one box"
// deployments and switch to NewRedis when you scale.
type Memory struct {
	mu      sync.Mutex
	entries map[string]memEntry
	now     func() time.Time
}

type memEntry struct {
	value     []byte
	expiresAt time.Time // zero = never
}

// NewMemory returns a Memory cache. A goroutine periodically purges
// expired entries; when the context provided to Run is cancelled the
// goroutine exits.
func NewMemory() *Memory {
	return &Memory{
		entries: make(map[string]memEntry),
		now:     time.Now,
	}
}

// Run starts the background sweeper. Cancel ctx to stop it. Calling
// Run is optional; if you don't, expired entries are still rejected
// on read but their memory isn't reclaimed until overwritten.
func (m *Memory) Run(ctx context.Context, sweep time.Duration) {
	if sweep <= 0 {
		sweep = time.Minute
	}
	t := time.NewTicker(sweep)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.sweep()
		}
	}
}

func (m *Memory) sweep() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for k, e := range m.entries {
		if !e.expiresAt.IsZero() && e.expiresAt.Before(now) {
			delete(m.entries, k)
		}
	}
}

// Get implements Cache.
func (m *Memory) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return nil, ErrMiss
	}
	if !e.expiresAt.IsZero() && e.expiresAt.Before(m.now()) {
		delete(m.entries, key)
		return nil, ErrMiss
	}
	out := make([]byte, len(e.value))
	copy(out, e.value)
	return out, nil
}

// Set implements Cache.
func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored := make([]byte, len(value))
	copy(stored, value)
	e := memEntry{value: stored}
	if ttl > 0 {
		e.expiresAt = m.now().Add(ttl)
	}
	m.entries[key] = e
	return nil
}

// SetNX implements Cache.
func (m *Memory) SetNX(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[key]; ok {
		if e.expiresAt.IsZero() || e.expiresAt.After(m.now()) {
			return false, nil
		}
	}
	stored := make([]byte, len(value))
	copy(stored, value)
	e := memEntry{value: stored}
	if ttl > 0 {
		e.expiresAt = m.now().Add(ttl)
	}
	m.entries[key] = e
	return true, nil
}

// Del implements Cache.
func (m *Memory) Del(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.entries, k)
	}
	return nil
}

// Incr implements Cache. The counter is stored as a base-10 string so
// it round-trips through Get/Set without a separate type.
func (m *Memory) Incr(_ context.Context, key string, ttl time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	e, ok := m.entries[key]
	if ok && !e.expiresAt.IsZero() && e.expiresAt.Before(now) {
		ok = false
	}
	var n int64
	if ok {
		// Parse without allocating a strconv.Atoi error path.
		for _, b := range e.value {
			if b < '0' || b > '9' {
				n = 0
				break
			}
			n = n*10 + int64(b-'0')
		}
	}
	n++
	out := []byte{}
	if n == 0 {
		out = []byte{'0'}
	}
	for v := n; v > 0; v /= 10 {
		out = append([]byte{byte('0' + v%10)}, out...)
	}
	e = memEntry{value: out}
	if !ok && ttl > 0 {
		e.expiresAt = now.Add(ttl)
	} else if ok {
		// preserve existing expiry
		e.expiresAt = m.entries[key].expiresAt
	}
	m.entries[key] = e
	return n, nil
}

// Ping implements Cache.
func (m *Memory) Ping(_ context.Context) error { return nil }

// Close implements Cache.
func (m *Memory) Close() error { return nil }
