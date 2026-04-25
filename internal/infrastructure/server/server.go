// Package server builds and runs the Fiber HTTP server.
package server

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/contrib/fiberzerolog"
	"github.com/gofiber/fiber/v2"
	fibercors "github.com/gofiber/fiber/v2/middleware/cors"
	fiberlimiter "github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/rs/zerolog"

	"github.com/dedeez14/goforge/internal/adapter/http/middleware"
	"github.com/dedeez14/goforge/internal/config"
	"github.com/dedeez14/goforge/pkg/errs"
	"github.com/dedeez14/goforge/pkg/httpx"
)

// New builds a fully-configured *fiber.App ready for routes to be
// registered on. The returned app has all global middlewares installed
// in a deliberate order: recover -> request id -> security headers ->
// cors -> rate limit -> request logger -> timeout.
func New(cfg *config.Config, log zerolog.Logger) *fiber.App {
	fcfg := fiber.Config{
		AppName:               cfg.App.Name,
		ServerHeader:          cfg.App.Name,
		DisableStartupMessage: cfg.IsProduction(),
		ReadTimeout:           cfg.HTTP.ReadTimeout,
		WriteTimeout:          cfg.HTTP.WriteTimeout,
		IdleTimeout:           cfg.HTTP.IdleTimeout,
		BodyLimit:             cfg.HTTP.BodyLimitBytes,
		Prefork:               cfg.HTTP.Prefork,
		JSONEncoder:           json.Marshal,
		JSONDecoder:           json.Unmarshal,
		ErrorHandler:          errorHandler,
		EnableIPValidation:    true,
	}
	if len(cfg.HTTP.TrustedProxies) > 0 {
		fcfg.EnableTrustedProxyCheck = true
		fcfg.TrustedProxies = cfg.HTTP.TrustedProxies
	}
	app := fiber.New(fcfg)

	// Order matters. Panic recovery MUST run first so every subsequent
	// middleware's errors surface as JSON envelopes, not raw 500s.
	app.Use(middleware.Recover(log))
	app.Use(middleware.RequestID())
	app.Use(middleware.SecurityHeaders())

	app.Use(fibercors.New(fibercors.Config{
		AllowOrigins: cfg.Security.CORSAllowOrigins,
		AllowMethods: "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization, X-Request-ID",
		MaxAge:       86400,
	}))

	app.Use(fiberlimiter.New(fiberlimiter.Config{
		Max:        cfg.Security.RateLimitPerMin,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			if cfg.Security.TrustXForwarded {
				if ip := c.Get(fiber.HeaderXForwardedFor); ip != "" {
					return ip
				}
			}
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return httpx.RespondError(c, errs.New(errs.KindRateLimited, "rate_limited", "too many requests"))
		},
	}))

	app.Use(fiberzerolog.New(fiberzerolog.Config{
		Logger: &log,
		Fields: []string{"ip", "latency", "status", "method", "url", "userAgent", "requestId"},
	}))

	if cfg.HTTP.WriteTimeout > 0 {
		app.Use(middleware.Timeout(cfg.HTTP.WriteTimeout))
	}

	return app
}

// errorHandler is the fallback Fiber ErrorHandler for any error that
// escapes a route handler without being rendered by httpx.RespondError.
// In practice this catches BodyParser panics and unknown route 404s.
func errorHandler(c *fiber.Ctx, err error) error {
	var fe *fiber.Error
	if errors.As(err, &fe) {
		switch fe.Code {
		case fiber.StatusNotFound:
			return httpx.RespondError(c, errs.NotFound("route.not_found", "route not found"))
		case fiber.StatusMethodNotAllowed:
			return httpx.RespondError(c, errs.InvalidInput("route.method_not_allowed", "method not allowed"))
		case fiber.StatusRequestEntityTooLarge:
			return httpx.RespondError(c, errs.InvalidInput("request.too_large", "request body too large"))
		}
	}
	return httpx.RespondError(c, err)
}
