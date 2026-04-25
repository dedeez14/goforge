package tenant

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/pkg/events"
	"github.com/dedeez14/goforge/pkg/httpx"
)

func TestWithIDAndRequire(t *testing.T) {
	t.Parallel()
	ctx := WithID(context.Background(), ID("tenant-1"))
	id, err := Require(ctx)
	if err != nil {
		t.Fatalf("require: %v", err)
	}
	if id != "tenant-1" {
		t.Fatalf("unexpected tenant: %q", id)
	}
}

func TestRequireMissing(t *testing.T) {
	t.Parallel()
	_, err := Require(context.Background())
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("expected ErrMissing, got %v", err)
	}
}

// TestWithID_PropagatesToEvents guards against a regression where the
// tenant context key in pkg/tenant and the one in pkg/events drift
// apart. Outbox/event consumers rely on this propagation.
func TestWithID_PropagatesToEvents(t *testing.T) {
	t.Parallel()
	ctx := WithID(context.Background(), ID("tenant-evt"))
	if got := events.TenantFromContext(ctx); got != "tenant-evt" {
		t.Fatalf("events.TenantFromContext: want tenant-evt, got %q", got)
	}
}

func TestMiddleware_RejectsMissingHeader(t *testing.T) {
	t.Parallel()
	app := fiber.New(fiber.Config{ErrorHandler: httpx.FiberErrorHandler})
	app.Use(Middleware(HeaderResolver("X-Tenant-ID")))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/", nil), -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("Middleware should reject missing tenant: got %d", resp.StatusCode)
	}
}

func TestOptionalMiddleware_AllowsMissingHeader(t *testing.T) {
	t.Parallel()
	app := fiber.New(fiber.Config{ErrorHandler: httpx.FiberErrorHandler})
	app.Use(OptionalMiddleware(HeaderResolver("X-Tenant-ID")))
	app.Get("/", func(c *fiber.Ctx) error {
		if _, ok := FromContext(c.UserContext()); ok {
			return c.SendString("scoped")
		}
		return c.SendString("anonymous")
	})

	// no header => still 200
	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/", nil), -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("OptionalMiddleware should not reject anonymous request: %d", resp.StatusCode)
	}

	// with header => tenant injected into ctx
	req := httptest.NewRequest(fiber.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "tenant-x")
	resp2, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
}
