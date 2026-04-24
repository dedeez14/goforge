package dto

import (
	"github.com/dedeez14/goforge/internal/domain/user"
	"github.com/dedeez14/goforge/internal/usecase"
)

// UserFromDomain converts a domain entity into its wire representation.
func UserFromDomain(u *user.User) UserResponse {
	return UserResponse{
		ID:        u.ID.String(),
		Email:     u.Email,
		Name:      u.Name,
		Role:      string(u.Role),
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

// TokensFromUseCase converts a use-case TokenPair into its wire representation.
func TokensFromUseCase(tp *usecase.TokenPair) TokenResponse {
	return TokenResponse{
		AccessToken:  tp.AccessToken,
		RefreshToken: tp.RefreshToken,
		TokenType:    "Bearer",
		ExpiresAt:    tp.AccessExpiry,
	}
}
