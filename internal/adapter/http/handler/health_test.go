package handler

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/internal/config"
	"github.com/dedeez14/goforge/pkg/lifecycle"
)

// TestHealthHandler_Ready_Draining asserts that once the drainer is
// tripped, /readyz short-circuits to 503 *before* hitting the DB.
// This is the contract Kubernetes relies on: removing the pod from
// the Service endpoints on the first failed readiness probe.
//
// The handler is constructed with a nil *db.Router deliberately. If
// the drain short-circuit regressed and execution fell through to
// router.Ping, this test would panic instead of passing - giving us
// a louder signal than a subtle response-code diff.
func TestHealthHandler_Ready_Draining(t *testing.T) {
	drainer := lifecycle.NewDrainer()
	drainer.StartDraining()

	h := &HealthHandler{app: config.App{Name: "t", Version: "0"}, router: nil, drainer: drainer}

	app := fiber.New()
	app.Get("/readyz", h.Ready)

	req := httptest.NewRequest("GET", "/readyz", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusServiceUnavailable)
	}
	body, _ := io.ReadAll(resp.Body)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if got := payload["status"]; got != "draining" {
		t.Fatalf("status field = %v, want \"draining\"; body=%s", got, body)
	}
	if got := payload["success"]; got != false {
		t.Fatalf("success field = %v, want false; body=%s", got, body)
	}
}

// TestHealthHandler_Live_UnaffectedByDrain asserts the liveness
// probe keeps returning 200 during drain. If it flipped to 503,
// Kubernetes would restart the pod mid-drain and kill in-flight
// traffic - the exact outcome the drain phase exists to prevent.
func TestHealthHandler_Live_UnaffectedByDrain(t *testing.T) {
	drainer := lifecycle.NewDrainer()
	drainer.StartDraining()

	h := &HealthHandler{app: config.App{Name: "t", Version: "0"}, router: nil, drainer: drainer}

	app := fiber.New()
	app.Get("/healthz", h.Live)

	req := httptest.NewRequest("GET", "/healthz", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
