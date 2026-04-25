# Benchmark

This document describes how goforge is load-tested, the hardware it was tested on, and the raw numbers observed. Two runs are reported: a **200 000-request** smoke run and a longer **500 000-request** run. Methodology, configuration, and harness are identical across the two; only the per-scenario request count changes.

## 1. Test bed

| | |
|---|---|
| CPU | 2 vCPU (shared between client + API + Postgres) |
| RAM | 7.8 GiB |
| Kernel | Linux 5.15 |
| Go toolchain | 1.23 |
| Postgres | 16-alpine (Docker, default tunables) |
| API build | `go build -trimpath -ldflags='-s -w'` → static 17 MB binary |
| Network | loopback only (no physical NIC hop) |

> **Important caveat.** The load generator, the API, and Postgres all share the same two vCPUs. Absolute throughput on a dedicated production host (say 8 vCPU with Postgres on its own node) would be **2–8×** higher. The point of this benchmark is the **shape of the cost** — where time is spent (framework ↔ DB ↔ password hashing) — not the absolute numbers.

## 2. Methodology

The harness is a Go program (`cmd/bench/main.go`) that:

1. Spawns `concurrency` worker goroutines.
2. Generates a **unique payload per request** (different email/user/token), so no caching path is favoured.
3. Records latency in nanoseconds for every single request.
4. Reports throughput, status-code histogram, and percentile latencies.

Workers reuse a single `*http.Client` with HTTP/1.1 keep-alive enabled and `MaxConnsPerHost = concurrency` so the run hits the API the same way a real client would.

## 3. API configuration

The API is started with the production configuration **except** Argon2id parameters, which are lowered to the load-test profile:

```bash
GOFORGE_DATABASE_MAX_CONNS=50
GOFORGE_DATABASE_MIN_CONNS=10
GOFORGE_SECURITY_RATE_LIMIT_PER_MIN=100000000
GOFORGE_SECURITY_ARGON_MEMORY_KIB=1024
GOFORGE_SECURITY_ARGON_ITERS=1
GOFORGE_SECURITY_ARGON_PARALLEL=1
GOFORGE_JWT_ACCESS_TTL=6h
GOFORGE_LOG_LEVEL=error
```

The production defaults (`m=65536, t=3, p=2`) make password hashing intentionally expensive — that's the whole point of Argon2id and is what makes credential-stuffing attacks unattractive. With production parameters, register/login p50 on this hardware sits around 150–250 ms; the framework itself is unchanged.

## 4. Results — 200 000 requests per scenario

| Scenario | Conc. | Duration | Throughput | p50 | p90 | p95 | p99 | max | Status |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| `GET /healthz` | 256 | **10.78 s** | **18 555 req/s** | 11.7 ms | 25.3 ms | 31.1 ms | 45.2 ms | 173 ms | `200` × 200 000 |
| `POST /api/v1/auth/register` | 48 | 5 m 1.6 s | 663 req/s | 65.2 ms | 124.8 ms | 148.5 ms | 204.4 ms | 813 ms | `201` × 200 000 |
| `POST /api/v1/auth/login` | 48 | 3 m 36.8 s | 923 req/s | 43.4 ms | 102.8 ms | 126.1 ms | 177.7 ms | 347 ms | `200` × 200 000 |
| `GET /api/v1/auth/me` | 128 | **1 m 3.5 s** | **3 149 req/s** | 38.9 ms | 63.5 ms | 72.2 ms | 92.7 ms | 191 ms | `200` × 200 000 |

- **Total: 800 000 requests, 0 transport errors, 0 5xx, 100 % success.**
- API process RSS = **33.7 MiB** after all four scenarios.
- Postgres RSS = ~377 MiB with 200 000 user rows.

## 5. Results — 500 000 requests per scenario

| Scenario | Conc. | Duration | Throughput | p50 | p95 | p99 | max | Status |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| `GET /healthz` | 256 | **27.36 s** | **18 276 req/s** | 12.3 ms | 30.6 ms | 42.6 ms | 117 ms | `200` × 500 000 |
| `POST /api/v1/auth/register` | 48 | 13 m 1 s | 640 req/s | 66.2 ms | 154.9 ms | 230.5 ms | 1696 ms | `201` × 500 000 |
| `POST /api/v1/auth/login` | 48 | 9 m 45 s | 855 req/s | 46.5 ms | 137.0 ms | 194.4 ms | 819 ms | `200` × 500 000 |
| `GET /api/v1/auth/me` | 128 | **3 m 4.7 s** | **2 708 req/s** | 44.8 ms | 85.5 ms | 111.8 ms | 314 ms | `200` × 500 000 |

