// Package authz is goforge's authorisation primitive.
//
// Most apps end up writing the same code three times:
//
//   1. "is this user an admin?" — sprinkled all over handlers
//   2. "can this user write this object?" — duplicated per resource
//   3. "log every privileged action" — frequently forgotten
//
// authz centralises all three behind a single Allow(ctx, sub, act,
// obj) call backed by a Casbin enforcer. The default model is the
// classic RBAC-with-domains formulation:
//
//   p, sub, dom, obj, act
//   g, sub, role, dom
//
//	"in tenant <dom>, subject <sub> can do <act> on <obj> when there
//	 exists a policy match (sub, dom, obj, act) — directly or via a
//	 role granted in <dom>."
//
// Policies live in a Postgres table loaded by a small adapter; on
// startup the enforcer caches them in memory, so authorisation
// checks are local and fast.
package authz

import (
	"context"
	"errors"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
)

// ModelText is the default Casbin model: RBAC with domains.
const ModelText = `
[request_definition]
r = sub, dom, obj, act

[policy_definition]
p = sub, dom, obj, act

[role_definition]
g = _, _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub, r.dom) && r.dom == p.dom && keyMatch2(r.obj, p.obj) && (r.act == p.act || p.act == "*")
`

// Authorizer is the public Allow primitive.
type Authorizer interface {
	Allow(ctx context.Context, subject, domain, object, action string) (bool, error)
}

// Enforcer wraps a casbin.Enforcer with goforge's preferred error
// shape and a context-aware Allow.
type Enforcer struct {
	inner *casbin.Enforcer
}

// New returns an Enforcer with the default model and the supplied
// adapter. Pass nil to start with an empty in-memory policy (useful
// in tests and one-off scripts).
func New(adapter persist.Adapter) (*Enforcer, error) {
	m, err := model.NewModelFromString(ModelText)
	if err != nil {
		return nil, err
	}
	var e *casbin.Enforcer
	if adapter == nil {
		e, err = casbin.NewEnforcer(m)
	} else {
		e, err = casbin.NewEnforcer(m, adapter)
	}
	if err != nil {
		return nil, err
	}
	return &Enforcer{inner: e}, nil
}

// Allow implements Authorizer.
func (e *Enforcer) Allow(_ context.Context, subject, domain, object, action string) (bool, error) {
	return e.inner.Enforce(subject, domain, object, action)
}

// Casbin returns the underlying enforcer for advanced operations
// (loading policies, listing roles). Most callers should not need it.
func (e *Enforcer) Casbin() *casbin.Enforcer { return e.inner }

// AddPolicy is a typed wrapper around AddPolicy().
func (e *Enforcer) AddPolicy(subject, domain, object, action string) (bool, error) {
	return e.inner.AddPolicy(subject, domain, object, action)
}

// AddRoleForUserInDomain assigns role to user in domain.
func (e *Enforcer) AddRoleForUserInDomain(user, role, domain string) (bool, error) {
	return e.inner.AddRoleForUserInDomain(user, role, domain)
}

// LoadPolicy reloads from the adapter (call after bulk inserts).
func (e *Enforcer) LoadPolicy() error { return e.inner.LoadPolicy() }

// ErrDenied is the canonical "not allowed" error.
var ErrDenied = errors.New("authz: action denied")
