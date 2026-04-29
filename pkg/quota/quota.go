// Package quota adds per-tenant rate limiting on top of pkg/ratelimit.
//
// The regular `pkg/ratelimit.Limiter` enforces a single fixed
// (window, max) budget. That's fine for IP-based throttling, but for
// SaaS workloads one tenant can legitimately bring far more traffic
// than another, and the enterprise tier should get a larger budget
// than the free tier. quota.Limiter looks up the Policy for each
// (tenant, policy-name) pair at request time, so a free-tier tenant
// and an enterprise-tier tenant hitting the same route transparently
// see different ceilings.
//
// Keying: every decision is scoped by tenant ID, so one tenant
// burning through their budget cannot affect another. This is the
// minimum viable "fair sharing" — a misbehaving tenant is shed at
// 429 while healthy tenants' requests pass through unimpeded.
//
// quota.Limiter is a small wrapper; it delegates the sliding-window
// accounting to the same cache contract `pkg/ratelimit` already
// uses, so swapping between in-memory (single replica) and Redis
// (distributed) is a cache-construction concern, not a quota
// concern.
package quota

import (
	"context"
	"fmt"
	"time"

	"github.com/dedeez14/goforge/pkg/cache"
	"github.com/dedeez14/goforge/pkg/ratelimit"
	"github.com/dedeez14/goforge/pkg/tenant"
)

// Policy is the budget for one (tenant, policy-name) pair. Zero or
// negative Max disables the limit for that combination — a common
// pattern for the internal tier.
type Policy struct {
	// Window is the length of the bucket. If zero, the limiter
	// substitutes time.Minute (the same default pkg/ratelimit uses).
	Window time.Duration

	// Max is the budget per window. Values <= 0 mean "no limit" and
	// Allow returns Allowed=true with Limit=-1 and Remaining=-1
	// sentinels so caller headers are still populated and clients
	// can distinguish "no cap" from "cap not configured".
	Max int
}

// Unlimited is the sentinel Policy for tenants or tiers exempt from
// a given policy. Callers comparing budgets use (p.Max <= 0).
var Unlimited = Policy{Window: time.Minute, Max: 0}

// Provider resolves (tenant, name) → Policy. The name is the
// logical policy identifier ("api.requests", "emails.send"), not a
// tier or tenant string. Implementations typically maintain an
// in-memory map reloaded from the tenants table.
type Provider interface {
	Policy(ctx context.Context, t tenant.ID, name string) (Policy, error)
}

// Limiter enforces per-tenant quotas backed by a cache.Cache.
type Limiter struct {
	cache    cache.Cache
	prefix   string
	provider Provider
}

// New returns a Limiter backed by c. prefix is prepended to every
// cache key so one cache can host multiple limiters without
// collision (e.g. "quota:", "burst:"). A nil provider panics — the
// whole point of quota is tenant-specific policy.
func New(c cache.Cache, prefix string, p Provider) *Limiter {
	if c == nil {
		panic("quota: cache must not be nil")
	}
	if p == nil {
		panic("quota: provider must not be nil")
	}
	return &Limiter{cache: c, prefix: prefix, provider: p}
}

// Allow consumes one unit of budget against the (tenant, policy)
// pair. Unlimited policies always return Allowed=true; policies that
// fail to resolve propagate the Provider's error unchanged so
// middleware can decide whether to fail open or 500.
func (l *Limiter) Allow(ctx context.Context, t tenant.ID, policy string) (ratelimit.Decision, error) {
	if t.Empty() {
		return ratelimit.Decision{}, tenant.ErrMissing
	}
	p, err := l.provider.Policy(ctx, t, policy)
	if err != nil {
		return ratelimit.Decision{}, err
	}
	if p.Max <= 0 {
		// Unlimited: report a very large Limit so X-Ratelimit-*
		// headers still look sensible to well-behaved clients.
		return ratelimit.Decision{
			Allowed:   true,
			Limit:     -1,
			Remaining: -1,
			ResetIn:   p.normaliseWindow(),
		}, nil
	}
	window := p.normaliseWindow()
	now := time.Now().UTC()
	bucket := now.Truncate(window).Unix()
	// Key ordering: prefix → policy → tenant. Putting the policy
	// name before the tenant helps with per-policy metrics scraping
	// (grep the prefix+policy to see all tenants sharing a bucket).
	cacheKey := fmt.Sprintf("%s%s:%s:%d", l.prefix, policy, t, bucket)
	count, err := l.cache.Incr(ctx, cacheKey, 2*window)
	if err != nil {
		return ratelimit.Decision{}, err
	}
	resetIn := window - now.Sub(time.Unix(bucket, 0))
	remaining := p.Max - int(count)
	if remaining < 0 {
		remaining = 0
	}
	return ratelimit.Decision{
		Allowed:   count <= int64(p.Max),
		Limit:     p.Max,
		Remaining: remaining,
		ResetIn:   resetIn,
	}, nil
}

func (p Policy) normaliseWindow() time.Duration {
	if p.Window <= 0 {
		return time.Minute
	}
	return p.Window
}
