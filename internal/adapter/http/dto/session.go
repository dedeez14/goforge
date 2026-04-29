package dto

import (
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/domain/session"
)

// SessionResponse is the JSON shape for a single user session in the
// "active devices" list. Current is set on exactly one row when the
// caller can be matched to it via the access-token's sid claim, so
// the UI can render a "this device" badge.
type SessionResponse struct {
	ID         uuid.UUID  `json:"id"`
	UserAgent  string     `json:"user_agent"`
	IP         string     `json:"ip"`
	Current    bool       `json:"current"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt time.Time  `json:"last_used_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// SessionFromDomain converts a domain Session into the public
// JSON shape. currentID is the caller's own session id so the
// rendered list can flag it; pass uuid.Nil to flag none.
func SessionFromDomain(s *session.Session, currentID uuid.UUID) SessionResponse {
	return SessionResponse{
		ID:         s.ID,
		UserAgent:  s.UserAgent,
		IP:         s.IP,
		Current:    s.ID == currentID && currentID != uuid.Nil,
		CreatedAt:  s.CreatedAt,
		LastUsedAt: s.LastUsedAt,
		ExpiresAt:  s.ExpiresAt,
		RevokedAt:  s.RevokedAt,
	}
}

// SessionsFromDomain converts a slice with the current-flag wired
// through.
func SessionsFromDomain(in []*session.Session, currentID uuid.UUID) []SessionResponse {
	out := make([]SessionResponse, 0, len(in))
	for _, s := range in {
		out = append(out, SessionFromDomain(s, currentID))
	}
	return out
}

// RevokeAllSessionsResponse is returned by DELETE /me/sessions; it
// reports how many devices were kicked so the UI can confirm the
// "logout everywhere" succeeded with a non-zero count.
type RevokeAllSessionsResponse struct {
	Revoked int64 `json:"revoked"`
}
