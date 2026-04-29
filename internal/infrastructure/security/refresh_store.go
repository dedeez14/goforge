package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RefreshStore tracks issued refresh tokens so the application can
// enforce single-use semantics (rotation). The database stores a hash
// of the JTI, never the raw token, so a DB read alone cannot be used
// to mint replays.
//
// The sessionID parameter on Save ties each token to a sessions row,
// letting the use-case cascade-revoke tokens when the user kills a
// device. Pass uuid.Nil for flows that are not session-bound (the
// non-interactive API-key exchange, say).
type RefreshStore interface {
	// Save persists a freshly-issued token. Called immediately
	// after the JWT is signed by the issuer.
	Save(ctx context.Context, jti string, userID, sessionID uuid.UUID, expiresAt time.Time) error
	// Use atomically marks jti as used. It returns ErrUnknownToken
	// when the JTI was never issued (or already cleaned up) and
	// ErrTokenReused when the JTI has already been consumed or
	// revoked. On reuse, the caller is expected to revoke every
	// remaining refresh token for the user. sessionID is returned
	// so the caller can Touch the owning session without an extra
	// DB round-trip; it is uuid.Nil for tokens saved without a
	// session binding (legacy or non-interactive flows).
	Use(ctx context.Context, jti string) (userID, sessionID uuid.UUID, err error)
	// LinkReplacement records that newJTI is the rotation of jti.
	LinkReplacement(ctx context.Context, jti, newJTI string) error
	// RevokeAllForUser kills every non-revoked, non-used token for
	// the user. Called when reuse is detected.
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
	// Sweep removes records that have already expired.
	Sweep(ctx context.Context) (int64, error)
}

// ErrUnknownToken signals that the supplied refresh token was never
// recorded by the store. The HTTP layer should map this to 401.
var ErrUnknownToken = errors.New("security: refresh token not found")

// ErrTokenReused signals that the refresh token has already been
// consumed or revoked. The HTTP layer should map this to 401 and the
// use-case should revoke the user's remaining tokens to contain a
// possible compromise.
var ErrTokenReused = errors.New("security: refresh token reuse detected")

// HashJTI returns a stable hex-encoded SHA-256 of the JTI. The store
// indexes rows by this hash; the raw JTI is never written to disk.
func HashJTI(jti string) string {
	sum := sha256.Sum256([]byte(jti))
	return hex.EncodeToString(sum[:])
}

// PostgresRefreshStore is the production implementation of RefreshStore.
type PostgresRefreshStore struct{ pool *pgxpool.Pool }

// NewPostgresRefreshStore wraps a pgx pool into a RefreshStore.
func NewPostgresRefreshStore(pool *pgxpool.Pool) *PostgresRefreshStore {
	return &PostgresRefreshStore{pool: pool}
}

// Save implements RefreshStore. sessionID may be uuid.Nil for flows
// that are not session-bound; in that case the row stores SQL NULL
// in session_id rather than the zero UUID, so future joins against
// sessions stay correct.
func (s *PostgresRefreshStore) Save(ctx context.Context, jti string, userID, sessionID uuid.UUID, expiresAt time.Time) error {
	const q = `INSERT INTO refresh_tokens (jti_hash, user_id, session_id, expires_at) VALUES ($1, $2, $3, $4)`
	var sid any
	if sessionID != uuid.Nil {
		sid = sessionID
	}
	_, err := s.pool.Exec(ctx, q, HashJTI(jti), userID, sid, expiresAt)
	return err
}

