-- Reverse migration 0010: convert the partitioned audit_log back to
-- a single flat table. Data is preserved.

BEGIN;

-- 1. Park the partitioned parent.
ALTER TABLE audit_log RENAME TO audit_log_partitioned;

-- 2. Rename indexes on the partitioned parent to clear the namespace
--    for the flat table we are about to recreate.
ALTER INDEX audit_log_occurred_at_idx
    RENAME TO audit_log_partitioned_occurred_at_idx;
ALTER INDEX audit_log_actor_idx
    RENAME TO audit_log_partitioned_actor_idx;
ALTER INDEX audit_log_tenant_idx
    RENAME TO audit_log_partitioned_tenant_idx;
ALTER INDEX audit_log_action_idx
    RENAME TO audit_log_partitioned_action_idx;

-- 3. Recreate the original flat schema (from migration 0006).
CREATE TABLE audit_log (
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

CREATE INDEX audit_log_occurred_at_idx ON audit_log (occurred_at DESC);
CREATE INDEX audit_log_actor_idx       ON audit_log (actor);
CREATE INDEX audit_log_tenant_idx      ON audit_log (tenant_id);
CREATE INDEX audit_log_action_idx      ON audit_log (action);

-- 4. Copy data back. ON CONFLICT tolerates the unlikely duplicate
--    that could slip in if the partitioned variant somehow recorded
--    the same id in two partitions (partition key makes it possible
--    in theory, though gen_random_uuid() makes it vanishingly rare).
INSERT INTO audit_log (
    id, occurred_at, tenant_id, actor, actor_kind, action,
    resource, request_id, ip, user_agent, before, after, metadata
)
SELECT
    id, occurred_at, tenant_id, actor, actor_kind, action,
    resource, request_id, ip, user_agent, before, after, metadata
FROM audit_log_partitioned
ON CONFLICT (id) DO NOTHING;

-- 5. Drop partitioned parent + all children + default partition.
DROP TABLE audit_log_partitioned CASCADE;

COMMIT;
