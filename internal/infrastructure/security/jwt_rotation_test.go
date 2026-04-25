package security

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/config"
)

func TestJWT_VerifyAcceptsRotatedSecret(t *testing.T) {
	t.Parallel()
	old := strings.Repeat("o", 32)
	newKey := strings.Repeat("n", 32)

	// Issue with the old secret.
	prev := NewTokenIssuer(config.JWT{
		Secret:     old,
		Issuer:     "iss",
		AccessTTL:  time.Minute,
		RefreshTTL: time.Hour,
	})
	tok, _, err := prev.Issue(uuid.New(), TokenAccess)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Verifier rotated to the new secret but still trusts the old.
	curr := NewTokenIssuer(config.JWT{
		Secret:      newKey,
		NextSecrets: []string{old},
		Issuer:      "iss",
		AccessTTL:   time.Minute,
		RefreshTTL:  time.Hour,
	})
	if _, err := curr.Parse(tok); err != nil {
		t.Fatalf("rotated verifier should accept legacy token: %v", err)
	}
}

func TestJWT_VerifyRejectsUnknownKid(t *testing.T) {
	t.Parallel()
	curr := NewTokenIssuer(config.JWT{
		Secret:     strings.Repeat("a", 32),
		Issuer:     "iss",
		AccessTTL:  time.Minute,
		RefreshTTL: time.Hour,
	})
	// A token signed by an unrelated secret has a kid that maps to
	// no known key.
	other := NewTokenIssuer(config.JWT{
		Secret:     strings.Repeat("z", 32),
		Issuer:     "iss",
		AccessTTL:  time.Minute,
		RefreshTTL: time.Hour,
	})
	tok, _, err := other.Issue(uuid.New(), TokenAccess)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := curr.Parse(tok); err == nil {
		t.Fatal("expected verify to fail when kid doesn't match any known secret")
	}
}

func TestJWT_PublicKeySetForHMACIsEmpty(t *testing.T) {
	t.Parallel()
	iss := NewTokenIssuer(config.JWT{
		Secret:     strings.Repeat("a", 32),
		Issuer:     "iss",
		AccessTTL:  time.Minute,
		RefreshTTL: time.Hour,
	})
	p, ok := iss.(PublicKeySetProvider)
	if !ok {
		t.Fatal("hmacIssuer should implement PublicKeySetProvider")
	}
	if got := len(p.PublicKeySet().Keys); got != 0 {
		t.Fatalf("HS256 must not publish keys; got %d", got)
	}
}
