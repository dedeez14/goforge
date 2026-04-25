// Package rbac is the domain package for role-based access control.
//
// It models three aggregates:
//
//   - Permission: a stable, dotted code (e.g. "users.read") that
//     identifies a single capability. Codes are application-wide.
//   - Role: a named bundle of permissions. Roles are tenant-scoped;
//     a NULL tenant means a "global" role shipped by the platform.
//   - UserRole: the grant linking a user to a role inside a tenant.
//
// The domain has no SQL, no HTTP, and no Casbin imports.
package rbac

import (
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/pkg/errs"
)

// Permission is a single capability identified by Code.
type Permission struct {
	ID          uuid.UUID
	Code        string
	Resource    string
	Action      string
	Description string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Role is a bundle of permissions, optionally scoped to a tenant.
type Role struct {
	ID          uuid.UUID
	TenantID    *uuid.UUID
	Code        string
	Name        string
	Description string
	IsSystem    bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// UserRole grants a Role to a User inside a (possibly nil) tenant.
type UserRole struct {
	UserID   uuid.UUID
	RoleID   uuid.UUID
	TenantID *uuid.UUID

	CreatedAt time.Time
}

// Domain errors. Handlers never construct these directly.
var (
	ErrPermissionNotFound = errs.NotFound("rbac.permission_not_found", "permission not found")
	ErrPermissionTaken    = errs.Conflict("rbac.permission_code_taken", "permission code already exists")

	ErrRoleNotFound = errs.NotFound("rbac.role_not_found", "role not found")
	ErrRoleTaken    = errs.Conflict("rbac.role_code_taken", "role code already exists in this tenant")
	ErrRoleSystem   = errs.Forbidden("rbac.role_is_system", "system roles cannot be modified")

	ErrUserRoleNotFound = errs.NotFound("rbac.user_role_not_found", "user role not found")
)
