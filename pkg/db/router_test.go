package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A fake pgxpool.Pool is impractical to build (unexported fields), so
// these tests use nil-pointer sentinels. Router never dereferences the
// pools itself - it only returns them - so identity comparison is
// sufficient to assert routing behaviour without a real database.

func TestRouter_WriteAlwaysPrimary(t *testing.T) {
	primary := &pgxpool.Pool{}
	replica := &pgxpool.Pool{}
	r := NewRouter(primary, replica)

	if got := r.Write(); got != primary {
		t.Fatalf("Write() = %p, want primary %p", got, primary)
	}
}

func TestRouter_ReadPrefersReplica(t *testing.T) {
	primary := &pgxpool.Pool{}
	replica := &pgxpool.Pool{}
	r := NewRouter(primary, replica)

	if got := r.Read(context.Background()); got != replica {
		t.Fatalf("Read() = %p, want replica %p", got, replica)
	}
}

func TestRouter_ReadFallsBackToPrimaryWhenNoReplica(t *testing.T) {
	primary := &pgxpool.Pool{}
	r := NewRouter(primary, nil)

	if got := r.Read(context.Background()); got != primary {
		t.Fatalf("Read() = %p, want primary %p (replica nil)", got, primary)
	}
	if r.HasReplica() {
		t.Fatalf("HasReplica() = true, want false")
	}
}

func TestRouter_WithPrimaryForcesPrimaryRead(t *testing.T) {
	primary := &pgxpool.Pool{}
	replica := &pgxpool.Pool{}
	r := NewRouter(primary, replica)

	ctx := WithPrimary(context.Background())
	if got := r.Read(ctx); got != primary {
		t.Fatalf("Read(WithPrimary) = %p, want primary %p", got, primary)
	}
	// The base context should still prefer the replica — the marker
	// is scoped to the derived context.
	if got := r.Read(context.Background()); got != replica {
		t.Fatalf("base context lost replica routing: got %p, want replica %p", got, replica)
	}
}

func TestRouter_ReadToleratesNilContext(t *testing.T) {
	primary := &pgxpool.Pool{}
	replica := &pgxpool.Pool{}
	r := NewRouter(primary, replica)

	// `nil` context is never recommended, but a careless caller
	// shouldn't panic - we should fall back to the replica (the
	// "read" intent) rather than crash.
	// nolint:staticcheck // SA1012 — intentionally testing nil-ctx tolerance.
	if got := r.Read(nil); got != replica {
		t.Fatalf("Read(nil) = %p, want replica %p", got, replica)
	}
}

func TestRouter_HasReplica(t *testing.T) {
	primary := &pgxpool.Pool{}
	replica := &pgxpool.Pool{}
	if !NewRouter(primary, replica).HasReplica() {
		t.Fatalf("HasReplica() = false for configured replica")
	}
	if NewRouter(primary, nil).HasReplica() {
		t.Fatalf("HasReplica() = true for nil replica")
	}
}
