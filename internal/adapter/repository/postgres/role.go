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
	qRoleInsert = `
INSERT INTO roles (id, tenant_id, code, name, description, is_system, created_at, updated_at, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW(), $7, $7)
`
	qRoleUpdate = `
UPDATE roles
SET name = $2, description = $3, updated_at = NOW(), updated_by = $4
WHERE id = $1 AND deleted_at IS NULL AND is_system = FALSE
`
	qRoleSoftDelete = `
UPDATE roles
SET deleted_at = NOW(), updated_at = NOW(), updated_by = $2
WHERE id = $1 AND deleted_at IS NULL AND is_system = FALSE
`
	qRoleSelectByID = `
SELECT id, tenant_id, code, name, description, is_system, created_at, updated_at
FROM roles WHERE id = $1 AND deleted_at IS NULL
`
	qRoleSelectByCode = `
SELECT id, tenant_id, code, name, description, is_system, created_at, updated_at
FROM roles
WHERE code = $1 AND deleted_at IS NULL
  AND COALESCE(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid)
    = COALESCE($2::uuid,  '00000000-0000-0000-0000-000000000000'::uuid)
`
	qRoleListPermissions = `
SELECT p.id, p.code, p.resource, p.action, p.description, p.created_at, p.updated_at
FROM role_permissions rp
JOIN permissions p ON p.id = rp.permission_id
WHERE rp.role_id = $1 AND p.deleted_at IS NULL
ORDER BY p.resource, p.code
`
	qRoleClearPermissions = `DELETE FROM role_permissions WHERE role_id = $1`
	qRoleAddPermission    = `
INSERT INTO role_permissions (role_id, permission_id, created_by)
VALUES ($1, $2, $3)
ON CONFLICT (role_id, permission_id) DO NOTHING
`
)

// RoleRepository is the pgx-backed implementation of rbac.RoleRepository.
type RoleRepository struct {
	pool *pgxpool.Pool
}

// NewRoleRepository wires a RoleRepository to the pool.
func NewRoleRepository(pool *pgxpool.Pool) *RoleRepository {
	return &RoleRepository{pool: pool}
}

// Create inserts r and maps unique-violation to ErrRoleTaken.
func (r *RoleRepository) Create(ctx context.Context, role *rbac.Role, actor *uuid.UUID) error {
	if role.ID == uuid.Nil {
		role.ID = uuid.New()
	}
	_, err := r.pool.Exec(ctx, qRoleInsert,
		role.ID, role.TenantID, role.Code, role.Name, role.Description, role.IsSystem, actorOrNil(actor),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return rbac.ErrRoleTaken
		}
		return errs.Wrap(errs.KindInternal, "role.create", "failed to create role", err)
	}
	return nil
}

// Update mutates the row in place. System roles are immutable so the
// SQL filter rejects them silently; we return ErrRoleSystem when the
// row exists but is system.
func (r *RoleRepository) Update(ctx context.Context, role *rbac.Role, actor *uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, qRoleUpdate, role.ID, role.Name, role.Description, actorOrNil(actor))
	if err != nil {
		return errs.Wrap(errs.KindInternal, "role.update", "failed to update role", err)
	}
	if tag.RowsAffected() == 0 {
		// Either not found or system.
		existing, ferr := r.FindByID(ctx, role.ID)
		if ferr != nil {
			return ferr
		}
		if existing.IsSystem {
			return rbac.ErrRoleSystem
		}
		return rbac.ErrRoleNotFound
	}
	return nil
}

// Delete is a soft delete; system roles are protected.
func (r *RoleRepository) Delete(ctx context.Context, id uuid.UUID, actor *uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, qRoleSoftDelete, id, actorOrNil(actor))
	if err != nil {
		return errs.Wrap(errs.KindInternal, "role.delete", "failed to delete role", err)
	}
	if tag.RowsAffected() == 0 {
		existing, ferr := r.FindByID(ctx, id)
		if ferr != nil {
			return ferr
		}
		if existing.IsSystem {
			return rbac.ErrRoleSystem
		}
		return rbac.ErrRoleNotFound
	}
	return nil
}

