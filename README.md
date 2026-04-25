<div align="center">

# goforge

**A high-performance, memory-compact, Clean-Architecture Go API blueprint.**

`Fiber v2` · `pgx/v5` · `zerolog` · `Argon2id` · `JWT` · `Postgres`

[![CI](https://github.com/dedeez14/goforge/actions/workflows/ci.yml/badge.svg)](https://github.com/dedeez14/goforge/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/dedeez14/goforge)](https://goreportcard.com/report/github.com/dedeez14/goforge)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.23%2B-00ADD8.svg)](go.mod)

</div>

---

`goforge` is a reusable backend blueprint designed for projects that need to start fast but stay sane as they grow:

- **Clean Architecture** with strict layer boundaries and dependency inversion.
- **Memory-compact** — server keeps a steady ~33 MiB RSS under 200 000 requests.
- **High concurrency** — Fiber/fasthttp + `pgxpool` saturate Postgres with predictable latency.
- **Secure by default** — Argon2id passwords, typed JWT pairs, security headers, rate limiter, panic recovery.
- **DRY** — one error taxonomy, one response envelope, one validator, one error mapper.
- **Scaffolded** — `make scaffold name=Order` generates a full vertical slice (domain → handler → migration) in one shot.

### Signature platform features

goforge ships with capabilities that are rare to find pre-wired in a Go starter:

- **Module system** — formal `Module` interface for opt-in feature packs (`pkg/module`).
- **Idempotency-Key middleware** — Stripe-style POST replay protection backed by Postgres (`pkg/idempotency`).
- **Transactional outbox** — write data + events in one transaction; dispatcher drains at-least-once (`pkg/outbox`).
- **Domain event bus** — in-process pub/sub with tenant propagation (`pkg/events`).
- **Server-Sent Events bridge** — live `/api/v1/stream` endpoint reflecting the bus (`pkg/realtime`).
- **Auto OpenAPI 3.1** — reflection-based spec at `/openapi.json`, Swagger UI at `/docs` (`pkg/openapi`).
- **Multi-tenancy primitives** — `tenant.ID`, context propagation, header resolver, middleware (`pkg/tenant`).
- **Prometheus metrics** — `/admin/metrics` with method/route/status histograms (`pkg/observability`).
- **Feature flags** — env + static sources, TTL cache, hot-reload (`pkg/flags`).
- **`forge` CLI** — single binary for `doctor`, `scaffold`, `migrate`, `openapi`, `bench`, `module` (`cmd/forge`).

See [`docs/platform.md`](./docs/platform.md) for full details and the [`docs/modules.md`](./docs/modules.md) for writing your own.

---

## Table of contents

- [Quickstart](#quickstart)
- [Architecture](#architecture)
- [Benchmarks](#benchmarks-200000-requests)
- [Project layout](#project-layout)
- [HTTP reference](#http-reference)
- [Configuration](#configuration)
- [Adding a new resource](#adding-a-new-resource)
- [Testing](#testing)
- [Production checklist](#production-checklist)
- [Documentation index](#documentation-index)
- [License](#license)

---

## Quickstart

```bash
git clone https://github.com/dedeez14/goforge.git && cd goforge
cp .env.example .env
# rotate GOFORGE_JWT_SECRET to something >= 32 random bytes

make up                                   # postgres + migrations + api
curl -s http://localhost:8080/healthz | jq
```

Register a user and call an authenticated endpoint:

```bash
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"me@example.com","password":"supersecret","name":"Me"}' \
  | jq -r '.data.tokens.access_token')

curl -s http://localhost:8080/api/v1/auth/me -H "Authorization: Bearer $TOKEN" | jq
```

Stop with `make down`. Postgres data persists in the `pgdata` Docker volume.

> Without Docker: install Go 1.23+, a running Postgres, and [`migrate`](https://github.com/golang-migrate/migrate). Then `make migrate-up && make run`.

---

## Architecture

```
   ┌───────────────────────────────────────────────────────────────┐
   │                     internal/app  (composition)               │
   └─────────────┬───────────────────────┬─────────────────────────┘
                 │                       │
       ┌─────────▼─────────┐    ┌────────▼────────────┐
       │   adapter/http    │    │  adapter/repository │   ←  Adapters
       │   (handler+dto+   │    │   (postgres/pgx)    │      depend on
       │    middleware)    │    │                     │      domain.
       └─────────┬─────────┘    └────────┬────────────┘
                 │                       │
                 │       ┌───────────────┘
                 │       │
       ┌─────────▼───────▼─────────┐
       │       internal/usecase    │   ← Business logic, transport-agnostic.
       └─────────────┬─────────────┘     Depends on domain interfaces only.
                     │
       ┌─────────────▼─────────────┐
       │     internal/domain       │   ← Pure entities, repo interfaces,
       │   (zero framework deps)   │     domain errors. No framework imports.
       └───────────────────────────┘
```

Dependency rule: **inner layers never import outer ones.** Use-cases depend on `user.Repository`, not on `*sql.DB`. That's what makes the business logic swappable, testable in-memory, and free from transport concerns.

Every success and failure response uses the same envelope:

```json
{
  "success": false,
  "error": {
    "code": "user.email_taken",
    "message": "email is already registered",
    "meta": { "fields": { "email": "must be a valid email" } }
  },
  "request_id": "0d5a4f3e-…"
}
```

Error → HTTP-status mapping happens in **exactly one place** (`pkg/httpx/response.go`). Handlers only call `httpx.RespondError(c, err)`.

For the full architectural reasoning see [`docs/architecture.md`](./docs/architecture.md).

---

## Benchmarks (200 000 requests)

Each scenario fires **200 000 requests with a unique payload per request** against a single API instance. Postgres, the API, and the load generator all run on the same 2 vCPU / 8 GiB VM, so absolute numbers are conservative — production servers with dedicated cores see 2–8× this throughput.

| Scenario | Requests | Concurrency | Duration | Throughput | p50 | p95 | p99 | Status |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `GET /healthz` | 200 000 | 256 | **10.8 s** | **18 555 req/s** | 11.7 ms | 31.1 ms | 45.2 ms | `200` × 200 000 |
| `POST /api/v1/auth/register` | 200 000 (unique users) | 48 | 5 m 1.6 s | 663 req/s | 65.2 ms | 148.5 ms | 204.4 ms | `201` × 200 000 |
| `POST /api/v1/auth/login` | 200 000 | 48 | 3 m 36.8 s | 923 req/s | 43.4 ms | 126.1 ms | 177.7 ms | `200` × 200 000 |
| `GET /api/v1/auth/me` | 200 000 (unique tokens) | 128 | **1 m 3.5 s** | **3 149 req/s** | 38.9 ms | 72.2 ms | 92.7 ms | `200` × 200 000 |

- **Total: 800 000 requests, 0 transport errors, 0 5xx, 100 % success.**
- API process RSS stayed at **33.7 MiB** throughout. Postgres at ~377 MiB with 200 000 user rows.
- Register/login throughput is dominated by **Argon2id cost (intentionally expensive)**, not framework overhead. The `/healthz` and `/me` numbers reflect the actual framework + DB ceiling on this hardware.

Reproduce locally:

```bash
go build -o /tmp/bench ./cmd/bench
/tmp/bench -scenario=healthz       -total=200000 -concurrency=256
/tmp/bench -scenario=register      -total=200000 -concurrency=48 -fixtures=/tmp/fx.json -prefix=run
/tmp/bench -scenario=login-refresh -total=200000 -concurrency=48 -fixtures=/tmp/fx.json
/tmp/bench -scenario=me            -total=200000 -concurrency=128 -fixtures=/tmp/fx.json
```

Full methodology, hardware notes, and 500 000-request comparison in [`docs/benchmark.md`](./docs/benchmark.md).

---

## Project layout

```
cmd/api/                              # thin entrypoint (calls internal/app.Run)
cmd/bench/                            # 200k/500k request load harness
internal/
  app/                                # composition root (wires everything)
  config/                             # viper + env + struct validation
  domain/<aggregate>/                 # entities, errors, repo interfaces
  usecase/                            # business logic (transport-agnostic)
  adapter/
    http/handler/                     # fiber handlers (thin)
    http/dto/                         # wire DTOs + domain mappers
    http/middleware/                  # requestid, auth, recover, timeout, security headers
    repository/postgres/              # pgx implementations of domain repositories
  infrastructure/
    database/                         # pgxpool factory
    logger/                           # zerolog factory
    security/                         # Argon2id hasher + JWT issuer
    server/                           # fiber app builder + route registrar
pkg/
  errs/                               # canonical *errs.Error taxonomy
  httpx/                              # response envelope + single-place error mapper
  validatorx/                         # validator wrapper (per-field error map)
  paginate/                           # clamped pagination helper
migrations/                           # golang-migrate SQL files
deploy/docker/                        # docker-compose.yml for local stack
scripts/scaffold.sh                   # `make scaffold name=Order`
docs/                                 # design + ops documentation
```

---

## HTTP reference

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| `GET`  | `/healthz` | — | Liveness |
| `GET`  | `/readyz`  | — | Readiness (pings DB) |
| `POST` | `/api/v1/auth/register` | — | Create user, return token pair |
| `POST` | `/api/v1/auth/login` | — | Verify credentials, return token pair |
| `POST` | `/api/v1/auth/refresh` | — | Exchange refresh token for new pair |
| `GET`  | `/api/v1/auth/me` | Bearer access | Current user |
| `GET`  | `/api/v1/me/access` | Bearer access | Caller's roles + effective permission codes |
| `GET`  | `/api/v1/menus/mine` | Bearer access | Menu tree filtered by caller's permissions |
| `GET\|POST\|PATCH\|DELETE` | `/api/v1/permissions[...]` | `rbac.manage` | Permission catalog CRUD |
| `GET\|POST\|PATCH\|DELETE` | `/api/v1/roles[...]` | `rbac.manage` | Role CRUD |
| `PUT`  | `/api/v1/roles/:id/permissions` | `rbac.manage` | Replace role's permission set |
| `PUT`  | `/api/v1/users/:id/roles` | `rbac.manage` | Replace user's roles in tenant |
| `GET\|POST\|PATCH\|DELETE` | `/api/v1/menus[...]` | `menu.manage` | Menu CRUD (tree-aware) |

Example success body:

```json
{
  "success": true,
  "data": {
    "user": { "id": "…", "email": "me@example.com", "name": "Me", "role": "user" },
    "tokens": {
      "access_token": "eyJ…",
      "refresh_token": "eyJ…",
      "token_type": "Bearer",
      "expires_at": "2026-04-24T18:24:53Z"
    }
  },
  "request_id": "0d5a4f3e-…"
}
```

Common error codes:

| Code | HTTP | When |
| --- | --- | --- |
| `validation` | 400 | DTO failed `validator` rules; per-field errors in `meta.fields` |
| `auth.missing_token` / `auth.invalid` | 401 | No / malformed / expired token |
| `user.invalid_credentials` | 401 | Wrong email or password |
| `user.email_taken` | 409 | Email already registered |
| `route.not_found` | 404 | Unknown route |
| `route.method_not_allowed` | 405 | Wrong HTTP method |
| `rate_limited` | 429 | Per-IP rate limit exceeded |
| `internal` | 500 | Unhandled error (stack traces stay server-side) |

---

## Configuration

All knobs are env-driven with the `GOFORGE_` prefix. See [`.env.example`](./.env.example) for the full list and [`docs/configuration.md`](./docs/configuration.md) for the reasoning behind each default.

Highlights:

| Key | Default | Notes |
| --- | --- | --- |
| `GOFORGE_HTTP_PORT` | `8080` | TCP port |
| `GOFORGE_HTTP_BODY_LIMIT_BYTES` | `1048576` | 1 MiB request body cap |
| `GOFORGE_HTTP_PREFORK` | `false` | Fiber prefork — multiplies memory by CPU count |
| `GOFORGE_DATABASE_MAX_CONNS` | `20` | `pgxpool` ceiling |
| `GOFORGE_DATABASE_STATEMENT_CACHE` | `true` | Disable when fronted by PgBouncer (txn mode) |
| `GOFORGE_JWT_SECRET` | — (required) | HS256 secret, ≥ 32 chars |
| `GOFORGE_JWT_ACCESS_TTL` / `_REFRESH_TTL` | `15m` / `168h` | Token lifetimes |
| `GOFORGE_SECURITY_RATE_LIMIT_PER_MIN` | `120` | Per-IP requests/min |
| `GOFORGE_SECURITY_ARGON_MEMORY_KIB` | `65536` | OWASP 2023 default |
| `GOFORGE_SECURITY_ARGON_ITERS` / `_PARALLEL` | `3` / `2` | Lower for load tests, raise as hardware improves |

---

## Adding a new resource

```bash
make scaffold name=Order
```

That generates the full vertical slice:

- `internal/domain/order/{order,repository}.go`
- `internal/usecase/order.go`
- `internal/adapter/repository/postgres/order.go`
- `internal/adapter/http/dto/order.go`
- `internal/adapter/http/handler/order.go`
- `migrations/NNNN_create_orders.{up,down}.sql`

Then:

1. Flesh out the repository's `Create` and any other methods.
2. Wire repo + use-case + handler in `internal/app/app.go`.
3. Register routes in `internal/infrastructure/server/router.go`.
4. `make migrate-up` and you're done.

Step-by-step walkthrough with conventions for transactions, pagination, and authorisation in [`docs/scaffolding.md`](./docs/scaffolding.md).

---

## Testing

```bash
make test           # go test -race -count=1 ./...
make lint           # gofmt + goimports + golangci-lint
```

The use-case layer is tested with an in-memory `user.Repository` so tests are hermetic and sub-millisecond. Integration tests for the Postgres repository can be added with [`testcontainers-go`](https://golang.testcontainers.org/) — drop them in `internal/adapter/repository/postgres/*_integration_test.go`.

---

## Production checklist

- [ ] Rotate `GOFORGE_JWT_SECRET` to at least 32 random bytes.
- [ ] Set `GOFORGE_SECURITY_CORS_ALLOW_ORIGINS` to the explicit origin list (no `*` for credentialed APIs).
- [ ] Decide on `GOFORGE_HTTP_PREFORK` — prefork helps saturate accept queues on high-core hosts but multiplies memory.
- [ ] Disable `GOFORGE_DATABASE_STATEMENT_CACHE` if you front Postgres with PgBouncer in transaction mode.
- [ ] Provision an external rate limiter (Cloudflare, NGINX, etc.) — the in-process limiter is best-effort and per-instance.
- [ ] Pipe logs to your aggregator. zerolog already emits one JSON line per request with `requestId`.
- [ ] Schedule periodic Argon2id parameter review — bump cost as your CPUs get faster, the framework rehashes transparently on next login.
- [ ] Run `make test && make lint` in CI for every PR. The provided GitHub Actions workflow is a good starting point.

---

## Documentation index

- [`docs/architecture.md`](./docs/architecture.md) — layers, dependency rule, error mapping, request lifecycle.
- [`docs/platform.md`](./docs/platform.md) — signature features (idempotency, outbox, realtime SSE, OpenAPI 3.1, metrics, flags).
- [`docs/modules.md`](./docs/modules.md) — Module interface, lifecycle, anatomy of a third-party module.
- [`docs/configuration.md`](./docs/configuration.md) — every config key explained with defaults and tuning notes.
- [`docs/scaffolding.md`](./docs/scaffolding.md) — adding a new resource step-by-step, conventions for transactions/pagination/authz.
- [`docs/benchmark.md`](./docs/benchmark.md) — full methodology and 200k + 500k request results.
- [`docs/security.md`](./docs/security.md) — threat model, password hashing, JWT design, header policy.
- [`docs/rbac-menu.md`](./docs/rbac-menu.md) — RBAC + dynamic menu management: schema, endpoints, bootstrap, route-level guarding.
- [`ROADMAP.md`](./ROADMAP.md) · [`CONTRIBUTING.md`](./CONTRIBUTING.md) · [`SECURITY.md`](./SECURITY.md) · [`AGENTS.md`](./AGENTS.md)

---

## License

MIT © [dedeez14](https://github.com/dedeez14). Free for personal and commercial use; no warranty.
