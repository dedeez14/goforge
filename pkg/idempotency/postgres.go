package idempotency

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore is a durable Idempotency-Key store backed by a single
// table. It is safe to share across replicas because uniqueness is
// enforced by the database, not the process.
//
// The expected schema is:
//
//	CREATE TABLE idempotency_keys (
//	    key            TEXT PRIMARY KEY,
//	    method         TEXT NOT NULL,
//	    path           TEXT NOT NULL,
//	    request_hash   TEXT NOT NULL,
//	    status_code    INT  NOT NULL,
//	    content_type   TEXT NOT NULL,
//	    body           BYTEA NOT NULL,
//	    created_at     TIMESTAMPTZ NOT NULL,
//	    expires_at     TIMESTAMPTZ NOT NULL
//	);
//	CREATE INDEX idx_idempotency_keys_expires_at ON idempotency_keys(expires_at);
//
// The framework ships this migration in the idempotency module so
// applications get it automatically when they enable the module.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore returns a store that writes to the given pool.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

// Lookup returns the record for key. Expired records are skipped via
// the WHERE clause so callers never see stale data.
func (s *PostgresStore) Lookup(ctx context.Context, key string) (*Record, error) {
	const q = `
		SELECT key, method, path, request_hash, status_code, content_type, body, created_at, expires_at
		FROM idempotency_keys
		WHERE key = $1 AND expires_at > now()
	`
	row := s.pool.QueryRow(ctx, q, key)
	var (
		rec              Record
		createdAt, expAt time.Time
	)
	err := row.Scan(
		&rec.Key, &rec.Method, &rec.Path, &rec.RequestHash,
		&rec.StatusCode, &rec.ContentType, &rec.Body, &createdAt, &expAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	rec.CreatedAt = createdAt
	rec.ExpiresAt = expAt
	return &rec, nil
}

// Save persists rec. Concurrent writes for the same key collapse onto
// the row created by the first writer; subsequent writers receive
// ErrConflict so the middleware can return 409.
func (s *PostgresStore) Save(ctx context.Context, rec *Record) error {
	const q = `
		INSERT INTO idempotency_keys
			(key, method, path, request_hash, status_code, content_type, body, created_at, expires_at)
		VALUES
			($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (key) DO NOTHING
	`
	tag, err := s.pool.Exec(ctx, q,
		rec.Key, rec.Method, rec.Path, rec.RequestHash,
		rec.StatusCode, rec.ContentType, rec.Body,
		rec.CreatedAt, rec.ExpiresAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrConflict
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

// Sweep deletes records whose ExpiresAt is in the past. Call this
// periodically from a worker; the framework's idempotency module wires
// it onto a 5-minute ticker by default.
func (s *PostgresStore) Sweep(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