// Use implements RefreshStore. The UPDATE is atomic: only one
// concurrent caller can set used_at; the others see RowsAffected() == 0
// and receive ErrTokenReused. sessionID is the id of the session the
// token belongs to, or uuid.Nil when the row was saved without one.
func (s *PostgresRefreshStore) Use(ctx context.Context, jti string) (uuid.UUID, uuid.UUID, error) {
	hash := HashJTI(jti)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID uuid.UUID
	var sessionID *uuid.UUID
	var usedAt, revokedAt *time.Time
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT user_id, session_id, used_at, revoked_at, expires_at FROM refresh_tokens WHERE jti_hash = $1 FOR UPDATE`, hash).
		Scan(&userID, &sessionID, &usedAt, &revokedAt, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, ErrUnknownToken
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	sid := uuid.Nil
	if sessionID != nil {
		sid = *sessionID
	}
	if revokedAt != nil || usedAt != nil {
		return userID, sid, ErrTokenReused
	}
	if time.Now().UTC().After(expiresAt) {
		return userID, sid, ErrTokenReused
	}
	if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET used_at = now() WHERE jti_hash = $1`, hash); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return userID, sid, nil
}

// LinkReplacement implements RefreshStore.
func (s *PostgresRefreshStore) LinkReplacement(ctx context.Context, jti, newJTI string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET replaced_by = $2 WHERE jti_hash = $1`,
		HashJTI(jti), HashJTI(newJTI),
	)
	return err
}

// RevokeAllForUser implements RefreshStore.
func (s *PostgresRefreshStore) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL AND used_at IS NULL`,
		userID,
	)
	return err
}

// Sweep implements RefreshStore.
func (s *PostgresRefreshStore) Sweep(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE expires_at < now() - INTERVAL '7 days'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// MemoryRefreshStore is an in-memory RefreshStore used by tests and
// single-process local development. It is concurrency-safe but does
// not survive a process restart.
type MemoryRefreshStore struct {
	mu      memMu
	records map[string]*memRecord
}

type memRecord struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	ExpiresAt time.Time
	UsedAt    *time.Time
	Revoked   bool
}

// NewMemoryRefreshStore returns a fresh in-memory store.
func NewMemoryRefreshStore() *MemoryRefreshStore {
	return &MemoryRefreshStore{records: make(map[string]*memRecord)}
}

// Save implements RefreshStore.
func (s *MemoryRefreshStore) Save(_ context.Context, jti string, userID, sessionID uuid.UUID, expiresAt time.Time) error {
	s.mu.lock()
	defer s.mu.unlock()
	s.records[HashJTI(jti)] = &memRecord{UserID: userID, SessionID: sessionID, ExpiresAt: expiresAt}
	return nil
}

// Use implements RefreshStore.
func (s *MemoryRefreshStore) Use(_ context.Context, jti string) (uuid.UUID, uuid.UUID, error) {
	s.mu.lock()
	defer s.mu.unlock()
	rec, ok := s.records[HashJTI(jti)]
	if !ok {
		return uuid.Nil, uuid.Nil, ErrUnknownToken
	}
	if rec.Revoked || rec.UsedAt != nil {
		return rec.UserID, rec.SessionID, ErrTokenReused
	}
	if time.Now().UTC().After(rec.ExpiresAt) {
		return rec.UserID, rec.SessionID, ErrTokenReused
	}
	now := time.Now().UTC()
	rec.UsedAt = &now
	return rec.UserID, rec.SessionID, nil
}

// LinkReplacement implements RefreshStore (no-op for tests).
func (s *MemoryRefreshStore) LinkReplacement(_ context.Context, _, _ string) error { return nil }

// RevokeAllForUser implements RefreshStore.
func (s *MemoryRefreshStore) RevokeAllForUser(_ context.Context, userID uuid.UUID) error {
	s.mu.lock()
	defer s.mu.unlock()
	for _, rec := range s.records {
		if rec.UserID == userID && !rec.Revoked && rec.UsedAt == nil {
			rec.Revoked = true
		}
	}
	return nil
}

// Sweep implements RefreshStore.
func (s *MemoryRefreshStore) Sweep(_ context.Context) (int64, error) {
	s.mu.lock()
	defer s.mu.unlock()
	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)
	removed := int64(0)
	for k, rec := range s.records {
		if rec.ExpiresAt.Before(cutoff) {
			delete(s.records, k)
			removed++
		}
	}
	return removed, nil
}
