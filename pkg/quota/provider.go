package quota

import (
	"context"
	"fmt"

	"github.com/dedeez14/goforge/pkg/tenant"
)

// StaticProvider is a simple Provider where every tenant is mapped
// to a tier ("free", "pro", "enterprise"), and each tier has its
// own policy table. It is the sensible default for apps that hard-
// code a handful of plans; apps with per-tenant overrides should
// implement Provider themselves against their tenants table.
//
// StaticProvider is safe for concurrent readers AFTER construction
// because all maps are set in NewStaticProvider and never mutated.
// Apps that need live updates should swap out the whole provider
// (e.g. via an atomic.Value) rather than mutate in place.
type StaticProvider struct {
	// TierOf decides which tier a tenant belongs to. Required.
	// Implementations typically read from a cached tenants table;
	// returning the empty string means "fall back to DefaultTier".
	TierOf func(tenant.ID) string

	// DefaultTier names the fallback tier used when TierOf returns
	// "" or a tier that is not present in Policies. Required.
	DefaultTier string

	// Policies maps tier → policy name → Policy. Missing entries
	// fall back to the same policy in DefaultTier; still missing →
	// Policy{Max: 0} (Unlimited) so an unknown policy does not
	// accidentally hard-cap a tenant.
	Policies map[string]map[string]Policy
}

// Policy implements the Provider interface.
func (s *StaticProvider) Policy(_ context.Context, t tenant.ID, name string) (Policy, error) {
	if s == nil || s.TierOf == nil {
		return Policy{}, fmt.Errorf("quota: StaticProvider is misconfigured")
	}
	tier := s.TierOf(t)
	if tier == "" {
		tier = s.DefaultTier
	}
	if p, ok := lookup(s.Policies, tier, name); ok {
		return p, nil
	}
	if p, ok := lookup(s.Policies, s.DefaultTier, name); ok {
		return p, nil
	}
	// Unknown policy: err on the side of not hard-capping a tenant.
	return Unlimited, nil
}

func lookup(m map[string]map[string]Policy, tier, name string) (Policy, bool) {
	if m == nil {
		return Policy{}, false
	}
	t, ok := m[tier]
	if !ok {
		return Policy{}, false
	}
	p, ok := t[name]
	return p, ok
}

// ProviderFunc adapts a bare function into a Provider. Useful for
// tests and for apps that source their quota from a live table.
type ProviderFunc func(ctx context.Context, t tenant.ID, name string) (Policy, error)

// Policy implements the Provider interface.
func (f ProviderFunc) Policy(ctx context.Context, t tenant.ID, name string) (Policy, error) {
	return f(ctx, t, name)
}
