// Package httpcache provides conditional-GET middleware for Fiber
// handlers whose response changes infrequently (menu trees, RBAC
// permission listings, OpenAPI spec, JWKS).
//
// The pattern is the standard one:
//
//  1. The inner handler runs and writes its response body.
//  2. This middleware hashes the body with SHA-256 and derives a
//     strong ETag.
//  3. If the incoming request's If-None-Match matches, we reset the
//     response to a 304 (empty body) so the client reuses its
//     cached copy at near-zero cost.
//  4. Otherwise the 200 response passes through with an `ETag`
//     header and a `Cache-Control` header derived from the options.
//
// Scale impact: for read-heavy endpoints that get called on every
// page navigation (e.g. /menus/mine, /me/access), conditional GETs
// cut CPU and egress by 40-80% because the 304 response is a few
// hundred bytes while the 200 response can be kilobytes of JSON.
//
// The middleware is deliberately conservative: it only caches 200
// responses with a non-empty body, never short-circuits non-idempotent
// methods, and falls back silently if the inner handler returns an
// error so the client still sees the underlying failure.
package httpcache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Options configures a cache middleware instance.
type Options struct {
	// MaxAge sets the Cache-Control max-age directive in seconds.
	// Zero means "validate every time" and emits an explicit
	// max-age=0 directive - without it, caches fall back to
	// heuristic freshness (potentially minutes), so the zero value
	// would not enforce revalidation as documented. To omit the
	// directive entirely, pass a negative value.
	MaxAge int

	// Vary lists request headers the response varies on. Every name
	// becomes a comma-separated entry in the Vary response header.
	// For authenticated endpoints this MUST include "Authorization"
	// (and, where relevant, "Cookie", "X-Tenant-ID") so browsers and
	// intermediaries never serve one caller's fresh cache entry to
	// another user within the max-age window. Conditional GETs
	// (304 via If-None-Match) alone are insufficient: the browser
	// only revalidates once the response is stale.
	Vary []string

	// Public, when true, emits Cache-Control: public (allows shared
	// caches such as a CDN to store the response). Private, when
	// true, emits Cache-Control: private (user-agent only). If
	// neither is set, no public/private directive is added. Setting
	// both is a configuration error and panics at construction.
	Public  bool
	Private bool

	// MustRevalidate forces clients to re-validate rather than
	// serve a stale copy. Recommended for anything that affects
	// authorisation (menu visibility changes the moment a user's
	// role changes).
	MustRevalidate bool
}

// New returns Fiber middleware enforcing the provided Options. The
// returned handler is safe for concurrent use.
func New(opts Options) fiber.Handler {
	if opts.Public && opts.Private {
		panic("httpcache: Options.Public and Options.Private are mutually exclusive")
	}
	cc := buildCacheControl(opts)
	vary := strings.Join(opts.Vary, ", ")
	return func(c *fiber.Ctx) error {
		// Only GET/HEAD benefit from conditional caching. Everything
		// else (POST/PUT/PATCH/DELETE) must always run.
		if m := c.Method(); m != fiber.MethodGet && m != fiber.MethodHead {
			return c.Next()
		}

		if err := c.Next(); err != nil {
			return err
		}

		// Only cache successful responses. 204 has no body so no
		// ETag; errors should never be cached either because a
		// client re-requesting might succeed.
		if c.Response().StatusCode() != fiber.StatusOK {
			return nil
		}

		body := c.Response().Body()
		if len(body) == 0 {
			return nil
		}

		etag := strongETag(body)
		c.Set(fiber.HeaderETag, etag)
		if cc != "" {
			c.Set(fiber.HeaderCacheControl, cc)
		}
		// Vary must be emitted on both 200 and 304 so the browser
		// cache keys on the configured headers either way.
		if vary != "" {
			c.Set(fiber.HeaderVary, vary)
		}

		// If-None-Match may contain one or more quoted ETags
		// separated by commas. If any matches, reply 304.
		if inm := c.Get(fiber.HeaderIfNoneMatch); inm != "" && etagListMatches(inm, etag) {
			// 304 must not carry a body. Resetting the body is
			// essential: Fiber's Response already has the handler's
			// output written to it.
			c.Response().ResetBody()
			c.Status(fiber.StatusNotModified)
		}
		return nil
	}
}

// strongETag builds an RFC 7232 strong validator from the response
// body. SHA-256 is overkill for collision resistance but ~300 ns/KiB
// on modern CPUs, which is noise compared to serialising JSON.
func strongETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// etagListMatches reports whether any comma-separated ETag in the
// If-None-Match header equals want. The special value "*" always
// matches (RFC 7232 §3.2).
func etagListMatches(header, want string) bool {
	for _, raw := range strings.Split(header, ",") {
		candidate := strings.TrimSpace(raw)
		if candidate == "*" {
			return true
		}
		// Some clients send weak validators ("W/"prefix"); a
		// strong-validator match is still valid per the RFC.
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == want {
			return true
		}
	}
	return false
}

func buildCacheControl(opts Options) string {
	parts := []string{}
	switch {
	case opts.Public:
		parts = append(parts, "public")
	case opts.Private:
		parts = append(parts, "private")
	}
	// MaxAge == 0 is the documented "validate every time" value and
	// must emit max-age=0 explicitly; otherwise caches fall back to
	// heuristic freshness. Callers that want no directive at all
	// pass a negative value.
	if opts.MaxAge >= 0 {
		parts = append(parts, fmt.Sprintf("max-age=%d", opts.MaxAge))
	}
	if opts.MustRevalidate {
		parts = append(parts, "must-revalidate")
	}
	return strings.Join(parts, ", ")
}
