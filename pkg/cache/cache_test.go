package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemory_GetSetMiss(t *testing.T) {
	t.Parallel()
	c := NewMemory()
	ctx := context.Background()
	if _, err := c.Get(ctx, "x"); !errors.Is(err, ErrMiss) {
		t.Fatalf("expected miss, got %v", err)
	}
	if err := c.Set(ctx, "x", []byte("hello"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := c.Get(ctx, "x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q", string(got))
	}
}

func TestMemory_TTLExpires(t *testing.T) {
	t.Parallel()
	c := NewMemory()
	frozen := time.Unix(1000, 0)
	c.now = func() time.Time { return frozen }
	ctx := context.Background()
	_ = c.Set(ctx, "x", []byte("v"), 100*time.Millisecond)
	c.now = func() time.Time { return frozen.Add(time.Second) }
	if _, err := c.Get(ctx, "x"); !errors.Is(err, ErrMiss) {
		t.Fatalf("expired key should miss; got %v", err)
	}
}

func TestMemory_SetNXKeepsExisting(t *testing.T) {
	t.Parallel()
	c := NewMemory()
	ctx := context.Background()
	ok, _ := c.SetNX(ctx, "k", []byte("first"), time.Minute)
	if !ok {
		t.Fatal("first SetNX should succeed")
	}
	ok, _ = c.SetNX(ctx, "k", []byte("second"), time.Minute)
	if ok {
		t.Fatal("second SetNX should report not stored")
	}
	got, _ := c.Get(ctx, "k")
	if string(got) != "first" {
		t.Fatalf("value clobbered: %q", got)
	}
}

func TestMemory_IncrIncrements(t *testing.T) {
	t.Parallel()
	c := NewMemory()
	ctx := context.Background()
	for i := int64(1); i <= 5; i++ {
		got, err := c.Incr(ctx, "counter", time.Minute)
		if err != nil {
			t.Fatalf("Incr: %v", err)
		}
		if got != i {
			t.Fatalf("Incr step %d returned %d", i, got)
		}
	}
}

func TestMemory_DelRemoves(t *testing.T) {
	t.Parallel()
	c := NewMemory()
	ctx := context.Background()
	_ = c.Set(ctx, "k", []byte("v"), 0)
	_ = c.Del(ctx, "k")
	if _, err := c.Get(ctx, "k"); !errors.Is(err, ErrMiss) {
		t.Fatalf("expected miss after Del; got %v", err)
	}
}
