// Package middleware centralises Fiber middlewares used across the app.
package middleware

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const (
	// HeaderRequestID is the canonical header name for request tracing.
	HeaderRequestID = "X-Request-ID"
	// CtxKeyRequestID is the Fiber Locals key holding the current request id.
	CtxKeyRequestID = "requestid"
)

// RequestID ensures every request has a stable X-Request-ID for logs,
// downstream calls, and client debugging. Existing ids are trusted and
// echoed back; otherwise a uuid v4 is generated.
func RequestID() fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Get(HeaderRequestID)
		if id == "" {
			id = uuid.NewString()
		}
		c.Locals(CtxKeyRequestID, id)
		c.Set(HeaderRequestID, id)
		return c.Next()
	}
}
