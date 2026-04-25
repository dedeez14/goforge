package rbac

import (
	"context"

	"github.com/google/uuid"
)

// PermissionRepository is the persistence port for Permission.
type PermissionRepository interface {
	Create(ctx context.Context, p *Permission, actor *uuid.UUID) error
	Update(ctx context.Context, p *Permission, actor *uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID, actor *uuid.UUID) error
	FindByID(ctx context.Context, id uuid.UUID) (*Permission, error)
	FindByCode(ctx context.Context, code string) (*Permission, error)
	List(ctx context.Context, filter PermissionFilter) ([]*Permission, error)
}

// PermissionFilter narrows List() results.
type PermissionFilter struct {
	Resource string
	Search   string // case-insensitive substring against code/description
	Limit    int
	Offset   int
}

// RoleRepository is the persistence port for Role.
type RoleRepository interface {
	Create(ctx context.Context, r *Role, actor *uuid.UUID) error
	Update(ctx context.Context, r *Role, actor *uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID, actor *uuid.UUID) error
	FindByID(ctx context.Context, id uuid.UUID) (*Role, error)
	FindByCode(ctx context.Context, tenantID *uuid.UUID, code string) (*Role, error)
	List(ctx context.Context, filter RoleFilter) ([]*Role, error)

	// Permission management.
	SetPermissions(ctx context.Context, roleID uuid.UUID, permissionIDs []uuid.UUID, actor *uuid.UUID) error
	ListPermissions(ctx context.Context, roleID uuid.UUID) ([]*Permission, error)
}

// RoleFilter narrows List() results.
type RoleFilter struct {
	TenantID *uuid.UUID // nil → don't filter by tenant; pointer-to-zero-uuid → only global roles
	Search   string
	Limit    int
	Offset   int
}

// UserRoleRepository is the persistence port for the user_roles
// grant table.
type UserRoleRepository interface {
	Assign(ctx context.Context, userID, roleID uuid.UUID, tenantID *uuid.UUID, actor *uuid.UUID) error
	Revoke(ctx context.Context, userID, roleID uuid.UUID, tenantID *uuid.UUID) error
	ReplaceForUser(ctx context.Context, userID uuid.UUID, tenantID *uuid.UUID, roleIDs []uuid.UUID, actor *uuid.UUID) error
	ListRoles(ctx context.Context, userID uuid.UUID, tenantID *uuid.UUID) ([]*Role, error)
	ListUserPermissionCodes(ctx context.Context, userID uuid.UUID, tenantID *uuid.UUID) ([]string, error)
}
