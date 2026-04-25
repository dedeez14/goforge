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
