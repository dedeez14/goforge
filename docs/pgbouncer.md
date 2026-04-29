# PgBouncer

PgBouncer sits between goforge and Postgres and amortises real
backend connections across all your pods. It is **required** at scale
- without it, horizontal scaling runs out of Postgres connections
long before it runs out of CPU.

## When to deploy PgBouncer

| Scenario | Do you need PgBouncer? |
|----------|------------------------|
| Single pod, < 20 pgx conns | No. `MaxConns=20` is fine. |
| ≥ 4 pods × 20 conns = 80 backend | Add it - you are starting to pay for unused idle connections. |
| Any production deploy on managed Postgres (AWS RDS, Cloud SQL) | Yes. The managed databases cap connections at 200–500 total; a rolling deploy of 10 pods can blow that budget during overlap. |
| Any k8s deploy with HPA | Yes. HPA bursts can easily create 2–5x pods during traffic spikes. |

## How the numbers work

goforge's pgx pool holds `MaxConns` sockets open **per pod**. Without
PgBouncer, `pods × MaxConns` directly becomes Postgres backends. Each
Postgres backend costs ~10 MiB of RAM plus a PID slot (default
`max_connections=100`). At 20 pods × 20 conns = 400 backends you are
out of headroom on any RDS db.t3.medium.

With PgBouncer in `pool_mode=transaction`, pgx conns become cheap -
they're virtual sessions that multiplex onto a **shared** pool of real
Postgres backends. A typical sizing:

```
pgx.MaxConns         = 20      per goforge pod
pods                 = 20
total client conns   = 400     → PgBouncer's max_client_conn
default_pool_size    = 25      → real backends Postgres sees
                                  (sized to Postgres cores × 2..4)
```

Rule of thumb: `default_pool_size` ≈ `Postgres_cores × 2..4`. Bumping
`max_client_conn` is nearly free; bumping `default_pool_size` costs
Postgres RAM.

## goforge configuration

Two env vars matter:

| Var | Value with PgBouncer | Why |
|-----|---------------------|-----|
| `GOFORGE_DATABASE_DSN` | `postgres://…@pgbouncer:6432/…` | Point at the bouncer, not Postgres directly. |
| `GOFORGE_DATABASE_STATEMENT_CACHE` | `false` | Disables pgx's server-side prepared statements; they are incompatible with `pool_mode=transaction`. goforge's `newPool` notices the flag and switches pgx to the simple query protocol automatically. |

If you forget `statement_cache=false`, goforge prints a loud warning
at startup (`postgres: statement_cache=true but DSN looks like
PgBouncer (port 6432)…`) so the misconfiguration is surfaced before
the first 3 a.m. page.

## Reference configuration

A production-grade PgBouncer config ships in
[`deploy/pgbouncer/pgbouncer.ini`](../deploy/pgbouncer/pgbouncer.ini)
with inline commentary. Userlist is at
[`deploy/pgbouncer/userlist.txt.example`](../deploy/pgbouncer/userlist.txt.example)
- copy it to `userlist.txt` and fill in SCRAM hashes before boot.

## Local validation

The repo ships an overlay compose file so you can run the bounced
topology locally before rolling it out:

```bash
cd deploy/docker
cp ../pgbouncer/userlist.txt.example ../pgbouncer/userlist.txt
# generate SCRAM hashes for goforge / pgbouncer_stats / pgbouncer_admin
# users and paste them in; see docs/sops/pgbouncer-userlist.md for
# a one-liner.
docker compose -f docker-compose.yml -f docker-compose.pgbouncer.yml up --build
```

The overlay:
- adds a `pgbouncer` service listening on `:6432`
- repoints the `api` service's DSN through the bouncer
- sets `GOFORGE_DATABASE_STATEMENT_CACHE=false` automatically

You can then run the benchmark harness against `:8080` and confirm
Postgres `pg_stat_activity` shows ≤ `default_pool_size` active
backends no matter how many concurrent requests you fire.

## Operational checklist

- [ ] `pool_mode = transaction` (never `session` - defeats the point)
- [ ] PgBouncer version ≥ 1.22 (earlier versions have a SCRAM bug)
- [ ] TLS on both hops (client → bouncer, bouncer → Postgres)
- [ ] Expose PgBouncer's `SHOW STATS` / `SHOW POOLS` to Prometheus
      (see the `pgbouncer_exporter` sidecar pattern)
- [ ] Alert on `cl_waiting > 0 for > 30s` (client starvation =
      `default_pool_size` too small)
- [ ] Alert on `maxwait > 30s`
- [ ] Alert on `sv_used / default_pool_size > 0.8` (pool is hot;
      add capacity before requests queue)

## Limitations

Things that become inoperable under `pool_mode=transaction`:
- `LISTEN / NOTIFY` - goforge does not use these; the outbox is
  polled via `pg_stat_activity` instead.
- Session-scoped `SET` / `SET LOCAL` outside a transaction.
- Server-side prepared statements (hence the `statement_cache=false`
  requirement).
- Advisory locks that span statements outside a transaction.

goforge is written to tolerate all of these.
