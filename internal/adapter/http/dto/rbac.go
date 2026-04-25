package dto

import (
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/domain/rbac"
)

// CreatePermissionRequest is the JSON input for POST /permissions.
type CreatePermissionRequest struct {
	Code        string `json:"code"        validate:"required,min=2,max=100"`
	Resource    string `json:"resource"    validate:"required,min=1,max=64"`
	Action      string `json:"action"      validate:"required,min=1,max=32"`
	Description string `json:"description" validate:"max=500"`
}

// UpdatePermissionRequest is the JSON input for PATCH /permissions/:id.
type UpdatePermissionRequest struct {
	Resource    string `json:"resource"    validate:"omitempty,min=1,max=64"`
	Action      string `json:"action"      validate:"omitempty,min=1,max=32"`
	Description string `json:"description" validate:"max=500"`
}

// PermissionResponse is the JSON shape of a single permission.
type PermissionResponse struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Resource    string    `json:"resource"`
	Action      string    `json:"action"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PermissionFromDomain maps domain → DTO.
func PermissionFromDomain(p *rbac.Permission) PermissionResponse {
	return PermissionResponse{
		ID:          p.ID,
		Code:        p.Code,
		Resource:    p.Resource,
		Action:      p.Action,
		Description: p.Description,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// PermissionsFromDomain maps a slice.
func PermissionsFromDomain(ps []*rbac.Permission) []PermissionResponse {
	out := make([]PermissionResponse, 0, len(ps))
	for _, p := range ps {
		out = append(out, PermissionFromDomain(p))
	}
	return out
}

// CreateRoleRequest is the JSON input for POST /roles.
type CreateRoleRequest struct {
	TenantID    *uuid.UUID `json:"tenant_id"`
	Code        string     `json:"code"        validate:"required,min=2,max=100"`
	Name        string     `json:"name"        validate:"required,min=1,max=128"`
	Description string     `json:"description" validate:"max=500"`
}

// UpdateRoleRequest is the JSON input for PATCH /roles/:id.
type UpdateRoleRequest struct {
	Name        string `json:"name"        validate:"omitempty,min=1,max=128"`
	Description string `json:"description" validate:"max=500"`
}

// SetRolePermissionsRequest is the JSON input for PUT /roles/:id/permissions.
type SetRolePermissionsRequest struct {
	PermissionIDs []uuid.UUID `json:"permission_ids" validate:"required,dive,uuid4"`
}

// RoleResponse is the JSON shape of a single role.
type RoleResponse struct {
	ID          uuid.UUID  `json:"id"`
	TenantID    *uuid.UUID `json:"tenant_id"`
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	IsSystem    bool       `json:"is_system"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// RoleFromDomain maps domain → DTO.
func RoleFromDomain(r *rbac.Role) RoleResponse {
	return RoleResponse{
		ID:          r.ID,
		TenantID:    r.TenantID,
		Code:        r.Code,
		Name:        r.Name,
		Description: r.Description,
		IsSystem:    r.IsSystem,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// RolesFromDomain maps a slice.
func RolesFromDomain(rs []*rbac.Role) []RoleResponse {
	out := make([]RoleResponse, 0, len(rs))
	for _, r := range rs {
		out = append(out, RoleFromDomain(r))
	}
	return out
}

// AssignRolesRequest is the JSON input for PUT /users/:id/roles.
type AssignRolesRequest struct {
	TenantID *uuid.UUID  `json:"tenant_id"`
	RoleIDs  []uuid.UUID `json:"role_ids" validate:"required,dive,uuid4"`
}

// UserAccessResponse aggregates a user's roles + effective permissions.
type UserAccessResponse struct {
	Roles       []RoleResponse `json:"roles"`
	Permissions []string       `json:"permissions"`
}
