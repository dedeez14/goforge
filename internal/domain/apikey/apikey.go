// Package apikey is the domain layer for service-to-service API
// keys: entity, repository contract, and the canonical not-found
// translation. The token format and crypto live separately in
// pkg/apikey so they can be reused outside the framework.
package apikey

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/pkg/errs"
)

// Key is a service-to-service credential issued to a user (or to
// nobody, for tenant-wide system keys). The secret portion is never
// stored - only the SHA-256 hash. Scopes is the denormalised list of
// permission codes the bearer is allowed to invoke.
type Key struct {
	ID         uuid.UUID
	Prefix     string // e.g. "gf_live_a1b2c3d4e5f6" - public, indexed
	Hash       string // hex(sha256(secret))
	Name       string // human label for UI
	UserID     *uuid.UUID
	TenantID   *uuid.UUID
	Scopes     []string // permission codes the key is allowed to use
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
	CreatedBy *uuid.UUID
	UpdatedBy *uuid.UUID
}

// IsActive reports whether the key may still be used right now.
func (k *Key) IsActive(at time.Time) bool {
	if k == nil {
		return false
	}
	if k.RevokedAt != nil {
		return false
	}
	if k.DeletedAt != nil {
		return false
	}
	if k.ExpiresAt != nil && !at.Before(*k.ExpiresAt) {
		return false
	}
	return true
}

// HasScope returns true when the key was issued with code or with
// the wildcard "*" (which grants every scope).
func (k *Key) HasScope(code string) bool {
	if k == nil {
		return false
	}
	for _, s := range k.Scopes {
		if s == "*" || s == code {
			return true
		}
	}
	return false
}

// ErrNotFound is returned when no row matches the requested
// prefix or id. Wrapped via errs.NotFound at the use-case boundary.
var ErrNotFound = errors.New("apikey not found")

// Repo is the persistence interface implemented by Postgres.
type Repo interface {
	Create(ctx context.Context, k *Key) error
	GetByPrefix(ctx context.Context, prefix string) (*Key, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Key, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]*Key, error)
	Revoke(ctx context.Context, id uuid.UUID, by *uuid.UUID, at time.Time) error
	UpdateLastUsed(ctx context.Context, id uuid.UUID, at time.Time) error
}

// MapNotFound converts ErrNotFound into the framework's canonical
// not-found, leaving any other error untouched.
func MapNotFound(err error) error {
	if errors.Is(err, ErrNotFound) {
		return errs.NotFound("apikey.not_found", "API key not found")
	}
	return err
}
