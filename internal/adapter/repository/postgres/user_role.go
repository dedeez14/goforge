package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dedeez14/goforge/internal/domain/rbac"
	"github.com/dedeez14/goforge/pkg/errs"
)

const (
	qUserRoleInsert = `
INSERT INTO user_roles (user_id, role_id, tenant_id, created_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING
`
	qUserRoleDelete = `
DELETE FROM user_roles
WHERE user_id = $1 AND role_id = $2
  AND COALESCE(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid)
    = COALESCE($3::uuid,  '00000000-0000-0000-0000-000000000000'::uuid)
`
	qUserRoleListRoles = `
SELECT r.id, r.tenant_id, r.code, r.name, r.description, r.is_system, r.created_at, r.updated_at
FROM user_roles ur
JOIN roles r ON r.id = ur.role_id
WHERE ur.user_id = $1
  AND r.deleted_at IS NULL
  AND COALESCE(ur.tenant_id, '00000000-0000-0000-0000-000000000000'::uuid)
    = COALESCE($2::uuid,    '00000000-0000-0000-0000-000000000000'::uuid)
ORDER BY r.name
`
	qUserRoleListPermissionCodes = `
SELECT DISTINCT p.code
FROM user_roles ur
JOIN role_permissions rp ON rp.role_id = ur.role_id
JOIN permissions p       ON p.id       = rp.permission_id
JOIN roles r             ON r.id       = ur.role_id
WHERE ur.user_id = $1
  AND r.deleted_at IS NULL
  AND p.deleted_at IS NULL
  AND COALESCE(ur.tenant_id, '00000000-0000-0000-0000-000000000000'::uuid)
    = COALESCE($2::uuid,    '00000000-0000-0000-0000-000000000000'::uuid)
ORDER BY p.code
`
)

// UserRoleRepository is the pgx-backed implementation of
// rbac.UserRoleRepository.
type UserRoleRepository struct {
	pool *pgxpool.Pool
}

// NewUserRoleRepository wires a UserRoleRepository to the pool.
func NewUserRoleRepository(pool *pgxpool.Pool) *UserRoleRepository {
	return &UserRoleRepository{pool: pool}
}

// Assign grants role to user in tenant.
func (r *UserRoleRepository) Assign(ctx context.Context, userID, roleID uuid.UUID, tenantID *uuid.UUID, actor *uuid.UUID) error {
	if _, err := r.pool.Exec(ctx, qUserRoleInsert, userID, roleID, tenantID, actorOrNil(actor)); err != nil {
		return errs.Wrap(errs.KindInternal, "user_role.assign", "failed to assign role", err)
	}
	return nil
}

// Revoke removes a single grant.
func (r *UserRoleRepository) Revoke(ctx context.Context, userID, roleID uuid.UUID, tenantID *uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, qUserRoleDelete, userID, roleID, tenantID)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "user_role.revoke", "failed to revoke role", err)
	}
	if tag.RowsAffected() == 0 {
		return rbac.ErrUserRoleNotFound
	}
	return nil
}

// ReplaceForUser sets the user's roles in tenant to exactly roleIDs.
func (r *UserRoleRepository) ReplaceForUser(ctx context.Context, userID uuid.UUID, tenantID *uuid.UUID, roleIDs []uuid.UUID, actor *uuid.UUID) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return errs.Wrap(errs.KindInternal, "user_role.replace.begin", "failed to begin tx", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM user_roles
WHERE user_id = $1
  AND COALESCE(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid)
    = COALESCE($2::uuid,  '00000000-0000-0000-0000-000000000000'::uuid)`,
		userID, tenantID,
	); err != nil {
		return errs.Wrap(errs.KindInternal, "user_role.replace.clear", "failed to clear grants", err)
	}
	for _, rid := range roleIDs {
		if _, err := tx.Exec(ctx, qUserRoleInsert, userID, rid, tenantID, actorOrNil(actor)); err != nil {
			return errs.Wrap(errs.KindInternal, "user_role.replace.insert", "failed to insert grant", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return errs.Wrap(errs.KindInternal, "user_role.replace.commit", "failed to commit", err)
	}
	return nil
}

// ListRoles returns the user's resolved roles inside tenantID.
func (r *UserRoleRepository) ListRoles(ctx context.Context, userID uuid.UUID, tenantID *uuid.UUID) ([]*rbac.Role, error) {
	rows, err := r.pool.Query(ctx, qUserRoleListRoles, userID, tenantID)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "user_role.list_roles", "failed to list user roles", err)
	}
	defer rows.Close()
	var out []*rbac.Role
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil && !errors.Is(err, rbac.ErrRoleNotFound) {
			return nil, err
		}
		if role != nil {
			out = append(out, role)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(errs.KindInternal, "user_role.list_roles_iter", "iterating user roles failed", err)
	}
	return out, nil
}

// ListUserPermissionCodes returns the union of permission codes the
// user holds via their assigned roles in tenantID.
func (r *UserRoleRepository) ListUserPermissionCodes(ctx context.Context, userID uuid.UUID, tenantID *uuid.UUID) ([]string, error) {
	rows, err := r.pool.Query(ctx, qUserRoleListPermissionCodes, userID, tenantID)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "user_role.list_perm_codes", "failed to list user permission codes", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, errs.Wrap(errs.KindInternal, "user_role.list_perm_codes.scan", "failed to scan permission code", err)
		}
		out = append(out, code)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(errs.KindInternal, "user_role.list_perm_codes_iter", "iterating permission codes failed", err)
	}
	return out, nil
}
