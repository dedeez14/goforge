// Package authn ships the modern auth primitives goforge apps reach
// for once 'email + password' is no longer enough:
//
//   - TOTP (RFC 6238) for second-factor authentication.
//   - Magic-link tokens — opaque, single-use, short-TTL.
//   - OAuth2/OIDC helpers — wrap golang.org/x/oauth2 with a
//     state-and-PKCE flow and a typed result type.
//
// Each piece is independent: an app can adopt magic-link without
// also turning on TOTP, and vice-versa.
package authn

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"image/png"
	"io"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTPSecret is a base32-encoded TOTP secret as accepted by RFC
// 6238 / Google Authenticator / 1Password.
type TOTPSecret string

// NewTOTPSecret generates a fresh 160-bit secret.
func NewTOTPSecret() (TOTPSecret, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return TOTPSecret(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

// Provision builds the otpauth:// URI an authenticator app turns
// into a QR code. issuer is your product name; account is typically
// the user's email.
func Provision(secret TOTPSecret, issuer, account string) (*otp.Key, error) {
	return otp.NewKeyFromURL("otpauth://totp/" + issuer + ":" + account +
		"?secret=" + string(secret) +
		"&issuer=" + issuer +
		"&algorithm=SHA1&digits=6&period=30")
}

// QRPNG renders the otpauth URI as a PNG QR code into w. 256x256 is
// a comfortable default that scans cleanly on any phone.
func QRPNG(key *otp.Key, w io.Writer, size int) error {
	if size <= 0 {
		size = 256
	}
	img, err := key.Image(size, size)
	if err != nil {
		return err
	}
	return png.Encode(w, img)
}

// Verify checks a 6-digit code against the secret. The default
// totp.Validate already supports a +/- 1 step skew, which is what
// every major authenticator app expects.
func Verify(secret TOTPSecret, code string) bool {
	return totp.Validate(code, string(secret))
}

// ErrInvalidCode is returned by callers that want to map a failed
// verify to a typed error.
var ErrInvalidCode = errors.New("authn: invalid TOTP code")
