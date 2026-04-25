package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/adapter/http/dto"
	"github.com/dedeez14/goforge/internal/adapter/http/middleware"
	"github.com/dedeez14/goforge/internal/usecase"
	"github.com/dedeez14/goforge/pkg/errs"
	"github.com/dedeez14/goforge/pkg/httpx"
	"github.com/dedeez14/goforge/pkg/validatorx"
)

// uuidNil mirrors uuid.Nil locally to keep the comparison expression
// short at call sites. UUID.IsZero is unavailable on this version.
var uuidNil = uuid.Nil

// APIKeyHandler exposes API-key management over HTTP. All routes
// here are scoped to the authenticated user; admin-style routes
// (e.g. minting service keys with no owner) live behind a
// permission gate at the route layer, not in the handler.
type APIKeyHandler struct {
	uc *usecase.APIKeyUseCase
}

// NewAPIKeyHandler wires the handler to its use case.
func NewAPIKeyHandler(uc *usecase.APIKeyUseCase) *APIKeyHandler {
	return &APIKeyHandler{uc: uc}
}

// List GET /api/v1/api-keys
//
// Returns the caller's own keys. Admin views over other users'
// keys are intentionally a separate route so RBAC permissions can
// gate them differently.
func (h *APIKeyHandler) List(c *fiber.Ctx) error {
	uid := middleware.UserIDFromCtx(c)
	keys, err := h.uc.List(c.UserContext(), uid)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.APIKeysFromDomain(keys))
}

// Create POST /api/v1/api-keys
//
// Mints a new key bound to the authenticated user. The plaintext
// is returned exactly once in the response; clients must store it
// immediately because the framework never persists it.
func (h *APIKeyHandler) Create(c *fiber.Ctx) error {
	var req dto.CreateAPIKeyRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.RespondError(c, errs.InvalidInput("request.malformed", "malformed request body"))
	}
	if err := validatorx.Struct(&req); err != nil {
		return httpx.RespondError(c, err)
	}
	uid := middleware.UserIDFromCtx(c)
	if uid == uuidNil {
		return httpx.RespondError(c,
			errs.Unauthorized("auth.required", "authentication required"))
	}
	res, err := h.uc.Create(c.UserContext(), usecase.CreateInput{
		Name:      req.Name,
		UserID:    &uid,
		Scopes:    req.Scopes,
		ExpiresAt: req.ExpiresAt,
		CreatedBy: &uid,
	})
	if err != nil {
		return httpx.RespondError(c, err)
	}
	out := dto.CreateAPIKeyResponse{
		APIKeyResponse: dto.APIKeyFromDomain(res.Key),
		Plaintext:      res.Plaintext,
	}
	return httpx.Created(c, out)
}

// Revoke DELETE /api/v1/api-keys/:id
//
// Revokes the named key. Idempotent: revoking an already-revoked
// key returns 404 because that's the closest semantic ("it's gone
// for the caller"); the audit log retains the original revoke event.
func (h *APIKeyHandler) Revoke(c *fiber.Ctx) error {
	id, err := parseUUIDParam(c, "id")
	if err != nil {
		return httpx.RespondError(c, err)
	}
	uid := middleware.UserIDFromCtx(c)
	if uid == uuidNil {
		return httpx.RespondError(c,
			errs.Unauthorized("auth.required", "authentication required"))
	}
	// uid is both the ownership filter and the audit actor: a
	// self-service revoke is always "I revoke my own key".
	if err := h.uc.Revoke(c.UserContext(), id, uid, &uid); err != nil {
		return httpx.RespondError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
