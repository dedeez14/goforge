package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dedeez14/goforge/internal/domain/rbac"
	"github.com/dedeez14/goforge/pkg/errs"
)

const (
	qPermInsert = `
INSERT INTO permissions (id, code, resource, action, description, created_at, updated_at, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, NOW(), NOW(), $6, $6)
`
	qPermUpdate = `
UPDATE permissions
SET resource = $2, action = $3, description = $4, updated_at = NOW(), updated_by = $5
WHERE id = $1 AND deleted_at IS NULL
`
	qPermSoftDelete = `
UPDATE permissions
SET deleted_at = NOW(), updated_at = NOW(), updated_by = $2
WHERE id = $1 AND deleted_at IS NULL
`
	qPermSelectByID = `
SELECT id, code, resource, action, description, created_at, updated_at
FROM permissions WHERE id = $1 AND deleted_at IS NULL
`
	qPermSelectByCode = `
SELECT id, code, resource, action, description, created_at, updated_at
FROM permissions WHERE code = $1 AND deleted_at IS NULL
`
)

// PermissionRepository is the pgx-backed implementation of
// rbac.PermissionRepository.
type PermissionRepository struct {
	pool *pgxpool.Pool
}

// NewPermissionRepository wires a PermissionRepository to the pool.
func NewPermissionRepository(pool *pgxpool.Pool) *PermissionRepository {
	return &PermissionRepository{pool: pool}
}

// Create inserts p, mapping unique-violation to ErrPermissionTaken.
func (r *PermissionRepository) Create(ctx context.Context, p *rbac.Permission, actor *uuid.UUID) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	_, err := r.pool.Exec(ctx, qPermInsert,
		p.ID, p.Code, p.Resource, p.Action, p.Description, actorOrNil(actor),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return rbac.ErrPermissionTaken
		}
		return errs.Wrap(errs.KindInternal, "permission.create", "failed to create permission", err)
	}
	return nil
}

// Update mutates the row in place. Code is immutable here — callers
// who really want to rename a permission should create a new one.
func (r *PermissionRepository) Update(ctx context.Context, p *rbac.Permission, actor *uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, qPermUpdate, p.ID, p.Resource, p.Action, p.Description, actorOrNil(actor))
	if err != nil {
		return errs.Wrap(errs.KindInternal, "permission.update", "failed to update permission", err)
	}
	if tag.RowsAffected() == 0 {
		return rbac.ErrPermissionNotFound
	}
	return nil
}

// Delete is a soft delete: sets deleted_at so historical role_permissions
// rows still resolve to a meaningful row.
func (r *PermissionRepository) Delete(ctx context.Context, id uuid.UUID, actor *uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, qPermSoftDelete, id, actorOrNil(actor))
	if err != nil {
		return errs.Wrap(errs.KindInternal, "permission.delete", "failed to delete permission", err)
	}
	if tag.RowsAffected() == 0 {
		return rbac.ErrPermissionNotFound
	}
	return nil
}

// FindByID returns the permission or ErrPermissionNotFound.
func (r *PermissionRepository) FindByID(ctx context.Context, id uuid.UUID) (*rbac.Permission, error) {
	return scanPermission(r.pool.QueryRow(ctx, qPermSelectByID, id))
}

// FindByCode returns the permission identified by Code.
func (r *PermissionRepository) FindByCode(ctx context.Context, code string) (*rbac.Permission, error) {
	return scanPermission(r.pool.QueryRow(ctx, qPermSelectByCode, code))
}

// List returns permissions matching filter. Result ordering is by
// resource then code so the response is stable across calls.
func (r *PermissionRepository) List(ctx context.Context, f rbac.PermissionFilter) ([]*rbac.Permission, error) {
	q := `
SELECT id, code, resource, action, description, created_at, updated_at
FROM permissions
WHERE deleted_at IS NULL`
	var args []any
	i := 1
	if f.Resource != "" {
		q += " AND resource = $" + itoa(i)
		args = append(args, f.Resource)
		i++
	}
	if f.Search != "" {
		q += " AND (LOWER(code) LIKE $" + itoa(i) + " OR LOWER(description) LIKE $" + itoa(i) + ")"
		args = append(args, "%"+strings.ToLower(f.Search)+"%")
		i++
	}
	q += " ORDER BY resource, code"
	if f.Limit > 0 {
		q += " LIMIT $" + itoa(i)
		args = append(args, f.Limit)
		i++
	}
	if f.Offset > 0 {
		q += " OFFSET $" + itoa(i)
		args = append(args, f.Offset)
	}
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "permission.list", "failed to list permissions", err)
	}
	defer rows.Close()
	var out []*rbac.Permission
	for rows.Next() {
		p, err := scanPermission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(errs.KindInternal, "permission.list_iter", "iterating permissions failed", err)
	}
	return out, nil
}

// rowScanner is the minimal surface we need to scan from either a
// *pgx.Row or a pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanPermission(row rowScanner) (*rbac.Permission, error) {
	var p rbac.Permission
	if err := row.Scan(&p.ID, &p.Code, &p.Resource, &p.Action, &p.Description, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, rbac.ErrPermissionNotFound
		}
		return nil, errs.Wrap(errs.KindInternal, "permission.scan", "failed to read permission", err)
	}
	return &p, nil
}
