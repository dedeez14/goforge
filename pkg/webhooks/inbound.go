package webhooks

import (
	"bytes"
	"errors"
	"io"

	"github.com/gofiber/fiber/v2"
)

// SecretLookup returns the signing secret for a given event/path
// pair. Receivers register one per integration ("github", "stripe",
// etc.). Returning ("", ok=false) marks the request as unauthorised.
type SecretLookup func(c *fiber.Ctx) (secret, eventID string, ok bool)

// InboundVerifier is the Fiber middleware that validates incoming
// webhook signatures. It is tolerant of providers that use a
// different header name — pass HeaderName, defaults to
// "Webhook-Signature".
type InboundVerifier struct {
	Lookup     SecretLookup
	HeaderName string
}

// Middleware returns the Fiber handler. The caller MUST mount it on
// a route with a body limit large enough to cover the provider's
// largest payload; goforge's default of 1 MiB suits Stripe/GitHub
// but is small for image-bearing providers.
func (v InboundVerifier) Middleware() fiber.Handler {
	header := v.HeaderName
	if header == "" {
		header = SignatureHeader
	}
	return func(c *fiber.Ctx) error {
		secret, eventID, ok := v.Lookup(c)
		if !ok || secret == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"success": false, "error": fiber.Map{
					"code":    "webhook.no_secret",
					"message": "no signing secret configured for this endpoint",
				},
			})
		}
		sig := c.Get(header)
		if sig == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"success": false, "error": fiber.Map{
					"code":    "webhook.missing_signature",
					"message": "signature header missing",
				},
			})
		}
		body := c.Body()
		// fiber.Ctx().Body() returns a slice into a fasthttp
		// buffer; we copy because subsequent handlers may consume
		// it via BodyParser.
		bodyCopy, err := io.ReadAll(bytes.NewReader(body))
		if err != nil {
			return err
		}
		if err := VerifySignature(secret, eventID, bodyCopy, sig); err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"success": false, "error": fiber.Map{
					"code":    "webhook.invalid_signature",
					"message": err.Error(),
				},
			})
		}
		return c.Next()
	}
}

// Errors that callers may want to map to specific HTTP responses.
var (
	ErrUnauthorised = errors.New("webhooks: unauthorised inbound request")
)
