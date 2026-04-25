CREATE TABLE IF NOT EXISTS idempotency_keys (
    key            TEXT PRIMARY KEY,
    method         TEXT        NOT NULL,
    path           TEXT        NOT NULL,
    request_hash   TEXT        NOT NULL,
    status_code    INT         NOT NULL,
    content_type   TEXT        NOT NULL,
    body           BYTEA       NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_idempotency_keys_expires_at ON idempotency_keys(expires_at);
