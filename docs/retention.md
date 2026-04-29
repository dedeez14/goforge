# Data retention & archival

`pkg/retention` archives aged-out rows from monthly-partitioned tables
to object storage, then drops the empty partitions. It is the
companion to `pkg/partition`: partitioning keeps the working set
small; retention keeps the archive intact while freeing the database
of data that is no longer read online.

## Safety model

Every run follows the same two-phase order, per plan:

1. **Archive**: enumerate the plan's partitions, pick those whose
   upper bound is older than `Retain`, and stream each partition's
   rows through the `Archiver` into `storage.Storage` under a
   deterministic key.
2. **Drop**: only after every eligible partition is archived, call
   `partition.Manager.DropBefore(cutoff)` once to release the space
   via a single DDL operation.

A crash between archive and drop leaves the data intact on disk and
the archive in storage — the next run writes the same deterministic
key (idempotent overwrite) before retrying the drop. A crash mid-
archive leaves zero data dropped: the runner's drop step never
executes for a plan whose archive step returned an error.

The default `_default` overflow partition is never archived or
dropped. Its contents are out-of-band (historical imports, clock
skew) and an operator should handle them explicitly.

## Default archive format

The bundled `PgxArchiver` streams rows through PostgreSQL's `COPY ...
TO STDOUT (FORMAT csv, HEADER true)` protocol and gzip-compresses
the result before upload. CSV was chosen because it is replayable
with `COPY ... FROM STDIN` — operators who need to pull archives
back into a staging table for analytics do not need a Parquet reader,
a JSON flattener, or custom tooling.

Blob keys are `<StoragePrefix>/<partition>.csv.gz`:

```
archive/audit_log/audit_log_y2026m01.csv.gz
archive/audit_log/audit_log_y2026m02.csv.gz
```

Applications that want a different on-disk format (JSONL, Parquet,
Avro, pageable NDJSON) can implement the `Archiver` interface and
pass it to `NewRunner`.

## Wiring

```go
// Infrastructure composed once at startup.
mgr := partition.NewManager(pool)
archiver := retention.NewPgxArchiver(pool)
runner := retention.NewRunner(mgr, archiver, s3, logger, retention.Options{
    Plans: []retention.Plan{{
        Spec:          partition.Spec{Parent: "audit_log", Column: "occurred_at"},
        Retain:        180 * 24 * time.Hour, // keep last 180 days online
        StoragePrefix: "archive/audit_log",
    }},
})

// Option A: pkg/jobs scheduled handler (recommended).
jobsRunner.Handlers["retention.archive"] = runner.Handler()
// INSERT INTO job_schedules (name, kind, interval_secs)
//   VALUES ('retention-archive', 'retention.archive', 86400)

// Option B: goroutine ticker (simpler, no jobs integration).
go func() {
    t := time.NewTicker(24 * time.Hour)
    for {
        select {
        case <-ctx.Done():
            return
        case <-t.C:
            runner.Run(ctx)
        }
    }
}()
```

Run the retention job **after** the partition maintainer's rollover
(`pkg/partition.Maintainer`). The maintainer's job is cheap and
monotonic: once this month's partition exists, it will not be
archived for another 180 days. The retention job's job is
destructive; it should run with fresh partition metadata.

## Retention horizons in practice

Typical values:

| Table | `Retain` | Rationale |
|---|---|---|
| `audit_log` | 180–365 days | Most compliance regimes require 6–12 months online; archived CSV is indexed by date, so longer-tail lookups go through S3 Select / Athena. |
| `jobs` (once partitioned) | 30 days | DLQ rows older than a month are almost never replayed; dropping them keeps `/admin/jobs` fast. |
| `webhook_deliveries` | 90 days | Debugging delivery failures past 3 months almost always means "re-send", not "inspect". |

## Operational notes

- **Storage cost**: gzip compresses CSV audit_log rows ~5x. A million
  rows/month typically fits in under 20 MB. Retention across years
  remains cheap on S3 Standard / B2 / R2.
- **Archive retrieval**: `COPY audit_log_staging FROM STDIN (FORMAT csv, HEADER true)`
  feeds a gzipped archive back into a staging table in seconds. Use
  this to answer "find all actions by user X between date A and B"
  without inflating the online audit_log.
- **Monitoring**: `Runner.Run` returns a `Report` with counts of
  archived and dropped partitions. Feed these into Prometheus /
  alerting to catch silent stalls ("retention has archived 0
  partitions in the last 48 hours").
- **Retry semantics**: if drop fails after a successful archive, the
  job returns an error so `pkg/jobs` reschedules it. The retry
  re-archives the same partition under the same key (idempotent)
  before re-attempting the drop. There is no "dropped-but-not-
  archived" window.
