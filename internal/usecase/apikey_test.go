package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/domain/apikey"
	keytoken "github.com/dedeez14/goforge/pkg/apikey"
	"github.com/dedeez14/goforge/pkg/errs"
)

// fakeRepo is an in-memory apikey.Repo used by these unit tests.
// It is safe for parallel goroutines.
type fakeRepo struct {
	mu        sync.Mutex
	keys      map[uuid.UUID]*apikey.Key
	byPrefix  map[string]*apikey.Key
	failNext  bool
	lastUsed  time.Time
	touchHits int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		keys:     make(map[uuid.UUID]*apikey.Key),
		byPrefix: make(map[string]*apikey.Key),
	}
}

func (r *fakeRepo) Create(_ context.Context, k *apikey.Key) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failNext {
		r.failNext = false
		return errors.New("forced failure")
	}
	r.keys[k.ID] = k
	r.byPrefix[k.Prefix] = k
	return nil
}

func (r *fakeRepo) GetByPrefix(_ context.Context, prefix string) (*apikey.Key, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if k, ok := r.byPrefix[prefix]; ok {
		return k, nil
	}
	return nil, apikey.ErrNotFound
}

func (r *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*apikey.Key, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if k, ok := r.keys[id]; ok {
		return k, nil
	}
	return nil, apikey.ErrNotFound
}

func (r *fakeRepo) ListByUser(_ context.Context, userID uuid.UUID) ([]*apikey.Key, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*apikey.Key
	for _, k := range r.keys {
		if k.UserID != nil && *k.UserID == userID {
			out = append(out, k)
		}
	}
	return out, nil
}

func (r *fakeRepo) Revoke(_ context.Context, id, ownerID uuid.UUID, by *uuid.UUID, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.keys[id]
	if !ok || k.RevokedAt != nil {
		return apikey.ErrNotFound
	}
	// Ownership filter mirrors the production SQL: a key with no
	// owner (system key) or a different owner cannot be revoked
	// through this path.
	if k.UserID == nil || *k.UserID != ownerID {
		return apikey.ErrNotFound
	}
	k.RevokedAt = &at
	k.UpdatedBy = by
	return nil
}

func (r *fakeRepo) UpdateLastUsed(_ context.Context, id uuid.UUID, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touchHits++
	r.lastUsed = at
	if k, ok := r.keys[id]; ok {
		k.LastUsedAt = &at
	}
	return nil
}

func newUC(repo *fakeRepo, now time.Time) *APIKeyUseCase {
	uc := NewAPIKeyUseCase(repo, "test")
	uc.clock = func() time.Time { return now }
	return uc
}

func TestCreate_RequiresName(t *testing.T) {
	uc := newUC(newFakeRepo(), time.Now())
	_, err := uc.Create(context.Background(), CreateInput{})
	if err == nil || !errors.As(err, new(*errs.Error)) {
		t.Fatalf("expected an *errs.Error for missing name; got %v", err)
	}
}

