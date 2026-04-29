// Package postgres implements domain repositories against PostgreSQL.
//
// Each repository owns its own SQL and owns the mapping from rows to
// domain entities. Queries are declared as package-level consts at the
// top of the file so they can be reviewed and profiled easily.
package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dedeez14/goforge/internal/domain/user"
	"github.com/dedeez14/goforge/pkg/errs"
)

const (
	uniqueViolation = "23505"

	qInsertUser = `
INSERT INTO users (id, email, password_hash, name, role, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
`
	qSelectUserByID = `
SELECT id, email, password_hash, name, role, created_at, updated_at
FROM users WHERE id = $1
`
	qSelectUserByEmail = `
SELECT id, email, password_hash, name, role, created_at, updated_at
FROM users WHERE email = $1
`
	qUpdateUserPasswordHash = `
UPDATE users SET password_hash = $2, updated_at = NOW() WHERE id = $1
`
	qListUsers = `
SELECT id, email, password_hash, name, role, created_at, updated_at
FROM users
WHERE ($1 = '' OR email ILIKE '%' || $1 || '%')
ORDER BY created_at ASC, id ASC
LIMIT $2 OFFSET $3
`
	qCountUsers = `
SELECT COUNT(*) FROM users WHERE ($1 = '' OR email ILIKE '%' || $1 || '%')
`
	// Hard cap so a caller passing Limit=1_000_000 cannot force the
	// server into a sort-all-rows query; matches the handler's
	// default page ceiling.
	userListMaxLimit = 200
)

// UserRepository is the pgx-backed implementation of user.Repository.
type UserRepository struct {
	pool *pgxpool.Pool
}

// NewUserRepository wires a UserRepository to the shared pool.
func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

// Create inserts u and maps unique-violation into a domain conflict error.
func (r *UserRepository) Create(ctx context.Context, u *user.User) error {
	_, err := r.pool.Exec(ctx, qInsertUser,
		u.ID, u.Email, u.PasswordHash, u.Name, string(u.Role), u.CreatedAt, u.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return user.ErrEmailTaken
		}
		return errs.Wrap(errs.KindInternal, "user.create", "failed to create user", err)
	}
	return nil
}

// FindByID returns the user by primary key or ErrNotFound.
func (r *UserRepository) FindByID(ctx context.Context, id uuid.UUID) (*user.User, error) {
	return r.scanOne(ctx, qSelectUserByID, id)
}

// FindByEmail returns the user by email or ErrNotFound.
func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*user.User, error) {
	return r.scanOne(ctx, qSelectUserByEmail, email)
}

// UpdatePasswordHash replaces the stored password hash for id.
func (r *UserRepository) UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error {
	tag, err := r.pool.Exec(ctx, qUpdateUserPasswordHash, id, hash)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "user.update_password", "failed to update password", err)
	}
	if tag.RowsAffected() == 0 {
		return user.ErrNotFound
	}
	return nil
}

// List returns the paginated user slice alongside the total count.
// The count is produced by a second query intentionally: modern
// Postgres optimises `SELECT COUNT(*)` well against the email
// trigram / btree index, and the two round-trips are cheaper than
// window-function tricks for the sizes this endpoint cares about
// (admin UI pagination). Callers that do not need a total can
// ignore it; the repository still computes it so the shape matches
// domain.user.Repository.
func (r *UserRepository) List(ctx context.Context, f user.ListFilter) ([]*user.User, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > userListMaxLimit {
		limit = userListMaxLimit
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	rows, err := r.pool.Query(ctx, qListUsers, f.Query, limit, offset)
	if err != nil {
		return nil, 0, errs.Wrap(errs.KindInternal, "user.list", "failed to list users", err)
	}
	defer rows.Close()

	items := make([]*user.User, 0, limit)
	for rows.Next() {
		var (
			u    user.User
			role string
		)
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &role, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, 0, errs.Wrap(errs.KindInternal, "user.scan", "failed to read user", err)
		}
		u.Role = user.Role(role)
		items = append(items, &u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, errs.Wrap(errs.KindInternal, "user.list", "row iteration failed", err)
	}

	var total int
	if err := r.pool.QueryRow(ctx, qCountUsers, f.Query).Scan(&total); err != nil {
		return nil, 0, errs.Wrap(errs.KindInternal, "user.count", "failed to count users", err)
	}
	return items, total, nil
}

func (r *UserRepository) scanOne(ctx context.Context, query string, args ...any) (*user.User, error) {
	var (
		u    user.User
		role string
	)
	row := r.pool.QueryRow(ctx, query, args...)
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &role, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, user.ErrNotFound
		}
		return nil, errs.Wrap(errs.KindInternal, "user.scan", "failed to read user", err)
	}
	u.Role = user.Role(role)
	return &u, nil
}
