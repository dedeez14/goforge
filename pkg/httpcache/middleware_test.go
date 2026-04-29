package httpcache

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// handlerReturning builds a Fiber test app that runs the middleware
// around a handler emitting the given body.
func handlerReturning(body string, opts Options) *fiber.App {
	app := fiber.New()
	app.Get("/", New(opts), func(c *fiber.Ctx) error {
		return c.SendString(body)
	})
	return app
}

func TestMiddleware_Sets_ETag_And_CacheControl(t *testing.T) {
	app := handlerReturning("hello world", Options{MaxAge: 60, Public: true, MustRevalidate: true})
	req := httptest.NewRequest("GET", "/", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.StatusCode; got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
	if got := resp.Header.Get("ETag"); got == "" {
		t.Fatal("ETag header missing")
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=60, must-revalidate" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestMiddleware_Returns_304_On_Match(t *testing.T) {
	app := handlerReturning("payload", Options{MaxAge: 30, Private: true})

	// First request to obtain ETag.
	first := httptest.NewRequest("GET", "/", nil)
	resp, err := app.Test(first, -1)
	if err != nil {
		t.Fatal(err)
	}
	etag := resp.Header.Get("ETag")
	_ = resp.Body.Close()
	if etag == "" {
		t.Fatal("no ETag on first response")
	}

	// Second request echoes the ETag in If-None-Match.
	second := httptest.NewRequest("GET", "/", nil)
	second.Header.Set("If-None-Match", etag)
	resp2, err := app.Test(second, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != fiber.StatusNotModified {
		t.Fatalf("status = %d, want 304", resp2.StatusCode)
	}
	body, _ := io.ReadAll(resp2.Body)
	if len(body) != 0 {
		t.Fatalf("304 body must be empty, got %q", body)
	}
	// The ETag should still be present on the 304 for client
	// bookkeeping.
	if got := resp2.Header.Get("ETag"); got != etag {
		t.Fatalf("304 ETag = %q, want %q", got, etag)
	}
}

func TestMiddleware_Star_Matches_Always(t *testing.T) {
	app := handlerReturning("x", Options{})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("If-None-Match", "*")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusNotModified {
		t.Fatalf("status = %d, want 304 on If-None-Match: *", resp.StatusCode)
	}
}

func TestMiddleware_Weak_Validator_Matches(t *testing.T) {
	app := handlerReturning("weak-body", Options{})
	first := httptest.NewRequest("GET", "/", nil)
	r, _ := app.Test(first, -1)
	etag := r.Header.Get("ETag")
	_ = r.Body.Close()

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("If-None-Match", "W/"+etag)
	r2, _ := app.Test(req, -1)
	defer func() { _ = r2.Body.Close() }()
	if r2.StatusCode != fiber.StatusNotModified {
		t.Fatalf("W/ validator did not match; status = %d", r2.StatusCode)
	}
}

// TestMiddleware_Mismatch_Passes_Body_Through guards against the
// reset-body short-circuit running when the ETag differs; regressing
// this would make the middleware silently drop response bodies.
func TestMiddleware_Mismatch_Passes_Body_Through(t *testing.T) {
	app := handlerReturning("one", Options{})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("If-None-Match", `"deadbeefdeadbeefdeadbeefdeadbeef"`)
	resp, _ := app.Test(req, -1)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "one" {
		t.Fatalf("expected 200 'one', got %d %q", resp.StatusCode, body)
	}
}

func TestMiddleware_NonGET_Bypasses(t *testing.T) {
	app := fiber.New()
	app.Post("/", New(Options{MaxAge: 60}), func(c *fiber.Ctx) error {
		return c.SendString("posted")
	})
	req := httptest.NewRequest("POST", "/", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("ETag"); got != "" {
		t.Fatalf("ETag set on POST: %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "" {
		t.Fatalf("Cache-Control set on POST: %q", got)
	}
}

func TestMiddleware_Non200_NotCached(t *testing.T) {
	app := fiber.New()
	app.Get("/", New(Options{MaxAge: 60}), func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusBadGateway).SendString("boom")
	})
	req := httptest.NewRequest("GET", "/", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("ETag"); got != "" {
		t.Fatalf("ETag set on %d response: %q", resp.StatusCode, got)
	}
}

func TestMiddleware_MaxAge_Zero_EmitsExplicitDirective(t *testing.T) {
	// Regression: previously the zero value was silently dropped,
	// letting caches fall back to heuristic freshness.
	app := handlerReturning("x", Options{MaxAge: 0, Public: true})
	req := httptest.NewRequest("GET", "/", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=0" {
		t.Fatalf("Cache-Control = %q, want %q", got, "public, max-age=0")
	}
}

func TestMiddleware_MaxAge_Negative_OmitsDirective(t *testing.T) {
	app := handlerReturning("x", Options{MaxAge: -1, Public: true})
	req := httptest.NewRequest("GET", "/", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Cache-Control"); got != "public" {
		t.Fatalf("Cache-Control = %q, want %q", got, "public")
	}
}

func TestMiddleware_Vary_EmittedOn200(t *testing.T) {
	app := handlerReturning("body", Options{Private: true, MaxAge: 30, Vary: []string{"Authorization"}})
	req := httptest.NewRequest("GET", "/", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Vary"); got != "Authorization" {
		t.Fatalf("Vary = %q, want Authorization", got)
	}
}

func TestMiddleware_Vary_EmittedOn304(t *testing.T) {
	app := handlerReturning("body", Options{Private: true, MaxAge: 30, Vary: []string{"Authorization", "X-Tenant-ID"}})
	first := httptest.NewRequest("GET", "/", nil)
	r, _ := app.Test(first, -1)
	etag := r.Header.Get("ETag")
	_ = r.Body.Close()

	second := httptest.NewRequest("GET", "/", nil)
	second.Header.Set("If-None-Match", etag)
	r2, err := app.Test(second, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r2.Body.Close() }()
	if r2.StatusCode != fiber.StatusNotModified {
		t.Fatalf("want 304, got %d", r2.StatusCode)
	}
	// Vary MUST be on 304 too so the browser keys its cache
	// on Authorization/X-Tenant-ID even when serving the cached
	// copy without revalidation.
	if got := r2.Header.Get("Vary"); got != "Authorization, X-Tenant-ID" {
		t.Fatalf("Vary on 304 = %q, want 'Authorization, X-Tenant-ID'", got)
	}
}

func TestOptions_Public_And_Private_Panic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("New did not panic for Public && Private")
		}
	}()
	_ = New(Options{Public: true, Private: true})
}
