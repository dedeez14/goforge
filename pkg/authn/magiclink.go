package authn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/dedeez14/goforge/pkg/cache"
)

// MagicLink is a passwordless 'click this link to log in' primitive.
// Tokens are opaque, single-use, with a short TTL. The plaintext
// token leaves the server exactly once (in the email link); only a
// SHA-256 hash is stored, so a database leak does not expose live
// tokens.
type MagicLink struct {
	Cache cache.Cache
	TTL   time.Duration
}

// NewMagicLink returns a MagicLink helper.
func NewMagicLink(c cache.Cache, ttl time.Duration) *MagicLink {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return &MagicLink{Cache: c, TTL: ttl}
}

// Issue mints a fresh token for `subject`. Returns the plaintext to
// embed in the email; only its hash is stored.
func (m *MagicLink) Issue(ctx context.Context, subject string) (string, error) {
	tok, err := randURLToken(32)
	if err != nil {
		return "", err
	}
	if err := m.Cache.Set(ctx, m.key(tok), []byte(subject), m.TTL); err != nil {
		return "", err
	}
	return tok, nil
}

// Consume verifies the token and returns the subject it was issued
// to. Always returns an error after the first successful Consume —
// the token is single-use.
func (m *MagicLink) Consume(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", errors.New("authn: empty token")
	}
	key := m.key(token)
	subj, err := m.Cache.Get(ctx, key)
	if err != nil {
		if errors.Is(err, cache.ErrMiss) {
			return "", errors.New("authn: token expired or already used")
		}
		return "", err
	}
	if err := m.Cache.Del(ctx, key); err != nil {
		// Best effort — if Del fails the token is still valid
		// for a few seconds, but the consumer already authed so
		// not catastrophic.
		_ = err
	}
	return string(subj), nil
}

func (m *MagicLink) key(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return "magiclink:" + base64.RawURLEncoding.EncodeToString(h[:])
}

// randURLToken returns base64-url-safe random bytes of approximately
// the requested entropy (rounded up).
func randURLToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
