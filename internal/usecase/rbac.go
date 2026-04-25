// Package usecase RBAC orchestrators.
//
// This file groups three use cases:
//
//   - PermissionUseCase: catalog-level CRUD over the Permission
//     aggregate. Permissions are framework-wide so they live outside
//     any tenant.
//   - RoleUseCase: CRUD over Role + permission assignment. System
//     roles (is_system = true) are read-only.
//   - UserAccessUseCase: assign/revoke roles to users, list a
//     user's effective permissions.
//
// Each method validates inputs and writes a row to the audit log
// (when an Auditor is configured) so security-relevant changes are
// traceable.
package usecase

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/dedeez14/goforge/internal/domain/rbac"
	"github.com/dedeez14/goforge/pkg/errs"
)

// Auditor is the minimal subset of pkg/audit we depend on. Defining
// it here keeps usecase imports light and lets tests pass nil for a
// no-op auditor.
type Auditor interface {
	Log(ctx context.Context, actor *uuid.UUID, action, resource string, before, after any) error
}

// CreatePermissionInput is the payload for creating a Permission.
type CreatePermissionInput struct {
	Code        string
	Resource    string
	Action      string
	Description string
}

// UpdatePermissionInput is the payload for updating a Permission.
type UpdatePermissionInput struct {
	Resource    string
	Action      string
	Description string
}

// PermissionUseCase orchestrates Permission CRUD.
type PermissionUseCase struct {
	repo  rbac.PermissionRepository
	audit Auditor
	log   zerolog.Logger
}

// NewPermissionUseCase constructs the use case.
func NewPermissionUseCase(repo rbac.PermissionRepository, audit Auditor, log zerolog.Logger) *PermissionUseCase {
	return &PermissionUseCase{repo: repo, audit: audit, log: log}
}

// Create validates the input then persists a new permission.
func (uc *PermissionUseCase) Create(ctx context.Context, in CreatePermissionInput, actor *uuid.UUID) (*rbac.Permission, error) {
	if err := validatePermissionCode(in.Code); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Resource) == "" || strings.TrimSpace(in.Action) == "" {
		return nil, errs.InvalidInput("rbac.permission_invalid", "resource and action are required")
	}
	p := &rbac.Permission{
		Code:        strings.ToLower(strings.TrimSpace(in.Code)),
		Resource:    strings.ToLower(strings.TrimSpace(in.Resource)),
		Action:      strings.ToLower(strings.TrimSpace(in.Action)),
		Description: strings.TrimSpace(in.Description),
	}
	if err := uc.repo.Create(ctx, p, actor); err != nil {
		return nil, err
	}
	uc.auditSafe(ctx, actor, "rbac.permission.create", p.Code, nil, p)
	return p, nil
}

// Update mutates a permission's metadata. Code is immutable.
func (uc *PermissionUseCase) Update(ctx context.Context, id uuid.UUID, in UpdatePermissionInput, actor *uuid.UUID) (*rbac.Permission, error) {
	current, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	updated := *current
	if strings.TrimSpace(in.Resource) != "" {
		updated.Resource = strings.ToLower(strings.TrimSpace(in.Resource))
	}
	if strings.TrimSpace(in.Action) != "" {
		updated.Action = strings.ToLower(strings.TrimSpace(in.Action))
	}
	updated.Description = strings.TrimSpace(in.Description)
	if err := uc.repo.Update(ctx, &updated, actor); err != nil {
		return nil, err
	}
	uc.auditSafe(ctx, actor, "rbac.permission.update", current.Code, current, updated)
	return &updated, nil
}

// Delete soft-deletes a permission.
func (uc *PermissionUseCase) Delete(ctx context.Context, id uuid.UUID, actor *uuid.UUID) error {
	current, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if err := uc.repo.Delete(ctx, id, actor); err != nil {
		return err
	}
	uc.auditSafe(ctx, actor, "rbac.permission.delete", current.Code, current, nil)
	return nil
}

// Get returns a single permission.
func (uc *PermissionUseCase) Get(ctx context.Context, id uuid.UUID) (*rbac.Permission, error) {
	return uc.repo.FindByID(ctx, id)
}

// List returns permissions matching the filter.
func (uc *PermissionUseCase) List(ctx context.Context, f rbac.PermissionFilter) ([]*rbac.Permission, error) {
	return uc.repo.List(ctx, f)
}

// CreateRoleInput is the payload for creating a Role.
type CreateRoleInput struct {
	TenantID    *uuid.UUID
	Code        string
	Name        string
	Description string
	IsSystem    bool
}

// UpdateRoleInput is the payload for updating a Role's metadata.
type UpdateRoleInput struct {
	Name        string
	Description string
}

