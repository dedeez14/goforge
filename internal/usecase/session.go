package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/domain/session"
	"github.com/dedeez14/goforge/pkg/errs"
)

// SessionUseCase exposes the read/revoke side of the session store
// to the HTTP layer. Creation is internal - sessions are minted by
// AuthUseCase during register/login/refresh, never directly by the
// user. This keeps the invariant that a session row always has a
// matching refresh-token chain.
//
// The repo's Revoke / RevokeAllForUser methods cascade to the
// refresh_tokens table inside the same transaction, so the use case
// itself does not need to hold a separate RefreshStore - one less
// collaborator to inject and one less moving part to keep in sync.
type SessionUseCase struct {
	repo  session.Repo
	clock func() time.Time
}

// NewSessionUseCase wires the use case to its repo. The repo is
// required; passing nil is treated as a permanent disabled state and
// every method short-circuits.
func NewSessionUseCase(repo session.Repo) *SessionUseCase {
	return &SessionUseCase{
		repo:  repo,
		clock: time.Now,
	}
}

// List returns every active session belonging to userID, newest
// first. Revoked or expired rows are filtered at the repo level so
// the response renders directly in "active devices" UI.
func (uc *SessionUseCase) List(ctx context.Context, userID uuid.UUID) ([]*session.Session, error) {
	if uc.repo == nil {
		return nil, nil
	}
	return uc.repo.ListByUser(ctx, userID, true)
}

// Revoke invalidates one owned session. currentSessionID is the
// session the caller's access token belongs to; revoking it is
// allowed (the caller's next /refresh will fail with auth.invalid,
// as expected).
func (uc *SessionUseCase) Revoke(ctx context.Context, id, ownerID uuid.UUID) error {
	if uc.repo == nil {
		return errs.Forbidden("session.disabled", "session management is not enabled")
	}
	if err := uc.repo.Revoke(ctx, id, ownerID, uc.clock().UTC()); err != nil {
		return err
	}
	return nil
}

// RevokeAllExceptCurrent is the "logout everywhere" action. It
// keeps the caller's own session alive (the one whose id the caller
// presented via the access-token's sid claim) and revokes every
// other active session for the user. Returns the count of revoked
// rows so the handler can echo it to the client.
//
// Pass uuid.Nil for currentSessionID to logout every device
// including the caller's.
func (uc *SessionUseCase) RevokeAllExceptCurrent(ctx context.Context, userID, currentSessionID uuid.UUID) (int64, error) {
	if uc.repo == nil {
		return 0, errs.Forbidden("session.disabled", "session management is not enabled")
	}
	return uc.repo.RevokeAllForUser(ctx, userID, currentSessionID, uc.clock().UTC())
}
