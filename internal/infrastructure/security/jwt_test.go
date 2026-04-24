package security

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/config"
)

func newTestIssuer() TokenIssuer {
	return NewTokenIssuer(config.JWT{
		Secret:     strings.Repeat("x", 32),
		Issuer:     "goforge-test",
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 24 * time.Hour,
	})
}

func TestTokenIssuer_IssueParseAccess(t *testing.T) {
	iss := newTestIssuer()
	sub := uuid.New()

	tok, exp, err := iss.Issue(sub, TokenAccess)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	if !exp.After(time.Now()) {
		t.Fatal("expiry must be in the future")
	}

	claims, err := iss.Parse(tok)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if claims.Subject != sub.String() {
		t.Fatalf("subject mismatch: got %s", claims.Subject)
	}
	if claims.Kind != TokenAccess {
		t.Fatalf("kind mismatch: got %s", claims.Kind)
	}
}

func TestTokenIssuer_ParseRejectsTampered(t *testing.T) {
	iss := newTestIssuer()
	tok, _, err := iss.Issue(uuid.New(), TokenAccess)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Flip a byte in the signature segment to simulate tampering.
	tampered := tok[:len(tok)-2] + "aa"
	if _, err := iss.Parse(tampered); err == nil {
		t.Fatal("Parse must reject tampered tokens")
	}
}
