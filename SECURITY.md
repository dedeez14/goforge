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
- JWT signed HS256 with 32+ char secret enforced at startup.
- Secure HTTP headers, rate limiter, panic recovery, request timeout.
- Idempotency replay protection on `/api/v1/auth/register` and any
  POST/PUT/PATCH/DELETE that opts in.
- Admin endpoints require either `X-Admin-Token` or localhost.
- Tenant middleware refuses requests without a tenant ID on tenant
  routes.

## Known sharp edges

- **CORS** defaults to `*`. Restrict it via
  `GOFORGE_SECURITY_CORS_ALLOW_ORIGINS` before going to production.
- **Trusted proxies** are off by default; if you're behind a
  load-balancer set `GOFORGE_HTTP_TRUSTED_PROXIES`.
- **JWT secret rotation** is manual; the framework supports running
  multiple verifiers but applications must orchestrate the rotation.

See [`docs/security.md`](docs/security.md) for the full threat model.
