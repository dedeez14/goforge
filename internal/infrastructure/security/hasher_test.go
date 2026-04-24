package security

import (
	"strings"
	"testing"
)

func TestPasswordHasher_RoundTrip(t *testing.T) {
	h := NewPasswordHasher(DefaultArgon2idParams)

	hash, err := h.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("expected argon2id prefix, got %q", hash)
	}

	ok, rehash, err := h.Verify("correct horse battery staple", hash)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("Verify must succeed for the correct password")
	}
	if rehash {
		t.Fatal("no rehash needed when params match current defaults")
	}
}

func TestPasswordHasher_WrongPassword(t *testing.T) {
	h := NewPasswordHasher(DefaultArgon2idParams)
	hash, _ := h.Hash("secret")
	ok, _, err := h.Verify("wrong", hash)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Fatal("Verify must fail for the wrong password")
	}
}

func TestPasswordHasher_NeedsRehash(t *testing.T) {
	weak := NewPasswordHasher(Argon2idParams{
		Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	})
	strong := NewPasswordHasher(DefaultArgon2idParams)

	weakHash, err := weak.Hash("pw")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	ok, rehash, err := strong.Verify("pw", weakHash)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("Verify must succeed across param upgrades")
	}
	if !rehash {
		t.Fatal("rehash must be requested when stored params are weaker")
	}
}
