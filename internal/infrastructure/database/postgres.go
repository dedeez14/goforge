// Package database wires PostgreSQL via pgx/v5 + pgxpool with sensible,
// performance-oriented defaults. The pool is the only framework-wide
// database handle; repositories receive *pgxpool.Pool directly and
// expose their own domain interfaces for test substitution.
package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/dedeez14/goforge/internal/config"
)

// New constructs a connected *pgxpool.Pool.
func New(ctx context.Context, cfg config.Database, log zerolog.Logger) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("database: parse dsn: %w", err)
	}

	if cfg.MinConns > 0 {
		pcfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConns > 0 {
		pcfg.MaxConns = cfg.MaxConns
	}
	if cfg.MaxConnLifetime > 0 {
		pcfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		pcfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.ConnectTimeout > 0 {
		pcfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	}
	if !cfg.StatementCache {
		// Useful behind PgBouncer (transaction mode) or when queries are
		// almost never repeated with the same text.
		pcfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	}

	pcfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("database: connect: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: ping: %w", err)
	}

	log.Info().
		Int32("min_conns", pcfg.MinConns).
		Int32("max_conns", pcfg.MaxConns).
		Msg("postgres connected")

	return pool, nil
}