func TestCreate_PersistsHashAndReturnsPlaintextOnce(t *testing.T) {
	repo := newFakeRepo()
	uc := newUC(repo, time.Now())
	uid := uuid.New()
	res, err := uc.Create(context.Background(), CreateInput{
		Name:   "deploy bot",
		UserID: &uid,
		Scopes: []string{"deploys.create", "deploys.create", ""}, // dup + empty
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Plaintext == "" {
		t.Fatalf("plaintext must be returned exactly once")
	}
	if res.Key.Hash == res.Plaintext {
		t.Fatalf("stored hash must not equal plaintext")
	}
	if !keytoken.VerifyHash(res.Plaintext, res.Key.Hash) {
		t.Fatalf("hash should verify against plaintext")
	}
	if got := len(res.Key.Scopes); got != 1 {
		t.Fatalf("scopes should be deduped & emptied; got %v", res.Key.Scopes)
	}
	if res.Key.Scopes[0] != "deploys.create" {
		t.Fatalf("unexpected scope value: %q", res.Key.Scopes[0])
	}
}

func TestAuthenticate_HappyPath(t *testing.T) {
	repo := newFakeRepo()
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	uc := newUC(repo, now)
	res, _ := uc.Create(context.Background(), CreateInput{Name: "k"})
	got, err := uc.Authenticate(context.Background(), res.Plaintext)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if got.ID != res.Key.ID {
		t.Fatalf("authenticated key mismatch")
	}
	if repo.touchHits != 1 {
		t.Fatalf("last_used should be touched once; got %d", repo.touchHits)
	}
}

func TestAuthenticate_RejectsTamperedSecret(t *testing.T) {
	repo := newFakeRepo()
	uc := newUC(repo, time.Now())
	res, _ := uc.Create(context.Background(), CreateInput{Name: "k"})
	// flip the last char of the secret
	tampered := res.Plaintext[:len(res.Plaintext)-1] + "0"
	if tampered == res.Plaintext {
		tampered = res.Plaintext[:len(res.Plaintext)-1] + "1"
	}
	_, err := uc.Authenticate(context.Background(), tampered)
	if err == nil {
		t.Fatalf("tampered secret should not authenticate")
	}
}

func TestAuthenticate_RejectsRevokedKey(t *testing.T) {
	repo := newFakeRepo()
	uc := newUC(repo, time.Now())
	owner := uuid.New()
	res, _ := uc.Create(context.Background(), CreateInput{Name: "k", UserID: &owner})
	if err := uc.Revoke(context.Background(), res.Key.ID, owner, &owner); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err := uc.Authenticate(context.Background(), res.Plaintext)
	if err == nil {
		t.Fatalf("revoked key must not authenticate")
	}
}

func TestAuthenticate_RejectsExpiredKey(t *testing.T) {
	repo := newFakeRepo()
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	uc := newUC(repo, now)
	exp := now.Add(-time.Second)
	res, _ := uc.Create(context.Background(), CreateInput{Name: "k", ExpiresAt: &exp})
	_, err := uc.Authenticate(context.Background(), res.Plaintext)
	if err == nil {
		t.Fatalf("expired key must not authenticate")
	}
}

func TestAuthenticate_MalformedTokenIs401(t *testing.T) {
	uc := newUC(newFakeRepo(), time.Now())
	_, err := uc.Authenticate(context.Background(), "definitely-not-a-key")
	if err == nil {
		t.Fatalf("malformed token must error")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindUnauthorized {
		t.Fatalf("expected unauthorized errs.Error, got %v", err)
	}
}

func TestRevoke_NotFoundMapsToErrsNotFound(t *testing.T) {
	uc := newUC(newFakeRepo(), time.Now())
	owner := uuid.New()
	err := uc.Revoke(context.Background(), uuid.New(), owner, &owner)
	if err == nil {
		t.Fatalf("expected revoke of unknown id to fail")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindNotFound {
		t.Fatalf("expected not-found errs.Error; got %v", err)
	}
}

// TestRevoke_RejectsForeignOwner is the regression test for the
// IDOR Devin Review caught on PR #16: an authenticated user must
// not be able to revoke another user's key by knowing its UUID.
// We collapse the response into NotFound (instead of Forbidden) so
// the endpoint cannot be used as an oracle for key existence.
func TestRevoke_RejectsForeignOwner(t *testing.T) {
	repo := newFakeRepo()
	uc := newUC(repo, time.Now())
	alice := uuid.New()
	eve := uuid.New()
	res, _ := uc.Create(context.Background(), CreateInput{Name: "alice's key", UserID: &alice})

	err := uc.Revoke(context.Background(), res.Key.ID, eve, &eve)
	if err == nil {
		t.Fatalf("eve must not be able to revoke alice's key")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindNotFound {
		t.Fatalf("foreign-owner revoke should map to NotFound; got %v", err)
	}
	// Belt and braces: the key must remain authentic-able.
	if _, err := uc.Authenticate(context.Background(), res.Plaintext); err != nil {
		t.Fatalf("alice's key should still authenticate after eve's failed revoke; got %v", err)
	}
}

// TestRevoke_AcceptsOwner ensures the new ownership filter does not
// regress the happy path for the legitimate owner.
func TestRevoke_AcceptsOwner(t *testing.T) {
	repo := newFakeRepo()
	uc := newUC(repo, time.Now())
	owner := uuid.New()
	res, _ := uc.Create(context.Background(), CreateInput{Name: "k", UserID: &owner})
	if err := uc.Revoke(context.Background(), res.Key.ID, owner, &owner); err != nil {
		t.Fatalf("owner must be able to revoke own key; got %v", err)
	}
	if _, err := uc.Authenticate(context.Background(), res.Plaintext); err == nil {
		t.Fatalf("revoked key must not authenticate")
	}
}

// TestCreate_StampsTimestamps is the regression test for the
// Devin Review finding on PR #16: the response DTO previously
// emitted CreatedAt as the zero time because the in-memory struct
// was never reconciled with the DB-assigned NOW(). The use-case
// now stamps the clock value before handing the struct to the
// repo, so the persisted value and the response value match.
func TestCreate_StampsTimestamps(t *testing.T) {
	repo := newFakeRepo()
	now := time.Date(2025, 6, 15, 9, 30, 0, 0, time.UTC)
	uc := newUC(repo, now)

	res, err := uc.Create(context.Background(), CreateInput{Name: "k"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Key.CreatedAt.IsZero() {
		t.Fatalf("CreatedAt must be stamped, got zero time")
	}
	if !res.Key.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt should match clock; got %s want %s", res.Key.CreatedAt, now)
	}
	if !res.Key.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt should match clock; got %s want %s", res.Key.UpdatedAt, now)
	}
}

func TestSanitiseScopes_DedupsAndPreservesOrder(t *testing.T) {
	got := sanitiseScopes([]string{"a", "b", "", "a", "c"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("at %d: got %q want %q", i, got[i], want[i])
		}
	}
}
