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

func sendAndClose(t *testing.T, app *fiber.App, key, body string) (status int, respBody []byte, header http.Header) {
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
	defer func() { _ = resp.Body.Close() }()
	respBody, _ = io.ReadAll(resp.Body)
	return resp.StatusCode, respBody, resp.Header
}

func TestMiddleware_ReplayReturnsCachedResponse(t *testing.T) {
	t.Parallel()
	var calls int32
	app := newTestApp(NewMemoryStore(), &calls)

	s1, body1, _ := sendAndClose(t, app, "key-1", `{"total":100}`)
	if s1 != fiber.StatusCreated {
		t.Fatalf("first call status: %d body=%s", s1, body1)
	}
	s2, body2, hdr2 := sendAndClose(t, app, "key-1", `{"total":100}`)
	if s2 != fiber.StatusCreated {
		t.Fatalf("replay status: %d body=%s", s2, body2)
	}
	if !bytes.Equal(body1, body2) {
		t.Fatalf("replay body differs: %s vs %s", body1, body2)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected handler invoked once, got %d", got)
	}
	if hdr2.Get("Idempotent-Replay") != "true" {
		t.Fatalf("expected replay marker on second response")
	}
}

func TestMiddleware_DifferentBodyConflict(t *testing.T) {
	t.Parallel()
	var calls int32
	app := newTestApp(NewMemoryStore(), &calls)

	if s, _, _ := sendAndClose(t, app, "key-2", `{"total":100}`); s != fiber.StatusCreated {
		t.Fatalf("first call status: %d", s)
	}
	if s, body, _ := sendAndClose(t, app, "key-2", `{"total":999}`); s != fiber.StatusConflict {
		t.Fatalf("expected 409 on body mismatch, got %d body=%s", s, body)
	}
}

func TestMiddleware_NoKeyPassesThrough(t *testing.T) {
	t.Parallel()
	var calls int32
	app := newTestApp(NewMemoryStore(), &calls)
	if s, _, _ := sendAndClose(t, app, "", `{"total":1}`); s != fiber.StatusCreated {
		t.Fatalf("status: %d", s)
	}
	if s, _, _ := sendAndClose(t, app, "", `{"total":1}`); s != fiber.StatusCreated {
		t.Fatalf("status: %d", s)
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
	if s, body, _ := sendAndClose(t, app, long, `{}`); s != fiber.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", s, body)
	}
}
