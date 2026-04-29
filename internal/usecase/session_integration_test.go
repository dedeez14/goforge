package usecase

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/dedeez14/goforge/internal/config"
	"github.com/dedeez14/goforge/internal/domain/session"
	"github.com/dedeez14/goforge/internal/infrastructure/security"
	"github.com/dedeez14/goforge/pkg/errs"
)

// memSessionRepo is the smallest functional implementation of
// session.Repo so the auth + session use cases can be exercised
// end-to-end without Postgres. It mirrors the production semantics
// closely enough that revoke cascades and "active" filtering can be
// asserted, but it deliberately does NOT implement the
// refresh_tokens cascade: that path is verified by the Postgres
// adapter's own integration tests.
type memSessionRepo struct {
	mu sync.Mutex
	// addressable storage so RevokeAllForUser can mutate in place
	rows map[uuid.UUID]*session.Session
}

func newMemSessionRepo() *memSessionRepo {
	return &memSessionRepo{rows: make(map[uuid.UUID]*session.Session)}
}

func (r *memSessionRepo) Create(_ context.Context, s *session.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *s
	r.rows[s.ID] = &cp
	return nil
}

func (r *memSessionRepo) GetByID(_ context.Context, id uuid.UUID) (*session.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.rows[id]
	if !ok {
		return nil, session.ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (r *memSessionRepo) ListByUser(_ context.Context, userID uuid.UUID, activeOnly bool) ([]*session.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	out := make([]*session.Session, 0, len(r.rows))
	for _, s := range r.rows {
		if s.UserID != userID {
			continue
		}
		if activeOnly && !s.IsActive(now) {
			continue
		}
		cp := *s
		out = append(out, &cp)
	}
	return out, nil
}

func (r *memSessionRepo) Touch(_ context.Context, id uuid.UUID, at, newExpiry time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.rows[id]
	if !ok {
		return session.ErrNotFound
	}
	s.LastUsedAt = at
	s.ExpiresAt = newExpiry
	return nil
}

func (r *memSessionRepo) Revoke(_ context.Context, id, ownerID uuid.UUID, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.rows[id]
	if !ok || s.UserID != ownerID || s.RevokedAt != nil {
		return session.ErrNotFound
	}
	t := at
	s.RevokedAt = &t
	return nil
}

func (r *memSessionRepo) RevokeAllForUser(_ context.Context, userID, exceptID uuid.UUID, at time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for id, s := range r.rows {
		if s.UserID != userID || s.RevokedAt != nil {
			continue
		}
		if exceptID != uuid.Nil && id == exceptID {
			continue
		}
		t := at
		s.RevokedAt = &t
		n++
	}
	return n, nil
}

func (r *memSessionRepo) Sweep(context.Context, time.Time) (int64, error) { return 0, nil }

func newSessionAwareAuth(t *testing.T) (*AuthUseCase, *memSessionRepo) {
	t.Helper()
	repo := newInMemoryUserRepo()
	hasher := security.NewPasswordHasher(security.Argon2idParams{
		Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 8, KeyLength: 16,
	})
	tokens := security.NewTokenIssuer(config.JWT{
		Secret: strings.Repeat("k", 32), Issuer: "test",
		AccessTTL: 5 * time.Minute, RefreshTTL: time.Hour,
	})
	sessions := newMemSessionRepo()
	uc := NewAuthUseCase(repo, hasher, tokens, security.NewMemoryRefreshStore(), sessions, time.Hour, zerolog.Nop())
	return uc, sessions
}

// TestAuth_Register_CreatesSession pins that a successful registration
// materialises a sessions row carrying the supplied device hints, so
// /me/sessions can render the just-logged-in device immediately.
func TestAuth_Register_CreatesSession(t *testing.T) {
	auth, sessions := newSessionAwareAuth(t)
	ctx := context.Background()
	u, _, err := auth.Register(ctx, RegisterInput{
		Email: "alice@example.com", Password: "rightpassword", Name: "A",
	}, SessionContext{UserAgent: "Mozilla/5.0", IP: "203.0.113.7"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	rows, _ := sessions.ListByUser(ctx, u.ID, true)
	if len(rows) != 1 {
		t.Fatalf("session count = %d, want 1", len(rows))
	}
	if rows[0].UserAgent != "Mozilla/5.0" || rows[0].IP != "203.0.113.7" {
		t.Fatalf("device hints not persisted: %+v", rows[0])
	}
}

// TestAuth_Refresh_ReusesSameSession asserts that refresh-token
// rotation does not spawn extra session rows; a "device" stays one
// row across many rotations.
func TestAuth_Refresh_ReusesSameSession(t *testing.T) {
	auth, sessions := newSessionAwareAuth(t)
	ctx := context.Background()
	u, tp, err := auth.Register(ctx, RegisterInput{
		Email: "bob@example.com", Password: "rightpassword", Name: "B",
	}, SessionContext{UserAgent: "ua", IP: "1.1.1.1"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	for i := 0; i < 3; i++ {
		tp, err = auth.Refresh(ctx, tp.RefreshToken)
		if err != nil {
			t.Fatalf("Refresh #%d: %v", i, err)
		}
	}
	rows, _ := sessions.ListByUser(ctx, u.ID, true)
	if len(rows) != 1 {
		t.Fatalf("session count after rotation = %d, want 1", len(rows))
	}
}

// TestAuth_RefreshReuse_RevokesAllSessions pins the blast-radius
// containment: when reuse detection fires, every session for the
// user must be revoked so the attacker loses every device hop.
func TestAuth_RefreshReuse_RevokesAllSessions(t *testing.T) {
	auth, sessions := newSessionAwareAuth(t)
	ctx := context.Background()
	u, tp1, err := auth.Register(ctx, RegisterInput{
		Email: "kill@example.com", Password: "rightpassword", Name: "K",
	}, SessionContext{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := auth.Refresh(ctx, tp1.RefreshToken); err != nil {
		t.Fatalf("legitimate rotate: %v", err)
	}
	// Replay the original token: triggers reuse detection.
	if _, err := auth.Refresh(ctx, tp1.RefreshToken); !errs.Is(err, errs.KindUnauthorized) {
		t.Fatalf("reused token should be unauthorized, got %v", err)
	}
	rows, _ := sessions.ListByUser(ctx, u.ID, true)
	if len(rows) != 0 {
		t.Fatalf("active sessions after reuse = %d, want 0", len(rows))
	}
}

// TestSessionUseCase_RevokeAllExceptCurrent_KeepsCaller pins
// "logout everywhere except this device": the caller's session
// stays, every other one for the user is revoked.
func TestSessionUseCase_RevokeAllExceptCurrent_KeepsCaller(t *testing.T) {
	auth, sessions := newSessionAwareAuth(t)
	ctx := context.Background()
	u, _, err := auth.Register(ctx, RegisterInput{
		Email: "multi@example.com", Password: "rightpassword", Name: "M",
	}, SessionContext{UserAgent: "device-A"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Login twice more to simulate two extra devices.
	for i := 0; i < 2; i++ {
		if _, _, err := auth.Login(ctx, LoginInput{
			Email: "multi@example.com", Password: "rightpassword",
		}, SessionContext{}); err != nil {
			t.Fatalf("Login #%d: %v", i, err)
		}
	}
	rows, _ := sessions.ListByUser(ctx, u.ID, true)
	if len(rows) != 3 {
		t.Fatalf("setup: session count = %d, want 3", len(rows))
	}

	uc := NewSessionUseCase(sessions)
	count, err := uc.RevokeAllExceptCurrent(ctx, u.ID, rows[0].ID)
	if err != nil {
		t.Fatalf("RevokeAllExceptCurrent: %v", err)
	}
	if count != 2 {
		t.Fatalf("revoked count = %d, want 2", count)
	}

	remaining, _ := sessions.ListByUser(ctx, u.ID, true)
	if len(remaining) != 1 {
		t.Fatalf("active after revoke-all = %d, want 1", len(remaining))
	}
	if remaining[0].ID != rows[0].ID {
		t.Fatalf("kept session id = %s, want %s", remaining[0].ID, rows[0].ID)
	}
}
