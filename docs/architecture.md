# Architecture

goforge follows a strict four-layer Clean Architecture. The dependency rule is the only rule that matters: **inner layers never import outer ones.**

```
domain  ←  usecase  ←  adapter  ←  infrastructure / app
```

## Layers

### `internal/domain`

Pure Go. No framework, no database, no HTTP. Each subpackage represents an *aggregate* (a transactional consistency boundary):

- **Entities** — plain structs whose constructors guarantee invariants (`user.New` validates email shape, hashes the password, rejects empty names).
- **Repository interfaces** — what the use-case needs from persistence, expressed in domain language (`FindByEmail`, not `SELECT * FROM users`).
- **Domain errors** — typed sentinels that map to canonical `*errs.Error` values.

If you can't compile the domain package without a `database/sql` import, the abstraction has leaked.

### `internal/usecase`

Business logic. Orchestrates domain entities and repository interfaces. **Transport-agnostic** — there is nothing here that knows about HTTP, gRPC, CLIs, or queues. That makes use-cases trivially unit-testable with in-memory repositories.

A use-case method always returns either a domain value or an `error`. It never returns HTTP status codes, JSON, or `*fiber.Ctx`.

### `internal/adapter`

Adapters translate between the outside world and the use-case layer.

- `adapter/http/handler` — Fiber handlers. Thin: parse DTO → call use-case → respond. ~20 lines each.
- `adapter/http/dto` — wire DTOs (`RegisterRequest`, `UserResponse`) and *mappers* between DTOs and domain entities. Domain types never leak past this boundary.
- `adapter/http/middleware` — request ID, auth, recover, timeout, security headers, rate limiter.
- `adapter/repository/postgres` — pgx implementations of domain repositories. Translates `pgx.ErrNoRows` and unique-violation `23505` into domain errors at this seam.

### `internal/infrastructure`

Concrete drivers and the HTTP server itself:

- `database/postgres.go` — `pgxpool` factory with sensible defaults (statement cache, health-check period, exec mode).
- `logger/logger.go` — zerolog factory; one place to configure level, pretty-print, sampling.
- `security/hasher.go` — Argon2id password hashing with **transparent parameter upgrades** on next login.
- `security/jwt.go` — HS256 access + refresh tokens with explicit `aud` separation so a refresh token can never be used as an access token.
- `server/router.go` — single source of truth for route registration.
- `server/server.go` — Fiber app builder, error handler, JSON encoder choice (`go-json` for speed).

### `internal/app`

The composition root. `app.Run(ctx)` wires every concrete dependency once, hands them to use-cases, and starts the HTTP server. **This is the only place** allowed to know about every layer at the same time.

## Request lifecycle

1. **Listener** (Fiber) accepts the connection.
2. **`requestid` middleware** assigns/forwards `X-Request-ID`, stores it on the context, and adds it to every log line.
3. **`recover` middleware** turns panics into `errs.Internal` so the panic message never reaches the client.
4. **`security_headers` middleware** sets HSTS, CSP, X-Frame-Options, etc.
5. **`timeout` middleware** binds a per-request `context.Context` deadline.
6. **Rate limiter** (per-IP, in-process) drops requests beyond the configured ceiling.
7. **Auth middleware** (when applied) parses Bearer token, validates signature/expiry/audience, attaches user ID + role to the context.
8. **Handler** parses the DTO with `validatorx`, calls a use-case method, calls `httpx.RespondData` or `httpx.RespondError`.
9. **Single error mapper** in `pkg/httpx/response.go` turns `*errs.Error` into JSON + HTTP status. *Handlers never call `c.Status(...)` directly.*

## Error model

Every domain or adapter error eventually becomes an `*errs.Error`:

```go
type Error struct {
    Code    string         // stable machine identifier, e.g. "user.email_taken"
    Message string         // human-friendly message
    Status  int            // HTTP status (used only by the response mapper)
    Meta    map[string]any // optional structured detail (e.g. per-field validation)
    err     error          // wrapped cause; preserved for logs, never leaked to client
}
```

Benefits:

- **Single mapper** — clients see consistent envelopes; you never accidentally leak a stack trace.
- **Stable codes** — frontend/mobile can `switch` on `error.code` without parsing English.
- **Preserved cause** — `errors.Is` / `errors.As` still work, so logs retain the full chain.

## DRY guarantees

| Concern | Single source of truth |
| --- | --- |
| Response envelope | `pkg/httpx/response.go` |
| Error → HTTP status mapping | `pkg/httpx/response.go` |
| Validation field-name extraction | `pkg/validatorx/validator.go` |
| Pagination clamping | `pkg/paginate/paginate.go` |
| Argon2id parameters | `infrastructure/security/hasher.go` (driven by config) |
| Route registration | `infrastructure/server/router.go` |
| App composition | `internal/app/app.go` |

If you find yourself copying logic out of any of these, prefer extending the helper. The blueprint is meant to encode "the one way" for the project.

## Why these specific libraries?

| Choice | Reason |
| --- | --- |
| **Fiber v2** | Built on fasthttp; lower per-request allocation than net/http and excellent ergonomics. |
| **pgx/v5 + pgxpool** | Native Postgres protocol, no `database/sql` reflection overhead, statement cache, batch support. |
| **zerolog** | Zero-allocation structured logging; JSON by default; sampling built-in. |
| **viper** | Config from env, file, flag, all with the same struct unmarshal. |
| **validator/v10** | The de-facto Go validator; field-tag based; we wrap it once for a uniform error map. |
| **golang-migrate** | Reversible SQL migrations; CLI works in CI/CD. |
| **golang-jwt v5** | Maintained fork with `jwt/v5` API, audiences, and clean error sentinels. |
| **argon2** | The OWASP-recommended password hash; we ship IETF defaults and let you bump them via config. |

## When to break the rules

The dependency rule is non-negotiable, but you may:

- Add new packages under `pkg/` for cross-cutting helpers — keep them stateless.
- Introduce alternative adapters (e.g. `adapter/repository/mongo`) without touching domain or use-case code; that's the entire point.
- Skip the use-case layer for trivial CRUD if it would be 100 % pass-through; document the choice in the handler.
