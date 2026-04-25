# API Keys

goforge ships service-to-service API key authentication with **scoped
permissions** that compose with the existing JWT + RBAC stack.

## Token format

```
gf_<env>_<id>_<secret>
└┬┘ └┬─┘ └┬──────────┘ └┬───────────────────────────────────────────┘
 │   │    │             └ 64 hex chars  (256 bits of secret entropy)
 │   │    └ 12 hex chars (48-bit unique id, used for DB lookup)
 │   └ env tag           ("live" / "staging" / "dev" / ...)
 └ framework tag         (`gf` — never changes)
```

Examples:

- `gf_live_a1b2c3d4e5f6_<64 hex chars>` — production key
- `gf_dev_<id>_<secret>` — local-development key

The visible env tag exists so a leaked key is *immediately* identifiable.
Everything before the secret is also stored as the `prefix` column on
`api_keys` and is what `forge` / dashboards display.

## Storage

Only the SHA-256 of the entire plaintext is stored (`hash` column). The
plaintext is never persisted — `POST /api/v1/api-keys` returns it
exactly once in the response, and clients must capture it at that
moment.

Why SHA-256 instead of Argon2id (used for password hashing)? The
secret already carries 256 bits of cryptographic randomness; the cost
of Argon2id buys nothing because there is no low-entropy input to
slow down. A constant-time hex compare prevents timing leaks.

## HTTP API

All endpoints below require the caller to be authenticated; they
operate against the *caller's own* keys. Admin views over other
users' keys can be exposed by registering the same handler behind
a tighter `RequirePermission("apikeys.manage", ...)` group.

### `POST /api/v1/api-keys`

Mint a new key for the authenticated user.

```json
{
  "name": "deploy-bot",
  "scopes": ["deploys.create", "deploys.read"],
  "expires_at": "2026-01-01T00:00:00Z"
}
```

`expires_at` is optional. Empty / missing means "never expires".

Response (201):

```json
{
  "id": "...",
  "prefix": "gf_live_a1b2c3d4e5f6",
  "name": "deploy-bot",
  "scopes": ["deploys.create", "deploys.read"],
  "user_id": "...",
  "created_at": "...",
  "plaintext": "gf_live_a1b2c3d4e5f6_<64 hex>"
}
```

Capture `plaintext` on the client side **immediately** — it is never
re-emitted.

### `GET /api/v1/api-keys`

List the caller's keys (public-visible fields only — no secrets, no
hashes).

### `DELETE /api/v1/api-keys/{id}`

Revoke a key. Idempotent from the caller's perspective (a second call
returns 404 because the key is gone). The audit log retains the
original revoke event with timestamp + actor.

## Authentication on protected routes

The framework's `Auth` middleware accepts both shapes:

```
Authorization: Bearer <jwt-access-token>
Authorization: Bearer gf_live_<id>_<secret>
```

When the bearer starts with `gf_`, the API-key path runs:

1. `pkg/apikey.Parse` validates the structural format (rejects
   malformed bearers with 401 — same code as JWT to avoid an oracle).
2. The repo looks up the key by prefix.
3. SHA-256 hash compared in constant time.
4. `IsActive(now)` checks `revoked_at` and `expires_at`.
5. On success, `c.Locals` carries the key's owner id + scopes, and
   `last_used_at` is touched (best-effort; failures here never reject
   an otherwise-valid request).

A bearer that does *not* start with `gf_` is forwarded to the JWT
middleware unchanged, so existing routes that already used `Auth`
keep working with no code change.

## Scopes vs RBAC roles

Scopes attached to the key are **denormalised** — they are not derived
from the owning user's role surface. This is intentional:

- A service key can be **narrower** than any role its owner has. For
  example, a user with the `admin` role mints a key with scope
  `["reports.read"]` for a CI job; the key cannot perform admin
  actions even though the user can.
- A service key can also carry a permission the owning user does *not*
  hold (rare; useful for impersonation use cases the deployment opts
  into).

`RequirePermission(code, ...)` short-circuits as soon as it sees an
API-key request:

```go
if scopes := APIKeyScopesFromCtx(c); scopes != nil {
    // exclusively use scopes — never fall back to RBAC roles
}
```

The wildcard `"*"` is honoured (operators use it for deployment
"break-glass" keys; the deployment is responsible for gating who may
issue those).

## Database

Migration: [`migrations/0008_api_keys.up.sql`](../migrations/0008_api_keys.up.sql).

Indexes:

- `api_keys_prefix_uidx` (unique on `prefix`) — fast O(1) lookup
- `api_keys_user_idx` (partial: `WHERE deleted_at IS NULL`) —
  list-by-user for the dashboard
- `api_keys_active_idx` (partial: not deleted, not revoked) — keeps
  the auth path index-only on the hot lookup

## Generating keys programmatically

```go
import keytoken "github.com/dedeez14/goforge/pkg/apikey"

gen, err := keytoken.Generate("live")
// gen.Plaintext = "gf_live_a1b2c3d4e5f6_<64 hex>"
// gen.Prefix    = "gf_live_a1b2c3d4e5f6"
// gen.Hash      = "<sha256(plaintext) as hex>"
```

`pkg/apikey` has no DB or HTTP dependencies and is safe to import from
CLI / migration helpers.
