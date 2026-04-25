package idempotency

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/pkg/httpx"
)

func newTestApp(store Store, calls *int32) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: httpx.FiberErrorHandler})
	app.Use(Middleware(Options{Store: store}))
	app.Post("/orders", func(c *fiber.Ctx) error {
		atomic.AddInt32(calls, 1)
		return httpx.RespondData(c, fiber.StatusCreated, fiber.Map{"id": "ord-1"})
	})
	return app
}

func doRequest(t *testing.T, app *fiber.App, key string, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(fiber.MethodPost, "/orders", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(HeaderName, key)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func TestMiddleware_ReplayReturnsCachedResponse(t *testing.T) {
	t.Parallel()
	var calls int32
	app := newTestApp(NewMemoryStore(), &calls)

	r1 := doRequest(t, app, "key-1", `{"total":100}`)
	body1, _ := io.ReadAll(r1.Body)
	if r1.StatusCode != fiber.StatusCreated {
		t.Fatalf("first call status: %d body=%s", r1.StatusCode, body1)
	}

	r2 := doRequest(t, app, "key-1", `{"total":100}`)
	body2, _ := io.ReadAll(r2.Body)
	if r2.StatusCode != fiber.StatusCreated {
		t.Fatalf("replay status: %d body=%s", r2.StatusCode, body2)
	}
	if !bytes.Equal(body1, body2) {
		t.Fatalf("replay body differs: %s vs %s", body1, body2)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected handler invoked once, got %d", got)
	}
	if r2.Header.Get("Idempotent-Replay") != "true" {
		t.Fatalf("expected replay marker on second response")
	}
}

func TestMiddleware_DifferentBodyConflict(t *testing.T) {
	t.Parallel()
	var calls int32
	app := newTestApp(NewMemoryStore(), &calls)

	if r := doRequest(t, app, "key-2", `{"total":100}`); r.StatusCode != fiber.StatusCreated {
		t.Fatalf("first call status: %d", r.StatusCode)
	}
	r := doRequest(t, app, "key-2", `{"total":999}`)
	if r.StatusCode != fiber.StatusConflict {
		body, _ := io.ReadAll(r.Body)
		t.Fatalf("expected 409 on body mismatch, got %d body=%s", r.StatusCode, body)
	}
}

func TestMiddleware_NoKeyPassesThrough(t *testing.T) {
	t.Parallel()
	var calls int32
	app := newTestApp(NewMemoryStore(), &calls)
	if r := doRequest(t, app, "", `{"total":1}`); r.StatusCode != fiber.StatusCreated {
		t.Fatalf("status: %d", r.StatusCode)
	}
	if r := doRequest(t, app, "", `{"total":1}`); r.StatusCode != fiber.StatusCreated {
		t.Fatalf("status: %d", r.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected handler invoked twice with no key, got %d", got)
	}
}

func TestMiddleware_RejectsLongKey(t *testing.T) {
	t.Parallel()
	var calls int32
	app := newTestApp(NewMemoryStore(), &calls)
	long := strings.Repeat("x", 300)
	r := doRequest(t, app, long, `{}`)
	if r.StatusCode != fiber.StatusBadRequest {
		body, _ := io.ReadAll(r.Body)
		t.Fatalf("expected 400, got %d body=%s", r.StatusCode, body)
	}
}
