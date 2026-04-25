package handler

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/adapter/http/dto"
	"github.com/dedeez14/goforge/internal/adapter/http/middleware"
	"github.com/dedeez14/goforge/internal/usecase"
	"github.com/dedeez14/goforge/pkg/errs"
	"github.com/dedeez14/goforge/pkg/httpx"
	"github.com/dedeez14/goforge/pkg/validatorx"
)

// MenuHandler exposes the Menu aggregate over HTTP.
type MenuHandler struct {
	menus  *usecase.MenuUseCase
	access *usecase.UserAccessUseCase
}

// NewMenuHandler constructs a MenuHandler.
func NewMenuHandler(menus *usecase.MenuUseCase, access *usecase.UserAccessUseCase) *MenuHandler {
	return &MenuHandler{menus: menus, access: access}
}

// List GET /api/v1/menus — flat list (admin view).
func (h *MenuHandler) List(c *fiber.Ctx) error {
	tenantID := tenantParam(c)
	all, err := h.menus.ListAll(c.UserContext(), tenantID)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	out := make([]dto.MenuResponse, 0, len(all))
	for _, m := range all {
		out = append(out, dto.MenuFromDomain(m))
	}
	return httpx.OK(c, out)
}

// Tree GET /api/v1/menus/tree — full tree (admin view).
func (h *MenuHandler) Tree(c *fiber.Ctx) error {
	tenantID := tenantParam(c)
	tree, err := h.menus.Tree(c.UserContext(), tenantID)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.MenuTreeFromDomain(tree))
}

// MyMenu GET /api/v1/menus/mine — tree filtered by the user's
// effective permissions in the active tenant. This is the endpoint
// frontends call when rendering the navigation drawer / sidebar.
func (h *MenuHandler) MyMenu(c *fiber.Ctx) error {
	uid := middleware.UserIDFromCtx(c)
	if uid == uuid.Nil {
		return httpx.RespondError(c, errs.Unauthorized("auth.missing_user", "authentication required"))
	}
	tenantID := middleware.HeaderTenantResolver(c)
	codes, err := h.access.EffectivePermissions(c.UserContext(), uid, tenantID)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	tree, err := h.menus.VisibleTree(c.UserContext(), tenantID, codes)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.MenuTreeFromDomain(tree))
}

// Get GET /api/v1/menus/:id
func (h *MenuHandler) Get(c *fiber.Ctx) error {
	id, err := parseUUIDParam(c, "id")
	if err != nil {
		return httpx.RespondError(c, err)
	}
	m, err := h.menus.Get(c.UserContext(), id)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.MenuFromDomain(m))
}

// Create POST /api/v1/menus
func (h *MenuHandler) Create(c *fiber.Ctx) error {
	var req dto.CreateMenuRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.RespondError(c, errs.InvalidInput("request.malformed", "malformed request body"))
	}
	if err := validatorx.Struct(&req); err != nil {
		return httpx.RespondError(c, err)
	}
	m, err := h.menus.Create(c.UserContext(), usecase.CreateMenuInput{
		TenantID:               req.TenantID,
		ParentID:               req.ParentID,
		Code:                   req.Code,
		Label:                  req.Label,
		Icon:                   req.Icon,
		Path:                   req.Path,
		SortOrder:              req.SortOrder,
		RequiredPermissionCode: req.RequiredPermissionCode,
		IsVisible:              req.IsVisible,
		Metadata:               req.Metadata,
	}, actorPtr(c))
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.Created(c, dto.MenuFromDomain(m))
}

// Update PATCH /api/v1/menus/:id
func (h *MenuHandler) Update(c *fiber.Ctx) error {
	id, err := parseUUIDParam(c, "id")
	if err != nil {
		return httpx.RespondError(c, err)
	}
	var req dto.UpdateMenuRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.RespondError(c, errs.InvalidInput("request.malformed", "malformed request body"))
	}
	if err := validatorx.Struct(&req); err != nil {
		return httpx.RespondError(c, err)
	}
	m, err := h.menus.Update(c.UserContext(), id, usecase.UpdateMenuInput{
		ParentID:                req.ParentID,
		UnsetParent:             req.UnsetParent,
		Label:                   req.Label,
		Icon:                    req.Icon,
		Path:                    req.Path,
		SortOrder:               req.SortOrder,
		RequiredPermissionCode:  req.RequiredPermissionCode,
		UnsetRequiredPermission: req.UnsetRequiredPermission,
		IsVisible:               req.IsVisible,
		Metadata:                req.Metadata,
	}, actorPtr(c))
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.MenuFromDomain(m))
}

// Delete DELETE /api/v1/menus/:id
func (h *MenuHandler) Delete(c *fiber.Ctx) error {
	id, err := parseUUIDParam(c, "id")
	if err != nil {
		return httpx.RespondError(c, err)
	}
	if err := h.menus.Delete(c.UserContext(), id, actorPtr(c)); err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.NoContent(c)
}

// tenantParam reads ?tenant_id= from the query (admin endpoints) and
// falls back to the X-Tenant-ID header so admins can list either
// global or tenant-scoped menus interchangeably.
func tenantParam(c *fiber.Ctx) *uuid.UUID {
	if raw := strings.TrimSpace(c.Query("tenant_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err == nil {
			return &id
		}
	}
	return middleware.HeaderTenantResolver(c)
}
