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
	"github.com/dedeez14/goforge/pkg/db"
)

// New constructs a connected *pgxpool.Pool against cfg.DSN. It is the
// single-primary constructor used by tools (migrations, CLI) that do
// not care about read-replica routing.
func New(ctx context.Context, cfg config.Database, log zerolog.Logger) (*pgxpool.Pool, error) {
	return newPool(ctx, cfg.DSN, cfg.MinConns, cfg.MaxConns, cfg, log, "primary")
}

// NewRouter builds the read-replica-aware pool graph. When
// cfg.ReplicaDSN is empty the router carries only the primary pool
// and every Read() call transparently hits the primary - the app can
// be written replica-aware from day one and operationally promoted
// later by setting the DSN.
func NewRouter(ctx context.Context, cfg config.Database, log zerolog.Logger) (*db.Router, error) {
	primary, err := newPool(ctx, cfg.DSN, cfg.MinConns, cfg.MaxConns, cfg, log, "primary")
	if err != nil {
		return nil, err
	}

	if cfg.ReplicaDSN == "" {
		return db.NewRouter(primary, nil), nil
	}

	replica, err := newPool(ctx, cfg.ReplicaDSN, cfg.ReplicaMinConns, cfg.ReplicaMaxConns, cfg, log, "replica")
	if err != nil {
		// Tear the primary back down so we don't leak a pool on a
		// half-successful startup. The caller will surface the
		// error and Run will exit.
		primary.Close()
		return nil, err
	}
	return db.NewRouter(primary, replica), nil
}

// newPool is the shared configuration path for both primary and
// replica pools. The role parameter is used only for the "connected"
// log line so operators can tell the two roles apart.
func newPool(ctx context.Context, dsn string, minConns, maxConns int32, cfg config.Database, log zerolog.Logger, role string) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("database: parse %s dsn: %w", role, err)
	}

	if minConns > 0 {
		pcfg.MinConns = minConns
	}
	if maxConns > 0 {
		pcfg.MaxConns = maxConns
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
		// almost never repeated with the same text. The simple query
		// protocol avoids the server-side prepared-statement cache
		// that is incompatible with PgBouncer pool_mode=transaction.
		pcfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		log.Info().
			Str("role", role).
			Msg("postgres: statement cache disabled (simple query protocol; PgBouncer-compatible)")
	} else if isLikelyPgBouncer(pcfg.ConnConfig.Port) {
		// StatementCache=true AND the DSN points at the conventional
		// PgBouncer port. Either the config is wrong or the operator
		// is running an unusual topology - either way they want a
		// loud warning, not a silent 'prepared statement does not
		// exist' error mid-request six hours from now.
		log.Warn().
			Str("role", role).
			Str("host", pcfg.ConnConfig.Host).
			Uint16("port", pcfg.ConnConfig.Port).
			Msg("postgres: statement_cache=true but DSN looks like PgBouncer (port 6432); expect prepared-statement errors under pool_mode=transaction. Set GOFORGE_DATABASE_STATEMENT_CACHE=false.")
	}

	pcfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("database: connect %s: %w", role, err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: ping %s: %w", role, err)
	}

	log.Info().
		Str("role", role).
		Int32("min_conns", pcfg.MinConns).
		Int32("max_conns", pcfg.MaxConns).
		Msg("postgres connected")

	return pool, nil
}

// isLikelyPgBouncer reports whether the connection target looks like
// PgBouncer based on the port. PgBouncer conventionally listens on
// 6432; Postgres on 5432. We only flag 6432 to avoid false positives
// against unusual topologies that happen to put Postgres on a
// non-standard port.
func isLikelyPgBouncer(port uint16) bool { return port == 6432 }
