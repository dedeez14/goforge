package handler

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dedeez14/goforge/internal/config"
	"github.com/dedeez14/goforge/pkg/httpx"
)

// HealthHandler exposes liveness and readiness probes. Liveness is a
// cheap static response; readiness pings real dependencies.
type HealthHandler struct {
	app  config.App
	pool *pgxpool.Pool
}

// NewHealthHandler constructs a HealthHandler.
func NewHealthHandler(app config.App, pool *pgxpool.Pool) *HealthHandler {
	return &HealthHandler{app: app, pool: pool}
}

// Live is a cheap liveness probe. Returns 200 as long as the process
// is up and the event loop is responsive.
func (h *HealthHandler) Live(c *fiber.Ctx) error {
	return httpx.OK(c, fiber.Map{
		"status":  "ok",
		"service": h.app.Name,
		"version": h.app.Version,
	})
}

// Ready validates downstream dependencies (DB) before declaring ready.
func (h *HealthHandler) Ready(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.UserContext(), 2*time.Second)
	defer cancel()
	if err := h.pool.Ping(ctx); err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"success": false,
			"status":  "degraded",
			"reason":  "database unavailable",
		})
	}
	return httpx.OK(c, fiber.Map{"status": "ready"})
}
