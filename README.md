# goforge

A high-performance, memory-compact, Clean Architecture **Go backend API framework** — a reusable blueprint for starting new services with batteries included: `fiber/v2` + `pgx/v5` + `zerolog` + `argon2id` + `jwt`, wrapped in a strict layered design that stays DRY and easy to extend.

- **Fast**: Fiber/fasthttp router, `pgxpool` with tuned defaults, zero-allocation logging via zerolog, minimal reflection in hot paths.
- **Clean Architecture**: domain → use-case → adapter (HTTP + repository) → infrastructure. Dependencies point inward only.
- **Secure by default**: Argon2id passwords, JWT access + refresh, security headers, CORS, rate limiting, request timeouts, panic recovery, structured request IDs, parameterised SQL.
- **Consistent**: every endpoint responds with the same JSON envelope; every error flows through a single mapper.
- **DRY**: reusable error taxonomy, response helpers, validator, pagination, middleware set.
- **Turn-key**: `make up` brings Postgres + migrations + API online in a single command; distroless production image is ~10 MB.
- **Scaffold-friendly**: `make scaffold name=Order` generates the full layered skeleton of a new resource in one shot.

---

## 1. Quickstart

```bash
# 1. clone + env
cp .env.example .env
# edit GOFORGE_JWT_SECRET to something >= 32 chars

# 2. start Postgres + run migrations + start API
make up
# or: docker compose -f deploy/docker/docker-compose.yml up --build

# 3. smoke-test
curl -s http://localhost:8080/healthz | jq
curl -s -X POST http://localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"me@example.com","password":"supersecret","name":"Me"}' | jq
```

Stop the stack with `make down`. Postgres data persists in the `pgdata` docker volume until you remove it.

### Without Docker

```bash
# prereqs: Go 1.25+, a running PostgreSQL, golang-migrate
make migrate-up          # applies /migrations against $MIGRATE_URL
make run                 # reads .env + starts the API on :8080
```

---

## 2. Architecture

```
cmd/api/main.go                  # thin entrypoint — calls internal/app.Run
internal/
  app/                           # composition root (wires graph, runs server)
  config/                        # viper + env + validation
  domain/                        # entities, domain errors, repo interfaces (pure Go)
    user/
  usecase/                       # business logic, transport-agnostic
  adapter/
    http/
      handler/                   # fiber handlers (thin)
      dto/                       # wire types + mappers to/from domain
      middleware/                # request id, auth, recover, timeout, headers
    repository/
      postgres/                  # pgx implementations of domain repositories
  infrastructure/
    database/                    # pgxpool factory
    logger/                      # zerolog factory
    security/                    # password hasher + jwt issuer
    server/                      # fiber app builder + route registrar
pkg/
  errs/                          # canonical *errs.Error taxonomy
  httpx/                         # response envelope + error mapper
  validatorx/                    # go-playground/validator wrapper
  paginate/                      # pagination params
migrations/                      # golang-migrate SQL files
scripts/                         # scaffold.sh generator
deploy/docker/                   # docker-compose.yml for local stack
```

### Dependency rule

Inner layers never import outer layers:

```
pkg/*             ←  used by all (no domain deps of its own)
domain/*          ←  imported by usecase, adapter/repository, adapter/http/dto
usecase/*         ←  imported by adapter/http/handler
adapter/*         ←  imported by internal/app only
infrastructure/*  ←  imported by internal/app only
```

Use-cases depend on **interfaces** (e.g. `user.Repository`, `security.PasswordHasher`, `security.TokenIssuer`). That's what makes the business logic swappable, testable in-memory, and protected from transport concerns.

### Response envelope

Every success and failure response is wrapped once:

```json
{
  "success": true,
  "data": { ... },
  "meta": { "page": 1, "page_size": 20, "total": 142, "total_pages": 8 },
  "request_id": "0d5a4f3e-..."
}
```

```json
{
  "success": false,
  "error": {
    "code": "user.email_taken",
    "message": "email is already registered",
    "meta": { "fields": { "email": "must be a valid email" } }
  },
  "request_id": "0d5a4f3e-..."
}
```

Error mapping happens in exactly one place: [`pkg/httpx/response.go`](./pkg/httpx/response.go). Handlers only need to call `httpx.RespondError(c, err)`.

---

## 3. Adding a new resource

Run the scaffolder:

```bash
make scaffold name=Order
```

It creates:

- `internal/domain/order/{order,repository}.go`
- `internal/usecase/order.go`
- `internal/adapter/repository/postgres/order.go`
- `internal/adapter/http/dto/order.go`
- `internal/adapter/http/handler/order.go`
- `migrations/NNNN_create_orders.(up|down).sql`

Then:

