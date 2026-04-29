package adminui

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestMount_Disabled(t *testing.T) {
	app := fiber.New()
	Mount(app, Config{Enabled: false})

	req := httptest.NewRequest("GET", "/panel/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("disabled Mount should leave /panel/ unrouted, got %d", resp.StatusCode)
	}
}

func TestMount_ServesIndex(t *testing.T) {
	app := fiber.New()
	Mount(app, Config{Enabled: true})

	req := httptest.NewRequest("GET", "/panel/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<title>goforge · admin</title>") {
		t.Fatalf("index.html not served; body prefix: %q", string(body[:min(len(body), 200)]))
	}
}

func TestMount_RedirectsNoSlash(t *testing.T) {
	app := fiber.New()
	Mount(app, Config{Enabled: true})

	req := httptest.NewRequest("GET", "/panel", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusPermanentRedirect {
		t.Fatalf("want 308, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/panel/" {
		t.Fatalf("want Location=/panel/, got %q", loc)
	}
}

func TestMount_SPAFallback(t *testing.T) {
	app := fiber.New()
	Mount(app, Config{Enabled: true})

	// Unknown subpath should fall back to index.html so SPA hash
	// routes work on first load. (The client normally uses hash
	// routing anyway; this covers the edge case of a bookmark to
	// /panel/users before the JS has rewritten the URL.)
	req := httptest.NewRequest("GET", "/panel/does-not-exist", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("SPA fallback should serve 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<!DOCTYPE html>") {
		t.Fatalf("fallback should be HTML; got %q", string(body[:min(len(body), 200)]))
	}
}

func TestMount_CustomPath(t *testing.T) {
	app := fiber.New()
	Mount(app, Config{Enabled: true, Path: "admin-ui"})

	req := httptest.NewRequest("GET", "/admin-ui/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("custom path /admin-ui/ should serve index.html, got %d", resp.StatusCode)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
