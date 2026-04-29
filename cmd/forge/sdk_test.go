package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdSDK_RequiresLanguage(t *testing.T) {
	err := cmdSDK(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "language is required") {
		t.Fatalf("cmdSDK() with no args = %v, want 'language is required'", err)
	}
}

func TestCmdSDK_RejectsUnknownLanguage(t *testing.T) {
	err := cmdSDK(context.Background(), []string{"cobol"})
	if err == nil || !strings.Contains(err.Error(), "unknown sdk language") {
		t.Fatalf("cmdSDK cobol = %v, want unknown-language error", err)
	}
}

func TestDownloadSpec_AtomicRename(t *testing.T) {
	// Serve a known document and make sure downloadSpec writes it
	// verbatim to dest. The atomic-rename contract means the
	// destination file must NOT exist until the body has been
	// fully copied - a mid-flight crash must leave no partial
	// openapi.json behind.
	body := `{"openapi":"3.1.0","info":{"title":"t","version":"0"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "openapi.json")

	if err := downloadSpec(context.Background(), srv.URL, dest); err != nil {
		t.Fatalf("downloadSpec: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != body {
		t.Fatalf("dest content = %q, want %q", got, body)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() == "openapi.json" {
			continue
		}
		t.Errorf("stray temp file left behind: %s", e.Name())
	}
}

func TestDownloadSpec_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "openapi.json")

	err := downloadSpec(context.Background(), srv.URL, dest)
	if err == nil || !strings.Contains(err.Error(), "unexpected status 500") {
		t.Fatalf("downloadSpec on 500 = %v, want unexpected status", err)
	}

	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("dest should not exist on failed download, got stat err = %v", err)
	}
}
