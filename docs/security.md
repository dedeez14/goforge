# Security

This document explains the security-relevant choices baked into the blueprint and what you must do to keep them effective.

## Threat model (what we defend against)

- **Credential theft via leaked database** — passwords are hashed with a memory-hard, salted KDF.
- **Token replay across audiences** — refresh tokens carry a different `aud` than access tokens.
- **Refresh token replay** — every refresh token is single-use; `pkg/security.RefreshStore` tracks the JTI in `refresh_tokens` and atomically marks it consumed. Replaying a rotated token revokes every other live refresh token for the user (reuse-detection / chain kill).
- **Username enumeration via login timing** — `usecase.AuthUseCase.Login` runs an Argon2id verify against a pre-computed dummy hash when the email is unknown, so the response time is independent of whether the user exists.
- **Brute force on login / register** — per-IP rate limiter + Argon2id cost.
- **Common web headers attacks** — HSTS, no-sniff, frame-deny, referrer policy applied globally.
- **Crashing the server with malformed input** — body limit, validator, panic recover middleware.
- **Trace leaks** — every error response carries a stable code and human message; underlying causes stay in server-side logs.

What we do **not** defend against out of the box (you must add these per project):

- DDoS at the network layer — front with Cloudflare, AWS Shield, etc.
- Account enumeration on `/register` — currently returns `409 user.email_taken`. If that's a concern, return generic "if your email isn't taken we'll create the account" messaging and accept the UX trade-off.
- Distributed rate limiting — the in-process limiter is per-instance.

## Password hashing

We use **Argon2id**, the OWASP-recommended password hash. Defaults follow the IETF / OWASP 2023 guidance:

| Parameter | Default | Meaning |
| --- | --- | --- |
| `m` (memory) | 64 MiB | Cost in memory. |
| `t` (iterations) | 3 | Number of passes over memory. |
| `p` (parallelism) | 2 | Number of lanes. |
| Salt | 16 random bytes | Never reused. |
| Output | 32 bytes | Stored alongside parameters in the hash string. |

Stored format follows the standard reference `$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>`, so other languages can verify it.

### Transparent parameter upgrades

When `verify(password, stored_hash)` succeeds *and* the stored parameters are weaker than the current config, the hasher rehashes the password with the new parameters and asks the use-case to persist it. Users never notice. To strengthen the cost over time, just bump the env vars; old hashes upgrade on next login.

## JWT

- **Algorithm**: HS256.
- **Access token lifetime**: 15 minutes (configurable).
- **Refresh token lifetime**: 7 days (configurable).
- **Audience separation**: access tokens carry `aud=goforge.api`; refresh tokens carry `aud=goforge.api.refresh`. The middleware rejects an access token with the wrong audience, preventing a stolen refresh from masquerading as an access token.
- **Secret**: ≥ 32 bytes. **Rotate at least yearly** and on every suspected compromise. Rotation requires updating the secret and running both old + new through a verifier briefly — the framework does not yet ship a multi-secret verifier; see issue tracker.

### Why HS256, not RS256?

HS256 is faster to verify (relevant under 18k rps for `/me`) and the secret is the same for issue + verify. If you need third parties to verify tokens without holding the signing key, switch to RS256/EdDSA — `infrastructure/security/jwt.go` is the only file that needs changes.

## Headers

`security_headers` middleware sets:

- `Strict-Transport-Security: max-age=31536000; includeSubDomains`
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `Referrer-Policy: no-referrer`
- `Cross-Origin-Opener-Policy: same-origin`
- `Cross-Origin-Resource-Policy: same-site`

Adjust to taste in `internal/adapter/http/middleware/security_headers.go`.

## CORS

CORS is **off by default for credentialed APIs** — the only sane default is no cross-origin credentials. To enable:

```bash
GOFORGE_SECURITY_CORS_ALLOW_ORIGINS=https://app.example.com,https://admin.example.com
GOFORGE_SECURITY_CORS_ALLOW_CREDENTIALS=true
```

The startup validator refuses `ALLOW_CREDENTIALS=true` while origins is `*`.

## Rate limiting

The bundled limiter is a per-IP, in-process token bucket sized by `GOFORGE_SECURITY_RATE_LIMIT_PER_MIN`. It is fine for development and small deployments; it is **not** suitable as your only line of defence in production. Pair with an edge limiter that maintains state across instances (NGINX, Cloudflare, Kong, etc.).

## Request body limit

`GOFORGE_HTTP_BODY_LIMIT_BYTES` (default 1 MiB) caps the body Fiber will read. Endpoints that need larger payloads should accept presigned-URL uploads to object storage instead of streaming through the API.

## Panic recovery

The `recover` middleware turns any panic into a structured `errs.Internal` and logs the stack with the request ID. Clients see a generic 500 with the request ID; engineers can look up the full stack in logs.

## Logging hygiene

