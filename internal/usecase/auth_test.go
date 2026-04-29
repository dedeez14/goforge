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

// List is required by the user.Repository interface but unused in
// the auth use-case tests. A no-op is fine: the admin UC has its
// own coverage.
func (r *inMemoryUserRepo) List(_ context.Context, _ user.ListFilter) ([]*user.User, int, error) {
	out := make([]*user.User, 0, len(r.byID))
	for _, u := range r.byID {
		out = append(out, u)
	}
	return out, len(out), nil
}

func newAuthFixture(t *testing.T) *AuthUseCase {
	t.Helper()
	repo := newInMemoryUserRepo()
	hasher := security.NewPasswordHasher(security.Argon2idParams{
		Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 8, KeyLength: 16,
	})
	tokens := security.NewTokenIssuer(config.JWT{
		Secret: strings.Repeat("k", 32), Issuer: "test",
		AccessTTL: 5 * time.Minute, RefreshTTL: time.Hour,
	})
	return NewAuthUseCase(repo, hasher, tokens, security.NewMemoryRefreshStore(), nil, time.Hour, zerolog.Nop())
}

func TestAuth_RegisterThenLogin(t *testing.T) {
	uc := newAuthFixture(t)
	ctx := context.Background()

	u, tp, err := uc.Register(ctx, RegisterInput{Email: "Alice@Example.com", Password: "hunter2hunter2", Name: "Alice"}, SessionContext{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if u.Email != "alice@example.com" {
		t.Fatalf("email must be normalised, got %q", u.Email)
	}
	if tp.AccessToken == "" || tp.RefreshToken == "" {
		t.Fatal("expected both tokens")
	}

	_, tp2, err := uc.Login(ctx, LoginInput{Email: "alice@example.com", Password: "hunter2hunter2"}, SessionContext{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tp2.AccessToken == "" {
		t.Fatal("expected access token on login")
	}
}

func TestAuth_RegisterEmailTaken(t *testing.T) {
	uc := newAuthFixture(t)
	ctx := context.Background()
	if _, _, err := uc.Register(ctx, RegisterInput{Email: "a@b.co", Password: "password", Name: "A"}, SessionContext{}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	_, _, err := uc.Register(ctx, RegisterInput{Email: "A@B.co", Password: "password", Name: "A"}, SessionContext{})
	if !errors.Is(err, user.ErrEmailTaken) {
		t.Fatalf("expected ErrEmailTaken, got %v", err)
	}
}

func TestAuth_LoginInvalid(t *testing.T) {
	uc := newAuthFixture(t)
	ctx := context.Background()
	_, _, err := uc.Login(ctx, LoginInput{Email: "missing@example.com", Password: "x"}, SessionContext{})
	if !errs.Is(err, errs.KindUnauthorized) {
		t.Fatalf("missing user should map to unauthorized, got %v", err)
	}

	if _, _, err := uc.Register(ctx, RegisterInput{Email: "z@b.co", Password: "rightpassword", Name: "Z"}, SessionContext{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, _, err = uc.Login(ctx, LoginInput{Email: "z@b.co", Password: "wrong"}, SessionContext{})
	if !errs.Is(err, errs.KindUnauthorized) {
		t.Fatalf("wrong password should map to unauthorized, got %v", err)
	}
}

func TestAuth_Refresh(t *testing.T) {
	uc := newAuthFixture(t)
	ctx := context.Background()
	_, tp, err := uc.Register(ctx, RegisterInput{Email: "r@b.co", Password: "rightpassword", Name: "R"}, SessionContext{})
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

// TestAuth_RefreshTokenIsSingleUse pins the security finding F1 from
// the 2026-04 audit: refresh tokens must rotate on use; replaying the
// original token is a 401.
func TestAuth_RefreshTokenIsSingleUse(t *testing.T) {
	uc := newAuthFixture(t)
	ctx := context.Background()
	_, tp, err := uc.Register(ctx, RegisterInput{Email: "rot@b.co", Password: "rightpassword", Name: "R"}, SessionContext{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := uc.Refresh(ctx, tp.RefreshToken); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	// Replaying the original refresh token must fail (rotation).
	_, err = uc.Refresh(ctx, tp.RefreshToken)
	if !errs.Is(err, errs.KindUnauthorized) {
		t.Fatalf("replayed refresh token should be unauthorized, got %v", err)
	}
}

// TestAuth_RefreshReuseRevokesAllTokens pins reuse-detection: when a
// rotated token is replayed, every other outstanding refresh token
// for the same user is revoked, so the live attacker chain is killed
// even if the legitimate user kept rotating.
func TestAuth_RefreshReuseRevokesAllTokens(t *testing.T) {
	uc := newAuthFixture(t)
	ctx := context.Background()
	_, tp1, err := uc.Register(ctx, RegisterInput{Email: "kill@b.co", Password: "rightpassword", Name: "K"}, SessionContext{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	tp2, err := uc.Refresh(ctx, tp1.RefreshToken)
	if err != nil {
		t.Fatalf("legitimate rotate: %v", err)
	}

	// Attacker replays tp1 (reuse). The store revokes tp2 too.
	if _, err := uc.Refresh(ctx, tp1.RefreshToken); !errs.Is(err, errs.KindUnauthorized) {
		t.Fatalf("reused token should be unauthorized, got %v", err)
	}
	// Legitimate user's chain (tp2) is now dead even though they
	// rotated correctly.
	if _, err := uc.Refresh(ctx, tp2.RefreshToken); !errs.Is(err, errs.KindUnauthorized) {
		t.Fatalf("rotated chain should be revoked after reuse detection, got %v", err)
	}
}

// TestAuth_LoginTimingEqualization pins finding F2: the duration of a
// failing login against a missing email must be on the same order of
// magnitude as a failing login against an existing email, so attackers
// cannot enumerate registered users by timing.
//
// The fixture uses a low-cost Argon2id parameter set; even so the
// missing-user path would short-circuit in microseconds without the
// dummy-verify guard. A 4x ratio is a comfortable upper bound that
// still catches a regression to "no dummy verify".
func TestAuth_LoginTimingEqualization(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	uc := newAuthFixture(t)
	ctx := context.Background()
	if _, _, err := uc.Register(ctx, RegisterInput{Email: "exists@b.co", Password: "rightpassword", Name: "E"}, SessionContext{}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	const samples = 5
	timeIt := func(email string) time.Duration {
		var total time.Duration
		for i := 0; i < samples; i++ {
			start := time.Now()
			_, _, _ = uc.Login(ctx, LoginInput{Email: email, Password: "wrongpassword"}, SessionContext{})
			total += time.Since(start)
		}
		return total / samples
	}
	avgExisting := timeIt("exists@b.co")
	avgMissing := timeIt("ghost@b.co")
	if avgMissing < avgExisting/4 {
		t.Fatalf("missing-user login (%s) is suspiciously fast vs existing (%s); username enumeration via timing", avgMissing, avgExisting)
	}
}