// RoleUseCase orchestrates Role CRUD and permission assignment.
type RoleUseCase struct {
	roles rbac.RoleRepository
	perms rbac.PermissionRepository
	audit Auditor
	log   zerolog.Logger
}

// NewRoleUseCase constructs the use case.
func NewRoleUseCase(roles rbac.RoleRepository, perms rbac.PermissionRepository, audit Auditor, log zerolog.Logger) *RoleUseCase {
	return &RoleUseCase{roles: roles, perms: perms, audit: audit, log: log}
}

// Create validates the input then persists a new role.
func (uc *RoleUseCase) Create(ctx context.Context, in CreateRoleInput, actor *uuid.UUID) (*rbac.Role, error) {
	if err := validateRoleCode(in.Code); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, errs.InvalidInput("rbac.role_invalid", "name is required")
	}
	r := &rbac.Role{
		TenantID:    in.TenantID,
		Code:        strings.ToLower(strings.TrimSpace(in.Code)),
		Name:        strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description),
		IsSystem:    in.IsSystem,
	}
	if err := uc.roles.Create(ctx, r, actor); err != nil {
		return nil, err
	}
	uc.auditSafe(ctx, actor, "rbac.role.create", r.Code, nil, r)
	return r, nil
}

// Update mutates a role's metadata. System roles are immutable.
func (uc *RoleUseCase) Update(ctx context.Context, id uuid.UUID, in UpdateRoleInput, actor *uuid.UUID) (*rbac.Role, error) {
	current, err := uc.roles.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	updated := *current
	if strings.TrimSpace(in.Name) != "" {
		updated.Name = strings.TrimSpace(in.Name)
	}
	updated.Description = strings.TrimSpace(in.Description)
	if err := uc.roles.Update(ctx, &updated, actor); err != nil {
		return nil, err
	}
	uc.auditSafe(ctx, actor, "rbac.role.update", current.Code, current, updated)
	return &updated, nil
}

// Delete soft-deletes a role. System roles are protected.
func (uc *RoleUseCase) Delete(ctx context.Context, id uuid.UUID, actor *uuid.UUID) error {
	current, err := uc.roles.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if err := uc.roles.Delete(ctx, id, actor); err != nil {
		return err
	}
	uc.auditSafe(ctx, actor, "rbac.role.delete", current.Code, current, nil)
	return nil
}

// Get returns a single role.
func (uc *RoleUseCase) Get(ctx context.Context, id uuid.UUID) (*rbac.Role, error) {
	return uc.roles.FindByID(ctx, id)
}

// List returns roles matching the filter.
func (uc *RoleUseCase) List(ctx context.Context, f rbac.RoleFilter) ([]*rbac.Role, error) {
	return uc.roles.List(ctx, f)
}

// SetPermissions replaces the role's permission set.
func (uc *RoleUseCase) SetPermissions(ctx context.Context, roleID uuid.UUID, permissionIDs []uuid.UUID, actor *uuid.UUID) ([]*rbac.Permission, error) {
	current, err := uc.roles.FindByID(ctx, roleID)
	if err != nil {
		return nil, err
	}
	if current.IsSystem {
		return nil, rbac.ErrRoleSystem
	}
	dedup := make([]uuid.UUID, 0, len(permissionIDs))
	seen := map[uuid.UUID]struct{}{}
	for _, id := range permissionIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		dedup = append(dedup, id)
	}
	if err := uc.roles.SetPermissions(ctx, roleID, dedup, actor); err != nil {
		return nil, err
	}
	uc.auditSafe(ctx, actor, "rbac.role.set_permissions", current.Code, nil, dedup)
	return uc.roles.ListPermissions(ctx, roleID)
}

// ListPermissions returns the role's resolved permissions.
func (uc *RoleUseCase) ListPermissions(ctx context.Context, roleID uuid.UUID) ([]*rbac.Permission, error) {
	if _, err := uc.roles.FindByID(ctx, roleID); err != nil {
		return nil, err
	}
	return uc.roles.ListPermissions(ctx, roleID)
}

// UserAccessUseCase orchestrates user-role grants and effective
// permission resolution.
type UserAccessUseCase struct {
	userRoles rbac.UserRoleRepository
	roles     rbac.RoleRepository
	audit     Auditor
	log       zerolog.Logger
}

// NewUserAccessUseCase constructs the use case.
func NewUserAccessUseCase(userRoles rbac.UserRoleRepository, roles rbac.RoleRepository, audit Auditor, log zerolog.Logger) *UserAccessUseCase {
	return &UserAccessUseCase{userRoles: userRoles, roles: roles, audit: audit, log: log}
}

