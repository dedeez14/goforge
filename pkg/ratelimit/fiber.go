package ratelimit

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
)

// KeyFunc returns the limiter key for a request. The default uses
// the (proxy-aware) client IP; per-route limiters often combine the
// IP with the authenticated user id or the requested path.
type KeyFunc func(c *fiber.Ctx) string

// DefaultKey returns the originating client's IP.
func DefaultKey(c *fiber.Ctx) string { return c.IP() }

// FiberMiddleware enforces the limiter on every request. Blocked
// requests get a JSON 429 response and the standard rate-limit
// headers; allowed requests get the headers too so well-behaved
// clients can self-throttle before they get blocked.
//
// We deliberately do NOT include the policy name in the response
// headers — exposing per-policy keys helps attackers map our limits.
func FiberMiddleware(l *Limiter, key KeyFunc) fiber.Handler {
	if key == nil {
		key = DefaultKey
	}
	return func(c *fiber.Ctx) error {
		decision, err := l.Allow(c.UserContext(), key(c))
		if err != nil {
			// Cache outage must not 503 every request — let the
			// request through and rely on other defences.
			return c.Next()
		}
		c.Set("X-Ratelimit-Limit", strconv.Itoa(decision.Limit))
		c.Set("X-Ratelimit-Remaining", strconv.Itoa(decision.Remaining))
		c.Set("X-Ratelimit-Reset", strconv.FormatInt(int64(decision.ResetIn/time.Second), 10))
		if !decision.Allowed {
			c.Set("Retry-After", strconv.FormatInt(int64(decision.ResetIn/time.Second)+1, 10))
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"success": false,
				"error": fiber.Map{
					"code":    "ratelimit.exceeded",
					"message": "rate limit exceeded; retry after window resets",
				},
			})
		}
		return c.Next()
	}
}
