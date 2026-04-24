package security

import (
	"errors"
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
type Claims struct {
	jwt.RegisteredClaims
	Kind TokenKind `json:"typ"`
}

// TokenIssuer mints and verifies JWTs.
type TokenIssuer interface {
	Issue(subject uuid.UUID, kind TokenKind) (string, time.Time, error)
	Parse(token string) (*Claims, error)
}

type hmacIssuer struct {
	secret     []byte
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewTokenIssuer returns an HS256 JWT issuer configured from config.JWT.
func NewTokenIssuer(cfg config.JWT) TokenIssuer {
	return &hmacIssuer{
		secret:     []byte(cfg.Secret),
		issuer:     cfg.Issuer,
		accessTTL:  cfg.AccessTTL,
		refreshTTL: cfg.RefreshTTL,
	}
}

func (i *hmacIssuer) Issue(subject uuid.UUID, kind TokenKind) (string, time.Time, error) {
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
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(i.secret)
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
		return i.secret, nil
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
