# Read-replica routing

goforge ships with an explicit read/write router (`pkg/db.Router`) on
top of `pgxpool.Pool`. Deployments that run a PostgreSQL read replica
can send heavy read-only traffic there; deployments that don't are
unaffected because `Read()` transparently falls back to the primary.

## When to use it

Turn on a replica when any of the following is true:

- Analytics / reporting queries compete with transactional traffic for
  primary CPU.
- List / search endpoints dominate the primary's pg_stat_statements
  top-N and the latency of writes starts climbing.
- You need to isolate a noisy neighbour (ad-hoc BI queries, nightly
  exports) from the OLTP workload.

Stay single-primary when:

- Traffic fits comfortably in the primary (most new products).
- You rely on read-your-writes *everywhere* (UI, worker jobs, webhook
  retries). Routing is opt-in per call site, but if the call sites
  all need the primary the replica adds operational complexity for no
  gain.

## Configuration

```env
# Primary (always required)
GOFORGE_DATABASE_DSN=postgres://user:pass@primary/goforge?sslmode=require

# Optional replica (empty = single-primary)
GOFORGE_DATABASE_REPLICA_DSN=postgres://user:pass@replica/goforge?sslmode=require
GOFORGE_DATABASE_REPLICA_MIN_CONNS=2
GOFORGE_DATABASE_REPLICA_MAX_CONNS=20
```

`MaxConnLifetime`, `MaxConnIdleTime`, `ConnectTimeout` and
`StatementCache` are shared between the two pools. If your replica
runs behind a different proxy (e.g. PgBouncer in transaction pooling
mode) and the primary does not, run them on separate pools at the
infrastructure level.

Readiness (`GET /readyz`) pings **both** pools and reports
`has_replica` in the payload - a service with a dead replica fails
readiness rather than silently routing everything to the primary and
overloading it.

## Usage

Existing repositories keep taking the primary `*pgxpool.Pool` via
`router.Write()`. No behaviour change for code that hasn't opted in.

New code that wants to route reads explicitly takes `*db.Router`
instead:

```go
type ReportRepository struct {
    db *db.Router
}

func (r *ReportRepository) Top10(ctx context.Context) ([]Row, error) {
    rows, err := r.db.Read(ctx).Query(ctx,
        "SELECT id, total FROM orders ORDER BY total DESC LIMIT 10")
    // ...
}
```

`Read(ctx)` returns the replica when one is configured, otherwise
the primary. `Write()` always returns the primary.

### Read-your-writes

Replication is asynchronous, so a client that just issued a write may
not see it yet on the replica. When a caller needs read-your-writes
consistency, mark the context:

```go
ctx = db.WithPrimary(ctx)
order, _ := orderRepo.GetByID(ctx, id) // even a Read() here hits the primary
```

The marker is scoped to the derived context, so it cannot leak to
unrelated calls.

## What the router deliberately does *not* do

- **No SQL parsing.** Deciding "is this SELECT-only?" at the driver
  level breaks on CTEs, `RETURNING`, advisory locks, pgx transactions
  and every workload with stored procedures. Explicit
  `Read()` / `Write()` calls keep the routing decision visible at the
  call site.
- **No lag monitoring.** Replication lag is deployment-specific
  (pglogical? streaming? a managed service?). Expose
  `pg_stat_replication` via your existing observability and alert on
  it there.
- **No automatic failover.** If the replica is down, readiness fails
  and Kubernetes / your load balancer stops sending traffic. Promotion
  is an operator action; it is not the application's job to quietly
  promote a replica to primary.
