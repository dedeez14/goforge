package adminui

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestMount_ResourceManifest_Empty(t *testing.T) {
	app := fiber.New()
	Mount(app, Config{Enabled: true})

	req := httptest.NewRequest("GET", "/panel/_resources.json", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var out struct {
		Items []Resource `json:"items"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, body)
	}
	if len(out.Items) != 0 {
		t.Fatalf("want empty items, got %v", out.Items)
	}
}

func TestMount_ResourceManifest_WithResources(t *testing.T) {
	app := fiber.New()
	Mount(app, Config{Enabled: true}, WithResources(
		Resource{
			Name:       "orders",
			Label:      "Orders",
			APIPath:    "orders",
			Permission: "orders.read",
			Searchable: true,
			Fields: []Field{
				{Name: "customer_email", Label: "Customer", Type: "email", Required: true},
				{Name: "total_cents", Type: "number"},
			},
		},
		Resource{
			Name:  "invoices",
			Label: "Invoices",
			Fields: []Field{
				{Name: "number", Required: true},
			},
		},
	))

	req := httptest.NewRequest("GET", "/panel/_resources.json", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var out struct {
		Items []Resource `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, body)
	}
	if len(out.Items) != 2 {
		t.Fatalf("want 2 items, got %d", len(out.Items))
	}
	if out.Items[0].Name != "orders" || out.Items[0].Searchable != true {
		t.Errorf("orders resource lost fields: %+v", out.Items[0])
	}
	if len(out.Items[0].Fields) != 2 || out.Items[0].Fields[0].Name != "customer_email" {
		t.Errorf("orders fields serialisation wrong: %+v", out.Items[0].Fields)
	}
	if out.Items[1].Name != "invoices" {
		t.Errorf("invoices resource missing: %+v", out.Items[1])
	}
}

func TestMount_ResourceManifest_Disabled(t *testing.T) {
	app := fiber.New()
	// Mount disabled: _resources.json should not exist either.
	Mount(app, Config{Enabled: false}, WithResources(Resource{Name: "x"}))

	req := httptest.NewRequest("GET", "/panel/_resources.json", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("disabled Mount should leave manifest 404, got %d", resp.StatusCode)
	}
}
