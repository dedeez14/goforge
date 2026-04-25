package flags

import (
	"context"
	"testing"
	"time"
)

func TestStaticAndEnvPriority(t *testing.T) {
	t.Setenv("GOFORGE_FLAG_FOO_BAR", "true")
	static := NewStaticSource()
	static.Set("foo.bar", "false")

	svc := New(time.Minute, EnvSource{}, static)
	if got := svc.Bool(context.Background(), "foo.bar", false); !got {
		t.Fatalf("env source should win, expected true got false")
	}
}

func TestFallback(t *testing.T) {
	t.Parallel()
	svc := New(0)
	if got := svc.Bool(context.Background(), "absent", true); !got {
		t.Fatalf("missing flag should fall back to true")
	}
	if got := svc.Int(context.Background(), "absent", 42); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestRefreshClearsCache(t *testing.T) {
	t.Parallel()
	static := NewStaticSource()
	static.Set("k", "v1")
	svc := New(time.Minute, static)
	if v, _ := svc.String(context.Background(), "k"); v != "v1" {
		t.Fatalf("v1, got %q", v)
	}
	static.Set("k", "v2")
	// Cached, still v1.
	if v, _ := svc.String(context.Background(), "k"); v != "v1" {
		t.Fatalf("cache miss: %q", v)
	}
	svc.Refresh()
	if v, _ := svc.String(context.Background(), "k"); v != "v2" {
		t.Fatalf("after refresh: %q", v)
	}
}