- **Never log password fields, JWTs, secrets, or PII** beyond what's already in the URL/path.
- The DTOs use `json:"-"` on the password field, but if you add new sensitive fields, add the same tag and audit the request log middleware.
- zerolog is configured with `time.RFC3339Nano` and JSON output; structured fields are easy to redact at the aggregator if needed.

## Database

- Use `sslmode=require` (or stricter) in production DSNs.
- The default DDL stores password hashes as `TEXT`. Don't add columns that hold raw secrets.
- Migrations are idempotent and reversible; new columns should use safe defaults so deploys don't lock the table.

## Pre-deploy checklist (security)

- [ ] `GOFORGE_JWT_SECRET` is rotated and at least 32 random bytes.
- [ ] CORS is restricted to production origins.
- [ ] Argon2id cost benchmarked on production CPU; takes 100–250 ms per hash.
- [ ] TLS terminated upstream; `Strict-Transport-Security` header is reaching clients.
- [ ] Edge rate limiter is configured; in-process limiter is a backstop.
- [ ] Logs land in an aggregator with retention; no `LOG_PRETTY=true`.
- [ ] Database connections use TLS.
- [ ] Backups are encrypted and tested.
- [ ] `cmd/pentest --base <prod_url>` exits 0 against a staging instance with prod-like config.

## 2026-04 penetration test results

A 15-scenario professional penetration test (`cmd/pentest`) was run against
goforge to look for OWASP API Top 10 issues plus Go-specific vectors.
Findings are listed below with severity (CVSS-style), the reproducer
that was used, and the mitigation that landed.

| #  | Severity   | Finding                                                                          | Mitigation                                                                                                                                                | Regression test                                          |
|----|------------|----------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------|
| F1 | 🔴 Critical | `/api/v1/auth/refresh` accepted the same refresh token indefinitely.             | Added `security.RefreshStore` (Postgres + memory impls). `Use` is atomic; replays return 401 with code `auth.token_reused` and revoke the user's chain.   | `TestAuth_RefreshTokenIsSingleUse`, `TestAuth_RefreshReuseRevokesAllTokens` |
| F2 | 🟠 High     | Login latency revealed which emails were registered (~95 ms vs <1 ms).           | `Login` runs an Argon2id verify against a pre-computed dummy hash on the missing-user path. The two paths now run within ~5% of each other.               | `TestAuth_LoginTimingEqualization`                       |
| F3 | 🟡 Medium   | A 10 000-byte `X-Tenant-ID` header crashed the request with a 500.               | Lifted `fasthttp.ReadBufferSize` to 16 KiB, bounded tenant IDs to 128 chars and rejected control bytes (`pkg/tenant.MaxTenantIDLength`, `ErrInvalid`).    | `cmd/pentest` `oversize_tenant_no_500`                   |
| F4 | 🟢 Low      | Devin Review #1: outbox tenant auto-detect silently always wrote `NULL`.         | Exported `events.TenantFromContext`; outbox now reuses the same context key as `pkg/tenant`. Removed the duplicate (and broken) private key.              | `TestWithID_PropagatesToEvents`                          |
| F5 | 🟢 Low      | Devin Review #2: SSE endpoint required `X-Tenant-ID` even in single-tenant apps. | Added `tenant.OptionalMiddleware`; SSE uses it so anonymous clients see all events while tenant-aware clients still get scoping.                          | `TestOptionalMiddleware_AllowsMissingHeader`             |
| F6 | 🟢 Info     | Unknown route under `/api/v1/*` returns 401 (auth) instead of 404.               | Documented as a deliberate trade-off: pre-auth route disclosure is worse than a generic 401. Kept as-is.                                                  | n/a                                                      |

**Verified-safe scenarios** (the framework already defended against
these; the harness now pins it):

- JWT `alg=none` rejected (401).
- JWT signed with HS256 but missing/invalid signature rejected (401).
- Empty / non-Bearer Authorization scheme rejected.
- SQL injection probe in the email field rejected by validator (400).
- Mass-assignment of `role`, `is_admin`, `id` at register dropped by DTO; new users always get `role=user`.
- `Idempotency-Key` reuse with a different body returns 409.
- Oversize body (6 MiB > 1 MiB BodyLimit) rejected (400/413/connection reset).
- HSTS, X-Content-Type-Options, X-Frame-Options, Referrer-Policy headers present on every response.
- Admin endpoints (`/admin/*`) require `X-Admin-Token` or localhost.
- Error envelope is consistent for unknown routes (`success:false`, canonical `code`).

### Reproducing the audit

```bash
docker compose -f deploy/docker/docker-compose.yml up -d
GOFORGE_JWT_SECRET=$(openssl rand -hex 32) go run ./cmd/api &
go run ./cmd/pentest --base http://localhost:8080
```

The harness exits non-zero on any regression. CI runs the unit-level
regression tests (`go test ./...`) on every push; the integration
harness is run before each release.
