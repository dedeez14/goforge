package authz

import (
	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/pkg/tenant"
)

// SubjectExtractor reads the subject identifier from a request. The
// default uses Locals("subject") (set by goforge's auth middleware
// to the user id); override for service-to-service calls.
type SubjectExtractor func(c *fiber.Ctx) string

// DefaultSubject reads "subject" from c.Locals.
func DefaultSubject(c *fiber.Ctx) string {
	if v, ok := c.Locals("subject").(string); ok {
		return v
	}
	return ""
}

// Require returns a Fiber middleware that allows the request only
// when Allow(subject, tenant, object, action) is true. action and
// object are typically literal strings ("write", "users:profile");
// for path-based resources they can include URL params and use
// Casbin's keyMatch2 (the default model already does).
func Require(e Authorizer, sub SubjectExtractor, object, action string) fiber.Handler {
	if sub == nil {
		sub = DefaultSubject
	}
	return func(c *fiber.Ctx) error {
		s := sub(c)
		if s == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"success": false, "error": fiber.Map{
					"code":    "authz.no_subject",
					"message": "no authenticated subject on the request",
				},
			})
		}
		dom, _ := tenant.FromContext(c.UserContext())
		ok, err := e.Allow(c.UserContext(), s, string(dom), object, action)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"success": false, "error": fiber.Map{
					"code":    "authz.error",
					"message": err.Error(),
				},
			})
		}
		if !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"success": false, "error": fiber.Map{
					"code":    "authz.denied",
					"message": "permission denied",
				},
			})
		}
		return c.Next()
	}
}