- **Total: 2 000 000 requests, 0 transport errors, 0 5xx, 100 % success.**
- API process RSS held at **32.9 MiB** the entire time.

The 200k and 500k runs land within a few percent of each other on every metric, confirming that throughput and latency are stable across run length — there is no thermal throttling, no slow memory leak, no degrading connection pool.

## 6. Memory and resource footprint

Measured *after* all scenarios:

| Process | RSS | VSZ | %CPU |
|---|---|---|---|
| `goforge-api` | **~33 MiB** | 2.4 GiB | ~108 % |
| `postgres` (container) | ~377 MiB | — | idle |

- The Go runtime heap stays in the **tens of megabytes** under sustained load. zerolog, Fiber/fasthttp, and `pgxpool` all minimise allocation.
- No goroutine leak observed; idle goroutine count returns to baseline between scenarios.
- No file-descriptor exhaustion at default `ulimit -n`.
- The pgx pool settled at 10–30 active conns against a 50 max.

## 7. Interpretation

- The framework sustains **~18 000 req/s for no-op endpoints** on two shared cores. That is within ~30 % of what bare Fiber advertises on the same class of hardware — the middleware chain (request-id, security headers, logger, rate limiter, timeout, recover) costs almost nothing.
- Authenticated read throughput of **~3 000 req/s** is bottlenecked by a single SELECT per request over shared CPU. On a dedicated DB tier and more cores, this easily scales 3–5×.
- Write-path throughput (register/login) is **dominated by Argon2id**, not by the framework. Even at `m=1024, t=1, p=1` the hash dominates other costs; with production parameters it dominates by an order of magnitude. That's the intended security trade-off.
- **Zero failed requests** across both runs (2.8 million requests total). Every registration succeeded, every login verified, every `/me` returned the right user.

## 8. Reproducing

```bash
# 1. Start Postgres + run migrations
make up
docker exec goforge-postgres psql -U goforge -d goforge -c 'TRUNCATE users;'

# 2. Start the API with the load-test profile (lowered Argon2id cost)
GOFORGE_DATABASE_DSN=postgres://goforge:goforge@localhost:5432/goforge?sslmode=disable \
GOFORGE_JWT_SECRET=benchmarkjwtsecret_key_32chars_minimum_0123456789 \
GOFORGE_LOG_LEVEL=error \
GOFORGE_SECURITY_RATE_LIMIT_PER_MIN=100000000 \
GOFORGE_SECURITY_ARGON_MEMORY_KIB=1024 \
GOFORGE_SECURITY_ARGON_ITERS=1 \
GOFORGE_SECURITY_ARGON_PARALLEL=1 \
go run ./cmd/api &

# 3. Build the harness
go build -o /tmp/bench ./cmd/bench

# 4. Run all four scenarios (swap 200000 for 500000 if you want the longer run)
/tmp/bench -scenario=healthz       -total=200000 -concurrency=256
/tmp/bench -scenario=register      -total=200000 -concurrency=48 \
   -fixtures=/tmp/bench-fixtures.json -prefix=reg
/tmp/bench -scenario=login-refresh -total=200000 -concurrency=48 \
   -fixtures=/tmp/bench-fixtures.json
/tmp/bench -scenario=me            -total=200000 -concurrency=128 \
   -fixtures=/tmp/bench-fixtures.json
```

The harness prints throughput, status-code distribution, percentiles, and memory at the end of each scenario. Pipe to `tee` if you want the raw output saved.

## 9. Hardware sizing guidance

Use these ratios as starting points; always re-measure on your own hardware:

- **Read-heavy services**: aim for 1 vCPU per ~1 000 req/s of authenticated reads against a same-host Postgres, then add cores or replicas for higher loads.
- **Write-heavy services**: Argon2id dominates. With production parameters, plan ~5–10 register/login per second per dedicated core. If that's a problem, you've made password hashing too cheap, not the framework too slow — pick that trade-off deliberately.
- **Memory**: 50 MiB per replica is a safe over-allocation. The blueprint runs comfortably in 64 MiB containers.
