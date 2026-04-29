package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/domain/session"
)

// stubSessionRepo is a deterministic test double for session.Repo.
// It records the last call to each mutating method so tests can
// assert on the arguments without spinning up Postgres.
type stubSessionRepo struct {
	listResult []*session.Session
	listErr    error

	revokedID, revokedOwner uuid.UUID
	revokedAt               time.Time
	revokeErr               error

	revokeAllUser, revokeAllExcept uuid.UUID
	revokeAllAt                    time.Time
	revokeAllCount                 int64
	revokeAllErr                   error
}

func (s *stubSessionRepo) Create(context.Context, *session.Session) error { return nil }
func (s *stubSessionRepo) GetByID(context.Context, uuid.UUID) (*session.Session, error) {
	return nil, session.ErrNotFound
}
func (s *stubSessionRepo) ListByUser(context.Context, uuid.UUID, bool) ([]*session.Session, error) {
	return s.listResult, s.listErr
}
func (s *stubSessionRepo) Touch(context.Context, uuid.UUID, time.Time, time.Time) error {
	return nil
}
func (s *stubSessionRepo) Revoke(_ context.Context, id, owner uuid.UUID, at time.Time) error {
	s.revokedID = id
	s.revokedOwner = owner
	s.revokedAt = at
	return s.revokeErr
}
func (s *stubSessionRepo) RevokeAllForUser(_ context.Context, uid, exceptID uuid.UUID, at time.Time) (int64, error) {
	s.revokeAllUser = uid
	s.revokeAllExcept = exceptID
	s.revokeAllAt = at
	return s.revokeAllCount, s.revokeAllErr
}
func (s *stubSessionRepo) Sweep(context.Context, time.Time) (int64, error) { return 0, nil }

func TestSessionUseCase_List_DelegatesToRepo(t *testing.T) {
	want := []*session.Session{{ID: uuid.New()}, {ID: uuid.New()}}
	repo := &stubSessionRepo{listResult: want}
	uc := NewSessionUseCase(repo)

	got, err := uc.List(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
}

func TestSessionUseCase_List_NilRepoReturnsEmpty(t *testing.T) {
	uc := NewSessionUseCase(nil)
	got, err := uc.List(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestSessionUseCase_Revoke_PassesOwnerID(t *testing.T) {
	repo := &stubSessionRepo{}
	uc := NewSessionUseCase(repo)
	uc.clock = func() time.Time { return time.Unix(1700000000, 0).UTC() }

	id, owner := uuid.New(), uuid.New()
	if err := uc.Revoke(context.Background(), id, owner); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if repo.revokedID != id {
		t.Fatalf("id = %s, want %s", repo.revokedID, id)
	}
	if repo.revokedOwner != owner {
		t.Fatalf("owner = %s, want %s", repo.revokedOwner, owner)
	}
	if !repo.revokedAt.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Fatalf("clock not threaded through, got %s", repo.revokedAt)
	}
}

func TestSessionUseCase_Revoke_PropagatesNotFound(t *testing.T) {
	repo := &stubSessionRepo{revokeErr: session.ErrNotFound}
	uc := NewSessionUseCase(repo)

	err := uc.Revoke(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("err = %v, want session.ErrNotFound", err)
	}
}

func TestSessionUseCase_RevokeAllExceptCurrent_ThreadsExceptID(t *testing.T) {
	repo := &stubSessionRepo{revokeAllCount: 3}
	uc := NewSessionUseCase(repo)

	user, current := uuid.New(), uuid.New()
	count, err := uc.RevokeAllExceptCurrent(context.Background(), user, current)
	if err != nil {
		t.Fatalf("RevokeAllExceptCurrent: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
	if repo.revokeAllUser != user {
		t.Fatalf("user = %s, want %s", repo.revokeAllUser, user)
	}
	if repo.revokeAllExcept != current {
		t.Fatalf("except = %s, want %s", repo.revokeAllExcept, current)
	}
}

func TestSessionUseCase_RevokeAllExceptCurrent_NilCurrentRevokesEverything(t *testing.T) {
	repo := &stubSessionRepo{revokeAllCount: 7}
	uc := NewSessionUseCase(repo)

	if _, err := uc.RevokeAllExceptCurrent(context.Background(), uuid.New(), uuid.Nil); err != nil {
		t.Fatalf("RevokeAllExceptCurrent: %v", err)
	}
	if repo.revokeAllExcept != uuid.Nil {
		t.Fatalf("except = %s, want uuid.Nil", repo.revokeAllExcept)
	}
}
