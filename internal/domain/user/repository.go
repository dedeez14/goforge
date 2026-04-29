package user

import (
	"context"

	"github.com/google/uuid"
)

// ListFilter constrains the rows returned by Repository.List.
//
// Limit clamps the result size (repositories enforce a safety
// maximum); Offset walks the stable (created_at, id) ordering for
// simple pagination; Query, when non-empty, matches against email
// using a case-insensitive substring search so the admin UI can
// filter a large user table without extra indexes.
type ListFilter struct {
	Limit  int
	Offset int
	Query  string
}

// Repository is implemented by the infrastructure layer and consumed by
// use-cases. Keep this interface narrow - one method per intent.
type Repository interface {
	Create(ctx context.Context, u *User) error
	FindByID(ctx context.Context, id uuid.UUID) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error
	// List returns a paginated slice of users ordered by creation
	// time (stable within a single microsecond via the id tiebreaker),
	// plus the total row count under the same filter. It is the
	// admin UI's "users tab" backing query - business-facing code
	// should keep using FindByID / FindByEmail for single-record
	// lookups so they remain index-only.
	List(ctx context.Context, f ListFilter) (items []*User, total int, err error)
}
