package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestScanPermissions_ExtractsCodesFromBothMiddlewareForms verifies
// the AST scanner picks up the codes whether they're passed to
// RequirePermission directly or as elements of the slice literal
// passed to RequireAnyPermission. The test writes synthetic Go
// fixtures to a tmp dir so it stays independent of any churn in the
// actual middleware/router files.
func TestScanPermissions_ExtractsCodesFromBothMiddlewareForms(t *testing.T) {
	root := t.TempDir()

	mustWrite := func(name, body string) {
		t.Helper()
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}

	mustWrite("router.go", `package r

import "x"

var _ = x.RequirePermission("orders.refund", nil, nil)
var _ = x.RequireAnyPermission([]string{"users.read", "users.manage"}, nil, nil)
var _ = x.RequirePermission("orders.refund", nil, nil) // duplicate
`)
	mustWrite("vendor/skip.go", `package skip
var _ = RequirePermission("vendor.code", nil, nil)
`)
	// Test files are scanned too.
	mustWrite("perm_test.go", `package r
var _ = RequirePermission("test.code", nil, nil)
`)
	// Bare ident form (no selector).
	mustWrite("bare.go", `package r
var _ = RequirePermission("dashboard.view", nil, nil)
`)

	got, err := scanPermissions(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	codes := make([]string, len(got))
	for i, p := range got {
		codes[i] = p.Code
	}
	sort.Strings(codes)

	want := []string{
		"dashboard.view",
		"orders.refund",
		"test.code",
		"users.manage",
		"users.read",
	}
	if len(codes) != len(want) {
		t.Fatalf("want %d codes, got %d (%v)", len(want), len(codes), codes)
	}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("codes[%d]: want %q got %q", i, want[i], codes[i])
		}
	}
}

func TestSplitCode_ProducesResourceAndAction(t *testing.T) {
	cases := map[string][2]string{
		"orders.refund":   {"orders", "refund"},
		"users.read":      {"users", "read"},
		"audit.view":      {"audit", "view"},
		"single":          {"single", ""},
		"deep.dot.action": {"deep", "dot.action"},
	}
	for code, want := range cases {
		r, a := splitCode(code)
		if r != want[0] || a != want[1] {
			t.Fatalf("splitCode(%q) = (%q,%q); want (%q,%q)",
				code, r, a, want[0], want[1])
		}
	}
}
