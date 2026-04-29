# Session Management

> Self-service "active devices" — list, revoke, and "logout everywhere"
> for the authenticated user.

`goforge` materialises every login as a row in the `sessions` table so
the user can see and manage every active device from a single endpoint.
Sessions are created implicitly by the auth flow (register / login /
refresh) and revoked explicitly through `/api/v1/me/sessions`.

## Data model

| column        | type           | notes                                                  |
|---------------|----------------|--------------------------------------------------------|
| `id`          | uuid           | primary key, surfaced to the user as the session id    |
| `user_id`     | uuid           | FK → `users.id`, ON DELETE CASCADE                     |
| `user_agent`  | text           | captured at login, truncated to 512 bytes              |
| `ip`          | text           | best-effort client IP from `c.IP()` (proxy-aware)      |
| `created_at`  | timestamptz    | initial login time                                     |
| `last_used_at`| timestamptz    | bumped on every successful refresh-token rotation      |
| `expires_at`  | timestamptz    | login time + JWT refresh TTL; advanced on every touch  |
| `revoked_at`  | timestamptz    | non-NULL means the session can no longer issue tokens  |

`refresh_tokens` carries a nullable `session_id` FK so revoking a
session cascades to every refresh token the device ever held — even if
the in-flight rotation chain is several hops deep.

## HTTP API

All endpoints are mounted under `/api/v1/me/sessions` and require an
**interactive user session** (JWT). API-key-authenticated requests are
rejected with `403 apikey.user_session_required` to prevent a leaked
narrow key from kicking the human off their own devices.

| Method | Path                       | Description                                                      |
|--------|----------------------------|------------------------------------------------------------------|
| GET    | `/api/v1/me/sessions`      | List active sessions; the caller's row is flagged `current=true` |
| DELETE | `/api/v1/me/sessions/{id}` | Revoke a single session (404 if not owned)                       |
| DELETE | `/api/v1/me/sessions`      | "Logout everywhere except this device", returns revoked count    |

The `current` flag is derived from the `sid` claim on the access token,
so the UI can render a "this device" badge without an extra round trip.

### Example

```http
GET /api/v1/me/sessions
Authorization: Bearer <access-token>
```

```json
{
  "data": [
    {
      "id": "0f3c…",
      "user_agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_2)",
      "ip": "203.0.113.7",
      "current": true,
      "created_at": "2026-04-22T09:14:11Z",
      "last_used_at": "2026-04-24T17:02:48Z",
      "expires_at": "2026-04-25T17:02:48Z"
    },
    {
      "id": "a1d2…",
      "user_agent": "okhttp/4.12 (Android 14)",
      "ip": "198.51.100.4",
      "current": false,
      "created_at": "2026-04-20T11:02:18Z",
      "last_used_at": "2026-04-24T08:31:06Z",
      "expires_at": "2026-04-25T08:31:06Z"
    }
  ]
}
```

## Lifecycle

1. **Register / Login** — `AuthUseCase` mints a session row populated
   with the request's `User-Agent` header and `c.IP()`. The `sid`
   claim on both the access and refresh tokens carries the session id.
2. **Refresh** — `AuthUseCase.Refresh` resolves the session via the
   refresh-store binding and calls `Repo.Touch` to bump
   `last_used_at` + `expires_at`. The session id is preserved across
   rotations so the device count stays stable.
3. **Reuse detection** — when a rotated refresh token is replayed,
   `AuthUseCase` revokes every refresh token *and* every session for
   the user. The attacker loses every device hop in one transaction.
4. **Sweep** — call `Repo.Sweep(ctx, before)` from a periodic job to
   delete rows whose `expires_at` is far in the past. The table stays
   small even for users who churn devices.

## Why a separate table?

A single `refresh_tokens` row already exists per JWT, so why double up?
Three reasons:

* **User-facing identity.** A single device may rotate through dozens
  of refresh tokens; surfacing the underlying chain would be confusing.
  One session = one entry in the UI.
* **Atomic revoke.** Marking the session row revoked + cascading to
  the refresh tokens runs in a single transaction. There is no window
  where the device list says "revoked" but the next rotation succeeds.
* **Device hints survive rotation.** `User-Agent` and IP are captured
  once at login and reused across every rotation, so the UI does not
  flicker between desktop and "unknown".
