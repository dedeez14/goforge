package middleware

import (
	"context"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	keytoken "github.com/dedeez14/goforge/pkg/apikey"
	"github.com/dedeez14/goforge/pkg/errs"
	"github.com/dedeez14/goforge/pkg/httpx"
)

// CtxKeyAPIKeyScopes stores the scopes attached to the API key
// that authenticated this request, when the caller used an API
// key instead of a JWT. Downstream middleware (RequirePermission)
// reads it before falling back to RBAC role lookups.
const CtxKeyAPIKeyScopes = "apikey_scopes"

// APIKeyAuthenticate is the closure shape the API-key middleware
// expects: given a presented bearer string, return the subject
// (user / tenant id derived from the key) and the scopes the key
// carries. Implementers typically wrap APIKeyUseCase.Authenticate.
//
// A function type rather than an interface keeps the middleware
// free of knowledge about the domain.Key type.
type APIKeyAuthenticate func(ctx context.Context, plaintext string) (subject uuid.UUID, scopes []string, err error)

// APIKeyOrJWTAuth returns a middleware that authenticates the
// request using either:
//
//   - the framework's API-key format (gf_<env>_<id>_<secret>) -
//     verified via authenticate and assigned the key's scopes;
//
//   - a regular JWT bearer (delegates to jwtNext).
//
// When the bearer doesn't look like an API key, the JWT middleware
// runs unchanged. This keeps existing routes that already used Auth
// working without any code change.
func APIKeyOrJWTAuth(authenticate APIKeyAuthenticate, jwtNext fiber.Handler) fiber.Handler {
	return func(c *fiber.Ctx) error {
		raw := c.Get(fiber.HeaderAuthorization)
		if raw == "" || !strings.HasPrefix(raw, "Bearer ") {
			return httpx.RespondError(c,
				errs.Unauthorized("auth.missing_token", "missing bearer token"))
		}
		token := strings.TrimPrefix(raw, "Bearer ")
		if !keytoken.LooksLikeAPIKey(token) {
			return jwtNext(c)
		}
		sub, scopes, err := authenticate(c.UserContext(), token)
		if err != nil {
			return httpx.RespondError(c, err)
		}
		c.Locals(CtxKeyUserID, sub)
		// Ensure scopes is non-nil so RequirePermission can tell an
		// API-key request from a JWT one even if the key carries
		// zero permissions (which would still be a valid request).
		if scopes == nil {
			scopes = []string{}
		}
		c.Locals(CtxKeyAPIKeyScopes, scopes)
		return c.Next()
	}
}

// APIKeyScopesFromCtx returns the scopes attached by an API-key
// authenticated request, or nil if the request was authenticated
// via JWT (or not at all). Used by RequirePermission to short-
// circuit on scopes instead of doing a DB round-trip for roles.
func APIKeyScopesFromCtx(c *fiber.Ctx) []string {
	v := c.Locals(CtxKeyAPIKeyScopes)
	if v == nil {
		return nil
	}
	scopes, _ := v.([]string)
	return scopes
}
