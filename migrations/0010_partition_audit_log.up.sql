-- Convert audit_log to a PARTITIONed table (range on occurred_at,
-- one partition per month). This keeps the biggest append-only table
-- in the framework from degrading into a single multi-hundred-million
-- row heap over time.
--
-- # Rationale
--
-- `audit_log` grows on every privileged action and is never mutated
-- after insert — the exact shape that partitioning was designed for.
-- Querying by time range (the overwhelmingly common access pattern
-- from the admin UI) becomes a partition-scan instead of a full-table
-- scan, and dropping cold data becomes an O(1) `DROP TABLE` instead
-- of a row-by-row DELETE.
--
-- # Constraint change
--
-- PostgreSQL requires the partition key to appear in every UNIQUE
-- constraint on a partitioned table. audit_log's PK was `id` alone;
-- it becomes `(id, occurred_at)`. Queries that look up by `id` alone
-- still work (the index on id is still present via the PK), they
-- just scan every partition rather than one. Admin UI already
-- filters by date range in the default case, so this is fine.
--
-- # Safety
--
-- The migration is wrapped in a single transaction: either the new
-- partitioned table replaces the old one fully, or nothing changes.
-- A DEFAULT partition catches any row whose occurred_at falls
-- outside the pre-created window (clock skew, historical import).
-- The pkg/partition.Maintainer keeps the window rolled forward.

BEGIN;

-- 1. Park the old table under a new name so we can recreate
--    `audit_log` as partitioned.
ALTER TABLE audit_log RENAME TO audit_log_legacy;

-- 2. Rename indexes on the legacy table to avoid name collisions
--    when we re-create them on the partitioned parent.
ALTER INDEX audit_log_occurred_at_idx RENAME TO audit_log_legacy_occurred_at_idx;
ALTER INDEX audit_log_actor_idx       RENAME TO audit_log_legacy_actor_idx;
ALTER INDEX audit_log_tenant_idx      RENAME TO audit_log_legacy_tenant_idx;
ALTER INDEX audit_log_action_idx      RENAME TO audit_log_legacy_action_idx;

-- 3. New partitioned parent. Column list + types are an exact copy of
--    migration 0006 plus the (id, occurred_at) compound PK required
--    by PostgreSQL's partitioning rules.
CREATE TABLE audit_log (
    id          UUID        NOT NULL DEFAULT gen_random_uuid(),
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
    metadata    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (id, occurred_at)
) PARTITION BY RANGE (occurred_at);

-- 4. Indexes on the parent are propagated to every child partition
--    at attach time.
CREATE INDEX audit_log_occurred_at_idx ON audit_log (occurred_at DESC);
CREATE INDEX audit_log_actor_idx       ON audit_log (actor);
CREATE INDEX audit_log_tenant_idx      ON audit_log (tenant_id);
CREATE INDEX audit_log_action_idx      ON audit_log (action);

-- 5. Pre-create a window of partitions: 2 months back, current, and
--    the next 6 months. `pkg/partition.Maintainer` rolls this window
--    forward daily in production.
DO $$
DECLARE
    base DATE := date_trunc('month', now())::DATE;
    d    DATE;
    lo   DATE;
    hi   DATE;
    pname TEXT;
BEGIN
    FOR i IN -2..6 LOOP
        d := base + (i || ' months')::INTERVAL;
        lo := d;
        hi := d + INTERVAL '1 month';
        pname := format('audit_log_y%sm%s',
            to_char(d, 'YYYY'), to_char(d, 'MM'));
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF audit_log FOR VALUES FROM (%L) TO (%L)',
            pname, lo, hi
        );
    END LOOP;
END $$;

-- 6. Default partition catches any row outside the rolling window —
--    for example, historical imports with pre-2024 timestamps.
CREATE TABLE IF NOT EXISTS audit_log_default PARTITION OF audit_log DEFAULT;

-- 7. Copy existing rows over. The default partition catches anything
--    outside the pre-created range so this INSERT cannot fail on
--    out-of-range timestamps. ON CONFLICT DO NOTHING keeps the
--    migration idempotent in the rare case it is rerun manually.
INSERT INTO audit_log (
    id, occurred_at, tenant_id, actor, actor_kind, action,
    resource, request_id, ip, user_agent, before, after, metadata
)
SELECT
    id, occurred_at, tenant_id, actor, actor_kind, action,
    resource, request_id, ip, user_agent, before, after, metadata
FROM audit_log_legacy
ON CONFLICT DO NOTHING;

-- 8. Drop the legacy table. Data now lives in the partition children.
DROP TABLE audit_log_legacy;

COMMIT;
