package quota

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dedeez14/goforge/pkg/cache"
	"github.com/dedeez14/goforge/pkg/tenant"
)

func staticPolicies() *StaticProvider {
	return &StaticProvider{
		TierOf: func(t tenant.ID) string {
			switch t {
			case "ent":
				return "enterprise"
			case "free":
				return "free"
			}
			return ""
		},
		DefaultTier: "free",
		Policies: map[string]map[string]Policy{
			"free": {
				"api.requests": {Window: time.Minute, Max: 2},
			},
			"enterprise": {
				"api.requests": {Window: time.Minute, Max: 100},
			},
		},
	}
}

func TestLimiter_PerTierBudget(t *testing.T) {
	t.Parallel()
	l := New(cache.NewMemory(), "q:", staticPolicies())
	ctx := context.Background()

	// Free tier: 2 requests allowed, third blocked.
	for i := 0; i < 2; i++ {
		d, err := l.Allow(ctx, "free", "api.requests")
		if err != nil || !d.Allowed {
			t.Fatalf("attempt %d: %v allowed=%v", i, err, d.Allowed)
		}
	}
	d, _ := l.Allow(ctx, "free", "api.requests")
	if d.Allowed {
		t.Fatal("3rd request on free tier must be blocked")
	}

	// Enterprise tier: unaffected by free tenant's exhaustion.
	d, _ = l.Allow(ctx, "ent", "api.requests")
	if !d.Allowed || d.Limit != 100 {
		t.Fatalf("ent tenant blocked or wrong limit: allowed=%v limit=%d", d.Allowed, d.Limit)
	}
}

func TestLimiter_TenantsAreIsolated(t *testing.T) {
	t.Parallel()
	l := New(cache.NewMemory(), "q:", staticPolicies())
	ctx := context.Background()
	// tenant A exhausts
	_, _ = l.Allow(ctx, "free", "api.requests")
	_, _ = l.Allow(ctx, "free", "api.requests")
	// tenant B (resolved to free tier because TierOf returns "" →
	// DefaultTier) gets its own budget.
	d, _ := l.Allow(ctx, "some-other-tenant", "api.requests")
	if !d.Allowed {
		t.Fatal("different tenant must have an independent bucket")
	}
}

func TestLimiter_UnlimitedPolicy(t *testing.T) {
	t.Parallel()
	sp := &StaticProvider{
		TierOf:      func(tenant.ID) string { return "internal" },
		DefaultTier: "internal",
		Policies: map[string]map[string]Policy{
			"internal": {"api.requests": {Max: 0}},
		},
	}
	l := New(cache.NewMemory(), "q:", sp)
	for i := 0; i < 1000; i++ {
		d, err := l.Allow(context.Background(), "anyone", "api.requests")
		if err != nil || !d.Allowed {
			t.Fatalf("unlimited policy must always allow, got err=%v allowed=%v", err, d.Allowed)
		}
	}
}

func TestLimiter_UnknownPolicyFallsBackToUnlimited(t *testing.T) {
	t.Parallel()
	l := New(cache.NewMemory(), "q:", staticPolicies())
	// Policy name that does not exist in any tier: should not hard-
	// cap the tenant just because we forgot to configure it.
	for i := 0; i < 50; i++ {
		d, err := l.Allow(context.Background(), "free", "unknown.policy")
		if err != nil || !d.Allowed {
			t.Fatalf("unknown policy must fall back to unlimited, got err=%v allowed=%v", err, d.Allowed)
		}
	}
}

func TestLimiter_EmptyTenantErrors(t *testing.T) {
	t.Parallel()
	l := New(cache.NewMemory(), "q:", staticPolicies())
	_, err := l.Allow(context.Background(), tenant.ID(""), "api.requests")
	if !errors.Is(err, tenant.ErrMissing) {
		t.Fatalf("err = %v, want tenant.ErrMissing", err)
	}
}

func TestLimiter_ProviderErrorPropagates(t *testing.T) {
	t.Parallel()
	boom := errors.New("db down")
	p := ProviderFunc(func(context.Context, tenant.ID, string) (Policy, error) {
		return Policy{}, boom
	})
	l := New(cache.NewMemory(), "q:", p)
	_, err := l.Allow(context.Background(), "t", "api.requests")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestLimiter_TierMissingFallsBackToDefault(t *testing.T) {
	t.Parallel()
	sp := &StaticProvider{
		TierOf:      func(tenant.ID) string { return "nonexistent" },
		DefaultTier: "free",
		Policies: map[string]map[string]Policy{
			"free": {"api.requests": {Window: time.Minute, Max: 1}},
		},
	}
	l := New(cache.NewMemory(), "q:", sp)
	d1, _ := l.Allow(context.Background(), "t", "api.requests")
	d2, _ := l.Allow(context.Background(), "t", "api.requests")
	if !d1.Allowed || d2.Allowed {
		t.Fatalf("expected exactly 1 allowed in default-tier fallback, got %v %v", d1.Allowed, d2.Allowed)
	}
}

func TestNew_PanicsOnMissingDeps(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("no panic")
		}
	}()
	New(nil, "q:", staticPolicies())
}

func TestNew_PanicsOnNilProvider(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("no panic")
		}
	}()
	New(cache.NewMemory(), "q:", nil)
}

func TestDecision_HeadersForUnlimited(t *testing.T) {
	t.Parallel()
	sp := &StaticProvider{
		TierOf:      func(tenant.ID) string { return "internal" },
		DefaultTier: "internal",
		Policies: map[string]map[string]Policy{
			"internal": {"api.requests": Unlimited},
		},
	}
	l := New(cache.NewMemory(), "q:", sp)
	d, _ := l.Allow(context.Background(), "t", "api.requests")
	if d.Limit != -1 || d.Remaining != -1 {
		t.Fatalf("unlimited decision should signal Limit=-1 (sentinel), got %+v", d)
	}
}
