package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/adapter/http/dto"
	"github.com/dedeez14/goforge/internal/domain/user"
	"github.com/dedeez14/goforge/internal/usecase"
)

// stubUserRepo is a minimal in-memory user.Repository implementation
// for exercising the handler's query-parameter handling without a
// real database.
type stubUserRepo struct {
	lastFilter user.ListFilter
}

func (s *stubUserRepo) List(_ context.Context, f user.ListFilter) ([]*user.User, int, error) {
	s.lastFilter = f
	// Caller validates we respected the filter; returning a single
	// fixed row is enough for the contract test below.
	u := &user.User{ID: uuid.New(), Email: "x@example.com"}
	return []*user.User{u}, 1, nil
}
func (s *stubUserRepo) Create(context.Context, *user.User) error { return nil }
func (s *stubUserRepo) FindByEmail(context.Context, string) (*user.User, error) {
	return nil, nil
}
func (s *stubUserRepo) FindByID(context.Context, uuid.UUID) (*user.User, error) { return nil, nil }
func (s *stubUserRepo) UpdatePasswordHash(context.Context, uuid.UUID, string) error {
	return nil
}

// TestUserHandler_List_ClampsLimitToRepositoryCap guards against the
// regression where the handler reports a larger limit than the number
// of rows the repository actually returns, which breaks "has next
// page" logic on the client side.
func TestUserHandler_List_ClampsLimitToRepositoryCap(t *testing.T) {
	repo := &stubUserRepo{}
	uc := usecase.NewUserUseCase(repo)
	h := NewUserHandler(uc)

	app := fiber.New()
	app.Get("/users", h.List)

	req := httptest.NewRequest("GET", "/users?limit=500&offset=0", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	// httpx.OK wraps the payload in {"data": ...}; unmarshal into a
	// matching envelope to read the limit echoed back.
	var env struct {
		Data dto.UserListResponse `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, body)
	}

	if env.Data.Limit != userListMaxLimit {
		t.Errorf("response.limit = %d, want %d (clamped)", env.Data.Limit, userListMaxLimit)
	}
	if repo.lastFilter.Limit != userListMaxLimit {
		t.Errorf("repo filter.Limit = %d, want %d", repo.lastFilter.Limit, userListMaxLimit)
	}
}

func TestUserHandler_List_DefaultsLimitTo50(t *testing.T) {
	repo := &stubUserRepo{}
	h := NewUserHandler(usecase.NewUserUseCase(repo))

	app := fiber.New()
	app.Get("/users", h.List)

	req := httptest.NewRequest("GET", "/users", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if repo.lastFilter.Limit != 50 {
		t.Errorf("default limit = %d, want 50", repo.lastFilter.Limit)
	}
}
