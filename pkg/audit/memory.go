package audit

import (
	"context"
	"sync"
)

// Memory is an in-process Logger used by tests. It records every
// Entry on a slice; callers can Snapshot() it to assert.
type Memory struct {
	mu      sync.Mutex
	entries []Entry
}

// NewMemory returns a Memory logger.
func NewMemory() *Memory { return &Memory{} }

// Log implements Logger.
func (m *Memory) Log(_ context.Context, e Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
	return nil
}

// Snapshot returns a copy of the recorded entries.
func (m *Memory) Snapshot() []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Entry, len(m.entries))
	copy(out, m.entries)
	return out
}
