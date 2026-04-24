// Package security contains password hashing (Argon2id) and JWT services.
//
// Argon2id parameters are conservative defaults suitable for interactive
// login on modern hardware; tune via configuration if you benchmark and
// determine otherwise. Parameters are embedded in the encoded hash so
// they can be upgraded later without a data migration.
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2idParams controls the cost of password hashing.
type Argon2idParams struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultArgon2idParams are IETF-recommended values good for ~50ms on a
// typical modern CPU core. Tune if your traffic profile demands it.
var DefaultArgon2idParams = Argon2idParams{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 2,
	SaltLength:  16,
	KeyLength:   32,
}

// PasswordHasher hashes and verifies passwords.
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encoded string) (ok bool, needsRehash bool, err error)
}

type argon2idHasher struct{ p Argon2idParams }

// NewPasswordHasher returns an Argon2id-based PasswordHasher.
func NewPasswordHasher(p Argon2idParams) PasswordHasher {
	if p.Memory == 0 {
		p = DefaultArgon2idParams
	}
	return &argon2idHasher{p: p}
}

func (h *argon2idHasher) Hash(password string) (string, error) {
	salt := make([]byte, h.p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("hasher: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.p.Iterations, h.p.Memory, h.p.Parallelism, h.p.KeyLength)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.p.Memory, h.p.Iterations, h.p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify constant-time-compares password to encoded. needsRehash is true
// when the stored params are weaker than the hasher's current params,
// so the caller may transparently upgrade on next successful login.
func (h *argon2idHasher) Verify(password, encoded string) (bool, bool, error) {
	p, salt, key, err := decodeArgon2id(encoded)
	if err != nil {
		return false, false, err
	}
	computed := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(key)))
	if subtle.ConstantTimeCompare(key, computed) != 1 {
		return false, false, nil
	}
	needsRehash := p.Memory < h.p.Memory || p.Iterations < h.p.Iterations || p.Parallelism < h.p.Parallelism
	return true, needsRehash, nil
}

func decodeArgon2id(s string) (Argon2idParams, []byte, []byte, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return Argon2idParams{}, nil, nil, errors.New("hasher: invalid encoded hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Argon2idParams{}, nil, nil, errors.New("hasher: incompatible argon2 version")
	}
	var p Argon2idParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return Argon2idParams{}, nil, nil, fmt.Errorf("hasher: parse params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return Argon2idParams{}, nil, nil, fmt.Errorf("hasher: decode salt: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return Argon2idParams{}, nil, nil, fmt.Errorf("hasher: decode key: %w", err)
	}
	p.SaltLength = uint32(len(salt))
	p.KeyLength = uint32(len(key))
	return p, salt, key, nil
}
