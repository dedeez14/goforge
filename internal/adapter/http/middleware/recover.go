package middleware

import (
	"runtime/debug"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"

	"github.com/dedeez14/goforge/pkg/errs"
	"github.com/dedeez14/goforge/pkg/httpx"
)

// Recover converts panics into a JSON 500 envelope while logging the
// stack trace. It intentionally does not expose the panic value to
// clients - the full detail is captured server-side only.
func Recover(log zerolog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) (err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Bytes("stack", debug.Stack()).
					Str("path", c.Path()).
					Str("method", c.Method()).
					Str("request_id", RequestIDFromCtx(c)).
					Msg("panic recovered")
				err = httpx.RespondError(c, errs.Internal("panic", "internal server error"))
			}
		}()
		return c.Next()
	}
}

// RequestIDFromCtx safely extracts the request id set by RequestID().
func RequestIDFromCtx(c *fiber.Ctx) string {
	v := c.Locals(CtxKeyRequestID)
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}
