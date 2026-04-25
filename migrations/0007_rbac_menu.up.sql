-- Role-Based Access Control + dynamic Menu Management.
--
-- Design choices:
--   * Permissions are identified by a stable dotted `code` (e.g.
--     "users.read", "menu.manage"). Code is globally unique so any
--     route, menu entry, or job can reference a permission without
--     knowing about tenants.
--   * Roles are tenant-scoped (tenant_id may be NULL for global
--     roles shipped with the application, e.g. "super_admin").
--     (tenant_id, code) is unique.
--   * user_roles is the grant table; one row per user × role × tenant.
--   * Menus form a tree (parent_id self-FK) and may optionally
--     require a permission code; a NULL required_permission_code
--     makes the menu entry visible to every authenticated user.
--   * All tables carry the goforge audit-column convention:
--     created_at / updated_at / deleted_at + created_by / updated_by.
--     deleted_at is used as a soft-delete tombstone; repositories
--     always filter `deleted_at IS NULL`.

CREATE TABLE IF NOT EXISTS permissions (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    code        TEXT        NOT NULL UNIQUE,
    resource    TEXT        NOT NULL,
    action      TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',

    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ NULL,
    created_by  UUID        NULL,
    updated_by  UUID        NULL
);

CREATE INDEX IF NOT EXISTS permissions_resource_idx
    ON permissions (resource)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS roles (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID        NULL,
    code        TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    is_system   BOOLEAN     NOT NULL DEFAULT FALSE,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ NULL,
    created_by  UUID        NULL,
    updated_by  UUID        NULL
);

-- (tenant, code) uniqueness. COALESCE lets us treat NULL tenant_id as
-- its own "global" bucket, giving us both "unique per tenant" and
-- "unique globally across global roles" with one index.
CREATE UNIQUE INDEX IF NOT EXISTS roles_tenant_code_uniq
    ON roles (COALESCE(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid), code)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS role_permissions (
    role_id       UUID        NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id UUID        NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by    UUID        NULL,
    PRIMARY KEY (role_id, permission_id)
);

CREATE INDEX IF NOT EXISTS role_permissions_permission_idx
    ON role_permissions (permission_id);

CREATE TABLE IF NOT EXISTS user_roles (
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id    UUID        NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    tenant_id  UUID        NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by UUID        NULL,
    PRIMARY KEY (user_id, role_id, COALESCE(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid))
);

CREATE INDEX IF NOT EXISTS user_roles_user_idx
    ON user_roles (user_id);

CREATE INDEX IF NOT EXISTS user_roles_role_idx
    ON user_roles (role_id);

CREATE TABLE IF NOT EXISTS menus (
    id                         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                  UUID        NULL,
    parent_id                  UUID        NULL REFERENCES menus(id) ON DELETE CASCADE,
    code                       TEXT        NOT NULL,
    label                      TEXT        NOT NULL,
    icon                       TEXT        NOT NULL DEFAULT '',
    path                       TEXT        NOT NULL DEFAULT '',
    sort_order                 INTEGER     NOT NULL DEFAULT 0,
    required_permission_code   TEXT        NULL,
    is_visible                 BOOLEAN     NOT NULL DEFAULT TRUE,
    metadata                   JSONB       NOT NULL DEFAULT '{}'::jsonb,

    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at                 TIMESTAMPTZ NULL,
    created_by                 UUID        NULL,
    updated_by                 UUID        NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS menus_tenant_code_uniq
    ON menus (COALESCE(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid), code)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS menus_parent_idx
    ON menus (parent_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS menus_perm_idx
    ON menus (required_permission_code)
    WHERE deleted_at IS NULL AND required_permission_code IS NOT NULL;

-- Bootstrap: a minimal set of permissions the framework relies on.
-- Apps may add more; these are idempotent so re-runs are safe.
INSERT INTO permissions (code, resource, action, description)
VALUES
    ('rbac.manage',   'rbac',   'manage', 'Manage roles, permissions and assignments'),
    ('menu.manage',   'menu',   'manage', 'Manage menu entries'),
    ('menu.view',     'menu',   'view',   'View menu entries applicable to the user'),
    ('users.manage',  'users',  'manage', 'Create, update and deactivate users'),
    ('users.read',    'users',  'read',   'Read user records'),
    ('audit.read',    'audit',  'read',   'Read the append-only audit log')
ON CONFLICT (code) DO NOTHING;
