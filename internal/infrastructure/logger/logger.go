// Package logger configures a zero-allocation zerolog logger.
//
// The logger is structured (JSON by default) in production and pretty
// in development. Context helpers let handlers attach per-request
// fields without allocating when logging is disabled.
package logger

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"

	"github.com/dedeez14/goforge/internal/config"
)

// New builds a zerolog.Logger tuned for the given configuration.
func New(cfg config.Log, app config.App) zerolog.Logger {
	level, err := zerolog.ParseLevel(cfg.Level)
	if err != nil {
		level = zerolog.InfoLevel
	}

	var out io.Writer = os.Stdout
	if cfg.Pretty {
		out = zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339,
		}
	}

	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.DefaultContextLogger = nil

	return zerolog.New(out).
		Level(level).
		With().
		Timestamp().
		Str("service", app.Name).
		Str("env", app.Env).
		Str("version", app.Version).
		Logger()
}
