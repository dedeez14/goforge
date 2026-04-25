-- Append-only audit table. Goforge handlers should call
-- audit.Log(ctx, who, action, resource, before, after) for every
-- privileged action; the row is permanent and never updated.
--
-- The before/after columns hold redacted JSON snapshots so an
-- operator can answer "what changed and who did it" without joining
-- a half-dozen domain tables.

CREATE TABLE IF NOT EXISTS audit_log (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    tenant_id   TEXT        NULL,
    actor       TEXT        NULL,
    actor_kind  TEXT        NOT NULL DEFAULT 'user',
    action      TEXT        NOT NULL,
    resource    TEXT        NULL,
    request_id  TEXT        NULL,
    ip          TEXT        NULL,
    user_agent  TEXT        NULL,
    before      JSONB       NULL,
    after       JSONB       NULL,
    metadata    JSONB       NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS audit_log_occurred_at_idx ON audit_log (occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_log_actor_idx       ON audit_log (actor);
CREATE INDEX IF NOT EXISTS audit_log_tenant_idx      ON audit_log (tenant_id);
CREATE INDEX IF NOT EXISTS audit_log_action_idx      ON audit_log (action);
