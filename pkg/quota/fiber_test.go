package quota

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/pkg/cache"
	"github.com/dedeez14/goforge/pkg/tenant"
)

func newTestApp(l *Limiter) *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(func(c *fiber.Ctx) error {
		// Simulate the tenant middleware: read X-Tenant-ID and
		// inject the context value the way pkg/tenant.Middleware
		// would.
		if raw := c.Get("X-Tenant-ID"); raw != "" {
			c.SetUserContext(tenant.WithID(c.UserContext(), tenant.ID(raw)))
		}
		return c.Next()
	})
	app.Get("/ping", FiberMiddleware(l, "api.requests"), func(c *fiber.Ctx) error {
		return c.SendString("pong")
	})
	return app
}

func TestFiberMiddleware_AllowsUnderQuota(t *testing.T) {
	t.Parallel()
	sp := &StaticProvider{
		TierOf:      func(tenant.ID) string { return "free" },
		DefaultTier: "free",
		Policies: map[string]map[string]Policy{
			"free": {"api.requests": {Window: time.Minute, Max: 5}},
		},
	}
	app := newTestApp(New(cache.NewMemory(), "q:", sp))
	req := httptest.NewRequest("GET", "/ping", nil)
	req.Header.Set("X-Tenant-ID", "t1")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Ratelimit-Limit") != "5" {
		t.Fatalf("limit header = %q", resp.Header.Get("X-Ratelimit-Limit"))
	}
}

func TestFiberMiddleware_BlocksWith429(t *testing.T) {
	t.Parallel()
	sp := &StaticProvider{
		TierOf:      func(tenant.ID) string { return "free" },
		DefaultTier: "free",
		Policies: map[string]map[string]Policy{
			"free": {"api.requests": {Window: time.Minute, Max: 1}},
		},
	}
	app := newTestApp(New(cache.NewMemory(), "q:", sp))
	req := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/ping", nil)
		r.Header.Set("X-Tenant-ID", "t1")
		_ = r // unused — fiber.Test uses its own recorder
		return nil
	}
	_ = req
	// First request — allowed.
	r1 := httptest.NewRequest("GET", "/ping", nil)
	r1.Header.Set("X-Tenant-ID", "t1")
	_, _ = app.Test(r1, -1)

	// Second — blocked.
	r2 := httptest.NewRequest("GET", "/ping", nil)
	r2.Header.Set("X-Tenant-ID", "t1")
	resp, _ := app.Test(r2, -1)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 429 {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("Retry-After header missing on 429")
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "quota.exceeded") {
		t.Fatalf("body = %s, want error code quota.exceeded", body)
	}
}

func TestFiberMiddleware_FailOpenOnCacheError(t *testing.T) {
	t.Parallel()
	// A provider that errors — simulates the cache being down
	// through the Provider layer. The middleware must still let
	// the request through.
	p := ProviderFunc(func(context.Context, tenant.ID, string) (Policy, error) {
		return Policy{}, errors.New("provider down")
	})
	app := newTestApp(New(cache.NewMemory(), "q:", p))
	r := httptest.NewRequest("GET", "/ping", nil)
	r.Header.Set("X-Tenant-ID", "t1")
	resp, _ := app.Test(r, -1)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (fail-open on provider error)", resp.StatusCode)
	}
}

func TestFiberMiddleware_AnonymousFallback(t *testing.T) {
	t.Parallel()
	sp := &StaticProvider{
		TierOf:      func(tenant.ID) string { return "free" },
		DefaultTier: "free",
		Policies: map[string]map[string]Policy{
			"free": {"api.requests": {Window: time.Minute, Max: 1}},
		},
	}
	app := newTestApp(New(cache.NewMemory(), "q:", sp))
	// No X-Tenant-ID header → limiter uses the "_anonymous" tenant.
	r1 := httptest.NewRequest("GET", "/ping", nil)
	r2 := httptest.NewRequest("GET", "/ping", nil)
	resp1, _ := app.Test(r1, -1)
	resp2, _ := app.Test(r2, -1)
	defer func() { _ = resp1.Body.Close() }()
	defer func() { _ = resp2.Body.Close() }()
	if resp1.StatusCode != 200 {
		t.Fatal("first anonymous request should pass")
	}
	if resp2.StatusCode != 429 {
		t.Fatal("second anonymous request should hit 429 from shared _anonymous bucket")
	}
}
