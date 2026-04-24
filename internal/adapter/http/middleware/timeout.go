package middleware

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Timeout attaches a deadline to the fiber user context so downstream
// database/HTTP calls can honour cancellation. Fiber will still wait
// for the handler to return; callers must check ctx.Err().
func Timeout(d time.Duration) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.UserContext(), d)
		defer cancel()
		c.SetUserContext(ctx)
		return c.Next()
	}
}