// AssignRole grants a role to a user in tenantID.
func (uc *UserAccessUseCase) AssignRole(ctx context.Context, userID, roleID uuid.UUID, tenantID *uuid.UUID, actor *uuid.UUID) error {
	role, err := uc.roles.FindByID(ctx, roleID)
	if err != nil {
		return err
	}
	if err := uc.userRoles.Assign(ctx, userID, roleID, tenantID, actor); err != nil {
		return err
	}
	uc.auditSafe(ctx, actor, "rbac.user.assign_role", userID.String(), nil, role)
	return nil
}

// RevokeRole removes a single grant.
func (uc *UserAccessUseCase) RevokeRole(ctx context.Context, userID, roleID uuid.UUID, tenantID *uuid.UUID, actor *uuid.UUID) error {
	role, err := uc.roles.FindByID(ctx, roleID)
	if err != nil && !errors.Is(err, rbac.ErrRoleNotFound) {
		return err
	}
	if err := uc.userRoles.Revoke(ctx, userID, roleID, tenantID); err != nil {
		return err
	}
	uc.auditSafe(ctx, actor, "rbac.user.revoke_role", userID.String(), role, nil)
	return nil
}

// ReplaceRoles sets the user's roles in tenant to exactly roleIDs.
func (uc *UserAccessUseCase) ReplaceRoles(ctx context.Context, userID uuid.UUID, tenantID *uuid.UUID, roleIDs []uuid.UUID, actor *uuid.UUID) ([]*rbac.Role, error) {
	if err := uc.userRoles.ReplaceForUser(ctx, userID, tenantID, roleIDs, actor); err != nil {
		return nil, err
	}
	roles, err := uc.userRoles.ListRoles(ctx, userID, tenantID)
	if err != nil {
		return nil, err
	}
	uc.auditSafe(ctx, actor, "rbac.user.replace_roles", userID.String(), nil, roles)
	return roles, nil
}

// ListRoles returns the user's roles in tenant.
func (uc *UserAccessUseCase) ListRoles(ctx context.Context, userID uuid.UUID, tenantID *uuid.UUID) ([]*rbac.Role, error) {
	return uc.userRoles.ListRoles(ctx, userID, tenantID)
}

// EffectivePermissions returns the union of permission codes the
// user holds via their assigned roles in tenantID.
func (uc *UserAccessUseCase) EffectivePermissions(ctx context.Context, userID uuid.UUID, tenantID *uuid.UUID) ([]string, error) {
	return uc.userRoles.ListUserPermissionCodes(ctx, userID, tenantID)
}

// validatePermissionCode enforces the dotted-lowercase convention
// (e.g. "users.read"). Codes are immutable after creation.
func validatePermissionCode(code string) error {
	c := strings.TrimSpace(code)
	if c == "" {
		return errs.InvalidInput("rbac.permission_code_required", "permission code is required")
	}
	if len(c) > 100 {
		return errs.InvalidInput("rbac.permission_code_long", "permission code must be at most 100 characters")
	}
	for _, r := range c {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-') {
			return errs.InvalidInput("rbac.permission_code_invalid", "permission code may only contain letters, digits, '.', '_' and '-'")
		}
	}
	return nil
}

// validateRoleCode mirrors the permission-code rules for role codes.
func validateRoleCode(code string) error {
	c := strings.TrimSpace(code)
	if c == "" {
		return errs.InvalidInput("rbac.role_code_required", "role code is required")
	}
	if len(c) > 100 {
		return errs.InvalidInput("rbac.role_code_long", "role code must be at most 100 characters")
	}
	for _, r := range c {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return errs.InvalidInput("rbac.role_code_invalid", "role code may only contain letters, digits, '_' and '-'")
		}
	}
	return nil
}

// auditSafe never blocks the success path — audit failures are
// logged at warn level only.
func (uc *PermissionUseCase) auditSafe(ctx context.Context, actor *uuid.UUID, action, resource string, before, after any) {
	if uc.audit == nil {
		return
	}
	if err := uc.audit.Log(ctx, actor, action, resource, before, after); err != nil {
		uc.log.Warn().Err(err).Str("action", action).Msg("audit log failed")
	}
}

func (uc *RoleUseCase) auditSafe(ctx context.Context, actor *uuid.UUID, action, resource string, before, after any) {
	if uc.audit == nil {
		return
	}
	if err := uc.audit.Log(ctx, actor, action, resource, before, after); err != nil {
		uc.log.Warn().Err(err).Str("action", action).Msg("audit log failed")
	}
}

func (uc *UserAccessUseCase) auditSafe(ctx context.Context, actor *uuid.UUID, action, resource string, before, after any) {
	if uc.audit == nil {
		return
	}
	if err := uc.audit.Log(ctx, actor, action, resource, before, after); err != nil {
		uc.log.Warn().Err(err).Str("action", action).Msg("audit log failed")
	}
}
