# Configuration

All configuration is read from environment variables with the prefix `GOFORGE_`. Defaults are defined in `internal/config/config.go` and validated at startup; the process refuses to boot with an invalid config.

`.env.example` ships the complete list. This document explains *why* each default exists and when to deviate.

## HTTP

| Key | Default | Notes |
| --- | --- | --- |
| `GOFORGE_HTTP_HOST` | `0.0.0.0` | Bind address. Set to `127.0.0.1` for sidecar deployments behind a proxy on the same host. |
| `GOFORGE_HTTP_PORT` | `8080` | TCP port. |
| `GOFORGE_HTTP_READ_TIMEOUT` | `15s` | Includes header + body read. Drop slowloris clients. |
| `GOFORGE_HTTP_WRITE_TIMEOUT` | `15s` | Bound long downloads. |
| `GOFORGE_HTTP_IDLE_TIMEOUT` | `60s` | Keep-alive idle window. |
| `GOFORGE_HTTP_BODY_LIMIT_BYTES` | `1048576` | 1 MiB. Raise only for endpoints that legitimately need it; consider streaming uploads to object storage instead. |
| `GOFORGE_HTTP_PREFORK` | `false` | Fiber prefork forks one process per CPU. **Multiplies memory by `GOMAXPROCS`.** Useful on bare-metal with idle cores; usually unhelpful in containers with one core slice. |

## Database

| Key | Default | Notes |
| --- | --- | --- |
| `GOFORGE_DATABASE_DSN` | — (required) | `postgres://user:pass@host:5432/db?sslmode=require`. |
| `GOFORGE_DATABASE_MAX_CONNS` | `20` | Pool ceiling. Sized for one app instance against a small Postgres. |
| `GOFORGE_DATABASE_MIN_CONNS` | `2` | Warm pool. Keep ≥ 2 for snappy first request after idle. |
| `GOFORGE_DATABASE_MAX_CONN_LIFETIME` | `1h` | Forces connection rotation; helps with rolling Postgres upgrades. |
| `GOFORGE_DATABASE_MAX_CONN_IDLE_TIME` | `30m` | Releases idle connections back to the OS. |
| `GOFORGE_DATABASE_HEALTH_CHECK_PERIOD` | `1m` | Pool background health check. |
| `GOFORGE_DATABASE_STATEMENT_CACHE` | `true` | Server-side prepared statements. **Disable when fronted by PgBouncer in transaction mode** — prepared statements don't survive across pooled connections. |

### Sizing the pool

A safe rule of thumb: `total_pool_connections_across_all_instances ≤ Postgres max_connections − reserved`. If you run 4 API replicas at `MAX_CONNS=20`, you need at least 80 + room for migrations + admin connections.

## JWT

| Key | Default | Notes |
| --- | --- | --- |
| `GOFORGE_JWT_SECRET` | — (required) | HS256 secret. **Must be ≥ 32 chars and rotated periodically.** |
| `GOFORGE_JWT_ACCESS_TTL` | `15m` | Short-lived access token. |
| `GOFORGE_JWT_REFRESH_TTL` | `168h` (7 d) | Refresh window before re-login. |
| `GOFORGE_JWT_ISSUER` | `goforge` | `iss` claim. |
| `GOFORGE_JWT_AUDIENCE` | `goforge.api` | `aud` claim for access tokens; refresh tokens use `<aud>.refresh` so they cannot be replayed as access tokens. |

## Logging

| Key | Default | Notes |
| --- | --- | --- |
| `GOFORGE_LOG_LEVEL` | `info` | `trace`, `debug`, `info`, `warn`, `error`. |
| `GOFORGE_LOG_PRETTY` | `false` | Human-readable console output. **Leave `false` in production** — JSON ingests faster and is safer. |

## Security

| Key | Default | Notes |
| --- | --- | --- |
| `GOFORGE_SECURITY_RATE_LIMIT_PER_MIN` | `120` | Per-IP requests per minute. In-process, best-effort, per-instance. Front with a real edge limiter for serious abuse mitigation. |
| `GOFORGE_SECURITY_CORS_ALLOW_ORIGINS` | `*` | Comma-separated. Set explicit origins for credentialed APIs. |
| `GOFORGE_SECURITY_CORS_ALLOW_CREDENTIALS` | `false` | Cannot be `true` while origins is `*`. |
| `GOFORGE_SECURITY_ARGON_MEMORY_KIB` | `65536` | Argon2id `m`. OWASP 2023 recommends ≥ 64 MiB. |
| `GOFORGE_SECURITY_ARGON_ITERS` | `3` | Argon2id `t`. |
| `GOFORGE_SECURITY_ARGON_PARALLEL` | `2` | Argon2id `p`. Set to `runtime.NumCPU()` upper bound only if you have CPU to spare. |

### Tuning Argon2id

Run a one-off test on production-class hardware to find parameters that take ~100–250 ms per hash. Bump them every 1–2 years as CPUs improve. The hasher rehashes transparently on the next successful login if it sees a hash with weaker parameters than the current config — users never see this.

For load tests, lower the cost (e.g. `m=1024, t=1, p=1`) so password hashing doesn't dominate the benchmark — see [`docs/benchmark.md`](./benchmark.md).

## Observability

The server emits one structured log line per request with `requestId`, `method`, `path`, `status`, and `latency`. Pipe stdout to your log aggregator. There is no Prometheus endpoint shipped by default; add one under `/metrics` once you decide on a metric library (the recommendation is `github.com/prometheus/client_golang`).

## Validating your config locally

```bash
make build
GOFORGE_DATABASE_DSN=… GOFORGE_JWT_SECRET=… ./bin/api --check-config
```

(The `--check-config` flag prints the effective config and exits non-zero if validation fails — wire it into your deploy preflight.)