1. Flesh out the repository's `Create` (and any additional methods).
2. Instantiate the repo + use-case + handler in `internal/app/app.go`.
3. Register routes in `internal/infrastructure/server/router.go`.
4. `make migrate-up` and you're ready.

---

## 4. Configuration

All configuration is loaded via viper with the precedence **defaults < file < environment**. Environment variables use the `GOFORGE_` prefix with underscores for nesting (e.g. `GOFORGE_DATABASE_DSN`, `GOFORGE_JWT_ACCESS_TTL`). See [.env.example](./.env.example) for every knob.

Highlights:

| Key | Meaning | Default |
| --- | --- | --- |
| `GOFORGE_HTTP_PORT` | TCP port | `8080` |
| `GOFORGE_HTTP_BODY_LIMIT_BYTES` | max request body | `1 MiB` |
| `GOFORGE_HTTP_PREFORK` | Fiber prefork (multi-process accept) | `false` |
| `GOFORGE_DATABASE_MAX_CONNS` | pgxpool ceiling | `20` |
| `GOFORGE_DATABASE_STATEMENT_CACHE` | disable when fronted by PgBouncer (txn mode) | `true` |
| `GOFORGE_JWT_SECRET` | HS256 secret (≥ 32 chars, required) | — |
| `GOFORGE_JWT_ACCESS_TTL` / `_REFRESH_TTL` | token lifetimes | `15m` / `168h` |
| `GOFORGE_SECURITY_RATE_LIMIT_PER_MIN` | per-IP requests/minute | `120` |

---

## 5. Performance & memory notes

- **Router**: Fiber v2 on fasthttp — typically 2-3× faster and significantly lower memory/allocation than `net/http` equivalents.
- **Database**: `pgx` native binary protocol + prepared-statement cache; tune `min_conns` / `max_conns` to your workload. Disable the statement cache when running behind PgBouncer in transaction mode.
- **Logging**: zerolog with level-gated chained calls skips field encoding entirely when disabled.
- **Hot paths**: no reflection in request-id / auth / timeout middleware; DTOs avoid `any` by mapping explicitly via `dto.UserFromDomain` et al.
- **Shutdown**: graceful drain honoring `GOFORGE_HTTP_SHUTDOWN_TIMEOUT` so in-flight requests aren't cut off on SIGTERM.

### Benchmarking

```bash
# inside the running compose stack
docker run --rm --network host williamyeh/wrk -t4 -c256 -d30s http://localhost:8080/healthz
```

Expect tens of thousands of req/s on commodity hardware for `/healthz`; the authenticated endpoints are gated primarily by Argon2id cost (intentional).

---

## 6. Security posture

- **Passwords**: Argon2id (memory 64 MiB, t=3, p=2) with embedded parameters for future upgrades. Login transparently rehashes when parameters have been tightened.
- **Tokens**: separate access + refresh JWTs; refresh tokens cannot be presented as access tokens (or vice versa) — the use-case enforces `typ`.
- **Transport headers**: `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, `Strict-Transport-Security`, `Permissions-Policy`, COOP/CORP.
- **CORS**: configured per environment via `GOFORGE_SECURITY_CORS_ALLOW_ORIGINS`.
- **Rate limiting**: per-IP sliding window, returns a structured `rate_limited` envelope.
- **Input validation**: every DTO runs through `go-playground/validator` via `pkg/validatorx`, which returns a per-field `fields` map that front-ends can bind directly to form errors.
- **SQL**: only parameterised queries via `pgx`; unique-violation `23505` is translated to a domain conflict.
- **Panics**: recovered in middleware, full stack logged server-side, generic 500 returned.

---

## 7. Testing

```bash
make test                  # unit tests, -race, -count=1
go test -run TestAuth_ ./internal/usecase -v
```

The use-case layer is tested with an in-memory `user.Repository` so tests are hermetic and sub-millisecond. Integration tests for the Postgres repository can be added with [`testcontainers-go`](https://golang.testcontainers.org/) — see `internal/adapter/repository/postgres/*_integration_test.go` (add as needed).

---

## 8. Tooling

- `golangci-lint` — run via `make lint`; configuration in `.golangci.yml`.
- `golang-migrate` — SQL migrations in `./migrations`.
- Pre-commit: the Makefile defaults (`fmt`, `vet`, `test`) are a good foundation.

---

## 9. HTTP reference

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| `GET`  | `/healthz` | — | Liveness |
| `GET`  | `/readyz`  | — | Readiness (pings DB) |
| `POST` | `/api/v1/auth/register` | — | Create user + return tokens |
| `POST` | `/api/v1/auth/login` | — | Verify credentials + return tokens |
| `POST` | `/api/v1/auth/refresh` | — | Exchange refresh token for new pair |
| `GET`  | `/api/v1/auth/me` | Bearer access | Current user |

---

## 10. License

MIT © dedeez14 — do whatever you want, just don't sue me.
