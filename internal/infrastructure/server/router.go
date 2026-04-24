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
	Auth   *handler.AuthHandler
	Health *handler.HealthHandler
}

// Register binds routes onto app. Keeping routing in one place makes it
// trivial to audit auth requirements and rate-limit coverage.
func Register(app *fiber.App, h Handlers, tokens security.TokenIssuer) {
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
}
