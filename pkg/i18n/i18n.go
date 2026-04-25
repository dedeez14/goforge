// Package i18n provides simple, dependency-free message translation
// for goforge error codes and validator messages. The design favours
// determinism, zero allocations on the hot path, and strict
// dependency injection (no package-level mutable state):
//
//   - A Bundle is an immutable map of (code, locale) → message,
//     constructed once at the composition root and read concurrently
//     afterwards.
//   - The bundle and active locale travel on the request context —
//     written by Middleware (or by manual calls to WithBundle /
//     WithLocale), read by every translation site.
//   - T(ctx, code, fallback) returns the translated message or the
//     fallback when no bundle was attached to ctx, no locale was
//     resolved, or no entry was registered for that code.
//
// Apps that don't use i18n simply never call WithBundle/Middleware;
// every T(...) call returns the supplied fallback. There is no
// global state to leak across tests or boot orderings.
//
// The package intentionally does not cover RTL, plurals, gender or
// number formatting. Use a richer library (golang.org/x/text/message,
// nicksnyder/go-i18n) when those are required.
package i18n

import (
	"context"
	"strings"
	"sync"
)

// Locale is an IETF BCP-47 language tag, simplified — we only ever
// match the primary subtag (the part before the first "-"). "en-US"
// and "en-GB" both reduce to "en".
type Locale string

// Common shipped locales. Apps may register more.
const (
	LocaleEN Locale = "en"
	LocaleID Locale = "id"
)

// Normalise lowercases and strips region/script subtags.
func (l Locale) Normalise() Locale {
	s := strings.ToLower(string(l))
	if i := strings.IndexAny(s, "-_"); i >= 0 {
		s = s[:i]
	}
	return Locale(s)
}

// Bundle is an immutable code→locale→message catalogue. Construct
// it via NewBundle then never mutate.
type Bundle struct {
	mu       sync.RWMutex
	defaultL Locale
	messages map[string]map[Locale]string
}

// NewBundle returns an empty Bundle with the given default locale.
// The default is used when no entry exists for the requested locale.
func NewBundle(defaultL Locale) *Bundle {
	return &Bundle{
		defaultL: defaultL.Normalise(),
		messages: make(map[string]map[Locale]string),
	}
}

// Add registers a translation for (code, locale). Calling Add after
// boot is supported but discouraged — bundles are designed to be
// frozen for the lifetime of the process.
func (b *Bundle) Add(code string, locale Locale, msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.messages[code] == nil {
		b.messages[code] = make(map[Locale]string)
	}
	b.messages[code][locale.Normalise()] = msg
}

// AddMany is a convenience for registering all locales of a single
// code in one call.
func (b *Bundle) AddMany(code string, byLocale map[Locale]string) {
	for l, m := range byLocale {
		b.Add(code, l, m)
	}
}

// Lookup returns the message for (code, locale) and a boolean
// indicating whether an entry was found. Falls back to the bundle's
// default locale when the requested locale is missing.
func (b *Bundle) Lookup(code string, locale Locale) (string, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	entries, ok := b.messages[code]
	if !ok {
		return "", false
	}
	if msg, ok := entries[locale.Normalise()]; ok {
		return msg, true
	}
	if msg, ok := entries[b.defaultL]; ok {
		return msg, true
	}
	return "", false
}

// DefaultLocale exposes the bundle's fallback locale.
func (b *Bundle) DefaultLocale() Locale { return b.defaultL }

// ----------------------------------------------------------------
// context-based bundle + locale routing

type (
	bundleCtxKey struct{}
	localeCtxKey struct{}
)

// WithBundle attaches a *Bundle to ctx so downstream T(...) calls
// can translate without taking a Bundle parameter.
func WithBundle(ctx context.Context, b *Bundle) context.Context {
	if b == nil {
		return ctx
	}
	return context.WithValue(ctx, bundleCtxKey{}, b)
}

// BundleFromContext returns the bundle attached to ctx, or nil when
// none was set. Callers must handle the nil case.
func BundleFromContext(ctx context.Context) *Bundle {
	if b, ok := ctx.Value(bundleCtxKey{}).(*Bundle); ok {
		return b
	}
	return nil
}

// WithLocale stores the active locale on ctx. Use in middleware or
// at the start of a job/CLI invocation.
func WithLocale(ctx context.Context, l Locale) context.Context {
	return context.WithValue(ctx, localeCtxKey{}, l.Normalise())
}

// FromContext returns the locale stored on ctx, or "" when none was
// set. Callers should treat "" as "use bundle default".
func FromContext(ctx context.Context) Locale {
	if v, ok := ctx.Value(localeCtxKey{}).(Locale); ok {
		return v
	}
	return ""
}

// T translates code into the locale attached to ctx, falling back
// to the bundle's default locale, then to the supplied fallback
// when no entry is registered. Returns fallback unchanged when no
// bundle is attached to ctx — apps that don't wire i18n keep their
// existing English messages.
func T(ctx context.Context, code, fallback string) string {
	b := BundleFromContext(ctx)
	if b == nil {
		return fallback
	}
	if msg, ok := b.Lookup(code, FromContext(ctx)); ok {
		return msg
	}
	return fallback
}
