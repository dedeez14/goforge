// Package usecase contains the application's business logic.
//
// Use-cases orchestrate domain entities, repositories, and infrastructure
// services (via interfaces). They MUST NOT depend on HTTP, Fiber, or any
// other transport-level concern.
package usecase

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/dedeez14/goforge/internal/domain/session"
	"github.com/dedeez14/goforge/internal/domain/user"
	"github.com/dedeez14/goforge/internal/infrastructure/security"
	"github.com/dedeez14/goforge/pkg/errs"
)

// AuthUseCase exposes authentication flows. It is transport-agnostic and
// can be driven from HTTP, gRPC, CLI, or tests with equal ease.
type AuthUseCase struct {
	users      user.Repository
	hasher     security.PasswordHasher
	tokens     security.TokenIssuer
	refresh    security.RefreshStore
	sessions   session.Repo
	refreshTTL time.Duration
	dummyHsh   string
	log        zerolog.Logger
}

// NewAuthUseCase constructs an AuthUseCase with its collaborators.
//
// refresh may be nil; when it is, refresh-token rotation is disabled
// and tokens behave as plain bearer JWTs (the legacy behaviour). All
// production deployments should pass a non-nil RefreshStore.
//
// sessions may also be nil; when it is, the use-case skips session
// row management entirely (tokens still rotate via the RefreshStore
// but /me/sessions returns an empty list). Production deployments
// that want the self-service "active devices" UI must pass both a
// non-nil RefreshStore and a non-nil session.Repo.
//
// refreshTTL is the wall-clock lifetime of a freshly-issued refresh
// token; the session's expires_at is set to login time + refreshTTL
// and extended on every rotation. Pass 0 to default to 24 hours -
// long enough for an active user to hit /refresh at least once,
// short enough that a stale device rolls off the list on its own.
func NewAuthUseCase(
	users user.Repository,
	hasher security.PasswordHasher,
	tokens security.TokenIssuer,
	refresh security.RefreshStore,
	sessions session.Repo,
	refreshTTL time.Duration,
	log zerolog.Logger,
) *AuthUseCase {
	if refreshTTL <= 0 {
		refreshTTL = 24 * time.Hour
	}
	uc := &AuthUseCase{
		users:      users,
		hasher:     hasher,
		tokens:     tokens,
		refresh:    refresh,
		sessions:   sessions,
		refreshTTL: refreshTTL,
		log:        log,
	}
	// Pre-compute a dummy Argon2id hash once. Login() runs Verify
	// against this hash whenever the supplied email is unknown so the
	// response time matches a real verify and the endpoint cannot be
	// used to enumerate registered emails by timing.
	if h, err := hasher.Hash("goforge-dummy-password-for-timing-equalization"); err == nil {
		uc.dummyHsh = h
	}
	return uc
}

// RegisterInput is the transport-agnostic registration command.
type RegisterInput struct {
	Email    string
	Password string
	Name     string
}

// TokenPair is emitted on successful register/login/refresh.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	AccessExpiry time.Time
}

// SessionContext carries optional device hints captured at the HTTP
// boundary (User-Agent header, best-effort client IP) so the sessions
// table can render a recognisable label in the "active devices" UI.
// Both fields are informational only - they never gate authentication.
type SessionContext struct {
	UserAgent string
	IP        string
}

// Register creates a new user and returns an initial token pair.
func (uc *AuthUseCase) Register(ctx context.Context, in RegisterInput, sc SessionContext) (*user.User, *TokenPair, error) {
	email := normaliseEmail(in.Email)
	if _, err := uc.users.FindByEmail(ctx, email); err == nil {
		return nil, nil, user.ErrEmailTaken
	} else if !errs.Is(err, errs.KindNotFound) {
		return nil, nil, err
	}

	hash, err := uc.hasher.Hash(in.Password)
	if err != nil {
		return nil, nil, errs.Wrap(errs.KindInternal, "auth.hash", "failed to secure password", err)
	}

	now := time.Now().UTC()
	u := &user.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: hash,
		Name:         strings.TrimSpace(in.Name),
		Role:         user.RoleUser,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := uc.users.Create(ctx, u); err != nil {
		return nil, nil, err
	}

	tp, err := uc.issuePair(ctx, u.ID, uuid.Nil, sc)
	if err != nil {
		return nil, nil, err
	}
	return u, tp, nil
}

