package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestMemory_PutGetRoundTrip(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	ctx := context.Background()
	if err := m.Put(ctx, "k/file.txt", bytes.NewReader([]byte("hello")), 5, "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	r, err := m.Get(ctx, "k/file.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = r.Close() }()
	got, _ := io.ReadAll(r)
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestMemory_GetMissingReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	if _, err := m.Get(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemory_PresignReturnsOpaqueURL(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	u, err := m.PresignPut(context.Background(), "k", 5*time.Minute, "image/png")
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	if !strings.HasPrefix(u, "memory://") {
		t.Fatalf("unexpected url: %s", u)
	}
}

func TestMemory_ListByPrefix(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	ctx := context.Background()
	_ = m.Put(ctx, "a/1", bytes.NewReader([]byte{}), 0, "")
	_ = m.Put(ctx, "a/2", bytes.NewReader([]byte{}), 0, "")
	_ = m.Put(ctx, "b/1", bytes.NewReader([]byte{}), 0, "")
	got, err := m.List(ctx, "a/", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestMemory_DeleteIdempotent(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	ctx := context.Background()
	if err := m.Delete(ctx, "ghost"); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
	_ = m.Put(ctx, "k", bytes.NewReader([]byte("v")), 1, "")
	if err := m.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.Get(ctx, "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("post-Delete Get: %v", err)
	}
}
