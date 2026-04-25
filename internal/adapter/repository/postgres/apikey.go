package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dedeez14/goforge/internal/domain/apikey"
	"github.com/dedeez14/goforge/pkg/errs"
)

const (
	qAPIKeyInsert = `
INSERT INTO api_keys (
    id, prefix, hash, name, user_id, tenant_id, scopes,
    expires_at, created_at, updated_at, created_by, updated_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW(), $9, $9
)`
	qAPIKeySelectActive = `
SELECT id, prefix, hash, name, user_id, tenant_id, scopes,
       expires_at, last_used_at, revoked_at, created_at, updated_at
FROM api_keys
WHERE prefix = $1 AND deleted_at IS NULL`
	qAPIKeySelectByID = `
SELECT id, prefix, hash, name, user_id, tenant_id, scopes,
       expires_at, last_used_at, revoked_at, created_at, updated_at
FROM api_keys
WHERE id = $1 AND deleted_at IS NULL`
	qAPIKeyListByUser = `
SELECT id, prefix, hash, name, user_id, tenant_id, scopes,
       expires_at, last_used_at, revoked_at, created_at, updated_at
FROM api_keys
WHERE user_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC`
	qAPIKeyRevoke = `
UPDATE api_keys
SET revoked_at = $3, updated_at = NOW(), updated_by = $2
WHERE id = $1 AND revoked_at IS NULL AND deleted_at IS NULL`
	qAPIKeyTouchLastUsed = `
UPDATE api_keys
SET last_used_at = $2
WHERE id = $1 AND deleted_at IS NULL`
)

// APIKeyRepository is the pgx-backed implementation of apikey.Repo.
type APIKeyRepository struct {
	pool *pgxpool.Pool
}

// NewAPIKeyRepository wires the repo to the pool.
func NewAPIKeyRepository(pool *pgxpool.Pool) *APIKeyRepository {
	return &APIKeyRepository{pool: pool}
}

// Create stores k. The caller is responsible for setting Hash and
// Prefix; if ID is the zero UUID, a new one is allocated.
func (r *APIKeyRepository) Create(ctx context.Context, k *apikey.Key) error {
	if k.ID == uuid.Nil {
		k.ID = uuid.New()
	}
	if k.Scopes == nil {
		k.Scopes = []string{}
	}
	_, err := r.pool.Exec(ctx, qAPIKeyInsert,
		k.ID, k.Prefix, k.Hash, k.Name,
		actorOrNil(k.UserID), actorOrNil(k.TenantID), k.Scopes,
		k.ExpiresAt, actorOrNil(k.CreatedBy),
	)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "apikey.create", "failed to create api key", err)
	}
	return nil
}

// GetByPrefix returns the key referenced by its public prefix.
func (r *APIKeyRepository) GetByPrefix(ctx context.Context, prefix string) (*apikey.Key, error) {
	row := r.pool.QueryRow(ctx, qAPIKeySelectActive, prefix)
	return scanAPIKey(row)
}

// GetByID returns the key by its primary key.
func (r *APIKeyRepository) GetByID(ctx context.Context, id uuid.UUID) (*apikey.Key, error) {
	return scanAPIKey(r.pool.QueryRow(ctx, qAPIKeySelectByID, id))
}

// ListByUser returns every key (active or revoked) belonging to userID.
func (r *APIKeyRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]*apikey.Key, error) {
	rows, err := r.pool.Query(ctx, qAPIKeyListByUser, userID)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "apikey.list", "failed to list api keys", err)
	}
	defer rows.Close()
	var out []*apikey.Key
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(errs.KindInternal, "apikey.list_iter", "iterating api keys failed", err)
	}
	return out, nil
}

// Revoke marks id as revoked at time at; idempotent if already revoked.
func (r *APIKeyRepository) Revoke(ctx context.Context, id uuid.UUID, by *uuid.UUID, at time.Time) error {
	tag, err := r.pool.Exec(ctx, qAPIKeyRevoke, id, actorOrNil(by), at)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "apikey.revoke", "failed to revoke api key", err)
	}
	if tag.RowsAffected() == 0 {
		return apikey.ErrNotFound
	}
	return nil
}

// UpdateLastUsed bumps last_used_at to at without contention on
// other columns. Best-effort; errors are surfaced to the caller
// who chooses whether to log them.
func (r *APIKeyRepository) UpdateLastUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	if _, err := r.pool.Exec(ctx, qAPIKeyTouchLastUsed, id, at); err != nil {
		return errs.Wrap(errs.KindInternal, "apikey.touch", "failed to update api key last_used_at", err)
	}
	return nil
}

func scanAPIKey(row rowScanner) (*apikey.Key, error) {
	var k apikey.Key
	var userID, tenantID *uuid.UUID
	err := row.Scan(
		&k.ID, &k.Prefix, &k.Hash, &k.Name,
		&userID, &tenantID, &k.Scopes,
		&k.ExpiresAt, &k.LastUsedAt, &k.RevokedAt,
		&k.CreatedAt, &k.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apikey.ErrNotFound
		}
		return nil, errs.Wrap(errs.KindInternal, "apikey.scan", "failed to read api key", err)
	}
	k.UserID = userID
	k.TenantID = tenantID
	return &k, nil
}
