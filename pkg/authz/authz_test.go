package authz

import (
	"context"
	"testing"
)

func mustEnforcer(t *testing.T) *Enforcer {
	t.Helper()
	e, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func TestAllow_DirectPolicyGrants(t *testing.T) {
	t.Parallel()
	e := mustEnforcer(t)
	if _, err := e.AddPolicy("alice", "tenantA", "/users/*", "read"); err != nil {
		t.Fatalf("AddPolicy: %v", err)
	}
	if _, err := e.AddRoleForUserInDomain("alice", "alice", "tenantA"); err != nil {
		t.Fatalf("AddRoleForUserInDomain: %v", err)
	}
	ok, err := e.Allow(context.Background(), "alice", "tenantA", "/users/123", "read")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !ok {
		t.Fatal("alice should be allowed")
	}
}

func TestAllow_DeniedByDefault(t *testing.T) {
	t.Parallel()
	e := mustEnforcer(t)
	ok, err := e.Allow(context.Background(), "bob", "tenantA", "/users/1", "read")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if ok {
		t.Fatal("default policy must deny")
	}
}

func TestAllow_RoleGrant(t *testing.T) {
	t.Parallel()
	e := mustEnforcer(t)
	if _, err := e.AddPolicy("admin", "tenantA", "/*", "*"); err != nil {
		t.Fatalf("AddPolicy: %v", err)
	}
	if _, err := e.AddRoleForUserInDomain("alice", "admin", "tenantA"); err != nil {
		t.Fatalf("AddRole: %v", err)
	}
	ok, err := e.Allow(context.Background(), "alice", "tenantA", "/anything/works", "delete")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !ok {
		t.Fatal("alice with admin role must be allowed")
	}
}

func TestAllow_RoleDoesNotCrossTenants(t *testing.T) {
	t.Parallel()
	e := mustEnforcer(t)
	if _, err := e.AddPolicy("admin", "tenantA", "/*", "*"); err != nil {
		t.Fatalf("AddPolicy: %v", err)
	}
	if _, err := e.AddRoleForUserInDomain("alice", "admin", "tenantA"); err != nil {
		t.Fatalf("AddRole: %v", err)
	}
	ok, err := e.Allow(context.Background(), "alice", "tenantB", "/anything", "read")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if ok {
		t.Fatal("admin role in tenantA must not leak into tenantB")
	}
}
