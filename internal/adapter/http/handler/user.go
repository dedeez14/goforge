package handler

import (
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/internal/adapter/http/dto"
	"github.com/dedeez14/goforge/internal/domain/user"
	"github.com/dedeez14/goforge/internal/usecase"
	"github.com/dedeez14/goforge/pkg/httpx"
)

// UserHandler exposes read-only directory queries backing the admin
// UI's "users" tab. Writes still go through the auth flow; this
// handler is a thin view over user.Repository.
type UserHandler struct {
	uc *usecase.UserUseCase
}

// NewUserHandler wires the handler to its use case.
func NewUserHandler(uc *usecase.UserUseCase) *UserHandler {
	return &UserHandler{uc: uc}
}

// List GET /api/v1/users?limit=50&offset=0&q=substring
//
// Admin-only (gated by rbac.manage at the route layer). Pagination
// defaults to a 50-row page, capped at the repository's safety
// ceiling. `q` is a case-insensitive substring match against email
// so operators can find a user without scanning the UI's full
// page list.
func (h *UserHandler) List(c *fiber.Ctx) error {
	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 {
		limit = 50
	}
	offset, _ := strconv.Atoi(c.Query("offset"))
	if offset < 0 {
		offset = 0
	}
	items, total, err := h.uc.List(c.UserContext(), user.ListFilter{
		Limit:  limit,
		Offset: offset,
		Query:  c.Query("q"),
	})
	if err != nil {
		return httpx.RespondError(c, err)
	}
	out := make([]dto.UserResponse, 0, len(items))
	for _, u := range items {
		out = append(out, dto.UserFromDomain(u))
	}
	return httpx.OK(c, dto.UserListResponse{
		Items:  out,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}
