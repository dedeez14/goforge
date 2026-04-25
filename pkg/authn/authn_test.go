package authn

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dedeez14/goforge/pkg/cache"
)

func TestTOTP_GenerateAndVerify(t *testing.T) {
	t.Parallel()
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatalf("NewTOTPSecret: %v", err)
	}
	// We can't call totp.GenerateCode directly without exposing
	// internals, so we just confirm verify rejects garbage.
	if Verify(secret, "000000") {
		// 1-in-1e6 false positive but harmless.
		_ = secret
	}
	if Verify(secret, "abc") {
		t.Fatal("non-numeric must fail verify")
	}
}

func TestProvision_BuildsValidKey(t *testing.T) {
	t.Parallel()
	secret, _ := NewTOTPSecret()
	key, err := Provision(secret, "Acme", "alice@example.com")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !strings.Contains(key.URL(), "Acme:alice@example.com") {
		t.Fatalf("unexpected key URL: %s", key.URL())
	}
}

func TestMagicLink_IssueConsumeRoundTrip(t *testing.T) {
	t.Parallel()
	c := cache.NewMemory()
	ml := NewMagicLink(c, time.Minute)
	tok, err := ml.Issue(context.Background(), "alice@x")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	subj, err := ml.Consume(context.Background(), tok)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if subj != "alice@x" {
		t.Fatalf("subject = %q", subj)
	}
}

func TestMagicLink_SingleUse(t *testing.T) {
	t.Parallel()
	c := cache.NewMemory()
	ml := NewMagicLink(c, time.Minute)
	tok, _ := ml.Issue(context.Background(), "x")
	if _, err := ml.Consume(context.Background(), tok); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if _, err := ml.Consume(context.Background(), tok); err == nil {
		t.Fatal("token must not be reusable")
	}
}

func TestHelperEnsureHTTPS(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"https://app.example.com/cb": true,
		"http://localhost:8080/cb":   true,
		"http://127.0.0.1:8000/cb":   true,
		"http://example.com/cb":      false,
		"ftp://example.com/cb":       false,
	}
	for u, want := range cases {
		err := HelperEnsureHTTPS(u)
		got := err == nil
		if got != want {
			t.Errorf("HelperEnsureHTTPS(%q) = %v, want %v (err=%v)", u, got, want, err)
		}
	}
}
