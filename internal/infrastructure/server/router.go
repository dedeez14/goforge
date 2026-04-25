package server

import (
	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/internal/adapter/http/handler"
	"github.com/dedeez14/goforge/internal/adapter/http/middleware"
	"github.com/dedeez14/goforge/internal/infrastructure/security"
)

// Handlers bundles every handler used by the HTTP layer so Register()
// can remain a single, readable routing declaration.
type Handlers struct {
	Auth        *handler.AuthHandler
	Health      *handler.HealthHandler
	Permissions *handler.PermissionHandler
	Roles       *handler.RoleHandler
	Menus       *handler.MenuHandler
}

// AccessControl bundles the dependencies the route layer needs to
// install permission-aware middleware. PermissionResolver is read by
// every RequirePermission middleware to look up the caller's
// effective permission codes; TenantResolver is optional and falls
// back to the X-Tenant-ID header when nil.
type AccessControl struct {
	Resolver middleware.PermissionResolver
	Tenant   middleware.TenantResolver
}

// Register binds routes onto app. Keeping routing in one place makes it
// trivial to audit auth requirements and rate-limit coverage.
func Register(app *fiber.App, h Handlers, tokens security.TokenIssuer, ac AccessControl) {
	// Liveness/Readiness - outside the /api prefix and unauthenticated.
	app.Get("/healthz", h.Health.Live)
	app.Get("/readyz", h.Health.Ready)

	api := app.Group("/api/v1")

	// Public auth endpoints.
	auth := api.Group("/auth")
	auth.Post("/register", h.Auth.Register)
	auth.Post("/login", h.Auth.Login)
	auth.Post("/refresh", h.Auth.Refresh)

	// Authenticated endpoints.
	authed := api.Group("", middleware.Auth(tokens))
	authed.Get("/auth/me", h.Auth.Me)

	// "what can I do here" — every authenticated user may ask.
	if h.Roles != nil {
		authed.Get("/me/access", h.Roles.MyAccess)
	}

	// Menus (when wired): every authenticated user can fetch their
	// visible menu tree; admin CRUD is gated by menu.manage.
	if h.Menus != nil && ac.Resolver != nil {
		authed.Get("/menus/mine", h.Menus.MyMenu)

		menuAdmin := authed.Group("/menus", middleware.RequirePermission("menu.manage", ac.Resolver, ac.Tenant))
		menuAdmin.Get("/", h.Menus.List)
		menuAdmin.Get("/tree", h.Menus.Tree)
		menuAdmin.Post("/", h.Menus.Create)
		menuAdmin.Get("/:id", h.Menus.Get)
		menuAdmin.Patch("/:id", h.Menus.Update)
		menuAdmin.Delete("/:id", h.Menus.Delete)
	}

	// RBAC admin (when wired): everything is gated by rbac.manage.
	if h.Permissions != nil && h.Roles != nil && ac.Resolver != nil {
		rbacAdmin := authed.Group("", middleware.RequirePermission("rbac.manage", ac.Resolver, ac.Tenant))

		rbacAdmin.Get("/permissions", h.Permissions.List)
		rbacAdmin.Post("/permissions", h.Permissions.Create)
		rbacAdmin.Get("/permissions/:id", h.Permissions.Get)
		rbacAdmin.Patch("/permissions/:id", h.Permissions.Update)
		rbacAdmin.Delete("/permissions/:id", h.Permissions.Delete)

		rbacAdmin.Get("/roles", h.Roles.List)
		rbacAdmin.Post("/roles", h.Roles.Create)
		rbacAdmin.Get("/roles/:id", h.Roles.Get)
		rbacAdmin.Patch("/roles/:id", h.Roles.Update)
		rbacAdmin.Delete("/roles/:id", h.Roles.Delete)
		rbacAdmin.Get("/roles/:id/permissions", h.Roles.ListPermissions)
		rbacAdmin.Put("/roles/:id/permissions", h.Roles.SetPermissions)

		rbacAdmin.Put("/users/:id/roles", h.Roles.AssignUserRoles)
	}
}
