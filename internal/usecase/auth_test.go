package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/dedeez14/goforge/internal/config"
	"github.com/dedeez14/goforge/internal/domain/user"
	"github.com/dedeez14/goforge/internal/infrastructure/security"
	"github.com/dedeez14/goforge/pkg/errs"
)

// inMemoryUserRepo is a deterministic test double for the user.Repository
// interface. It keeps tests hermetic (no database needed) while exercising
// the full use-case logic.
type inMemoryUserRepo struct {
	byID    map[uuid.UUID]*user.User
	byEmail map[string]*user.User
}

func newInMemoryUserRepo() *inMemoryUserRepo {
	return &inMemoryUserRepo{
		byID:    make(map[uuid.UUID]*user.User),
		byEmail: make(map[string]*user.User),
	}
}

func (r *inMemoryUserRepo) Create(_ context.Context, u *user.User) error {
	if _, ok := r.byEmail[u.Email]; ok {
		return user.ErrEmailTaken
	}
	r.byID[u.ID] = u
	r.byEmail[u.Email] = u
	return nil
}

func (r *inMemoryUserRepo) FindByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	if u, ok := r.byID[id]; ok {
		return u, nil
	}
	return nil, user.ErrNotFound
}

func (r *inMemoryUserRepo) FindByEmail(_ context.Context, email string) (*user.User, error) {
	if u, ok := r.byEmail[email]; ok {
		return u, nil
	}
	return nil, user.ErrNotFound
}

func (r *inMemoryUserRepo) UpdatePasswordHash(_ context.Context, id uuid.UUID, hash string) error {
	u, ok := r.byID[id]
	if !ok {
		return user.ErrNotFound
	}
	u.PasswordHash = hash
	return nil
}

func newAuthFixture(t *testing.T) (*AuthUseCase, *inMemoryUserRepo) {
	t.Helper()
	repo := newInMemoryUserRepo()
	hasher := security.NewPasswordHasher(security.Argon2idParams{
		Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 8, KeyLength: 16,
	})
	tokens := security.NewTokenIssuer(config.JWT{
		Secret: strings.Repeat("k", 32), Issuer: "test",
		AccessTTL: 5 * time.Minute, RefreshTTL: time.Hour,
	})
	return NewAuthUseCase(repo, hasher, tokens, zerolog.Nop()), repo
}

func TestAuth_RegisterThenLogin(t *testing.T) {
	uc, _ := newAuthFixture(t)
	ctx := context.Background()

	u, tp, err := uc.Register(ctx, RegisterInput{Email: "Alice@Example.com", Password: "hunter2hunter2", Name: "Alice"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if u.Email != "alice@example.com" {
		t.Fatalf("email must be normalised, got %q", u.Email)
	}
	if tp.AccessToken == "" || tp.RefreshToken == "" {
		t.Fatal("expected both tokens")
	}

	_, tp2, err := uc.Login(ctx, LoginInput{Email: "alice@example.com", Password: "hunter2hunter2"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tp2.AccessToken == "" {
		t.Fatal("expected access token on login")
	}
}

func TestAuth_RegisterEmailTaken(t *testing.T) {
	uc, _ := newAuthFixture(t)
	ctx := context.Background()
	if _, _, err := uc.Register(ctx, RegisterInput{Email: "a@b.co", Password: "password", Name: "A"}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	_, _, err := uc.Register(ctx, RegisterInput{Email: "A@B.co", Password: "password", Name: "A"})
	if !errors.Is(err, user.ErrEmailTaken) {
		t.Fatalf("expected ErrEmailTaken, got %v", err)
	}
}

func TestAuth_LoginInvalid(t *testing.T) {
	uc, _ := newAuthFixture(t)
	ctx := context.Background()
	_, _, err := uc.Login(ctx, LoginInput{Email: "missing@example.com", Password: "x"})
	if !errs.Is(err, errs.KindUnauthorized) {
		t.Fatalf("missing user should map to unauthorized, got %v", err)
	}

	if _, _, err := uc.Register(ctx, RegisterInput{Email: "z@b.co", Password: "rightpassword", Name: "Z"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, _, err = uc.Login(ctx, LoginInput{Email: "z@b.co", Password: "wrong"})
	if !errs.Is(err, errs.KindUnauthorized) {
		t.Fatalf("wrong password should map to unauthorized, got %v", err)
	}
}

func TestAuth_Refresh(t *testing.T) {
	uc, _ := newAuthFixture(t)
	ctx := context.Background()
	_, tp, err := uc.Register(ctx, RegisterInput{Email: "r@b.co", Password: "rightpassword", Name: "R"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	tp2, err := uc.Refresh(ctx, tp.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tp2.AccessToken == "" || tp2.RefreshToken == "" {
		t.Fatal("expected a new token pair")
	}

	// The access token is not a valid refresh input.
	if _, err := uc.Refresh(ctx, tp.AccessToken); !errs.Is(err, errs.KindUnauthorized) {
		t.Fatalf("refresh with access token should be unauthorized, got %v", err)
	}
}
