package handler

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/adapter/http/dto"
	"github.com/dedeez14/goforge/internal/adapter/http/middleware"
	"github.com/dedeez14/goforge/internal/domain/rbac"
	"github.com/dedeez14/goforge/internal/usecase"
	"github.com/dedeez14/goforge/pkg/errs"
	"github.com/dedeez14/goforge/pkg/httpx"
	"github.com/dedeez14/goforge/pkg/validatorx"
)

// PermissionHandler exposes the Permission catalog over HTTP.
type PermissionHandler struct {
	uc *usecase.PermissionUseCase
}

// NewPermissionHandler constructs a PermissionHandler.
func NewPermissionHandler(uc *usecase.PermissionUseCase) *PermissionHandler {
	return &PermissionHandler{uc: uc}
}

// List GET /api/v1/permissions
func (h *PermissionHandler) List(c *fiber.Ctx) error {
	limit, _ := strconv.Atoi(c.Query("limit", "100"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))
	perms, err := h.uc.List(c.UserContext(), rbac.PermissionFilter{
		Resource: c.Query("resource"),
		Search:   c.Query("q"),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.PermissionsFromDomain(perms))
}

// Get GET /api/v1/permissions/:id
func (h *PermissionHandler) Get(c *fiber.Ctx) error {
	id, err := parseUUIDParam(c, "id")
	if err != nil {
		return httpx.RespondError(c, err)
	}
	p, err := h.uc.Get(c.UserContext(), id)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.PermissionFromDomain(p))
}

// Create POST /api/v1/permissions
func (h *PermissionHandler) Create(c *fiber.Ctx) error {
	var req dto.CreatePermissionRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.RespondError(c, errs.InvalidInput("request.malformed", "malformed request body"))
	}
	if err := validatorx.Struct(&req); err != nil {
		return httpx.RespondError(c, err)
	}
	actor := actorPtr(c)
	p, err := h.uc.Create(c.UserContext(), usecase.CreatePermissionInput{
		Code:        req.Code,
		Resource:    req.Resource,
		Action:      req.Action,
		Description: req.Description,
	}, actor)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.Created(c, dto.PermissionFromDomain(p))
}

// Update PATCH /api/v1/permissions/:id
func (h *PermissionHandler) Update(c *fiber.Ctx) error {
	id, err := parseUUIDParam(c, "id")
	if err != nil {
		return httpx.RespondError(c, err)
	}
	var req dto.UpdatePermissionRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.RespondError(c, errs.InvalidInput("request.malformed", "malformed request body"))
	}
	if err := validatorx.Struct(&req); err != nil {
		return httpx.RespondError(c, err)
	}
	p, err := h.uc.Update(c.UserContext(), id, usecase.UpdatePermissionInput{
		Resource:    req.Resource,
		Action:      req.Action,
		Description: req.Description,
	}, actorPtr(c))
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.PermissionFromDomain(p))
}

// Delete DELETE /api/v1/permissions/:id
func (h *PermissionHandler) Delete(c *fiber.Ctx) error {
	id, err := parseUUIDParam(c, "id")
	if err != nil {
		return httpx.RespondError(c, err)
	}
	if err := h.uc.Delete(c.UserContext(), id, actorPtr(c)); err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.NoContent(c)
}

// RoleHandler exposes the Role aggregate over HTTP.
type RoleHandler struct {
	roles  *usecase.RoleUseCase
	access *usecase.UserAccessUseCase
}

// NewRoleHandler constructs a RoleHandler.
func NewRoleHandler(roles *usecase.RoleUseCase, access *usecase.UserAccessUseCase) *RoleHandler {
	return &RoleHandler{roles: roles, access: access}
}

// List GET /api/v1/roles
func (h *RoleHandler) List(c *fiber.Ctx) error {
	limit, _ := strconv.Atoi(c.Query("limit", "100"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))

	var tenantID *uuid.UUID
	if raw := strings.TrimSpace(c.Query("tenant_id")); raw != "" {
		t, err := uuid.Parse(raw)
		if err != nil {
			return httpx.RespondError(c, errs.InvalidInput("request.tenant_id_invalid", "tenant_id is not a valid UUID"))
		}
		tenantID = &t
	}

	roles, err := h.roles.List(c.UserContext(), rbac.RoleFilter{
		TenantID: tenantID,
		Search:   c.Query("q"),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.RolesFromDomain(roles))
}

// Get GET /api/v1/roles/:id
func (h *RoleHandler) Get(c *fiber.Ctx) error {
	id, err := parseUUIDParam(c, "id")
	if err != nil {
		return httpx.RespondError(c, err)
	}
	r, err := h.roles.Get(c.UserContext(), id)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.RoleFromDomain(r))
}

