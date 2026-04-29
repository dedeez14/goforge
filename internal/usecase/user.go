package usecase

import (
	"context"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/domain/user"
)

// UserUseCase serves read-only queries against the user directory
// that the admin UI needs (listing, single-record lookup for the
// "assign roles" form). It is deliberately narrow: mutations still
// flow through AuthUseCase so business rules (password hashing,
// email uniqueness, …) stay in one place.
type UserUseCase struct {
	users user.Repository
}

// NewUserUseCase wires a UserUseCase to the repository.
func NewUserUseCase(users user.Repository) *UserUseCase {
	return &UserUseCase{users: users}
}

// List returns a paginated slice + total count. The handler clamps
// pagination parameters; the repository clamps limits again as a
// safety net so a misconfigured handler cannot drive the DB into
// a full-table scan.
func (uc *UserUseCase) List(ctx context.Context, f user.ListFilter) ([]*user.User, int, error) {
	return uc.users.List(ctx, f)
}

// Get returns a single user by id or user.ErrNotFound.
func (uc *UserUseCase) Get(ctx context.Context, id uuid.UUID) (*user.User, error) {
	return uc.users.FindByID(ctx, id)
}
