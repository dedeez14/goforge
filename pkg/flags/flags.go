// Package flags is a tiny feature-flag service designed for goforge.
//
// It supports two layers stacked in priority order:
//
//  1. EnvSource - reads boolean / string flags from environment variables
//     prefixed `GOFORGE_FLAG_<UPPER_NAME>`. Highest priority; ideal for
//     emergency kill switches and CI overrides.
//  2. StaticSource - in-process map populated from config or code.
//
// Applications can register additional Sources (Postgres, Unleash,
// LaunchDarkly etc.) by implementing the Source interface. The Service
// caches lookups for a configurable TTL so even high-traffic call sites
// pay no per-evaluation IO.
//
// goforge ships flag tooling because most starters either omit it
// entirely (forcing a config rebuild for every toggle) or pull in a
// heavyweight SaaS dependency. This package gives you the 80%
// solution out of the box.
package flags

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Source is the contract every flag backend implements. Implementations
// must be safe for concurrent use.
type Source interface {
	// Lookup returns the raw string value of name. The boolean is
	// false when the source has no opinion on name; callers fall
	// back to the next source in the chain.
	Lookup(ctx context.Context, name string) (string, bool)
}

// Service evaluates a flag name against an ordered chain of sources,
// caches results for a small TTL and offers typed accessors.
type Service struct {
	sources []Source
	ttl     time.Duration

	mu    sync.RWMutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	value     string
	hit       bool
	cachedAt  time.Time
}

// New constructs a Service. The ttl is applied to every cache entry;
// pass 0 to disable caching (lookups hit every source on every call).
func New(ttl time.Duration, sources ...Source) *Service {
	return &Service{
		sources: sources,
		ttl:     ttl,
		cache:   make(map[string]cacheEntry),
	}
}

// Refresh wipes the cache so the next lookup reflects the current
// state of every source. The flags module wires this onto SIGHUP and
// onto an admin HTTP endpoint.
func (s *Service) Refresh() {
	s.mu.Lock()
	s.cache = make(map[string]cacheEntry)
	s.mu.Unlock()
}

// String returns the string value for name and whether any source
// supplied it. When no source has an opinion, the second return is
// false and callers should fall back to the static default.
func (s *Service) String(ctx context.Context, name string) (string, bool) {
	if e, ok := s.cached(name); ok {
		return e.value, e.hit
	}
	for _, src := range s.sources {
		if v, ok := src.Lookup(ctx, name); ok {
			s.store(name, v, true)
			return v, true
		}
	}
	s.store(name, "", false)
	return "", false
}

// Bool returns the boolean value of name. Truthy strings are 1, t,
// true, yes, on (case-insensitive). Anything else falls through to
// fallback.
func (s *Service) Bool(ctx context.Context, name string, fallback bool) bool {
	v, ok := s.String(ctx, name)
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	case "0", "f", "false", "n", "no", "off":
		return false
	default:
		return fallback
	}
}

// Int returns the integer value of name or fallback when the flag is
// not set or cannot be parsed as an int.
func (s *Service) Int(ctx context.Context, name string, fallback int) int {
	v, ok := s.String(ctx, name)
	if !ok {
		return fallback
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return parsed
}

func (s *Service) cached(name string) (cacheEntry, bool) {
	if s.ttl <= 0 {
		return cacheEntry{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.cache[name]
	if !ok {
		return cacheEntry{}, false
	}
	if time.Since(e.cachedAt) > s.ttl {
		return cacheEntry{}, false
	}
	return e, true
}

func (s *Service) store(name, value string, hit bool) {
	if s.ttl <= 0 {
		return
	}
	s.mu.Lock()
	s.cache[name] = cacheEntry{value: value, hit: hit, cachedAt: time.Now()}
	s.mu.Unlock()
}

// EnvSource reads flags from environment variables. The flag `foo.bar`
// becomes `GOFORGE_FLAG_FOO_BAR`.
type EnvSource struct{ Prefix string }

// Lookup implements Source.
func (e EnvSource) Lookup(_ context.Context, name string) (string, bool) {
	prefix := e.Prefix
	if prefix == "" {
		prefix = "GOFORGE_FLAG_"
	}
	key := prefix + strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(name))
	v, ok := os.LookupEnv(key)
	return v, ok
}

// StaticSource is an in-memory map source. Add it last in the chain so
// it acts as the default layer.
type StaticSource struct {
	mu sync.RWMutex
	m  map[string]string
}

// NewStaticSource returns an empty StaticSource ready for Set.
func NewStaticSource() *StaticSource { return &StaticSource{m: make(map[string]string)} }

// Set assigns value to name.
func (s *StaticSource) Set(name, value string) {
	s.mu.Lock()
	s.m[name] = value
	s.mu.Unlock()
}

// Lookup implements Source.
func (s *StaticSource) Lookup(_ context.Context, name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[name]
	return v, ok
}
