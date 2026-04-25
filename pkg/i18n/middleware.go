package i18n

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Middleware reads the Accept-Language header and attaches both the
// resolved locale and the supplied bundle to c.UserContext(). Every
// downstream handler can then call i18n.T(c.UserContext(), code,
// fallback) to translate without needing the bundle as a parameter.
//
// Supported locales are matched via primary subtag only ("en-US" →
// "en") against the set passed in. When no header is present, or no
// language matches, the default locale is stored.
//
// Passing a nil bundle is a no-op apart from the locale resolution,
// which is harmless: T() with no bundle on ctx returns the supplied
// fallback message unchanged.
func Middleware(bundle *Bundle, defaultL Locale, supported ...Locale) fiber.Handler {
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
		ctx := WithLocale(c.UserContext(), chosen)
		if bundle != nil {
			ctx = WithBundle(ctx, bundle)
		}
		c.SetUserContext(ctx)
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
