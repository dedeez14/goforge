// Package platform wires goforge's signature features (idempotency,
// outbox, realtime SSE, OpenAPI, Prometheus metrics) into the running
// HTTP server. It is the single bridge between the generic pkg/*
// packages and the concrete *fiber.App built by internal/infrastructure.
//
// Each feature is opt-in via cfg.Platform and renders into clearly
// named groups so they are easy to audit:
//
//	/admin/healthz, /admin/modules, /admin/metrics
//	/openapi.json, /docs (Swagger UI)
//	/api/v1/stream (SSE)
//
// The platform package is intentionally not a Module itself - it is
// the host that owns Modules. Third-party modules implement the
// pkg/module.Module interface and are registered the same way the
// builtin features are.
package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/dedeez14/goforge/internal/config"
	"github.com/dedeez14/goforge/pkg/events"
	"github.com/dedeez14/goforge/pkg/idempotency"
	"github.com/dedeez14/goforge/pkg/module"
	"github.com/dedeez14/goforge/pkg/observability"
	"github.com/dedeez14/goforge/pkg/openapi"
	"github.com/dedeez14/goforge/pkg/outbox"
	"github.com/dedeez14/goforge/pkg/realtime"
	"github.com/dedeez14/goforge/pkg/tenant"
)

// Services aggregates the platform components. Application code holds
// a single Services value and wires sub-pieces (e.g. the openapi
// document) into existing handlers.
type Services struct {
	Bus      *events.Bus
	OpenAPI  *openapi.Document
	Metrics  *observability.Metrics
	Realtime *realtime.Hub
	Modules  *module.Registry

	cfg     config.Platform
	app     *fiber.App
	log     zerolog.Logger
	workers []module.Worker
}

// Build constructs the platform services and mounts every enabled
// feature on app. The returned Services keeps a reference to running
// workers so callers can drive their lifetime via Start/Shutdown.
func Build(app *fiber.App, pool *pgxpool.Pool, cfg config.Platform, info openapi.Info, log zerolog.Logger) *Services {
	s := &Services{
		Bus:     events.NewBus(log.With().Str("component", "events.bus").Logger()),
		OpenAPI: openapi.New(info),
		Modules: &module.Registry{},
		cfg:     cfg,
		app:     app,
		log:     log,
	}
	if cfg.MetricsEnabled {
		s.Metrics = observability.New()
		app.Use(observability.Middleware(s.Metrics))
	}
	s.Realtime = realtime.NewHub(s.Bus, log.With().Str("component", "realtime").Logger())
	s.mountAdmin(app)
	s.mountOpenAPI(app)
	s.mountRealtime(app)
	s.mountIdempotency(app, pool)
	s.mountOutbox(pool)
	return s
}

// Workers returns the long-running goroutines registered by enabled
// features (currently the outbox dispatcher and any module workers).
// internal/app supervises them.
func (s *Services) Workers() []module.Worker { return s.workers }

func (s *Services) mountAdmin(app *fiber.App) {
	admin := app.Group("/admin", s.adminAuthMiddleware())
	admin.Get("/modules", func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"success": true,
			"data":    s.Modules.Names(),
		})
	})
	admin.Get("/healthz", func(c *fiber.Ctx) error {
		mods := s.Modules.Each()
		results := make([]fiber.Map, 0, len(mods))
		ok := true
		for _, m := range mods {
			err := m.Health(c.UserContext())
			results = append(results, fiber.Map{
				"name":    m.Name(),
				"healthy": err == nil,
				"detail": func() string {
					if err == nil {
						return ""
					}
					return err.Error()
				}(),
			})
			if err != nil {
				ok = false
			}
		}
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"healthy": ok, "modules": results})
	})
	if s.Metrics != nil {
		admin.Get("/metrics", observability.Handler(s.Metrics))
	}
}

func (s *Services) adminAuthMiddleware() fiber.Handler {
	token := s.cfg.AdminToken
	return func(c *fiber.Ctx) error {
		if token == "" {
			// Without a configured token, /admin is local-only.
			if c.IP() != "127.0.0.1" && c.IP() != "::1" {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"success": false,
					"error":   fiber.Map{"code": "admin.forbidden", "message": "admin endpoints require a token"},
				})
			}
			return c.Next()
		}
		if c.Get("X-Admin-Token") != token {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"success": false,
				"error":   fiber.Map{"code": "admin.unauthorized", "message": "invalid admin token"},
			})
		}
		return c.Next()
	}
}

func (s *Services) mountOpenAPI(app *fiber.App) {
	if !s.cfg.OpenAPIEnabled {
		return
	}
	app.Get("/openapi.json", s.OpenAPI.JSONHandler())
	app.Get("/docs", s.OpenAPI.SwaggerUIHandler("/openapi.json"))
}

func (s *Services) mountRealtime(app *fiber.App) {
	if !s.cfg.RealtimeEnabled {
		return
	}
	stream := app.Group("/api/v1/stream", tenant.Middleware(tenant.HeaderResolver(s.cfg.TenantHeader)))
	stream.Get("", s.Realtime.Handler())
}

func (s *Services) mountIdempotency(app *fiber.App, pool *pgxpool.Pool) {
	if !s.cfg.IdempotencyEnabled {
		return
	}
	ttl, err := time.ParseDuration(s.cfg.IdempotencyTTL)
	if err != nil || ttl <= 0 {
		ttl = 24 * time.Hour
	}
	store := idempotency.NewPostgresStore(pool)
	app.Use(idempotency.Middleware(idempotency.Options{Store: store, TTL: ttl}))
	s.workers = append(s.workers, func(ctx context.Context) error {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
				removed, err := store.Sweep(ctx)
				if err != nil {
					s.log.Warn().Err(err).Msg("idempotency sweep failed")
					continue
				}
				if removed > 0 {
					s.log.Info().Int64("removed", removed).Msg("idempotency keys swept")
				}
			}
		}
	})
}

func (s *Services) mountOutbox(pool *pgxpool.Pool) {
	if !s.cfg.OutboxEnabled {
		return
	}
	disp := &outbox.Dispatcher{
		Pool:        pool,
		Sink:        outbox.BusSink{Bus: s.Bus},
		Logger:      s.log.With().Str("component", "outbox").Logger(),
		BatchSize:   s.cfg.OutboxBatchSize,
		Interval:    time.Duration(s.cfg.OutboxIntervalMs) * time.Millisecond,
		MaxAttempts: 12,
	}
	s.workers = append(s.workers, func(ctx context.Context) error {
		err := disp.Run(ctx)
		if err != nil && ctx.Err() == nil {
			return fmt.Errorf("outbox dispatcher: %w", err)
		}
		return nil
	})
}

// JSON returns a JSON-encoded snapshot of the platform configuration.
// Useful for boot-time logging and the admin dashboard.
func (s *Services) JSON() []byte {
	raw, _ := json.Marshal(map[string]any{
		"openapi_enabled":     s.cfg.OpenAPIEnabled,
		"realtime_enabled":    s.cfg.RealtimeEnabled,
		"metrics_enabled":     s.cfg.MetricsEnabled,
		"idempotency_enabled": s.cfg.IdempotencyEnabled,
		"outbox_enabled":      s.cfg.OutboxEnabled,
	})
	return raw
}
