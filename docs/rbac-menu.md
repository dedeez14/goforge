# RBAC + Dynamic Menu Management

goforge ships a Role-Based Access Control system and a permission-aware
menu tree. Both are dynamic and tenant-aware: there's nothing
hard-coded in the binary about which roles, permissions, or menu
entries exist — everything is data, managed at runtime via HTTP.

## Concepts

- **Permission** — an atomic capability identified by a stable dotted
  code (`users.read`, `menu.manage`). Codes are globally unique and
  immutable; the rest of the row (resource, action, description) is
  free-form metadata.
- **Role** — a named bundle of permissions. Roles are tenant-scoped:
  `tenant_id NULL` is a global role shipped with the application;
  `tenant_id = <uuid>` is a per-tenant role. `(tenant_id, code)` is
  unique. `is_system = true` roles are immutable from the API.
- **Grant** — a row in `user_roles` saying "user U holds role R in
  tenant T". A user may hold multiple roles in the same tenant; the
  effective permission set is the union of all role permissions.
- **Menu** — a tree-shaped record describing a single navigation
  entry. Each entry may carry an optional `required_permission_code`;
  when set, the entry is only visible to users that hold that
  permission. `is_visible = false` hides the entry unconditionally.

## Migration

The schema lives in `migrations/0007_rbac_menu.up.sql`:

| Table | Purpose |
| --- | --- |
| `permissions` | Catalog of permission codes |
| `roles` | Roles (global or tenant-scoped) |
| `role_permissions` | Many-to-many join: role × permission |
| `user_roles` | Grant table: user × role × tenant |
| `menus` | Menu tree (parent_id self-FK) |

All tables follow the goforge audit-column convention
(`created_at` / `updated_at` / `deleted_at` + `created_by` /
`updated_by`); repositories filter `deleted_at IS NULL`.

## Bootstrapping the first super-admin

The migration does not seed any role grants — bootstrapping is up to
the operator. A typical first-run flow:

```sql
-- 1. Create an empty 'super_admin' role.
INSERT INTO roles (code, name, description, is_system)
VALUES ('super_admin', 'Super Admin', 'Owns everything', TRUE);

-- 2. Grant every existing permission to it.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.code = 'super_admin' AND p.deleted_at IS NULL;

-- 3. Grant the role to your first user.
INSERT INTO user_roles (user_id, role_id)
SELECT '<user-uuid>', id FROM roles WHERE code = 'super_admin';
```

After this the user can hit every `rbac.manage` / `menu.manage`
endpoint and continue managing the system through the API.

## HTTP surface

All endpoints are versioned under `/api/v1` and require a bearer
access token. The admin endpoints additionally require the
`rbac.manage` or `menu.manage` permission, enforced by
`middleware.RequirePermission`.

```
GET    /api/v1/me/access                 — caller's roles + perm codes
GET    /api/v1/menus/mine                — visible menu tree (filtered)

# rbac.manage
GET    /api/v1/permissions
POST   /api/v1/permissions
GET    /api/v1/permissions/:id
PATCH  /api/v1/permissions/:id
DELETE /api/v1/permissions/:id

GET    /api/v1/roles
POST   /api/v1/roles
GET    /api/v1/roles/:id
PATCH  /api/v1/roles/:id
DELETE /api/v1/roles/:id
GET    /api/v1/roles/:id/permissions
PUT    /api/v1/roles/:id/permissions
PUT    /api/v1/users/:id/roles

# menu.manage
GET    /api/v1/menus
GET    /api/v1/menus/tree
POST   /api/v1/menus
GET    /api/v1/menus/:id
PATCH  /api/v1/menus/:id
DELETE /api/v1/menus/:id
```

The active tenant is taken from the `X-Tenant-ID` header by default;
plug a custom `middleware.TenantResolver` into
`server.AccessControl{Tenant: ...}` if your app derives the tenant
from a subdomain or path segment instead.

## Filtering rules for `/menus/mine`

The visible-tree filter applies three rules in this order:

1. `is_visible = false` → drop unconditionally.
2. `required_permission_code IS NULL` → keep (visible to every
   authenticated user).
3. `required_permission_code = <code>` → keep only when the caller's
   effective permission set contains `<code>`.

Children whose parent was dropped are also dropped — there is no way
to "skip" a denied parent and still expose its descendants.

## Guarding your own routes

Use `middleware.RequirePermission(code, resolver, tenant)` to gate
any handler:

```go
api.Post("/v1/orders/:id/refund",
    middleware.Auth(tokens),
    middleware.RequirePermission("orders.refund", accessUC, nil),
    orders.Refund,
)
```

For "either A or B" semantics use `RequireAnyPermission([]string{...},
resolver, tenant)`. Both middlewares cache the resolved permission
codes on the request locals (`middleware.PermissionsFromCtx(c)`) so
your handler can read them without a second DB round-trip.