// Create POST /api/v1/roles
func (h *RoleHandler) Create(c *fiber.Ctx) error {
	var req dto.CreateRoleRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.RespondError(c, errs.InvalidInput("request.malformed", "malformed request body"))
	}
	if err := validatorx.Struct(&req); err != nil {
		return httpx.RespondError(c, err)
	}
	r, err := h.roles.Create(c.UserContext(), usecase.CreateRoleInput{
		TenantID:    req.TenantID,
		Code:        req.Code,
		Name:        req.Name,
		Description: req.Description,
	}, actorPtr(c))
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.Created(c, dto.RoleFromDomain(r))
}

// Update PATCH /api/v1/roles/:id
func (h *RoleHandler) Update(c *fiber.Ctx) error {
	id, err := parseUUIDParam(c, "id")
	if err != nil {
		return httpx.RespondError(c, err)
	}
	var req dto.UpdateRoleRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.RespondError(c, errs.InvalidInput("request.malformed", "malformed request body"))
	}
	if err := validatorx.Struct(&req); err != nil {
		return httpx.RespondError(c, err)
	}
	r, err := h.roles.Update(c.UserContext(), id, usecase.UpdateRoleInput{
		Name:        req.Name,
		Description: req.Description,
	}, actorPtr(c))
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.RoleFromDomain(r))
}

// Delete DELETE /api/v1/roles/:id
func (h *RoleHandler) Delete(c *fiber.Ctx) error {
	id, err := parseUUIDParam(c, "id")
	if err != nil {
		return httpx.RespondError(c, err)
	}
	if err := h.roles.Delete(c.UserContext(), id, actorPtr(c)); err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.NoContent(c)
}

// ListPermissions GET /api/v1/roles/:id/permissions
func (h *RoleHandler) ListPermissions(c *fiber.Ctx) error {
	id, err := parseUUIDParam(c, "id")
	if err != nil {
		return httpx.RespondError(c, err)
	}
	perms, err := h.roles.ListPermissions(c.UserContext(), id)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.PermissionsFromDomain(perms))
}

// SetPermissions PUT /api/v1/roles/:id/permissions
func (h *RoleHandler) SetPermissions(c *fiber.Ctx) error {
	id, err := parseUUIDParam(c, "id")
	if err != nil {
		return httpx.RespondError(c, err)
	}
	var req dto.SetRolePermissionsRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.RespondError(c, errs.InvalidInput("request.malformed", "malformed request body"))
	}
	if err := validatorx.Struct(&req); err != nil {
		return httpx.RespondError(c, err)
	}
	perms, err := h.roles.SetPermissions(c.UserContext(), id, req.PermissionIDs, actorPtr(c))
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.PermissionsFromDomain(perms))
}

// AssignUserRoles PUT /api/v1/users/:id/roles
func (h *RoleHandler) AssignUserRoles(c *fiber.Ctx) error {
	uid, err := parseUUIDParam(c, "id")
	if err != nil {
		return httpx.RespondError(c, err)
	}
	var req dto.AssignRolesRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.RespondError(c, errs.InvalidInput("request.malformed", "malformed request body"))
	}
	if err := validatorx.Struct(&req); err != nil {
		return httpx.RespondError(c, err)
	}
	roles, err := h.access.ReplaceRoles(c.UserContext(), uid, req.TenantID, req.RoleIDs, actorPtr(c))
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.RolesFromDomain(roles))
}

// MyAccess GET /api/v1/me/access — returns the current user's roles
// and effective permission codes for the active tenant. Frontends
// use this to gate UI features.
func (h *RoleHandler) MyAccess(c *fiber.Ctx) error {
	uid := middleware.UserIDFromCtx(c)
	if uid == uuid.Nil {
		return httpx.RespondError(c, errs.Unauthorized("auth.missing_user", "authentication required"))
	}
	tenantID := middleware.HeaderTenantResolver(c)
	roles, err := h.access.ListRoles(c.UserContext(), uid, tenantID)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	perms, err := h.access.EffectivePermissions(c.UserContext(), uid, tenantID)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.UserAccessResponse{
		Roles:       dto.RolesFromDomain(roles),
		Permissions: perms,
	})
}

// parseUUIDParam parses the named route param as a UUID and returns
// a stable error when the value is not a valid UUID. Every current
// caller passes "id" but the parameter stays explicit so future
// handlers binding `:user_id` etc. behave correctly instead of
// silently reading `:id`.
//
//nolint:unparam // see comment above
func parseUUIDParam(c *fiber.Ctx, name string) (uuid.UUID, error) {
	raw := c.Params(name)
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errs.InvalidInput("request.invalid_uuid", "path parameter "+name+" is not a valid UUID")
	}
	return id, nil
}

// actorPtr returns a pointer to the authenticated user id, or nil
// when the request was unauthenticated (which the route layer should
// have rejected, but we don't crash on a missing local).
func actorPtr(c *fiber.Ctx) *uuid.UUID {
	id := middleware.UserIDFromCtx(c)
	if id == uuid.Nil {
		return nil
	}
	return &id
}