// FindByID returns the role.
func (r *RoleRepository) FindByID(ctx context.Context, id uuid.UUID) (*rbac.Role, error) {
	return scanRole(r.pool.QueryRow(ctx, qRoleSelectByID, id))
}

// FindByCode returns the role identified by (tenant, code).
func (r *RoleRepository) FindByCode(ctx context.Context, tenantID *uuid.UUID, code string) (*rbac.Role, error) {
	return scanRole(r.pool.QueryRow(ctx, qRoleSelectByCode, code, tenantID))
}

// List returns roles matching filter, ordered by name.
func (r *RoleRepository) List(ctx context.Context, f rbac.RoleFilter) ([]*rbac.Role, error) {
	q := `
SELECT id, tenant_id, code, name, description, is_system, created_at, updated_at
FROM roles
WHERE deleted_at IS NULL`
	var args []any
	i := 1
	if f.TenantID != nil {
		// pointer-to-zero-uuid means "global only"
		if *f.TenantID == uuid.Nil {
			q += " AND tenant_id IS NULL"
		} else {
			q += " AND tenant_id = $" + itoa(i)
			args = append(args, *f.TenantID)
			i++
		}
	}
	if f.Search != "" {
		q += " AND (LOWER(name) LIKE $" + itoa(i) + " OR LOWER(code) LIKE $" + itoa(i) + ")"
		args = append(args, "%"+strings.ToLower(f.Search)+"%")
		i++
	}
	q += " ORDER BY name"
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
		return nil, errs.Wrap(errs.KindInternal, "role.list", "failed to list roles", err)
	}
	defer rows.Close()
	var out []*rbac.Role
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, role)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(errs.KindInternal, "role.list_iter", "iterating roles failed", err)
	}
	return out, nil
}

// SetPermissions replaces a role's permission set atomically.
func (r *RoleRepository) SetPermissions(ctx context.Context, roleID uuid.UUID, permissionIDs []uuid.UUID, actor *uuid.UUID) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return errs.Wrap(errs.KindInternal, "role.set_perms.begin", "failed to begin tx", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Confirm role exists & is not soft-deleted.
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM roles WHERE id = $1 AND deleted_at IS NULL)`, roleID,
	).Scan(&exists); err != nil {
		return errs.Wrap(errs.KindInternal, "role.set_perms.exists", "failed to verify role", err)
	}
	if !exists {
		return rbac.ErrRoleNotFound
	}

	if _, err := tx.Exec(ctx, qRoleClearPermissions, roleID); err != nil {
		return errs.Wrap(errs.KindInternal, "role.set_perms.clear", "failed to clear role permissions", err)
	}
	for _, pid := range permissionIDs {
		if _, err := tx.Exec(ctx, qRoleAddPermission, roleID, pid, actorOrNil(actor)); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
				return rbac.ErrPermissionNotFound
			}
			return errs.Wrap(errs.KindInternal, "role.set_perms.insert", "failed to assign permission", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return errs.Wrap(errs.KindInternal, "role.set_perms.commit", "failed to commit", err)
	}
	return nil
}

// ListPermissions returns the resolved permissions of a role.
func (r *RoleRepository) ListPermissions(ctx context.Context, roleID uuid.UUID) ([]*rbac.Permission, error) {
	rows, err := r.pool.Query(ctx, qRoleListPermissions, roleID)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "role.list_perms", "failed to list role permissions", err)
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
		return nil, errs.Wrap(errs.KindInternal, "role.list_perms_iter", "iterating role permissions failed", err)
	}
	return out, nil
}

// foreignKeyViolation is Postgres' SQLSTATE for FK errors. We use it
// to convert a missing permission_id into rbac.ErrPermissionNotFound.
const foreignKeyViolation = "23503"

func scanRole(row rowScanner) (*rbac.Role, error) {
	var (
		role     rbac.Role
		tenantID *uuid.UUID
	)
	if err := row.Scan(&role.ID, &tenantID, &role.Code, &role.Name, &role.Description, &role.IsSystem, &role.CreatedAt, &role.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, rbac.ErrRoleNotFound
		}
		return nil, errs.Wrap(errs.KindInternal, "role.scan", "failed to read role", err)
	}
	role.TenantID = tenantID
	return &role, nil
}
