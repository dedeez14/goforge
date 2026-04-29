package quota

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/pkg/tenant"
)

// FiberMiddleware enforces a named quota on every request. The
// tenant is read from the request context (install pkg/tenant's
// Middleware upstream), so the guard is per-tenant automatically.
//
// On a blocked request, the middleware responds 429 with the
// quota.exceeded envelope and the standard X-Ratelimit-* headers.
// On a cache outage the request is allowed through — a quota miss
// is vastly preferable to an outage storm.
//
// For routes that must always carry a tenant (most business
// endpoints) install tenant.Middleware before this. For routes
// where the tenant is optional, the middleware falls back to the
// limiter's own policy keyed by a synthetic anonymous tenant so
// unauthenticated traffic is at least coarse-grained bucketed.
func FiberMiddleware(l *Limiter, policy string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		t, ok := tenant.FromContext(c.UserContext())
		if !ok {
			t = tenant.ID("_anonymous")
		}
		decision, err := l.Allow(c.UserContext(), t, policy)
		if err != nil {
			// Cache/provider outage must not 503 every request —
			// let it through and rely on other defences. The
			// operator sees the error via the Provider's own logs.
			return c.Next()
		}
		// Always emit X-Ratelimit-* headers, including the -1
		// sentinel for Unlimited policies, so well-behaved clients
		// can tell the difference between "no cap" and "cap not
		// configured". This matches pkg/ratelimit and the behaviour
		// documented in docs/quota.md.
		c.Set("X-Ratelimit-Limit", strconv.Itoa(decision.Limit))
		c.Set("X-Ratelimit-Remaining", strconv.Itoa(decision.Remaining))
		c.Set("X-Ratelimit-Reset", strconv.FormatInt(int64(decision.ResetIn/time.Second), 10))
		if !decision.Allowed {
			c.Set("Retry-After", strconv.FormatInt(int64(decision.ResetIn/time.Second)+1, 10))
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"success": false,
				"error": fiber.Map{
					"code":    "quota.exceeded",
					"message": "tenant quota exceeded; retry after window resets",
				},
			})
		}
		return c.Next()
	}
}
