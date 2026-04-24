package user

import (
	"context"

	"github.com/google/uuid"
)

// Repository is implemented by the infrastructure layer and consumed by
// use-cases. Keep this interface narrow - one method per intent.
type Repository interface {
	Create(ctx context.Context, u *User) error
	FindByID(ctx context.Context, id uuid.UUID) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error
}
