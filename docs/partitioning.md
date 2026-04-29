# Table partitioning

`pkg/partition` turns high-churn, append-only tables into PostgreSQL
monthly range-partitioned tables and keeps the rolling window of child
partitions healthy in production.

## Why

Any table whose row count grows roughly linearly with wall-clock time
— audit logs, background-job history, webhook deliveries, short-lived
auth tokens — eventually dwarfs the rest of the schema. Non-partitioned
tables at that scale get slow vacuums, slow DROP queries, and
eventually slow reads even with the right indexes. Declarative
partitioning fixes the structural problem:

- Queries filtered by the partition column scan one partition instead
  of the whole heap.
- Dropping old data is `DROP TABLE` on a child — O(1), no row-by-row
  `DELETE`, no bloat left behind.
- Each child can be vacuumed / autovacuumed independently.

`pkg/partition` is the bookkeeping primitive. It makes the migration
layer simpler (`CREATE TABLE ... PARTITION OF ...` scales with a
small DO block) and gives you a scheduled job to roll partitions
forward without operator intervention.

## What is partitioned today

| Table | Partition key | Monthly migration |
|---|---|---|
| `audit_log` | `occurred_at` | `0010_partition_audit_log.up.sql` |

`jobs`, `refresh_tokens`, and (future) `webhook_deliveries` have
per-table design considerations — primary key layout, unique-dedupe
index, cross-partition FKs — that are tracked as follow-up work. Use
`pkg/partition` today for any new high-churn table you add.

## Bootstrap a new partitioned table

Schema:

```sql
CREATE TABLE events (
    id          UUID        NOT NULL DEFAULT gen_random_uuid(),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload     JSONB       NOT NULL,
    PRIMARY KEY (id, occurred_at)           -- partition key is in PK
) PARTITION BY RANGE (occurred_at);

CREATE INDEX events_occurred_at_idx ON events (occurred_at DESC);

CREATE TABLE IF NOT EXISTS events_default PARTITION OF events DEFAULT;
```

Application wiring (composition root):

```go
mgr := partition.NewManager(pool)
maint := partition.NewMaintainer(mgr, logger, partition.MaintenancePlan{
    Spec:      partition.Spec{Parent: "events", Column: "occurred_at"},
    Past:      1,                     // keep last month pre-created
    Future:    3,                     // pre-create 3 months ahead
    DropAfter: 180 * 24 * time.Hour,  // retention: 6 months (0 = never drop)
})

// Option A: run on startup, then hourly.
go func() {
    _ = maint.Run(ctx)  // initial warmup
    t := time.NewTicker(time.Hour)
    for {
        select {
        case <-ctx.Done():
            return
        case <-t.C:
            _ = maint.Run(ctx)
        }
    }
}()

// Option B: register as a pkg/jobs scheduled handler.
jobsRunner.Handlers["partition.maintenance"] = maint.MaintenanceHandler()
// then INSERT into job_schedules (name, kind, interval_secs) VALUES
//   ('partition-maintenance', 'partition.maintenance', 3600)
```

## Naming convention

Child partitions follow `<parent>_yYYYYmMM`:

```
audit_log_y2026m01
audit_log_y2026m02
audit_log_y2026m03
audit_log_default      -- catches everything outside the range
```

`pkg/partition.Manager` parses this pattern directly rather than
joining `pg_inherits`; the flip side is that any partition not
following the convention is invisible to `Partitions()` and
`DropBefore()`. Use `EnsureMonths` as the only thing that creates
range partitions.

## Retention

`MaintenancePlan.DropAfter` drops partitions whose upper bound is
strictly older than `now() - DropAfter`. The default partition is
never dropped — detaching it requires an operator decision because
it may still hold rows that do not fit any remaining partition.

If you archive data elsewhere (S3 Parquet, cold storage), archive
**before** the drop — `DROP TABLE` is destructive. Scale-up feature
#10 adds an archival job that streams rows to object storage first
and only then calls `DropBefore`.

## Operational notes

- **Foreground migrations.** `migrations/0010_partition_audit_log.up.sql`
  is wrapped in a single transaction. On a fresh / small database it
  runs in milliseconds. On a large database (>10M `audit_log` rows),
  run in a maintenance window or split the INSERT into batches.
- **Partition gaps.** If a scheduled run is missed for days/weeks and
  inserts happen into the `_default` partition, the default grows
  until you detach it, pre-create the missing months, and re-attach
  the overflow rows. Run the maintainer at least once a day — every
  hour is cheap.
- **Queries across partitions.** PostgreSQL's query planner prunes
  partitions automatically when the predicate includes the partition
  column. A query `WHERE id = $1` with no time filter scans every
  partition. This is by design — set a reasonable time bound in
  admin queries.
- **Backup / restore.** `pg_dump` restores partitions one by one.
  Logical replication understands the hierarchy. Point-in-time
  restore works unchanged.
