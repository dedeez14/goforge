# Per-tenant quota (`pkg/quota`)

`pkg/ratelimit` enforces a single (window, max) budget per key —
good for coarse IP throttling. `pkg/quota` extends this so each
tenant gets its own budget, sized by whatever tier they are on
("free", "pro", "enterprise", …). A misbehaving tenant is shed at
429; healthy tenants' traffic is unaffected.

## Why this exists

In a SaaS a single tenant can legitimately generate far more traffic
than another. A shared rate limit either under-limits paying
customers (revenue risk) or over-limits them (reliability risk).
Per-tenant quotas solve both.

They are also the minimum viable "fair sharing" defence: a tenant
stuck in an import loop cannot starve the rest of the platform.

## Shape

- `Policy{Window, Max}` — one bucket definition.
- `Provider.Policy(ctx, tenantID, name) (Policy, error)` — resolves
  a Policy for a (tenant, policy-name) pair.
- `Limiter.Allow(ctx, tenantID, policyName)` — charges one unit.
- `FiberMiddleware(limiter, policyName)` — the 429-enforcing
  middleware for HTTP routes.

## Quick start

```go
provider := &quota.StaticProvider{
    TierOf:      tenantTier, // func(tenant.ID) string, typically reads from a cached tenants table
    DefaultTier: "free",
    Policies: map[string]map[string]quota.Policy{
        "free": {
            "api.requests": {Window: time.Minute, Max: 60},
            "emails.send":  {Window: time.Hour,   Max: 50},
        },
        "pro": {
            "api.requests": {Window: time.Minute, Max: 600},
            "emails.send":  {Window: time.Hour,   Max: 5_000},
        },
        "enterprise": {
            "api.requests": quota.Unlimited,
            "emails.send":  quota.Unlimited,
        },
    },
}

limiter := quota.New(redisCache, "q:", provider)

// Wire into a route (after tenant.Middleware):
authed := app.Group("/api/v1", tenant.Middleware(tenant.HeaderResolver("X-Tenant-ID")))
authed.Use(quota.FiberMiddleware(limiter, "api.requests"))

// …or guard an expensive endpoint specifically:
authed.Post("/emails", quota.FiberMiddleware(limiter, "emails.send"), h.SendEmail)
```

## Semantics

- **Keying**: `prefix:policyName:tenantID:bucket`. Two tenants on
  the same route never share a counter.
- **Anonymous traffic**: if no tenant is on the context, the
  middleware falls back to tenant ID `_anonymous`. This gives you a
  coarse bucket for unauthenticated pre-login traffic instead of
  letting it pass unmetered.
- **Unlimited policies** (`Max: 0`) always return `Allowed=true`.
  The response headers carry `-1` sentinels so well-behaved
  clients can detect the no-cap state.
- **Unknown policy names** fall back to unlimited rather than
  hard-capping. This prevents a forgotten-config outage where a
  newly-deployed route suddenly blocks all traffic because no
  policy exists yet.
- **Missing tier** (TierOf returns `""` or an unknown string) falls
  back to `DefaultTier`. Keep the default conservative.
- **Fail-open on provider/cache error**: better a quota miss than
  an outage storm. The provider's own logs surface the issue.

## Composition with other limits

`pkg/quota` is not a replacement for `pkg/ratelimit` — it complements
it. Stack both, in this order:

```go
app.Use(ratelimit.FiberMiddleware(ipLimiter, nil))                      // 1. anti-abuse
app.Use(tenant.Middleware(tenant.HeaderResolver("X-Tenant-ID")))        // 2. resolve tenant
app.Use(quota.FiberMiddleware(tenantLimiter, "api.requests"))           // 3. per-tenant fair share
```

- The IP limiter sheds brute-force / scraping traffic before you
  ever look at the tenant.
- The per-tenant quota shares the surviving traffic fairly across
  paying customers.

## Live updates

`StaticProvider` is immutable after construction. To reload policy
tables (e.g. after editing the tenants table in the admin UI), swap
the entire provider atomically:

```go
var providerPtr atomic.Pointer[quota.StaticProvider]
providerPtr.Store(loadFromDB())

// The wrapper passed to New reads from the atomic pointer on every call.
providerWrapper := quota.ProviderFunc(func(ctx context.Context, t tenant.ID, name string) (quota.Policy, error) {
    return providerPtr.Load().Policy(ctx, t, name)
})

limiter := quota.New(cache, "q:", providerWrapper)
```

## Observability

- Cache key pattern begins with the policy name, so metric scrapers
  can grep `q:api.requests:` to count tenants sharing the bucket.
- Blocked requests log at the HTTP layer via the standard access
  log (status 429). Count by tenant to spot noisy neighbours.
- `Decision.Remaining` on every Allow call is a natural gauge for
  a Prometheus histogram keyed by (tenant, policy).
