package i18n

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Middleware reads the Accept-Language header and stores the first
// supported locale on the request context. Supported locales are
// matched via primary subtag only ("en-US" → "en") against the set
// passed in. When no header is present, or no language matches, the
// default locale is stored.
//
// The middleware is independent of any specific Bundle so the same
// instance can serve multiple bundles in tests.
func Middleware(defaultL Locale, supported ...Locale) fiber.Handler {
	allow := make(map[Locale]struct{}, len(supported)+1)
	for _, l := range supported {
		allow[l.Normalise()] = struct{}{}
	}
	allow[defaultL.Normalise()] = struct{}{}
	def := defaultL.Normalise()

	return func(c *fiber.Ctx) error {
		raw := c.Get(fiber.HeaderAcceptLanguage)
		chosen := def
		if raw != "" {
			if l := pickLocale(raw, allow); l != "" {
				chosen = l
			}
		}
		c.SetUserContext(WithLocale(c.UserContext(), chosen))
		return c.Next()
	}
}

// pickLocale parses an Accept-Language header and returns the first
// supported locale, or "" when none match. q-values are honoured in
// declaration order; the parser intentionally ignores numeric q
// values to keep the dependency footprint zero — Accept-Language
// nearly always lists the user's preferred language first anyway.
func pickLocale(header string, allow map[Locale]struct{}) Locale {
	for _, item := range strings.Split(header, ",") {
		item = strings.TrimSpace(item)
		if i := strings.Index(item, ";"); i >= 0 {
			item = item[:i]
		}
		if item == "" || item == "*" {
			continue
		}
		l := Locale(item).Normalise()
		if _, ok := allow[l]; ok {
			return l
		}
	}
	return ""
}
