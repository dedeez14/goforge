package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dedeez14/goforge/internal/domain/session"
	"github.com/dedeez14/goforge/pkg/errs"
)

const (
	qSessionInsert = `
INSERT INTO sessions (id, user_id, user_agent, ip, created_at, last_used_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $5, $6)`

	qSessionSelectByID = `
SELECT id, user_id, user_agent, ip, created_at, last_used_at, expires_at, revoked_at
FROM sessions
WHERE id = $1`

	qSessionListByUser = `
SELECT id, user_id, user_agent, ip, created_at, last_used_at, expires_at, revoked_at
FROM sessions
WHERE user_id = $1
ORDER BY last_used_at DESC`

	qSessionListActiveByUser = `
SELECT id, user_id, user_agent, ip, created_at, last_used_at, expires_at, revoked_at
FROM sessions
WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > $2
ORDER BY last_used_at DESC`

	qSessionTouch = `
UPDATE sessions
SET last_used_at = $2, expires_at = $3
WHERE id = $1 AND revoked_at IS NULL`

	// Ownership-scoped - an authenticated user must not be able to
	// revoke somebody else's session just by knowing its UUID. Same
	// IDOR defence as apikey.Revoke.
	qSessionRevoke = `
UPDATE sessions
SET revoked_at = $3
WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`

	// Revoke every active session for the user except exceptID.
	// Passing uuid.Nil for exceptID revokes everything.
	qSessionRevokeAll = `
UPDATE sessions
SET revoked_at = $3
WHERE user_id = $1 AND id <> $2 AND revoked_at IS NULL`

	// Cascade: every refresh token tied to the revoked session must
	// also be invalidated so /refresh cannot resurrect a killed
	// device. We set revoked_at rather than delete to preserve the
	// forensic trail.
	qSessionRevokeTokens = `
UPDATE refresh_tokens
SET revoked_at = $2
WHERE session_id = $1 AND revoked_at IS NULL AND used_at IS NULL`

	qSessionSweep = `DELETE FROM sessions WHERE expires_at < $1`
)

// SessionRepository is the pgx-backed implementation of session.Repo.
type SessionRepository struct {
	pool *pgxpool.Pool
}

// NewSessionRepository wires the repo to the pool.
func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

// Create inserts s. If ID is zero, a new UUID is allocated so the
// caller (the auth use-case) receives the id it needs to stamp into
// the JWT claims.
func (r *SessionRepository) Create(ctx context.Context, s *session.Session) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	if s.LastUsedAt.IsZero() {
		s.LastUsedAt = s.CreatedAt
	}
	_, err := r.pool.Exec(ctx, qSessionInsert,
		s.ID, s.UserID, s.UserAgent, s.IP, s.CreatedAt, s.ExpiresAt,
	)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "session.create", "failed to create session", err)
	}
	return nil
}

// GetByID returns a single session or session.ErrNotFound.
func (r *SessionRepository) GetByID(ctx context.Context, id uuid.UUID) (*session.Session, error) {
	row := r.pool.QueryRow(ctx, qSessionSelectByID, id)
	return scanSession(row)
}

// ListByUser returns sessions belonging to userID. When activeOnly is
// true, revoked or expired rows are filtered server-side.
func (r *SessionRepository) ListByUser(ctx context.Context, userID uuid.UUID, activeOnly bool) ([]*session.Session, error) {
	var rows pgx.Rows
	var err error
	if activeOnly {
		rows, err = r.pool.Query(ctx, qSessionListActiveByUser, userID, time.Now().UTC())
	} else {
		rows, err = r.pool.Query(ctx, qSessionListByUser, userID)
	}
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "session.list", "failed to list sessions", err)
	}
	defer rows.Close()
	var out []*session.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(errs.KindInternal, "session.list_iter", "iterating sessions failed", err)
	}
	return out, nil
}

// Touch extends the session's activity window.
func (r *SessionRepository) Touch(ctx context.Context, id uuid.UUID, at, newExpiry time.Time) error {
	_, err := r.pool.Exec(ctx, qSessionTouch, id, at, newExpiry)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "session.touch", "failed to touch session", err)
	}
	return nil
}

// Revoke marks a single owned session as revoked. Also cascades to
// refresh_tokens bound to it so /refresh cannot resurrect the device.
// The two updates run in a single transaction because a successful
// session revoke with a stale token chain is worse than a failure.
func (r *SessionRepository) Revoke(ctx context.Context, id, ownerID uuid.UUID, at time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "session.revoke_tx", "failed to open tx", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, qSessionRevoke, id, ownerID, at)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "session.revoke", "failed to revoke session", err)
	}
	if tag.RowsAffected() == 0 {
		return session.ErrNotFound
	}
	if _, err := tx.Exec(ctx, qSessionRevokeTokens, id, at); err != nil {
		return errs.Wrap(errs.KindInternal, "session.revoke_tokens", "failed to revoke session tokens", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return errs.Wrap(errs.KindInternal, "session.revoke_commit", "failed to commit revoke", err)
	}
	return nil
}

// RevokeAllForUser invalidates every active session for userID except
// exceptID (pass uuid.Nil to revoke everything). Returns the count so
// the "logout everywhere" endpoint can report how many devices were
// kicked. Runs in a single transaction so the token cascade stays
// consistent with the session revoke.
func (r *SessionRepository) RevokeAllForUser(ctx context.Context, userID, exceptID uuid.UUID, at time.Time) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, errs.Wrap(errs.KindInternal, "session.revoke_all_tx", "failed to open tx", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, qSessionRevokeAll, userID, exceptID, at)
	if err != nil {
		return 0, errs.Wrap(errs.KindInternal, "session.revoke_all", "failed to revoke sessions", err)
	}
	// Cascade refresh tokens for every session we just revoked.
	// A join keeps this one round-trip.
	const qCascade = `
UPDATE refresh_tokens SET revoked_at = $3
WHERE session_id IN (
    SELECT id FROM sessions
    WHERE user_id = $1 AND id <> $2 AND revoked_at = $3
) AND revoked_at IS NULL AND used_at IS NULL`
	if _, err := tx.Exec(ctx, qCascade, userID, exceptID, at); err != nil {
		return 0, errs.Wrap(errs.KindInternal, "session.revoke_all_tokens", "failed to cascade revoke", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, errs.Wrap(errs.KindInternal, "session.revoke_all_commit", "failed to commit revoke-all", err)
	}
	return tag.RowsAffected(), nil
}

// Sweep drops rows whose expires_at is older than before. Called
// periodically by a background job to keep the table small.
func (r *SessionRepository) Sweep(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, qSessionSweep, before)
	if err != nil {
		return 0, errs.Wrap(errs.KindInternal, "session.sweep", "failed to sweep sessions", err)
	}
	return tag.RowsAffected(), nil
}

func scanSession(r rowScanner) (*session.Session, error) {
	var s session.Session
	var revokedAt *time.Time
	err := r.Scan(
		&s.ID, &s.UserID, &s.UserAgent, &s.IP,
		&s.CreatedAt, &s.LastUsedAt, &s.ExpiresAt, &revokedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "session.scan", "failed to scan session", err)
	}
	s.RevokedAt = revokedAt
	return &s, nil
}
