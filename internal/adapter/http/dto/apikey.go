package dto

import (
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/domain/apikey"
)

// CreateAPIKeyRequest is the JSON shape accepted by
// POST /api/v1/api-keys.
type CreateAPIKeyRequest struct {
	Name      string     `json:"name" validate:"required,min=1,max=120"`
	Scopes    []string   `json:"scopes" validate:"dive,max=120"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// APIKeyResponse describes the public-visible fields of an API key.
// Notably it never contains the plaintext - only Prefix is shown so
// admins can identify keys.
type APIKeyResponse struct {
	ID         uuid.UUID  `json:"id"`
	Prefix     string     `json:"prefix"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	UserID     *uuid.UUID `json:"user_id,omitempty"`
	TenantID   *uuid.UUID `json:"tenant_id,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// CreateAPIKeyResponse augments APIKeyResponse with the plaintext
// secret. The handler returns this exactly once at creation time;
// the framework never stores or re-emits the plaintext.
type CreateAPIKeyResponse struct {
	APIKeyResponse
	Plaintext string `json:"plaintext"`
}

// APIKeyFromDomain converts an apikey.Key into its public DTO.
func APIKeyFromDomain(k *apikey.Key) APIKeyResponse {
	if k == nil {
		return APIKeyResponse{}
	}
	scopes := k.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return APIKeyResponse{
		ID:         k.ID,
		Prefix:     k.Prefix,
		Name:       k.Name,
		Scopes:     scopes,
		UserID:     k.UserID,
		TenantID:   k.TenantID,
		ExpiresAt:  k.ExpiresAt,
		LastUsedAt: k.LastUsedAt,
		RevokedAt:  k.RevokedAt,
		CreatedAt:  k.CreatedAt,
	}
}

// APIKeysFromDomain converts a slice in one go.
func APIKeysFromDomain(keys []*apikey.Key) []APIKeyResponse {
	out := make([]APIKeyResponse, 0, len(keys))
	for _, k := range keys {
		out = append(out, APIKeyFromDomain(k))
	}
	return out
}
