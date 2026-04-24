// Package user is the domain package for the user aggregate.
// It has zero non-stdlib dependencies beyond google/uuid.
package user

import (
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/pkg/errs"
)

// Role enumerates coarse-grained authorisation roles.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// User is the persistent domain entity.
type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	Name         string
	Role         Role
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Domain errors. Handlers never construct these; use-cases do.
var (
	ErrNotFound     = errs.NotFound("user.not_found", "user not found")
	ErrEmailTaken   = errs.Conflict("user.email_taken", "email is already registered")
	ErrInvalidCreds = errs.Unauthorized("user.invalid_credentials", "invalid email or password")
)
