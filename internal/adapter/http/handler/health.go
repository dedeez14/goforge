package handler

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/internal/config"
	"github.com/dedeez14/goforge/pkg/db"
	"github.com/dedeez14/goforge/pkg/httpx"
	"github.com/dedeez14/goforge/pkg/lifecycle"
)

// HealthHandler exposes liveness and readiness probes. Liveness is a
// cheap static response; readiness pings real dependencies.
type HealthHandler struct {
	app     config.App
	router  *db.Router
	drainer *lifecycle.Drainer
}

// NewHealthHandler constructs a HealthHandler. The router is used for
// readiness checks so that when a read replica is configured, the
// readiness probe fails whenever *either* pool is unreachable - the
// service must have both a writable primary and a reachable replica
// before it declares itself ready.
//
// The optional drainer lets the process declare itself "draining" -
// /readyz will then return 503 so Kubernetes removes the pod from
// its Service endpoints before the HTTP server actually shuts down.
// Liveness is unaffected: the process is still alive and servicing
// in-flight requests, so killing it prematurely would cause the
// same rolling-deploy 5xx we are trying to avoid.
func NewHealthHandler(app config.App, router *db.Router, drainer *lifecycle.Drainer) *HealthHandler {
	return &HealthHandler{app: app, router: router, drainer: drainer}
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

// Ready validates downstream dependencies (DB) before declaring
// ready. It also reports 503 once shutdown has begun so Kubernetes
// can drain the pod out of the Service before any in-flight request
// is cut off.
func (h *HealthHandler) Ready(c *fiber.Ctx) error {
	if h.drainer != nil && h.drainer.IsDraining() {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"success": false,
			"status":  "draining",
			"reason":  "shutdown in progress",
		})
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 2*time.Second)
	defer cancel()
	if err := h.router.Ping(ctx); err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"success": false,
			"status":  "degraded",
			"reason":  "database unavailable",
		})
	}
	return httpx.OK(c, fiber.Map{
		"status":      "ready",
		"has_replica": h.router.HasReplica(),
	})
}
