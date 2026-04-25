-- API key authentication.
-- Each row represents one issued, possibly-revoked API key. The
-- secret portion is never stored - only a SHA-256 hash. Prefix
-- is the public-visible part shown in admin UIs and used as a
-- fast lookup index (`gf_live_<8>`).
--
-- Scopes is a denormalised array of permission codes the key may
-- use; it intentionally duplicates RBAC role->permission grants
-- because keys exist for service-to-service calls and shouldn't
-- inherit a user's full role surface.
CREATE TABLE IF NOT EXISTS api_keys (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    prefix       TEXT        NOT NULL UNIQUE,
    hash         TEXT        NOT NULL,
    name         TEXT        NOT NULL,
    user_id      UUID        NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id    UUID        NULL,
    scopes       TEXT[]      NOT NULL DEFAULT '{}',
    expires_at   TIMESTAMPTZ NULL,
    last_used_at TIMESTAMPTZ NULL,
    revoked_at   TIMESTAMPTZ NULL,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ NULL,
    created_by   UUID        NULL,
    updated_by   UUID        NULL
);

CREATE INDEX IF NOT EXISTS api_keys_user_idx
    ON api_keys (user_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS api_keys_active_idx
    ON api_keys (prefix)
    WHERE deleted_at IS NULL AND revoked_at IS NULL;
