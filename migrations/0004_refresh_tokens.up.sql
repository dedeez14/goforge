-- refresh_tokens tracks every issued refresh token so the framework
-- can enforce single-use semantics (rotation) and detect replay
-- attempts. We store SHA-256 of the JTI to keep raw token IDs out of
-- the database; an attacker with read-only DB access cannot mint
-- replays from the rows alone.
CREATE TABLE IF NOT EXISTS refresh_tokens (
    jti_hash    TEXT PRIMARY KEY,
    user_id     UUID        NOT NULL,
    issued_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ NULL,
    revoked_at  TIMESTAMPTZ NULL,
    replaced_by TEXT        NULL  -- jti_hash of the replacement token
);

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id
    ON refresh_tokens (user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires_at
    ON refresh_tokens (expires_at)
    WHERE revoked_at IS NULL;
