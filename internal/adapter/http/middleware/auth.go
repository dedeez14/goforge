package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/infrastructure/security"
	"github.com/dedeez14/goforge/pkg/errs"
	"github.com/dedeez14/goforge/pkg/httpx"
)

// CtxKeyUserID is the Fiber Locals key holding the authenticated user id.
const CtxKeyUserID = "user_id"

// Auth validates a bearer token and attaches the user id to c.Locals.
// Handlers downstream read the id via UserIDFromCtx.
func Auth(issuer security.TokenIssuer) fiber.Handler {
	return func(c *fiber.Ctx) error {
		raw := c.Get(fiber.HeaderAuthorization)
		if raw == "" || !strings.HasPrefix(raw, "Bearer ") {
			return httpx.RespondError(c, errs.Unauthorized("auth.missing_token", "missing bearer token"))
		}
		claims, err := issuer.Parse(strings.TrimPrefix(raw, "Bearer "))
		if err != nil {
			return httpx.RespondError(c, err)
		}
		if claims.Kind != security.TokenAccess {
			return httpx.RespondError(c, errs.Unauthorized("auth.wrong_token_kind", "access token required"))
		}
		id, err := uuid.Parse(claims.Subject)
		if err != nil {
			return httpx.RespondError(c, errs.Unauthorized("auth.invalid_subject", "invalid token subject"))
		}
		c.Locals(CtxKeyUserID, id)
		return c.Next()
	}
}

// UserIDFromCtx returns the authenticated user id or uuid.Nil if absent.
func UserIDFromCtx(c *fiber.Ctx) uuid.UUID {
	v := c.Locals(CtxKeyUserID)
	if v == nil {
		return uuid.Nil
	}
	id, _ := v.(uuid.UUID)
	return id
}