// LoginInput is the transport-agnostic login command.
type LoginInput struct {
	Email    string
	Password string
}

// Login verifies credentials and returns a token pair on success.
//
// To prevent username enumeration via timing, when the email does not
// resolve to a user we still run a verify call against a pre-computed
// dummy hash so the response time is indistinguishable from a real
// verify against an existing user with the wrong password.
func (uc *AuthUseCase) Login(ctx context.Context, in LoginInput, sc SessionContext) (*user.User, *TokenPair, error) {
	u, err := uc.users.FindByEmail(ctx, normaliseEmail(in.Email))
	if err != nil {
		if errs.Is(err, errs.KindNotFound) {
			if uc.dummyHsh != "" {
				_, _, _ = uc.hasher.Verify(in.Password, uc.dummyHsh)
			}
			return nil, nil, user.ErrInvalidCreds
		}
		return nil, nil, err
	}

	ok, needsRehash, err := uc.hasher.Verify(in.Password, u.PasswordHash)
	if err != nil {
		return nil, nil, errs.Wrap(errs.KindInternal, "auth.verify", "failed to verify password", err)
	}
	if !ok {
		return nil, nil, user.ErrInvalidCreds
	}

	if needsRehash {
		if newHash, hErr := uc.hasher.Hash(in.Password); hErr == nil {
			if uErr := uc.users.UpdatePasswordHash(ctx, u.ID, newHash); uErr != nil {
				uc.log.Warn().Err(uErr).Str("user_id", u.ID.String()).Msg("rehash update failed")
			}
		}
	}

	tp, err := uc.issuePair(ctx, u.ID, uuid.Nil, sc)
	if err != nil {
		return nil, nil, err
	}
	return u, tp, nil
}

// Refresh validates a refresh token and mints a new access + refresh
// pair. If a RefreshStore is configured, refresh tokens are
// single-use: a successful call rotates the token, and a second
// attempt with the same token revokes every outstanding refresh token
// for the user (reuse-detection). This contains the blast radius of a
// stolen refresh token.
func (uc *AuthUseCase) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := uc.tokens.Parse(refreshToken)
	if err != nil {
		return nil, err
	}
	if claims.Kind != security.TokenRefresh {
		return nil, errs.Unauthorized("auth.refresh_token_required", "refresh token required")
	}
	id, perr := uuid.Parse(claims.Subject)
	if perr != nil {
		return nil, errs.Unauthorized("auth.invalid_subject", "invalid token subject")
	}
	if _, err := uc.users.FindByID(ctx, id); err != nil {
		return nil, err
	}

	var sessionID uuid.UUID
	if uc.refresh != nil {
		uid, sid, err := uc.refresh.Use(ctx, claims.ID)
		switch {
		case errors.Is(err, security.ErrUnknownToken):
			return nil, errs.Unauthorized("auth.invalid", "invalid or revoked refresh token")
		case errors.Is(err, security.ErrTokenReused):
			// Reuse detection - someone replayed a token we already
			// rotated. Revoke every outstanding refresh token for
			// this user as a precaution, and kill every session so
			// the attacker loses the ability to refresh from any
			// device.
			if rerr := uc.refresh.RevokeAllForUser(ctx, uid); rerr != nil {
				uc.log.Error().Err(rerr).Str("user_id", uid.String()).Msg("revoke-all failed after refresh reuse")
			}
			if uc.sessions != nil {
				if _, rerr := uc.sessions.RevokeAllForUser(ctx, uid, uuid.Nil, time.Now().UTC()); rerr != nil {
					uc.log.Error().Err(rerr).Str("user_id", uid.String()).Msg("session revoke-all failed after refresh reuse")
				}
			}
			uc.log.Warn().Str("user_id", uid.String()).Msg("refresh token reuse detected; revoking all refresh tokens")
			return nil, errs.Unauthorized("auth.token_reused", "refresh token reuse detected; please log in again")
		case err != nil:
			return nil, errs.Wrap(errs.KindInternal, "auth.refresh_store", "refresh store error", err)
		}
		sessionID = sid
		if sessionID == uuid.Nil && claims.SessionID != "" {
			// Fall back to the claim if the DB row was saved without
			// a session binding (pre-migration tokens): honour what
			// the client presented rather than creating a new session.
			if parsed, err := uuid.Parse(claims.SessionID); err == nil {
				sessionID = parsed
			}
		}
	}

	tp, err := uc.issuePair(ctx, id, sessionID, SessionContext{})
	if err != nil {
		return nil, err
	}

	if uc.refresh != nil {
		// Best-effort link old jti -> new jti for forensics.
		if newClaims, perr := uc.tokens.Parse(tp.RefreshToken); perr == nil {
			if lerr := uc.refresh.LinkReplacement(ctx, claims.ID, newClaims.ID); lerr != nil {
				uc.log.Debug().Err(lerr).Msg("link replacement failed")
			}
		}
	}
	return tp, nil
}

