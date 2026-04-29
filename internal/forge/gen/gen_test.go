package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerate_RejectsInvalidName(t *testing.T) {
	t.Parallel()
	if _, err := Generate(t.TempDir(), "", "x"); err == nil {
		t.Fatal("empty name must fail")
	}
	if _, err := Generate(t.TempDir(), "lowerCase", "x"); err == nil {
		t.Fatal("non-PascalCase name must fail")
	}
}

func TestGenerate_ProducesAllFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "migrations"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	files, err := Generate(root, "Widget", "github.com/example/app")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(files) < 8 {
		t.Fatalf("expected at least 8 files, got %d (%v)", len(files), files)
	}
	want := []string{
		"internal/domain/widget/widget.go",
		"internal/domain/widget/repository.go",
		"internal/usecase/widget.go",
		"internal/adapter/repository/postgres/widget.go",
		"internal/adapter/http/dto/widget.go",
		"internal/adapter/http/handler/widget.go",
		"migrations/0001_create_widgets.up.sql",
		"migrations/0001_create_widgets.down.sql",
	}
	for _, w := range want {
		full := filepath.Join(root, w)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("expected file %s: %v", w, err)
		}
	}
}

func TestGenerate_RefusesToOverwrite(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "migrations"), 0o755)
	if _, err := Generate(root, "Order", "github.com/example/app"); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	_, err := Generate(root, "Order", "github.com/example/app")
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("second Generate must refuse, got %v", err)
	}
}

func TestGenerateWithOptions_WithAdmin(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "migrations"), 0o755)

	files, err := GenerateWithOptions(root, "Invoice", "github.com/example/app", Options{WithAdmin: true})
	if err != nil {
		t.Fatalf("GenerateWithOptions: %v", err)
	}

	want := "internal/app/admin_invoice.go"
	full := filepath.Join(root, want)
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("expected admin companion %s: %v", want, err)
	}

	body, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	b := string(body)
	for _, must := range []string{
		"package app",
		"InvoiceAdminResource()",
		"adminui.Resource",
		"Name:       \"invoices\"",
		"APIPath:    \"invoices\"",
	} {
		if !strings.Contains(b, must) {
			t.Errorf("admin companion missing %q:\n%s", must, b)
		}
	}

	// Ensure the admin file is in the returned list too.
	var listed bool
	for _, f := range files {
		if f == want {
			listed = true
		}
	}
	if !listed {
		t.Errorf("admin companion not in returned file list: %v", files)
	}
}

func TestGenerate_NoAdminByDefault(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "migrations"), 0o755)

	if _, err := Generate(root, "Thing", "github.com/example/app"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// No admin companion should be emitted unless explicitly asked for.
	if _, err := os.Stat(filepath.Join(root, "internal/app/admin_thing.go")); err == nil {
		t.Fatalf("admin companion must not be emitted by default")
	}
}

func TestGenerate_PicksNextMigrationID(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mig := filepath.Join(root, "migrations")
	_ = os.MkdirAll(mig, 0o755)
	_ = os.WriteFile(filepath.Join(mig, "0001_init.up.sql"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(mig, "0042_things.up.sql"), nil, 0o644)
	files, err := Generate(root, "Stuff", "github.com/example/app")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var found bool
	for _, f := range files {
		if strings.Contains(f, "0043_create_stuffs.up.sql") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 0043 migration; got %v", files)
	}
}
