# Security policy

## Supported versions

goforge follows semver. The latest minor of the latest major receives
security fixes; older majors are best-effort.

| Version | Supported          |
| ------- | ------------------ |
| 0.x     | :white_check_mark: |

## Reporting a vulnerability

Please report security issues **privately** so we have time to ship a
fix before the details are public.

Email: febriansyahd65@gmail.com (subject: `[goforge] security advisory`).

What to include:

1. The component (e.g. `pkg/idempotency`, `internal/infrastructure/security`).
2. A reproducer (PoC code or curl invocation is best).
3. Affected versions / commit SHAs.
4. Impact: what does an attacker gain?
5. Suggested mitigation, if you have one.

## Disclosure timeline

- **Day 0**: report received, acknowledged within 72 hours.
- **Day ≤ 30**: fix prepared, shipped in a patch release.
- **Day ≤ 60**: public advisory + CVE if applicable.

We will credit the reporter unless they prefer anonymity.

## Defensive defaults

goforge ships with the following defaults so a fresh install is not
trivially exploitable:

- Argon2id password hashing with OWASP 2023 parameters.
- JWT signed HS256 with 32+ char secret enforced at startup; only
  HS256 is accepted when verifying so `alg=none` and algorithm
  confusion attacks are rejected.
- **Refresh token rotation with reuse-detection** — every refresh
  token is single-use and is tracked in `refresh_tokens`. Replaying a
  rotated token revokes every outstanding refresh token for the user
  (containment).
- **Login timing equalization** — when the supplied email does not
  resolve to a user, `Login` still runs an Argon2id `Verify` against
  a pre-computed dummy hash so the response time is independent of
  whether the email exists (no enumeration).
- Secure HTTP headers, rate limiter, panic recovery, request timeout.
- 16 KiB header cap (fasthttp `ReadBufferSize`) so an attacker cannot
  amplify memory by sending a multi-megabyte `X-Tenant-ID`.
- Tenant identifiers are bounded to 128 bytes and forbid control
  characters (`pkg/tenant.MaxTenantIDLength`).
- Idempotency replay protection on `/api/v1/auth/register` and any
  POST/PUT/PATCH/DELETE that opts in.
- Admin endpoints require either `X-Admin-Token` or localhost.
- Tenant middleware refuses requests without a tenant ID on routes
  that opt into strict tenancy; non-tenant-aware deployments use
  `tenant.OptionalMiddleware` instead, which never rejects.

## Pentest harness

Run `cmd/pentest` against any goforge instance to confirm the
hardening assumptions still hold:

```bash
go run ./cmd/pentest --base http://localhost:8080
```

It executes 15 attack scenarios (OWASP API Top 10 + Go-specific
vectors) and exits non-zero if any of them regresses. See
[`docs/security.md`](docs/security.md) for the full list and the
mitigation each scenario verifies.

## Known sharp edges

- **CORS** defaults to `*`. Restrict it via
  `GOFORGE_SECURITY_CORS_ALLOW_ORIGINS` before going to production.
- **Trusted proxies** are off by default; if you're behind a
  load-balancer set `GOFORGE_HTTP_TRUSTED_PROXIES`. Until you do,
  `X-Forwarded-For` is ignored by the rate limiter, which means
  requests share a key by client IP — correct in single-host mode,
  bypassable behind a misconfigured proxy.
- **JWT secret rotation** is manual; the framework supports running
  multiple verifiers but applications must orchestrate the rotation.
- **Header > 16 KiB** is rejected by fasthttp before fiber sees it,
  which surfaces as a 500 envelope (the parse error never reaches
  the application's error handler). Realistic clients never hit this.

See [`docs/security.md`](docs/security.md) for the full threat model
and pentest results.