// Me returns the currently-authenticated user by id.
func (uc *AuthUseCase) Me(ctx context.Context, id uuid.UUID) (*user.User, error) {
	return uc.users.FindByID(ctx, id)
}

// issuePair mints a new access + refresh token pair. When sessionID
// is uuid.Nil the use-case creates a fresh sessions row using the
// supplied SessionContext (register / login path). When sessionID is
// non-nil the use-case reuses that session and just touches its
// last_used_at / expires_at (refresh path); sc is ignored in that
// case because the device hints were already captured at login.
func (uc *AuthUseCase) issuePair(ctx context.Context, userID, sessionID uuid.UUID, sc SessionContext) (*TokenPair, error) {
	now := time.Now().UTC()
	refreshExp := now.Add(uc.refreshTTL)

	// Create or touch the session row before minting the tokens so
	// the sid claim carries the right id and so an abandoned Save
	// after signing is impossible. If the refresh store is wired
	// without sessions (nil repo), we just skip the row and mint a
	// token without an sid.
	if uc.sessions != nil {
		if sessionID == uuid.Nil {
			s := &session.Session{
				ID:         uuid.New(),
				UserID:     userID,
				UserAgent:  truncateUA(sc.UserAgent),
				IP:         sc.IP,
				CreatedAt:  now,
				LastUsedAt: now,
				ExpiresAt:  refreshExp,
			}
			if err := uc.sessions.Create(ctx, s); err != nil {
				return nil, err
			}
			sessionID = s.ID
		} else {
			// Touch is best-effort: a vanished session (race with
			// revoke) is handled by the subsequent refresh-store
			// Use returning ErrTokenReused, so we don't abort here.
			if err := uc.sessions.Touch(ctx, sessionID, now, refreshExp); err != nil {
				uc.log.Warn().Err(err).Str("session_id", sessionID.String()).Msg("session touch failed")
			}
		}
	}

	access, exp, err := uc.tokens.IssueWithSession(userID, sessionID, security.TokenAccess)
	if err != nil {
		return nil, err
	}
	refresh, _, err := uc.tokens.IssueWithSession(userID, sessionID, security.TokenRefresh)
	if err != nil {
		return nil, err
	}
	if uc.refresh != nil {
		// Persist the refresh token so it can be rotated. Use the
		// JTI from the freshly-signed JWT. We just signed `refresh`
		// with the same key/algorithm we're about to verify it
		// with, so a Parse failure here is almost impossible - but
		// when it does happen we MUST refuse to return the token
		// pair: a successful return would hand the client a JWT
		// the RefreshStore has never heard of, and the very next
		// /refresh would 401 with auth.unknown_token, locking the
		// user out.
		claims, perr := uc.tokens.Parse(refresh)
		if perr != nil {
			return nil, errs.Wrap(errs.KindInternal, "auth.issue_pair",
				"failed to read JTI from freshly-issued refresh token", perr)
		}
		if serr := uc.refresh.Save(ctx, claims.ID, userID, sessionID, refreshExp); serr != nil {
			return nil, errs.Wrap(errs.KindInternal, "auth.refresh_store", "persist refresh token", serr)
		}
	}
	return &TokenPair{AccessToken: access, RefreshToken: refresh, AccessExpiry: exp}, nil
}

// truncateUA bounds the User-Agent we persist so a malicious client
// cannot blow up the sessions table with a megabyte-long header. The
// cap is generous (512 bytes covers every mainstream browser) but
// finite.
func truncateUA(s string) string {
	const maxUA = 512
	if len(s) > maxUA {
		return s[:maxUA]
	}
	return s
}

func normaliseEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
