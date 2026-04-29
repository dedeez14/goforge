package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/adapter/http/dto"
	"github.com/dedeez14/goforge/internal/adapter/http/middleware"
	"github.com/dedeez14/goforge/internal/usecase"
	"github.com/dedeez14/goforge/pkg/errs"
	"github.com/dedeez14/goforge/pkg/httpx"
)

// SessionHandler exposes the self-service device-list endpoints. The
// minted-by-auth invariant (sessions are only created by login /
// register / refresh) keeps the public API small: list + revoke one
// + revoke all.
type SessionHandler struct {
	uc *usecase.SessionUseCase
}

// NewSessionHandler wires the handler to its use case.
func NewSessionHandler(uc *usecase.SessionUseCase) *SessionHandler {
	return &SessionHandler{uc: uc}
}

// List GET /api/v1/me/sessions
//
// Returns every active session for the authenticated user, with
// the caller's own session flagged as current so the UI can render
// a "this device" badge.
func (h *SessionHandler) List(c *fiber.Ctx) error {
	uid := middleware.UserIDFromCtx(c)
	if uid == uuid.Nil {
		return httpx.RespondError(c,
			errs.Unauthorized("auth.required", "authentication required"))
	}
	sessions, err := h.uc.List(c.UserContext(), uid)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	current := middleware.SessionIDFromCtx(c)
	return httpx.OK(c, dto.SessionsFromDomain(sessions, current))
}

// Revoke DELETE /api/v1/me/sessions/:id
//
// Revokes a single session belonging to the caller. Returns 204 on
// success and 404 when the session does not exist or belongs to a
// different user (collapsed to defeat IDOR enumeration).
func (h *SessionHandler) Revoke(c *fiber.Ctx) error {
	id, perr := parseUUIDParam(c, "id")
	if perr != nil {
		return httpx.RespondError(c, perr)
	}
	uid := middleware.UserIDFromCtx(c)
	if uid == uuid.Nil {
		return httpx.RespondError(c,
			errs.Unauthorized("auth.required", "authentication required"))
	}
	if err := h.uc.Revoke(c.UserContext(), id, uid); err != nil {
		return httpx.RespondError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// RevokeAll DELETE /api/v1/me/sessions
//
// "Logout everywhere except this device". The caller's own session
// (identified via the access-token's sid claim) is preserved; every
// other session for the user is revoked along with its refresh-token
// chain.
func (h *SessionHandler) RevokeAll(c *fiber.Ctx) error {
	uid := middleware.UserIDFromCtx(c)
	if uid == uuid.Nil {
		return httpx.RespondError(c,
			errs.Unauthorized("auth.required", "authentication required"))
	}
	current := middleware.SessionIDFromCtx(c)
	count, err := h.uc.RevokeAllExceptCurrent(c.UserContext(), uid, current)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.RevokeAllSessionsResponse{Revoked: count})
}
