// Package apikey contains the format, generator and verifier for
// goforge API keys. Keys are opaque, prefix-id, secret-suffix tokens
// designed for service-to-service authentication:
//
//	gf_<env>_<id>_<secret>
//	└┬┘ └┬─┘ └┬┘ └──┬───┘
//	 │   │   │     └──── 32-byte URL-safe random secret (high entropy)
//	 │   │   └────────── 12-byte random id (used as DB lookup index)
//	 │   └────────────── env tag, e.g. "live", "test", "dev"
//	 └────────────────── framework tag (constant)
//
// The prefix `gf_<env>_<id>` is stored in the DB and shown to admins;
// the secret is never stored - we keep only sha256(secret) and verify
// in constant time. SHA-256 is sufficient because the secret carries
// 32 bytes of entropy (256 bits) - pre-image resistance, not slow
// hashing, is what we need; argon2id would be wasted CPU.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Generated bundles the public bits returned at issue-time. The
// caller stores Prefix and Hash in the DB; Plaintext is shown to
// the user exactly once.
type Generated struct {
	Plaintext string // full key, e.g. gf_live_a1b2c3d4e5f6_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
	Prefix    string // gf_live_a1b2c3d4e5f6 (used as DB lookup column)
	Hash      string // hex(sha256(plaintext)) - stored, never compared with raw secret
}

// IDLen is the random portion of the public-visible prefix in
// hexadecimal characters (12 hex chars = 6 random bytes = 48-bit
// uniqueness, fine when collisions are checked at insert time).
const IDLen = 12

// SecretLen is the length of the secret portion in hexadecimal
// characters (64 hex chars = 32 random bytes = 256-bit entropy).
const SecretLen = 64

// FrameworkTag is the constant prefix used to recognise goforge
// API keys at parse-time.
const FrameworkTag = "gf"

// Generate mints a new API key for the given environment tag. It
// returns the bundle to persist and to hand back to the caller.
//
// env is a free-form short tag like "live", "test", "dev". The
// caller chooses; the framework only stores it as part of the
// prefix so leaked test keys can be visually distinguished from
// production ones.
func Generate(env string) (*Generated, error) {
	env = strings.ToLower(strings.TrimSpace(env))
	if env == "" {
		env = "live"
	}
	if !isSafeTag(env) {
		return nil, fmt.Errorf("apikey: env tag %q must be lowercase alphanumeric", env)
	}

	idBytes := make([]byte, IDLen/2)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, fmt.Errorf("apikey: random id: %w", err)
	}
	secretBytes := make([]byte, SecretLen/2)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, fmt.Errorf("apikey: random secret: %w", err)
	}

	id := hex.EncodeToString(idBytes)
	secret := hex.EncodeToString(secretBytes)
	prefix := fmt.Sprintf("%s_%s_%s", FrameworkTag, env, id)
	plaintext := prefix + "_" + secret
	sum := sha256.Sum256([]byte(plaintext))
	return &Generated{
		Plaintext: plaintext,
		Prefix:    prefix,
		Hash:      hex.EncodeToString(sum[:]),
	}, nil
}

// Parsed holds the structural breakdown of a presented bearer
// token. Prefix is what the caller looks up in the DB; Plaintext
// is what they hash and compare to the stored Hash.
type Parsed struct {
	Plaintext string
	Prefix    string
}

// ErrMalformed is returned when a presented bearer token does not
// match the framework's API-key shape. Callers treat it as "this
// is not an API key, fall back to the next auth scheme".
var ErrMalformed = errors.New("apikey: malformed token")

// Parse validates the structural shape of an API key without
// touching the DB. Returns ErrMalformed when the input is not one
// of our keys (so the caller can try a different auth scheme).
func Parse(s string) (*Parsed, error) {
	parts := strings.Split(s, "_")
	if len(parts) != 4 {
		return nil, ErrMalformed
	}
	if parts[0] != FrameworkTag {
		return nil, ErrMalformed
	}
	if !isSafeTag(parts[1]) {
		return nil, ErrMalformed
	}
	if len(parts[2]) != IDLen || !isHex(parts[2]) {
		return nil, ErrMalformed
	}
	if len(parts[3]) != SecretLen || !isHex(parts[3]) {
		return nil, ErrMalformed
	}
	return &Parsed{
		Plaintext: s,
		Prefix:    strings.Join(parts[:3], "_"),
	}, nil
}

// VerifyHash returns true when sha256(plaintext) equals the stored
// hexadecimal hash. Comparison is constant-time to prevent timing
// side-channels even though the input space is enormous.
func VerifyHash(plaintext, storedHexHash string) bool {
	sum := sha256.Sum256([]byte(plaintext))
	got := make([]byte, hex.EncodedLen(len(sum)))
	hex.Encode(got, sum[:])
	return subtle.ConstantTimeCompare(got, []byte(storedHexHash)) == 1
}

// LooksLikeAPIKey is a fast pre-check used by middleware to decide
// whether to attempt API-key parsing or fall through to JWT.
func LooksLikeAPIKey(s string) bool {
	return strings.HasPrefix(s, FrameworkTag+"_")
}

func isSafeTag(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}
