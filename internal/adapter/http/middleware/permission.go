package middleware

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/dedeez14/goforge/pkg/errs"
	"github.com/dedeez14/goforge/pkg/httpx"
)

// PermissionResolver returns the permission codes the user holds in
// tenant. Implementations typically wrap UserAccessUseCase.
type PermissionResolver interface {
	EffectivePermissions(ctx context.Context, userID uuid.UUID, tenantID *uuid.UUID) ([]string, error)
}

// TenantResolver derives the active tenant id from the request. The
// default implementation reads the X-Tenant-ID header; apps with a
// different convention can plug their own resolver here.
type TenantResolver func(c *fiber.Ctx) *uuid.UUID

// HeaderTenantResolver reads the X-Tenant-ID header. Returns nil when
// the header is missing or unparseable so the request is treated as
// "global tenant".
func HeaderTenantResolver(c *fiber.Ctx) *uuid.UUID {
	raw := c.Get("X-Tenant-ID")
	if raw == "" {
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil
	}
	return &id
}

// CtxKeyPermissions stores the resolved permission codes once
// RequirePermission has loaded them, so downstream handlers can avoid
// a second DB round-trip.
const CtxKeyPermissions = "user_permissions"

// RequirePermission returns a middleware that allows the request only
// when the authenticated user holds the named permission code.
//
// The middleware MUST run after Auth — it relies on UserIDFromCtx.
//
// Behaviour:
//
//	missing/invalid bearer    → 401 (already handled by Auth)
//	user has no roles         → 403 rbac.permission_required
//	user has the code         → next handler
//	resolver returns an error → 500
//
// To gate "either A or B", use RequireAnyPermission instead.
func RequirePermission(code string, resolver PermissionResolver, tenant TenantResolver) fiber.Handler {
	if tenant == nil {
		tenant = HeaderTenantResolver
	}
	return func(c *fiber.Ctx) error {
		uid := UserIDFromCtx(c)
		if uid == uuid.Nil {
			return httpx.RespondError(c, errs.Unauthorized("auth.missing_user", "authentication required"))
		}
		codes, err := resolver.EffectivePermissions(c.UserContext(), uid, tenant(c))
		if err != nil {
			return httpx.RespondError(c, err)
		}
		c.Locals(CtxKeyPermissions, codes)
		for _, have := range codes {
			if have == code {
				return c.Next()
			}
		}
		return httpx.RespondError(c, errs.Forbidden("rbac.permission_required", "missing required permission: "+code))
	}
}

// RequireAnyPermission allows the request when the user holds at
// least one of the named codes.
func RequireAnyPermission(codes []string, resolver PermissionResolver, tenant TenantResolver) fiber.Handler {
	if tenant == nil {
		tenant = HeaderTenantResolver
	}
	wanted := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		wanted[c] = struct{}{}
	}
	return func(c *fiber.Ctx) error {
		uid := UserIDFromCtx(c)
		if uid == uuid.Nil {
			return httpx.RespondError(c, errs.Unauthorized("auth.missing_user", "authentication required"))
		}
		held, err := resolver.EffectivePermissions(c.UserContext(), uid, tenant(c))
		if err != nil {
			return httpx.RespondError(c, err)
		}
		c.Locals(CtxKeyPermissions, held)
		for _, h := range held {
			if _, ok := wanted[h]; ok {
				return c.Next()
			}
		}
		return httpx.RespondError(c, errs.Forbidden("rbac.permission_required", "missing required permission"))
	}
}

// PermissionsFromCtx returns the resolved permission codes set by
// RequirePermission/RequireAnyPermission, or nil if the middleware
// did not run.
func PermissionsFromCtx(c *fiber.Ctx) []string {
	v := c.Locals(CtxKeyPermissions)
	if v == nil {
		return nil
	}
	codes, _ := v.([]string)
	return codes
}
