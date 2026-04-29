-- sessions materialises each "logged-in device" as a first-class row
-- so users can see every active login, revoke individual sessions, or
-- logout everywhere. The existing refresh_tokens table already tracks
-- single-use rotation but its chain of (jti, replaced_by) entries is
-- an implementation detail - users think in terms of devices, not
-- token chains. Sessions own that chain.
--
-- Lifecycle:
--   * Register / Login creates a new sessions row and issues the
--     first refresh_token whose session_id points here.
--   * Refresh rotates the token inside the same session, bumping
--     last_used_at and extending expires_at to the new refresh TTL.
--   * Revoke sets revoked_at; the application also flips revoked_at
--     on every refresh_token tied to the session so a subsequent
--     /refresh call cannot resurrect it.
CREATE TABLE IF NOT EXISTS sessions (
    id            UUID PRIMARY KEY,
    user_id       UUID        NOT NULL,
    user_agent    TEXT        NOT NULL DEFAULT '',
    ip            TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    revoked_at    TIMESTAMPTZ NULL
);

-- Fast per-user list of active sessions (what GET /me/sessions returns).
CREATE INDEX IF NOT EXISTS idx_sessions_user_active
    ON sessions (user_id)
    WHERE revoked_at IS NULL;

-- Sweep support for expired rows.
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at
    ON sessions (expires_at)
    WHERE revoked_at IS NULL;

-- Link every refresh token to the session it belongs to. Nullable so
-- tokens minted before this migration (and future non-interactive
-- flows, e.g. API-key exchange) stay valid without a back-fill.
ALTER TABLE refresh_tokens
    ADD COLUMN IF NOT EXISTS session_id UUID NULL;

-- Revoke cascade is handled in application code (we need to set
-- revoked_at, not DELETE), but an index keeps the cascade cheap.
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_session_id
    ON refresh_tokens (session_id)
    WHERE session_id IS NOT NULL;
