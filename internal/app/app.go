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
	"syscall"
	"time"

	"github.com/dedeez14/goforge/internal/adapter/http/handler"
	pgrepo "github.com/dedeez14/goforge/internal/adapter/repository/postgres"
	"github.com/dedeez14/goforge/internal/config"
	"github.com/dedeez14/goforge/internal/infrastructure/database"
	"github.com/dedeez14/goforge/internal/infrastructure/logger"
	"github.com/dedeez14/goforge/internal/infrastructure/security"
	"github.com/dedeez14/goforge/internal/infrastructure/server"
	"github.com/dedeez14/goforge/internal/usecase"
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

	log := logger.New(cfg.Log, cfg.App)
	log.Info().Str("env", cfg.App.Env).Int("port", cfg.HTTP.Port).Msg("starting service")

	pool, err := database.New(ctx, cfg.Database, log)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Infrastructure services.
	hasher := security.NewPasswordHasher(security.DefaultArgon2idParams)
	tokens := security.NewTokenIssuer(cfg.JWT)

	// Repositories.
	users := pgrepo.NewUserRepository(pool)

	// Use-cases.
	authUC := usecase.NewAuthUseCase(users, hasher, tokens, log)

	// Handlers.
	handlers := server.Handlers{
		Auth:   handler.NewAuthHandler(authUC),
		Health: handler.NewHealthHandler(cfg.App, pool),
	}

	app := server.New(cfg, log)
	server.Register(app, handlers, tokens)

	addr := fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port)

	// Run the server in the background and wait for a shutdown signal.
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
		return err
	}

	// Allow outstanding work a last chance to drain.
	time.Sleep(100 * time.Millisecond)
	log.Info().Msg("shutdown complete")
	return nil
}
