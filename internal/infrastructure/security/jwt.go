package security

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/config"
	"github.com/dedeez14/goforge/pkg/errs"
)

// TokenKind identifies access vs refresh tokens.
type TokenKind string

const (
	TokenAccess  TokenKind = "access"
	TokenRefresh TokenKind = "refresh"
)

// Claims is the JWT payload used across the application.
//
// SessionID ties an access / refresh token to a sessions row so the
// /me/sessions endpoint can mark the caller's current device and so
// Refresh can touch the owning session's last_used_at without an
// extra DB round-trip. It is omitted from tokens that are not
// session-bound (the non-interactive API-key exchange flow is the
// motivating example; those tokens never rotate and never appear in
// the UI's device list).
type Claims struct {
	jwt.RegisteredClaims
	Kind      TokenKind `json:"typ"`
	SessionID string    `json:"sid,omitempty"`
}

// TokenIssuer mints and verifies JWTs. Issue is kept for callers
// that do not care about sessions; IssueWithSession is the
// preferred entrypoint when minting user-session tokens so the
// resulting Claims carry the sid JSON tag.
type TokenIssuer interface {
	Issue(subject uuid.UUID, kind TokenKind) (string, time.Time, error)
	IssueWithSession(subject, sessionID uuid.UUID, kind TokenKind) (string, time.Time, error)
	Parse(token string) (*Claims, error)
}

// hmacKey couples a raw HMAC secret with its public key id (`kid`).
// Tokens carry the kid in their header; verify uses the kid to pick
// the right secret. Without this the framework cannot rotate secrets
// without invalidating every live token at once.
type hmacKey struct {
	id     string
	secret []byte
}

func newHMACKey(secret string) hmacKey {
	sum := sha256.Sum256([]byte(secret))
	return hmacKey{
		id:     hex.EncodeToString(sum[:8]),
		secret: []byte(secret),
	}
}

type hmacIssuer struct {
	active     hmacKey            // signs new tokens
	all        map[string]hmacKey // kid -> key (active + retired/next)
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewTokenIssuer returns an HS256 JWT issuer configured from config.JWT.
//
// Multi-secret rotation: cfg.NextSecrets is a list of legacy or
// upcoming secrets that may appear on incoming tokens but must not be
// used for new tokens. To rotate:
//
//  1. Add the new secret to NextSecrets in production.
//  2. After all instances reload, swap Secret to the new value and move
//     the old one to NextSecrets.
//  3. After every outstanding token has expired, drop the old secret.
//
// Tokens carry a `kid` header (the first 8 bytes of sha256 of the
// secret) so verification picks the right key without trying every
// secret on every request.
func NewTokenIssuer(cfg config.JWT) TokenIssuer {
	active := newHMACKey(cfg.Secret)
	all := map[string]hmacKey{active.id: active}
	for _, s := range cfg.NextSecrets {
		s = strings.TrimSpace(s)
		if s == "" || s == cfg.Secret {
			continue
		}
		k := newHMACKey(s)
		all[k.id] = k
	}
	return &hmacIssuer{
		active:     active,
		all:        all,
		issuer:     cfg.Issuer,
		accessTTL:  cfg.AccessTTL,
		refreshTTL: cfg.RefreshTTL,
	}
}

func (i *hmacIssuer) Issue(subject uuid.UUID, kind TokenKind) (string, time.Time, error) {
	return i.IssueWithSession(subject, uuid.Nil, kind)
}

// IssueWithSession mints a token whose Claims include the given
// sessionID as the sid claim. Pass uuid.Nil to omit it (the Issue
// shortcut above does exactly that).
func (i *hmacIssuer) IssueWithSession(subject, sessionID uuid.UUID, kind TokenKind) (string, time.Time, error) {
	ttl := i.accessTTL
	if kind == TokenRefresh {
		ttl = i.refreshTTL
	}
	now := time.Now().UTC()
	exp := now.Add(ttl)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    i.issuer,
			Subject:   subject.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        uuid.NewString(),
		},
		Kind: kind,
	}
	if sessionID != uuid.Nil {
		claims.SessionID = sessionID.String()
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = i.active.id
	signed, err := tok.SignedString(i.active.secret)
	if err != nil {
		return "", time.Time{}, errs.Wrap(errs.KindInternal, "jwt.sign", "failed to sign token", err)
	}
	return signed, exp, nil
}

func (i *hmacIssuer) Parse(raw string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		// When a kid is present we trust only the secret that
		// matches it. When it isn't, we fall back to the active
		// secret to stay compatible with tokens issued before
		// rotation was introduced.
		if kid, ok := t.Header["kid"].(string); ok && kid != "" {
			if k, found := i.all[kid]; found {
				return k.secret, nil
			}
			return nil, errors.New("unknown key id")
		}
		return i.active.secret, nil
	}, jwt.WithIssuer(i.issuer), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, errs.Unauthorized("jwt.invalid", "invalid or expired token")
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, errs.Unauthorized("jwt.invalid", "invalid or expired token")
	}
	return claims, nil
}

// PublicKeySet returns a JWKS-compatible view of the issuer's public
// material. For HS256 secrets, the returned set is intentionally empty
// — symmetric secrets must not be exposed. Asymmetric issuers (RS256,
// EdDSA) override this to publish their public keys at
// /.well-known/jwks.json.
func (i *hmacIssuer) PublicKeySet() JWKS { return JWKS{Keys: []JWK{}} }

// PublicKeySetProvider is implemented by issuers that can expose a
// public key set. The HTTP layer uses it to serve JWKS only when the
// configured issuer has something useful to publish.
type PublicKeySetProvider interface {
	PublicKeySet() JWKS
}

// JWKS is a minimal JSON Web Key Set as defined by RFC 7517.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// JWK is a minimal JSON Web Key. Only the fields required for
// verification of HS-prefixed (no-op) and RS-prefixed signatures are
// modelled; extend if you switch to EdDSA or ECDSA.
type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Kid string `json:"kid"`
	Alg string `json:"alg,omitempty"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
}
