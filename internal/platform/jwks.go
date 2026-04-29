package platform

import (
	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/internal/infrastructure/security"
	"github.com/dedeez14/goforge/pkg/httpcache"
)

// MountJWKS exposes the issuer's public key set at
// `/.well-known/jwks.json`. The endpoint is unauthenticated and CORS-
// friendly because that is exactly what RFC 7517 says it must be.
//
// For HS256 issuers (the framework default) the response is an empty
// `{"keys":[]}` set: HMAC secrets are symmetric and must never appear
// in a JWKS document. Returning the well-known empty document is
// preferable to a 404 because clients (e.g. an API gateway configured
// to fetch keys at startup) get a deterministic answer instead of a
// retry storm.
//
// Issuers that hold asymmetric material (RS256, EdDSA) implement
// security.PublicKeySetProvider and have their actual public keys
// returned here.
func MountJWKS(app *fiber.App, issuer security.TokenIssuer) {
	// JWKS is a classic conditional-GET target: API gateways and
	// downstream services poll it to refresh their verification key
	// sets, and the document only changes during a key rotation
	// (minutes to months apart). Pairing max-age=300 with a strong
	// ETag means steady-state polls return 304 with no body.
	cache := httpcache.New(httpcache.Options{MaxAge: 300, Public: true, MustRevalidate: true})
	app.Get("/.well-known/jwks.json", cache, func(c *fiber.Ctx) error {
		jwks := security.JWKS{Keys: []security.JWK{}}
		if p, ok := issuer.(security.PublicKeySetProvider); ok {
			jwks = p.PublicKeySet()
		}
		return c.Status(fiber.StatusOK).JSON(jwks)
	})
}
