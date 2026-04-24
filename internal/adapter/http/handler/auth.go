// Package handler defines HTTP handlers bound to use-cases.
//
// Handlers are intentionally thin: parse -> validate -> call use-case ->
// render envelope. No business logic lives here.
package handler

import (
	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/internal/adapter/http/dto"
	"github.com/dedeez14/goforge/internal/adapter/http/middleware"
	"github.com/dedeez14/goforge/internal/usecase"
	"github.com/dedeez14/goforge/pkg/errs"
	"github.com/dedeez14/goforge/pkg/httpx"
	"github.com/dedeez14/goforge/pkg/validatorx"
)

// AuthHandler exposes authentication HTTP endpoints.
type AuthHandler struct {
	auth *usecase.AuthUseCase
}

// NewAuthHandler constructs an AuthHandler.
func NewAuthHandler(auth *usecase.AuthUseCase) *AuthHandler {
	return &AuthHandler{auth: auth}
}

// Register godoc
//
//	POST /api/v1/auth/register
func (h *AuthHandler) Register(c *fiber.Ctx) error {
	var req dto.RegisterRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.RespondError(c, errs.InvalidInput("request.malformed", "malformed request body"))
	}
	if err := validatorx.Struct(&req); err != nil {
		return httpx.RespondError(c, err)
	}
	u, tp, err := h.auth.Register(c.UserContext(), usecase.RegisterInput{
		Email:    req.Email,
		Password: req.Password,
		Name:     req.Name,
	})
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.Created(c, dto.AuthResponse{
		User:   dto.UserFromDomain(u),
		Tokens: dto.TokensFromUseCase(tp),
	})
}

// Login godoc
//
//	POST /api/v1/auth/login
func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req dto.LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.RespondError(c, errs.InvalidInput("request.malformed", "malformed request body"))
	}
	if err := validatorx.Struct(&req); err != nil {
		return httpx.RespondError(c, err)
	}
	u, tp, err := h.auth.Login(c.UserContext(), usecase.LoginInput{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.AuthResponse{
		User:   dto.UserFromDomain(u),
		Tokens: dto.TokensFromUseCase(tp),
	})
}

// Refresh godoc
//
//	POST /api/v1/auth/refresh
func (h *AuthHandler) Refresh(c *fiber.Ctx) error {
	var req dto.RefreshRequest
	if err := c.BodyParser(&req); err != nil {
		return httpx.RespondError(c, errs.InvalidInput("request.malformed", "malformed request body"))
	}
	if err := validatorx.Struct(&req); err != nil {
		return httpx.RespondError(c, err)
	}
	tp, err := h.auth.Refresh(c.UserContext(), req.RefreshToken)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.TokensFromUseCase(tp))
}

// Me godoc
//
//	GET /api/v1/auth/me  (requires Bearer access token)
func (h *AuthHandler) Me(c *fiber.Ctx) error {
	id := middleware.UserIDFromCtx(c)
	u, err := h.auth.Me(c.UserContext(), id)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.UserFromDomain(u))
}
