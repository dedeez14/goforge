package server

import (
	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/internal/adapter/http/handler"
	"github.com/dedeez14/goforge/internal/adapter/http/middleware"
	"github.com/dedeez14/goforge/internal/infrastructure/security"
	"github.com/dedeez14/goforge/pkg/httpcache"
)

// perUserCache builds a cache middleware instance for endpoints whose
// response varies per user. The Vary header lines we'd normally add
// (Authorization, Cookie, X-Tenant-ID) are effectively encoded in
// the resulting body hash - different callers get different bodies
// and therefore different ETags - but we still emit Cache-Control:
// private, must-revalidate so shared caches cannot collapse two
// users' responses onto one cache key.
func perUserCache() fiber.Handler {
	return httpcache.New(httpcache.Options{MaxAge: 30, Private: true, MustRevalidate: true})
}

// Handlers bundles every handler used by the HTTP layer so Register()
// can remain a single, readable routing declaration.
type Handlers struct {
	Auth        *handler.AuthHandler
	Health      *handler.HealthHandler
	Permissions *handler.PermissionHandler
	Roles       *handler.RoleHandler
	Menus       *handler.MenuHandler
	APIKeys     *handler.APIKeyHandler
	Sessions    *handler.SessionHandler
	Users       *handler.UserHandler
}

// AccessControl bundles the dependencies the route layer needs to
// install permission-aware middleware. PermissionResolver is read by
// every RequirePermission middleware to look up the caller's
// effective permission codes; TenantResolver is optional and falls
// back to the X-Tenant-ID header when nil; APIKeyAuth, when non-nil,
// upgrades the route group's auth from JWT-only to "JWT or API key".
type AccessControl struct {
	Resolver   middleware.PermissionResolver
	Tenant     middleware.TenantResolver
	APIKeyAuth middleware.APIKeyAuthenticate
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

	// Authenticated endpoints. When an API-key authenticator is
	// supplied, the bearer can be either a JWT (existing behaviour)
	// or a goforge-format API key; everything past this group reads
	// the user id and scopes from c.Locals identically.
	jwtAuth := middleware.Auth(tokens)
	var authMW fiber.Handler = jwtAuth
	if ac.APIKeyAuth != nil {
		authMW = middleware.APIKeyOrJWTAuth(ac.APIKeyAuth, jwtAuth)
	}
	authed := api.Group("", authMW)
	authed.Get("/auth/me", h.Auth.Me)

	// API-key self-service: every authenticated user manages their
	// own keys. Crucially, this sub-group rejects requests that
	// authenticated with an API key - otherwise a leaked narrow
	// key (e.g. scopes=["reports.read"]) could call POST /api-keys
	// to mint a fresh wildcard key for itself, escalating from
	// read-only to admin in a single hop. Credential management is
	// only allowed from a real interactive user session (JWT).
	if h.APIKeys != nil {
		keys := authed.Group("/api-keys", middleware.RequireUserSession())
		keys.Get("", h.APIKeys.List)
		keys.Post("", h.APIKeys.Create)
		keys.Delete("/:id", h.APIKeys.Revoke)
	}

	// "what can I do here" — every authenticated user may ask. The
	// SPA reloads this on every page navigation, so conditional-GET
	// caching is a meaningful CPU/egress win: when nothing has
	// changed, the response is a 304 with zero body bytes.
	if h.Roles != nil {
		authed.Get("/me/access", perUserCache(), h.Roles.MyAccess)
	}

	// Self-service device list. Same hardening as /api-keys: an
	// API-key-authenticated request must never be able to revoke the
	// owning user's interactive sessions, otherwise a leaked narrow
	// key can lock out the human and pivot to "single-credential
	// takeover". Credential-management endpoints are JWT-only.
	if h.Sessions != nil {
		me := authed.Group("/me/sessions", middleware.RequireUserSession())
		me.Get("", h.Sessions.List)
		me.Delete("", h.Sessions.RevokeAll)
		me.Delete("/:id", h.Sessions.Revoke)
	}

	// Menus (when wired): every authenticated user can fetch their
	// visible menu tree; admin CRUD is gated by menu.manage. The
	// tree is per-user (permission-pruned) so the cache is marked
	// private; the SPA hits this on every render, so conditional-GET
	// caching is worth wiring.
	if h.Menus != nil && ac.Resolver != nil {
		authed.Get("/menus/mine", perUserCache(), h.Menus.MyMenu)

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

		// Read-only user directory behind the same rbac.manage
		// gate - the admin UI uses it to populate the "assign
		// roles" form. Passwords and password hashes are never
		// rendered; the handler maps to the same UserResponse
		// the auth endpoints already emit.
		if h.Users != nil {
			rbacAdmin.Get("/users", h.Users.List)
		}
	}
}
