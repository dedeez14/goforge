// Package dbx contains opinionated database conventions every goforge
// resource should follow: a uniform set of audit columns, a soft-delete
// flag, and helpers that keep them populated automatically.
//
// The package is intentionally small: it is a *convention library*, not
// a query builder. Repositories continue to use pgx directly. The point
// is that every aggregate root in your system carries the same six
// columns and uses the same naming, so cross-cutting concerns
// (tombstone cleanup, audit log dashboards, "who deleted my row at
// 3am" investigations) can rely on one schema.
package dbx

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Audit is the set of columns every persistent entity should embed.
// CreatedBy / UpdatedBy / DeletedBy hold the actor that performed the
// action; the framework populates them from the request context (see
// WithActor). DeletedAt marks the row as soft-deleted; queries on
// these tables should add `WHERE deleted_at IS NULL` (or use the
// SoftDeleteFilter helper).
//
//	type Order struct {
//	    ID     uuid.UUID
//	    Total  int64
//	    dbx.Audit
//	}
//
// The corresponding DDL fragment is provided by AuditColumnsDDL.
type Audit struct {
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
	CreatedBy *uuid.UUID
	UpdatedBy *uuid.UUID
	DeletedBy *uuid.UUID
}

// Touch updates UpdatedAt and (when an actor is in the context)
// UpdatedBy. Repositories should call it from every Update path so
// the columns stay consistent without each call site remembering.
func (a *Audit) Touch(ctx context.Context, now time.Time) {
	a.UpdatedAt = now
	if id, ok := ActorFromContext(ctx); ok {
		idCopy := id
		a.UpdatedBy = &idCopy
	}
}

// Create stamps the creation columns. Call it once when constructing a
// new entity, before INSERT.
func (a *Audit) Create(ctx context.Context, now time.Time) {
	a.CreatedAt = now
	a.UpdatedAt = now
	if id, ok := ActorFromContext(ctx); ok {
		idCopy := id
		a.CreatedBy = &idCopy
		a.UpdatedBy = &idCopy
	}
}

// SoftDelete marks the entity as deleted at `now`, attributing the
// deletion to the actor in ctx when present. The caller is expected to
// persist the change with an UPDATE — we never DELETE rows.
func (a *Audit) SoftDelete(ctx context.Context, now time.Time) {
	t := now
	a.DeletedAt = &t
	if id, ok := ActorFromContext(ctx); ok {
		idCopy := id
		a.DeletedBy = &idCopy
	}
}

// IsDeleted reports whether DeletedAt is set. Use it instead of
// comparing the pointer manually so call-sites stay readable.
func (a Audit) IsDeleted() bool { return a.DeletedAt != nil }

// AuditColumnsDDL is the canonical column block to embed in a CREATE
// TABLE statement. Keep the column order: deletion-related columns
// last, indexed columns first. The companion file
// migrations/_template/audit_columns.sql ships the same content.
const AuditColumnsDDL = `
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ NULL,
    created_by  UUID        NULL,
    updated_by  UUID        NULL,
    deleted_by  UUID        NULL`

// SoftDeleteFilter is the SQL fragment that turns a SELECT into a
// soft-delete-aware query. Concatenate it onto your WHERE clause.
//
//	q := "SELECT id FROM orders WHERE tenant_id = $1" + dbx.SoftDeleteFilter
const SoftDeleteFilter = " AND deleted_at IS NULL"
