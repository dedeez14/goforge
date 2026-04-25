package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is an in-process Storage implementation for tests and
// small embedded scenarios. It is NOT durable. The presigned URLs
// it returns are opaque ("memory://bucket/key?exp=..."); your test
// harness can intercept them.
type Memory struct {
	mu      sync.RWMutex
	objects map[string]memObject
}

type memObject struct {
	body  []byte
	ctype string
}

// NewMemory returns a Memory storage.
func NewMemory() *Memory {
	return &Memory{objects: make(map[string]memObject)}
}

// Put implements Storage.
func (m *Memory) Put(_ context.Context, key string, body io.Reader, _ int64, ctype string) error {
	buf, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = memObject{body: buf, ctype: ctype}
	return nil
}

// Get implements Storage.
func (m *Memory) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(o.body)), nil
}

// Delete implements Storage.
func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

// PresignPut implements Storage. The URL is opaque memory://; tests
// can match on it.
func (m *Memory) PresignPut(_ context.Context, key string, ttl time.Duration, ctype string) (string, error) {
	return memURL("PUT", key, ttl, ctype), nil
}

// PresignGet implements Storage.
func (m *Memory) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	return memURL("GET", key, ttl, ""), nil
}

// List implements Storage.
func (m *Memory) List(_ context.Context, prefix string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 1000
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.objects))
	for k := range m.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	return keys, nil
}

func memURL(method, key string, ttl time.Duration, ctype string) string {
	u := url.URL{Scheme: "memory", Host: "bucket", Path: "/" + key}
	q := u.Query()
	q.Set("method", method)
	q.Set("exp", time.Now().Add(ttl).Format(time.RFC3339))
	if ctype != "" {
		q.Set("ctype", ctype)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Errors aren't surfaced through Memory but interface compatibility
// requires the import — guard against unused-import lints.
var _ = errors.New
