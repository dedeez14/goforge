// Package tenant exposes the helpers goforge applications use to
// implement multi-tenancy without sprinkling tenant_id checks across
// the codebase.
//
// The model is row-level isolation by default: every tenant-aware
// table carries a `tenant_id` column, the HTTP middleware extracts the
// tenant from the request (header, subdomain or claim), and a
// repository decorator forces every read or write to be scoped to that
// tenant. Use-cases call repositories normally - the framework refuses
// to leak rows across tenants by construction.
package tenant

import (
	"context"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/pkg/errs"
	"github.com/dedeez14/goforge/pkg/events"
)

// ID is the canonical tenant identifier type. Using a named string
// keeps raw strings from being passed where a tenant is required.
type ID string

// String returns the underlying string form of the tenant ID.
func (t ID) String() string { return string(t) }

// Empty reports whether the tenant ID is the zero value. Operations
// that require a tenant should reject ID("").
func (t ID) Empty() bool { return string(t) == "" }

type tenantContextKey struct{}

// FromContext returns the tenant ID stored on ctx and whether one was
// present. Callers that require a tenant should use Require instead.
func FromContext(ctx context.Context) (ID, bool) {
	if v, ok := ctx.Value(tenantContextKey{}).(ID); ok && !v.Empty() {
		return v, true
	}
	return "", false
}

// Require returns the tenant ID stored on ctx or ErrMissing when none
// is present. The error already carries a stable code so callers can
// return it directly from a handler.
func Require(ctx context.Context) (ID, error) {
	if id, ok := FromContext(ctx); ok {
		return id, nil
	}
	return "", ErrMissing
}

// WithID returns a derived context that carries tenant. The events
// package picks up the same key automatically so domain events emitted
// from the returned context inherit the tenant.
func WithID(ctx context.Context, tenant ID) context.Context {
	ctx = context.WithValue(ctx, tenantContextKey{}, tenant)
	return events.WithTenant(ctx, tenant.String())
}

// ErrMissing indicates the request did not carry a tenant identifier
// even though the route requires one. The wrapping *errs.Error returns
// 401 Unauthorized so the client knows to authenticate properly.
var ErrMissing = errs.Unauthorized("tenant.missing", "tenant context is required for this operation")

// Resolver decides which tenant a request belongs to. The default
// implementation reads X-Tenant-ID; applications can swap in subdomain
// or JWT-claim resolvers.
type Resolver func(c *fiber.Ctx) (ID, error)

// MaxTenantIDLength bounds the size of an accepted tenant identifier.
// Real tenant IDs are short (UUID, slug, ULID); rejecting anything
// longer prevents memory amplification when an attacker sends a
// gigantic X-Tenant-ID hoping it propagates into a context value, a
// log line, or a downstream key.
const MaxTenantIDLength = 128

// ErrInvalid is returned by the default HeaderResolver when the supplied
// tenant identifier is too long or contains characters that have no
// business in a tenant id (whitespace inside, control bytes, …). The
// caller's HTTP layer maps this to 400 Bad Request.
var ErrInvalid = errs.InvalidInput("tenant.invalid", "invalid tenant identifier")

// HeaderResolver returns a Resolver that reads the tenant ID from the
// configured HTTP header. Empty values yield ErrMissing; values that
// exceed MaxTenantIDLength or contain illegal characters yield
// ErrInvalid.
func HeaderResolver(header string) Resolver {
	header = strings.TrimSpace(header)
	if header == "" {
		header = "X-Tenant-ID"
	}
	return func(c *fiber.Ctx) (ID, error) {
		raw := strings.TrimSpace(c.Get(header))
		if raw == "" {
			return "", ErrMissing
		}
		if len(raw) > MaxTenantIDLength {
			return "", ErrInvalid
		}
		for i := 0; i < len(raw); i++ {
			ch := raw[i]
			if ch < 0x20 || ch == 0x7f {
				return "", ErrInvalid
			}
		}
		return ID(raw), nil
	}
}

// Middleware injects the resolved tenant ID into the request context.
// Routes mounted behind it are guaranteed to see a non-empty tenant or
// an immediate 401 response.
func Middleware(resolve Resolver) fiber.Handler {
	if resolve == nil {
		resolve = HeaderResolver("X-Tenant-ID")
	}
	return func(c *fiber.Ctx) error {
		id, err := resolve(c)
		if err != nil {
			return err
		}
		c.SetUserContext(WithID(c.UserContext(), id))
		return c.Next()
	}
}

// OptionalMiddleware behaves like Middleware but never rejects a
// request. When the resolver returns a tenant the context is updated
// the same way; when it returns ErrMissing (or any other resolver
// error) the request is forwarded as-is so downstream handlers can
// decide whether the tenant is required for that particular path.
//
// It exists for endpoints that benefit from tenant scoping when one is
// present (e.g. the realtime SSE bridge filtering by tenant) but
// should still serve clients running in single-tenant mode.
func OptionalMiddleware(resolve Resolver) fiber.Handler {
	if resolve == nil {
		resolve = HeaderResolver("X-Tenant-ID")
	}
	return func(c *fiber.Ctx) error {
		if id, err := resolve(c); err == nil && !id.Empty() {
			c.SetUserContext(WithID(c.UserContext(), id))
		}
		return c.Next()
	}
}
