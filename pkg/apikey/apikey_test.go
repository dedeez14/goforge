package apikey

import (
	"strings"
	"testing"
)

func TestGenerateProducesParseableKey(t *testing.T) {
	g, err := Generate("live")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.HasPrefix(g.Plaintext, "gf_live_") {
		t.Fatalf("unexpected plaintext shape: %q", g.Plaintext)
	}
	if !strings.HasPrefix(g.Plaintext, g.Prefix+"_") {
		t.Fatalf("plaintext should start with prefix + underscore")
	}
	if len(g.Hash) != 64 {
		t.Fatalf("hash should be 64 hex chars (sha256), got %d", len(g.Hash))
	}

	parsed, err := Parse(g.Plaintext)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Prefix != g.Prefix {
		t.Fatalf("parse prefix mismatch: %q vs %q", parsed.Prefix, g.Prefix)
	}
}

func TestVerifyHash_AcceptsMatchAndRejectsTampered(t *testing.T) {
	g, _ := Generate("test")
	if !VerifyHash(g.Plaintext, g.Hash) {
		t.Fatalf("genuine plaintext should verify")
	}
	if VerifyHash(g.Plaintext+"X", g.Hash) {
		t.Fatalf("tampered plaintext must not verify")
	}
}

func TestParse_RejectsObvioslyInvalid(t *testing.T) {
	cases := []string{
		"",
		"random-string",
		"gf_live_short",
		"gh_live_aaaaaaaaaaaa_" + strings.Repeat("0", 64), // wrong tag
		"gf_LIVE_aaaaaaaaaaaa_" + strings.Repeat("0", 64), // uppercase env
		"gf_live_aaaaaaaaaaaa_" + strings.Repeat("z", 64), // non-hex secret
		"gf_live_aaaaaaaaaaa_" + strings.Repeat("0", 64),  // 11-char id
		"eyJhbGciOi..." + strings.Repeat("a", 60),         // looks like a JWT
	}
	for _, c := range cases {
		if _, err := Parse(c); err == nil {
			t.Fatalf("Parse(%q) should have failed", c)
		}
	}
}

func TestLooksLikeAPIKey(t *testing.T) {
	if !LooksLikeAPIKey("gf_live_xxxxxxxx_yyyy") {
		t.Fatalf("framework prefix should be recognised")
	}
	if LooksLikeAPIKey("eyJhbGciOiJIUzI1NiJ9...") {
		t.Fatalf("a JWT should not look like an API key")
	}
}

func TestGenerate_DifferentInvocationsAreUnique(t *testing.T) {
	a, _ := Generate("live")
	b, _ := Generate("live")
	if a.Prefix == b.Prefix {
		t.Fatalf("two generated keys should have different prefixes")
	}
	if a.Plaintext == b.Plaintext {
		t.Fatalf("two generated keys should have different plaintexts")
	}
}
