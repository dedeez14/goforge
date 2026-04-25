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

	"github.com/dedeez14/goforge/internal/domain/user"
	"github.com/dedeez14/goforge/internal/infrastructure/security"
	"github.com/dedeez14/goforge/pkg/errs"
)

// AuthUseCase exposes authentication flows. It is transport-agnostic and
// can be driven from HTTP, gRPC, CLI, or tests with equal ease.
type AuthUseCase struct {
	users    user.Repository
	hasher   security.PasswordHasher
	tokens   security.TokenIssuer
	refresh  security.RefreshStore
	dummyHsh string
	log      zerolog.Logger
}

// NewAuthUseCase constructs an AuthUseCase with its collaborators.
//
// refresh may be nil; when it is, refresh-token rotation is disabled
// and tokens behave as plain bearer JWTs (the legacy behaviour). All
// production deployments should pass a non-nil RefreshStore.
func NewAuthUseCase(
	users user.Repository,
	hasher security.PasswordHasher,
	tokens security.TokenIssuer,
	refresh security.RefreshStore,
	log zerolog.Logger,
) *AuthUseCase {
	uc := &AuthUseCase{
		users:   users,
		hasher:  hasher,
		tokens:  tokens,
		refresh: refresh,
		log:     log,
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

// Register creates a new user and returns an initial token pair.
func (uc *AuthUseCase) Register(ctx context.Context, in RegisterInput) (*user.User, *TokenPair, error) {
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

	tp, err := uc.issuePair(ctx, u.ID)
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
func (uc *AuthUseCase) Login(ctx context.Context, in LoginInput) (*user.User, *TokenPair, error) {
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

	tp, err := uc.issuePair(ctx, u.ID)
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

	if uc.refresh != nil {
		userID, err := uc.refresh.Use(ctx, claims.ID)
		switch {
		case errors.Is(err, security.ErrUnknownToken):
			return nil, errs.Unauthorized("auth.invalid", "invalid or revoked refresh token")
		case errors.Is(err, security.ErrTokenReused):
			// Reuse detection - someone replayed a token we already
			// rotated. Revoke every outstanding refresh token for
			// this user as a precaution.
			if rerr := uc.refresh.RevokeAllForUser(ctx, userID); rerr != nil {
				uc.log.Error().Err(rerr).Str("user_id", userID.String()).Msg("revoke-all failed after refresh reuse")
			}
			uc.log.Warn().Str("user_id", userID.String()).Msg("refresh token reuse detected; revoking all refresh tokens")
			return nil, errs.Unauthorized("auth.token_reused", "refresh token reuse detected; please log in again")
		case err != nil:
			return nil, errs.Wrap(errs.KindInternal, "auth.refresh_store", "refresh store error", err)
		}
	}

	tp, err := uc.issuePair(ctx, id)
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

func (uc *AuthUseCase) issuePair(ctx context.Context, id uuid.UUID) (*TokenPair, error) {
	access, exp, err := uc.tokens.Issue(id, security.TokenAccess)
	if err != nil {
		return nil, err
	}
	refresh, refreshExp, err := uc.tokens.Issue(id, security.TokenRefresh)
	if err != nil {
		return nil, err
	}
	if uc.refresh != nil {
		// Persist the refresh token so it can be rotated. Use the
		// JTI from the freshly-signed JWT. We just signed `refresh`
		// with the same key/algorithm we're about to verify it
		// with, so a Parse failure here is almost impossible — but
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
		if serr := uc.refresh.Save(ctx, claims.ID, id, refreshExp); serr != nil {
			return nil, errs.Wrap(errs.KindInternal, "auth.refresh_store", "persist refresh token", serr)
		}
	}
	return &TokenPair{AccessToken: access, RefreshToken: refresh, AccessExpiry: exp}, nil
}

func normaliseEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
