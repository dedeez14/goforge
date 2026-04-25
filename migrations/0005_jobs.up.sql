-- Background job queue, dead-letter store, and recurring schedule.
--
-- The queue is single-table on purpose: every job goes through the
-- same lifecycle (pending -> running -> done/failed/dead) and the
-- dispatcher uses `FOR UPDATE SKIP LOCKED` to claim work, which means
-- multiple replicas can drain the queue without coordination.
--
-- Status legend:
--   pending  -- waiting to be picked up
--   running  -- claimed by a worker; locked_at + locked_by populated
--   done     -- terminated successfully
--   failed   -- last attempt failed; will retry until attempts == max
--   dead     -- exceeded max_attempts; left for human triage

CREATE TABLE IF NOT EXISTS jobs (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    queue           TEXT        NOT NULL DEFAULT 'default',
    kind            TEXT        NOT NULL,
    payload         JSONB       NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending',
    attempts        INT         NOT NULL DEFAULT 0,
    max_attempts    INT         NOT NULL DEFAULT 5,
    last_error      TEXT        NULL,
    run_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_at       TIMESTAMPTZ NULL,
    locked_by       TEXT        NULL,
    completed_at    TIMESTAMPTZ NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    dedupe_key      TEXT        NULL
);

-- Skip-locked dispatcher index: it reads (queue, status, run_at)
-- ordered by run_at so the oldest pending job is found first.
CREATE INDEX IF NOT EXISTS jobs_dispatch_idx
    ON jobs (queue, status, run_at)
    WHERE status IN ('pending', 'failed');

-- Optional dedupe: when dedupe_key is set, another row with the same
-- key (in a non-terminal state) cannot be inserted. Idempotency for
-- enqueue calls.
CREATE UNIQUE INDEX IF NOT EXISTS jobs_dedupe_open_idx
    ON jobs (queue, dedupe_key)
    WHERE dedupe_key IS NOT NULL AND status IN ('pending', 'running', 'failed');

-- Recurring schedules (cron-style). The runner enqueues a fresh
-- jobs row whenever next_run_at <= now() and advances next_run_at
-- atomically.
CREATE TABLE IF NOT EXISTS job_schedules (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT        NOT NULL UNIQUE,
    queue           TEXT        NOT NULL DEFAULT 'default',
    kind            TEXT        NOT NULL,
    payload         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    interval_secs   INT         NOT NULL,
    next_run_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    enabled         BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
