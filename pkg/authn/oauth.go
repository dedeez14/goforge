package authn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/oauth2"

	"github.com/dedeez14/goforge/pkg/cache"
)

// OAuthProvider holds a single provider's configuration.
type OAuthProvider struct {
	Name         string
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
	Scopes       []string
	RedirectURL  string
}

// OAuth wraps a provider with a state cache so callbacks can be
// validated. The same struct supports any provider that follows the
// authorization-code-with-PKCE flow (Google, GitHub, GitLab, Apple,
// Microsoft, Auth0).
type OAuth struct {
	Provider OAuthProvider
	Cache    cache.Cache
	StateTTL time.Duration
	HTTP     *http.Client
}

// NewOAuth returns an OAuth helper.
func NewOAuth(p OAuthProvider, c cache.Cache) *OAuth {
	return &OAuth{
		Provider: p,
		Cache:    c,
		StateTTL: 10 * time.Minute,
		HTTP:     &http.Client{Timeout: 15 * time.Second},
	}
}

// AuthCodeURL returns the URL the browser should be redirected to.
// The framework generates a random state, stores its PKCE verifier
// keyed by that state, and forwards the corresponding code_challenge
// to the provider. The caller stashes state in a signed cookie /
// session so it can be replayed on /callback.
func (o *OAuth) AuthCodeURL(ctx context.Context) (authURL, state string, err error) {
	state, err = randURLToken(24)
	if err != nil {
		return "", "", err
	}
	verifier, err := randURLToken(48)
	if err != nil {
		return "", "", err
	}
	if err := o.Cache.Set(ctx, "oauth:state:"+state, []byte(verifier), o.StateTTL); err != nil {
		return "", "", err
	}
	cfg := o.config()
	challenge := pkceChallenge(verifier)
	u := cfg.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	return u, state, nil
}

// Exchange completes the callback. It validates that state was
// issued by us, recovers the PKCE verifier, swaps the code for a
// token, then fetches the userinfo document.
func (o *OAuth) Exchange(ctx context.Context, state, code string) (*oauth2.Token, json.RawMessage, error) {
	if state == "" || code == "" {
		return nil, nil, errors.New("authn: missing state or code")
	}
	verifierBytes, err := o.Cache.Get(ctx, "oauth:state:"+state)
	if err != nil {
		if errors.Is(err, cache.ErrMiss) {
			return nil, nil, errors.New("authn: unknown or expired state")
		}
		return nil, nil, err
	}
	_ = o.Cache.Del(ctx, "oauth:state:"+state)
	verifier := string(verifierBytes)

	cfg := o.config()
	tok, err := cfg.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return nil, nil, fmt.Errorf("oauth exchange: %w", err)
	}

	if o.Provider.UserInfoURL == "" {
		return tok, nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.Provider.UserInfoURL, nil)
	if err != nil {
		return tok, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return tok, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return tok, nil, err
	}
	if resp.StatusCode/100 != 2 {
		return tok, nil, fmt.Errorf("userinfo: HTTP %d", resp.StatusCode)
	}
	return tok, body, nil
}

func (o *OAuth) config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     o.Provider.ClientID,
		ClientSecret: o.Provider.ClientSecret,
		RedirectURL:  o.Provider.RedirectURL,
		Endpoint:     oauth2.Endpoint{AuthURL: o.Provider.AuthURL, TokenURL: o.Provider.TokenURL},
		Scopes:       o.Provider.Scopes,
	}
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// HelperEnsureHTTPS returns an error if u is not https — most
// providers reject http callbacks except for localhost. Use this in
// configuration validation to fail fast.
func HelperEnsureHTTPS(redirect string) error {
	u, err := url.Parse(redirect)
	if err != nil {
		return err
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" {
		return nil
	}
	return errors.New("authn: oauth redirect must be HTTPS in production")
}

// Random TLS-grade randomness; we keep it inline so callers don't
// reach into crypto/rand directly.
var _ = rand.Reader
