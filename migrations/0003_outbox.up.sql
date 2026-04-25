CREATE TABLE IF NOT EXISTS outbox_messages (
    id            UUID PRIMARY KEY,
    topic         TEXT        NOT NULL,
    tenant_id     TEXT        NULL,
    payload       JSONB       NOT NULL,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at  TIMESTAMPTZ NULL,
    attempts      INT         NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_outbox_messages_pending
    ON outbox_messages (occurred_at)
    WHERE published_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_outbox_messages_tenant_topic
    ON outbox_messages (tenant_id, topic, occurred_at);
