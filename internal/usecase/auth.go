// Package usecase contains the application's business logic.
//
// Use-cases orchestrate domain entities, repositories, and infrastructure
// services (via interfaces). They MUST NOT depend on HTTP, Fiber, or any
// other transport-level concern.
package usecase

import (
	"context"
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
	users  user.Repository
	hasher security.PasswordHasher
	tokens security.TokenIssuer
	log    zerolog.Logger
}

// NewAuthUseCase constructs an AuthUseCase with its collaborators.
func NewAuthUseCase(
	users user.Repository,
	hasher security.PasswordHasher,
	tokens security.TokenIssuer,
	log zerolog.Logger,
) *AuthUseCase {
	return &AuthUseCase{
		users:  users,
		hasher: hasher,
		tokens: tokens,
		log:    log,
	}
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

	tp, err := uc.issuePair(u.ID)
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
func (uc *AuthUseCase) Login(ctx context.Context, in LoginInput) (*user.User, *TokenPair, error) {
	u, err := uc.users.FindByEmail(ctx, normaliseEmail(in.Email))
	if err != nil {
		if errs.Is(err, errs.KindNotFound) {
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

	tp, err := uc.issuePair(u.ID)
	if err != nil {
		return nil, nil, err
	}
	return u, tp, nil
}

// Refresh validates a refresh token and mints a new access + refresh pair.
func (uc *AuthUseCase) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := uc.tokens.Parse(refreshToken)
	if err != nil {
		return nil, err
	}
	if claims.Kind != security.TokenRefresh {
		return nil, errs.Unauthorized("auth.wrong_token_kind", "refresh token required")
	}
	id, perr := uuid.Parse(claims.Subject)
	if perr != nil {
		return nil, errs.Unauthorized("auth.invalid_subject", "invalid token subject")
	}
	if _, err := uc.users.FindByID(ctx, id); err != nil {
		return nil, err
	}
	return uc.issuePair(id)
}

// Me returns the currently-authenticated user by id.
func (uc *AuthUseCase) Me(ctx context.Context, id uuid.UUID) (*user.User, error) {
	return uc.users.FindByID(ctx, id)
}

func (uc *AuthUseCase) issuePair(id uuid.UUID) (*TokenPair, error) {
	access, exp, err := uc.tokens.Issue(id, security.TokenAccess)
	if err != nil {
		return nil, err
	}
	refresh, _, err := uc.tokens.Issue(id, security.TokenRefresh)
	if err != nil {
		return nil, err
	}
	return &TokenPair{AccessToken: access, RefreshToken: refresh, AccessExpiry: exp}, nil
}

func normaliseEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
