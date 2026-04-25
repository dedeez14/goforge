package middleware

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/dedeez14/goforge/pkg/errs"
)

// staticResolver implements PermissionResolver and always returns
// the same set of codes so we can exercise RequirePermission alone.
type staticResolver struct {
	codes []string
	err   error
	hits  int
}

func (s *staticResolver) EffectivePermissions(_ context.Context, _ uuid.UUID, _ *uuid.UUID) ([]string, error) {
	s.hits++
	return s.codes, s.err
}

// makeApp builds a tiny app that:
//   - uses APIKeyOrJWTAuth at the top, with the supplied authenticate;
//   - the JWT branch is a no-op that just stores a fixed user ID;
//   - the route gates on RequirePermission(want, ...).
//
// We then inspect the recorder for status / body / resolver hit count.
func makeApp(t *testing.T, want string, authenticate APIKeyAuthenticate, resolver *staticResolver) *fiber.App {
	t.Helper()
	app := fiber.New()
	jwtAuth := func(c *fiber.Ctx) error {
		// simulate a valid JWT - same id every time
		c.Locals(CtxKeyUserID, uuid.MustParse("11111111-1111-1111-1111-111111111111"))
		return c.Next()
	}
	app.Use(APIKeyOrJWTAuth(authenticate, jwtAuth))
	app.Use(RequirePermission(want, resolver, nil))
	app.Get("/x", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func TestAPIKey_GrantedScope_BypassesRBACLookup(t *testing.T) {
	resolver := &staticResolver{codes: nil, err: errors.New("must not be called")}
	uid := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	auth := func(_ context.Context, _ string) (uuid.UUID, []string, error) {
		return uid, []string{"deploys.create"}, nil
	}
	app := makeApp(t, "deploys.create", auth, resolver)

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer gf_test_aaaaaaaaaaaa_"+pad64('a'))
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("test req: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d body=%q", resp.StatusCode, body)
	}
	if resolver.hits != 0 {
		t.Fatalf("RBAC resolver should be skipped for API-key requests, got %d hits", resolver.hits)
	}
}

func TestAPIKey_NarrowScope_DeniesEvenIfRoleWouldGrant(t *testing.T) {
	// The role lookup *would* satisfy "deploys.create", but an API
	// key with a narrower scope must not piggyback on the owning
	// user's roles. RequirePermission must reject without ever
	// consulting the resolver.
	resolver := &staticResolver{codes: []string{"deploys.create"}, err: nil}
	uid := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	auth := func(_ context.Context, _ string) (uuid.UUID, []string, error) {
		return uid, []string{"reports.read"}, nil
	}
	app := makeApp(t, "deploys.create", auth, resolver)

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer gf_test_aaaaaaaaaaaa_"+pad64('a'))
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("test req: %v", err)
	}
	if resp.StatusCode != 403 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 403, got %d body=%q", resp.StatusCode, body)
	}
	if resolver.hits != 0 {
		t.Fatalf("API-key short-circuit must not consult RBAC resolver; got %d hits", resolver.hits)
	}
}

func TestAPIKey_WildcardScope_GrantsAnyPermission(t *testing.T) {
	resolver := &staticResolver{codes: nil, err: errors.New("must not be called")}
	uid := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	auth := func(_ context.Context, _ string) (uuid.UUID, []string, error) {
		return uid, []string{"*"}, nil
	}
	app := makeApp(t, "anything.goes", auth, resolver)

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer gf_test_aaaaaaaaaaaa_"+pad64('a'))
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("test req: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("wildcard scope should grant any permission; got %d", resp.StatusCode)
	}
}

func TestJWT_FallbackPathStillUsesRBACResolver(t *testing.T) {
	// A JWT request (bearer doesn't look like an API key) must not
	// touch APIKeyAuth; it must consult the resolver as before.
	resolver := &staticResolver{codes: []string{"deploys.create"}}
	authCalled := false
	auth := func(_ context.Context, _ string) (uuid.UUID, []string, error) {
		authCalled = true
		return uuid.Nil, nil, errs.Unauthorized("apikey.invalid", "should not be called")
	}
	app := makeApp(t, "deploys.create", auth, resolver)

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer header.payload.sig") // looks like a JWT, not gf_*
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("test req: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d body=%q", resp.StatusCode, body)
	}
	if authCalled {
		t.Fatalf("APIKeyAuth must not be invoked for non-gf_ bearers")
	}
	if resolver.hits != 1 {
		t.Fatalf("expected one RBAC lookup for the JWT branch; got %d", resolver.hits)
	}
}

func TestAPIKey_AuthenticatorErrorBecomes401Body(t *testing.T) {
	resolver := &staticResolver{codes: nil}
	auth := func(_ context.Context, _ string) (uuid.UUID, []string, error) {
		return uuid.Nil, nil, errs.Unauthorized("apikey.invalid", "API key is invalid")
	}
	app := makeApp(t, "x", auth, resolver)

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer gf_test_aaaaaaaaaaaa_"+pad64('a'))
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("test req: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

// pad64 builds a 64-char "secret" string so the test bearer parses
// as gf_<env>_<id>_<secret>. The middleware-only tests stub
// APIKeyAuthenticate so the secret content is not actually verified
// here; we just need the bearer to satisfy LooksLikeAPIKey.
//
//nolint:unparam // c stays explicit so future test cases can vary it
func pad64(c byte) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = c
	}
	return string(out)
}
