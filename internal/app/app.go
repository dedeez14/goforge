// Package app composes the application graph and runs the server.
// It is the single composition root - nothing outside this package
// constructs concrete infrastructure dependencies.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/adapter/http/dto"
	"github.com/dedeez14/goforge/internal/adapter/http/handler"
	pgrepo "github.com/dedeez14/goforge/internal/adapter/repository/postgres"
	"github.com/dedeez14/goforge/internal/config"
	"github.com/dedeez14/goforge/internal/infrastructure/database"
	"github.com/dedeez14/goforge/internal/infrastructure/logger"
	"github.com/dedeez14/goforge/internal/infrastructure/security"
	"github.com/dedeez14/goforge/internal/infrastructure/server"
	"github.com/dedeez14/goforge/internal/platform"
	"github.com/dedeez14/goforge/internal/usecase"
	"github.com/dedeez14/goforge/pkg/observability"
	"github.com/dedeez14/goforge/pkg/openapi"
)

// Run boots the application and blocks until the process receives
// SIGINT/SIGTERM or an unrecoverable startup error occurs.
func Run(ctx context.Context) error {
	configPath := flag.String("config", "", "path to config file (optional)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("config load: %w", err)
	}
	// Production-aware sanity checks (weak JWT secret, wildcard CORS,
	// trusted-proxy mis-wiring, argon below OWASP minimums, …).
	// Fail-fast at boot so the service never starts in an unsafe
	// state. Lower environments are lenient by design.
	if err := cfg.Verify(); err != nil {
		return fmt.Errorf("config verify: %w", err)
	}

	log := logger.New(cfg.Log, cfg.App)
	log.Info().Str("env", cfg.App.Env).Int("port", cfg.HTTP.Port).Msg("starting service")

	// OpenTelemetry: when an OTLP endpoint is configured, every
	// request handler, outbox dispatch and (future) DB call emits a
	// span. When it is empty, the global tracer provider is the
	// standard no-op so the rest of the framework can keep calling
	// observability.Tracer(...) unconditionally.
	tracingShutdown, err := observability.InitTracing(ctx, observability.TracingConfig{
		Endpoint:    cfg.Platform.OtelEndpoint,
		Insecure:    cfg.Platform.OtelInsecure,
		ServiceName: cfg.App.Name,
		Version:     cfg.App.Version,
		Environment: cfg.App.Env,
		SampleRatio: cfg.Platform.OtelSampleRatio,
	})
	if err != nil {
		return fmt.Errorf("init tracing: %w", err)
	}
	defer func() {
		if err := tracingShutdown(context.Background()); err != nil {
			log.Warn().Err(err).Msg("tracer shutdown")
		}
	}()

	pool, err := database.New(ctx, cfg.Database, log)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Infrastructure services.
	hasher := security.NewPasswordHasher(security.Argon2idParams{
		Memory:      cfg.Security.ArgonMemoryKiB,
		Iterations:  cfg.Security.ArgonIters,
		Parallelism: cfg.Security.ArgonParallel,
		SaltLength:  security.DefaultArgon2idParams.SaltLength,
		KeyLength:   security.DefaultArgon2idParams.KeyLength,
	})
	tokens := security.NewTokenIssuer(cfg.JWT)
	refreshStore := security.NewPostgresRefreshStore(pool)

	// Repositories.
	users := pgrepo.NewUserRepository(pool)
	permissions := pgrepo.NewPermissionRepository(pool)
	roles := pgrepo.NewRoleRepository(pool)
	userRoles := pgrepo.NewUserRoleRepository(pool)
	menus := pgrepo.NewMenuRepository(pool)
	apiKeys := pgrepo.NewAPIKeyRepository(pool)

	// Use-cases.
	authUC := usecase.NewAuthUseCase(users, hasher, tokens, refreshStore, log)
	// audit is wired by the platform.Build call below; for now the
	// RBAC + Menu use-cases run with a nil auditor (no-op). Apps
	// that want a persisted audit trail should pass an *audit.Logger.
	permUC := usecase.NewPermissionUseCase(permissions, nil, log)
	roleUC := usecase.NewRoleUseCase(roles, permissions, nil, log)
	accessUC := usecase.NewUserAccessUseCase(userRoles, roles, nil, log)
	menuUC := usecase.NewMenuUseCase(menus, nil, log)
	// API-key env tag: "live" in production, otherwise the configured
	// app environment ("staging", "dev", ...). Included in the visible
	// prefix so operators can tell at a glance which environment
	// minted a leaked key.
	apiKeyEnv := cfg.App.Env
	if cfg.IsProduction() {
		apiKeyEnv = "live"
	}
	apiKeyUC := usecase.NewAPIKeyUseCase(apiKeys, apiKeyEnv)

	// Handlers.
	handlers := server.Handlers{
		Auth:        handler.NewAuthHandler(authUC),
		Health:      handler.NewHealthHandler(cfg.App, pool),
		Permissions: handler.NewPermissionHandler(permUC),
		Roles:       handler.NewRoleHandler(roleUC, accessUC),
		Menus:       handler.NewMenuHandler(menuUC, accessUC),
		APIKeys:     handler.NewAPIKeyHandler(apiKeyUC),
	}

	app := server.New(cfg, log)

	// Platform features (idempotency, outbox, realtime, openapi, metrics).
	plat := platform.Build(app, pool, cfg.Platform, openapi.Info{
		Title:       cfg.App.Name,
		Version:     cfg.App.Version,
		Description: "Auto-generated OpenAPI 3.1 spec for the goforge API.",
	}, log)
	registerOpenAPI(plat.OpenAPI)
	platform.MountJWKS(app, tokens)

	server.Register(app, handlers, tokens, server.AccessControl{
		Resolver: accessUC,
		// Adapter from APIKeyUseCase.Authenticate (returns *apikey.Key)
		// to the middleware's APIKeyAuthenticate signature (returns
		// uuid.UUID + []string). When the key has no owner the
		// subject falls back to the key's own ID so request logs
		// still carry a stable identity.
		APIKeyAuth: func(ctx context.Context, plaintext string) (uuid.UUID, []string, error) {
			k, err := apiKeyUC.Authenticate(ctx, plaintext)
			if err != nil {
				return uuid.Nil, nil, err
			}
			subject := k.ID
			if k.UserID != nil {
				subject = *k.UserID
			}
			return subject, k.Scopes, nil
		},
	})

	addr := fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port)

	// Run server + module workers concurrently.
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for i, w := range plat.Workers() {
		wg.Add(1)
		go func(idx int, fn func(context.Context) error) {
			defer wg.Done()
			if err := fn(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
				log.Warn().Err(err).Int("worker", idx).Msg("platform worker exited with error")
			}
		}(i, w)
	}

	serverErr := make(chan error, 1)
	go func() {
		if err := app.Listen(addr); err != nil {
			serverErr <- err
		}
		close(serverErr)
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		if err != nil {
			cancelWorkers()
			wg.Wait()
			return fmt.Errorf("server error: %w", err)
		}
	case s := <-sig:
		log.Info().Str("signal", s.String()).Msg("shutdown signal received")
	case <-ctx.Done():
		log.Info().Msg("context cancelled; shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()

	if err := app.ShutdownWithContext(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error().Err(err).Msg("graceful shutdown failed")
		cancelWorkers()
		wg.Wait()
		return err
	}

	cancelWorkers()
	wg.Wait()

	// Allow outstanding work a last chance to drain.
	time.Sleep(100 * time.Millisecond)
	log.Info().Msg("shutdown complete")
	return nil
}

// registerOpenAPI declares the public auth + health endpoints in the
// generated OpenAPI document. Other modules add their own operations
// during their Init phase.
func registerOpenAPI(doc *openapi.Document) {
	doc.AddOperation(openapi.Operation{
		Method: "GET", Path: "/healthz",
		Summary: "Liveness probe", Tags: []string{"system"},
		ResponseType: map[string]any{"status": "ok"}, ResponseCode: 200,
	})
	doc.AddOperation(openapi.Operation{
		Method: "GET", Path: "/readyz",
		Summary: "Readiness probe", Tags: []string{"system"},
		ResponseType: map[string]any{"status": "ok"}, ResponseCode: 200,
	})
	doc.AddOperation(openapi.Operation{
		Method: "POST", Path: "/api/v1/auth/register",
		Summary: "Register a new user", Tags: []string{"auth"},
		RequestType:  dto.RegisterRequest{},
		ResponseType: dto.AuthResponse{},
		ResponseCode: 201,
	})
	doc.AddOperation(openapi.Operation{
		Method: "POST", Path: "/api/v1/auth/login",
		Summary: "Exchange credentials for a token pair", Tags: []string{"auth"},
		RequestType:  dto.LoginRequest{},
		ResponseType: dto.AuthResponse{},
		ResponseCode: 200,
	})
	doc.AddOperation(openapi.Operation{
		Method: "POST", Path: "/api/v1/auth/refresh",
		Summary: "Exchange refresh token for new pair", Tags: []string{"auth"},
		RequestType:  dto.RefreshRequest{},
		ResponseType: dto.AuthResponse{},
		ResponseCode: 200,
	})
	doc.AddOperation(openapi.Operation{
		Method: "GET", Path: "/api/v1/auth/me",
		Summary: "Return the authenticated user", Tags: []string{"auth"},
		ResponseType: dto.UserResponse{},
		ResponseCode: 200, RequiresAuth: true,
	})
	doc.AddOperation(openapi.Operation{
		Method: "GET", Path: "/api/v1/api-keys",
		Summary: "List the caller's API keys", Tags: []string{"api-keys"},
		ResponseType: []dto.APIKeyResponse{},
		ResponseCode: 200, RequiresAuth: true,
	})
	doc.AddOperation(openapi.Operation{
		Method: "POST", Path: "/api/v1/api-keys",
		Summary:      "Mint a new API key (plaintext returned exactly once)",
		Tags:         []string{"api-keys"},
		RequestType:  dto.CreateAPIKeyRequest{},
		ResponseType: dto.CreateAPIKeyResponse{},
		ResponseCode: 201, RequiresAuth: true,
	})
	doc.AddOperation(openapi.Operation{
		Method: "DELETE", Path: "/api/v1/api-keys/{id}",
		Summary: "Revoke an API key by id", Tags: []string{"api-keys"},
		ResponseCode: 204, RequiresAuth: true,
	})
}
