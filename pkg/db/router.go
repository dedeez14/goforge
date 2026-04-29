// Package db provides a thin read/write router on top of pgxpool.Pool
// so applications can opt into sending read-only traffic to a
// PostgreSQL read replica while every write still lands on the
// primary.
//
// The router is intentionally *explicit*: callers pick Read or Write
// per query, rather than relying on automatic SQL parsing or driver
// heuristics (both of which break silently around transactions,
// common table expressions and CTE-with-RETURNING). Being explicit
// also documents the caller's consistency expectations at the call
// site.
//
// # Usage
//
//	r := db.NewRouter(primary, replica) // replica may be nil
//	// read-only query — replica when available, primary otherwise:
//	rows, err := r.Read(ctx).Query(ctx, "SELECT ...")
//	// writes always go to the primary:
//	_, err := r.Write().Exec(ctx, "INSERT ...")
//
// # Read-your-writes
//
// Replication is asynchronous, so a client that just issued an INSERT
// against the primary may not see it yet on the replica. Callers that
// need read-your-writes can mark a context with [WithPrimary]; Read
// will then return the primary pool even when a replica is configured:
//
//	ctx = db.WithPrimary(ctx)
//	rows, _ := r.Read(ctx).Query(ctx, "SELECT ... WHERE id = $1", justInserted)
//
// [Router] never attempts to detect staleness itself — it is the
// caller's domain knowledge that decides when a read is "fresh
// enough".
package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Router fans reads between a primary pool and an optional replica
// pool. Write operations always go to the primary. The zero value is
// not useful; use [NewRouter].
type Router struct {
	primary *pgxpool.Pool
	replica *pgxpool.Pool
}

// NewRouter constructs a Router. The primary pool is required; the
// replica pool may be nil, in which case Read is equivalent to Write
// and no routing decisions are made.
//
// The caller retains ownership of both pools. [Router.Close] closes
// whichever pools were passed in; if the caller has already taken
// responsibility for closing them (e.g. they're reused elsewhere),
// reach for [Router.Replica] / [Router.Primary] and call Close on
// the underlying *pgxpool.Pool directly instead.
func NewRouter(primary, replica *pgxpool.Pool) *Router {
	return &Router{primary: primary, replica: replica}
}

// Write returns the primary pool. All INSERT / UPDATE / DELETE / DDL
// and any read that must observe its own writes goes here.
func (r *Router) Write() *pgxpool.Pool { return r.primary }

// Read returns the replica pool when one is configured and the
// context does not carry a [WithPrimary] marker; otherwise it falls
// back to the primary pool. This keeps call sites safe to add before
// a replica is provisioned — they simply hit the primary until a
// replica DSN is configured.
func (r *Router) Read(ctx context.Context) *pgxpool.Pool {
	if r.replica == nil {
		return r.primary
	}
	if forcedPrimary(ctx) {
		return r.primary
	}
	return r.replica
}

// HasReplica reports whether a replica pool is configured. Code that
// wants to log or expose the routing decision can use this, but
// routing itself should always go through [Router.Read].
func (r *Router) HasReplica() bool { return r.replica != nil }

// Primary is an escape hatch returning the primary pool directly. Use
// it when you genuinely need the *pgxpool.Pool (e.g. migrations,
// LISTEN/NOTIFY, pgx transactions started outside the Router).
func (r *Router) Primary() *pgxpool.Pool { return r.primary }

// Replica is an escape hatch returning the replica pool directly or
// nil when no replica is configured. Callers almost always want
// [Router.Read] instead.
func (r *Router) Replica() *pgxpool.Pool { return r.replica }

// Ping verifies connectivity to both pools. It returns a non-nil
// error as soon as any pool fails to respond. Pings run serially so
// the caller can distinguish primary-down from replica-down from the
// error chain; for a parallel ping the caller can call Ping on the
// pools directly.
func (r *Router) Ping(ctx context.Context) error {
	if err := r.primary.Ping(ctx); err != nil {
		return err
	}
	if r.replica != nil {
		if err := r.replica.Ping(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Close closes both pools. Subsequent calls are no-ops because
// pgxpool.Pool.Close is itself idempotent.
func (r *Router) Close() {
	if r == nil {
		return
	}
	if r.primary != nil {
		r.primary.Close()
	}
	if r.replica != nil {
		r.replica.Close()
	}
}

// primaryCtxKey is the unexported type used for the WithPrimary
// marker so no other package can accidentally collide with it.
type primaryCtxKey struct{}

// WithPrimary returns a context derived from parent that forces
// [Router.Read] to return the primary pool. Use it on the read path
// immediately after a write when the caller needs read-your-writes
// consistency.
func WithPrimary(parent context.Context) context.Context {
	return context.WithValue(parent, primaryCtxKey{}, true)
}

func forcedPrimary(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(primaryCtxKey{}).(bool)
	return v
}
