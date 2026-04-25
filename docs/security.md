# Security

This document explains the security-relevant choices baked into the blueprint and what you must do to keep them effective.

## Threat model (what we defend against)

- **Credential theft via leaked database** — passwords are hashed with a memory-hard, salted KDF.
- **Token replay across audiences** — refresh tokens carry a different `aud` than access tokens.
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
