// Package session is the domain layer for authenticated user
// sessions. One row per "logged-in device"; the refresh-token chain
// tied to it is an implementation detail managed by the security
// package. This file declares the entity, repository contract, and
// canonical not-found sentinel.
package session

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/pkg/errs"
)

// Session is a user-visible login on a device. The UserAgent and IP
// are captured at login time so the "active devices" UI can render a
// recognisable label; both are best-effort hints, not security
// primitives.
type Session struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	UserAgent  string
	IP         string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
}

// IsActive reports whether the session is still usable at t - neither
// revoked nor expired.
func (s *Session) IsActive(t time.Time) bool {
	if s == nil {
		return false
	}
	if s.RevokedAt != nil {
		return false
	}
	return t.Before(s.ExpiresAt)
}

// ErrNotFound is the canonical sentinel for "no session matched".
// Pre-built *errs.Error so callers can return it straight to the
// HTTP layer, matching the user/menu/apikey convention.
var ErrNotFound = errs.NotFound("session.not_found", "session not found")

// Repo persists and retrieves sessions. The method set is narrow on
// purpose - sessions are created implicitly by the auth flow, and
// the only user-driven mutations are "revoke one" and "revoke all".
type Repo interface {
	Create(ctx context.Context, s *Session) error
	GetByID(ctx context.Context, id uuid.UUID) (*Session, error)
	ListByUser(ctx context.Context, userID uuid.UUID, activeOnly bool) ([]*Session, error)
	// Touch updates last_used_at and extends expires_at. Called on
	// every successful refresh-token rotation so the "active"
	// window tracks real usage rather than initial login time.
	Touch(ctx context.Context, id uuid.UUID, at time.Time, newExpiry time.Time) error
	// Revoke marks a single session as revoked. ownerID scopes the
	// update to the caller's own sessions so an authenticated user
	// cannot revoke somebody else's session just by knowing its
	// UUID. Returns ErrNotFound when no row matched.
	Revoke(ctx context.Context, id, ownerID uuid.UUID, at time.Time) error
	// RevokeAllForUser revokes every active session for the user
	// except ID==exceptID (which may be uuid.Nil to revoke
	// everything). Used by the "logout everywhere" action and by
	// auth.go when reuse detection fires. Returns the number of
	// rows actually revoked so the caller can report it.
	RevokeAllForUser(ctx context.Context, userID, exceptID uuid.UUID, at time.Time) (int64, error)
	// Sweep removes rows whose expires_at is far in the past so the
	// table does not grow unbounded.
	Sweep(ctx context.Context, before time.Time) (int64, error)
}
