package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/dedeez14/goforge/pkg/cache"
)

func TestLimiter_FirstRequestAllowed(t *testing.T) {
	t.Parallel()
	l := New(cache.NewMemory(), "t:", time.Minute, 5)
	d, err := l.Allow(context.Background(), "alice")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !d.Allowed {
		t.Fatal("first request must be allowed")
	}
	if d.Remaining != 4 {
		t.Fatalf("remaining = %d, want 4", d.Remaining)
	}
}

func TestLimiter_BlocksOverLimit(t *testing.T) {
	t.Parallel()
	l := New(cache.NewMemory(), "t:", time.Minute, 3)
	ctx := context.Background()
	_, _ = l.Allow(ctx, "bob")
	_, _ = l.Allow(ctx, "bob")
	_, _ = l.Allow(ctx, "bob")
	d, err := l.Allow(ctx, "bob")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if d.Allowed {
		t.Fatal("4th request must be blocked")
	}
	if d.Remaining != 0 {
		t.Fatalf("remaining = %d, want 0", d.Remaining)
	}
}

func TestLimiter_KeysAreIndependent(t *testing.T) {
	t.Parallel()
	l := New(cache.NewMemory(), "t:", time.Minute, 1)
	ctx := context.Background()
	if d, _ := l.Allow(ctx, "k1"); !d.Allowed {
		t.Fatal("k1 first call must allow")
	}
	if d, _ := l.Allow(ctx, "k2"); !d.Allowed {
		t.Fatal("k2 first call must allow despite k1's bucket being full")
	}
}
