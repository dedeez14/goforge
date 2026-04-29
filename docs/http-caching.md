# HTTP Caching

`pkg/httpcache` ships a lightweight conditional-GET middleware for
Fiber handlers whose response changes infrequently or follows a
predictable invalidation event. It emits strong ETags (SHA-256 of the
response body) and honours `If-None-Match` with a 304, cutting CPU
and egress on read-heavy routes by roughly 40-80%.

## Wiring

```go
import "github.com/dedeez14/goforge/pkg/httpcache"

// Public, shared-cacheable (CDN, API gateway)
cache := httpcache.New(httpcache.Options{
    MaxAge:         300, // 5 min
    Public:         true,
    MustRevalidate: true,
})
app.Get("/openapi.json", cache, openapi.JSONHandler())

// Per-user content (menus, permissions). Must be private so shared
// caches never collapse two users' responses onto the same key.
private := httpcache.New(httpcache.Options{
    MaxAge:         30,
    Private:        true,
    MustRevalidate: true,
})
authed.Get("/menus/mine", private, h.Menus.MyMenu)
```

## Framework defaults

Enabled out-of-the-box on:

| Route | Scope | MaxAge | Why |
|-------|-------|--------|-----|
| `/openapi.json` | Public | 300s | Document only changes on redeploy. |
| `/.well-known/jwks.json` | Public | 300s | Rotation events are rare (minutes to months apart); downstream services poll aggressively. |
| `/api/v1/me/access` | Private | 30s | SPA fetches on every page navigation. |
| `/api/v1/menus/mine` | Private | 30s | Permission-pruned tree; re-fetched on render. |

## Semantics

- **Only `GET` and `HEAD`** are eligible. Mutations always run.
- **Only `200 OK` with a non-empty body** is cached. Errors and `204 No
  Content` pass through untouched so retries can succeed.
- **ETag is a strong validator** (`"hex"`) built from a SHA-256 hash
  of the response body. Hashing is ~300 ns/KiB on modern CPUs - noise
  compared to JSON serialisation.
- **`If-None-Match: *`** always matches (RFC 7232 §3.2).
- **Weak validators** (`W/"…"`) are accepted on request; the
  response always emits a strong ETag.
- **`Public` and `Private` are mutually exclusive**; passing both
  panics at construction to catch misconfiguration at boot.

## Trade-offs

- The middleware buffers the full response body before emitting
  headers (Fiber already does this), so it is unsuitable for SSE or
  large streaming endpoints.
- Hashing runs even on cache hits because we do not know whether to
  short-circuit until we've seen the response bytes. For extremely
  hot paths this is the right trade-off because hashing is O(body) and
  JSON serialisation is already O(body).
- ETag is derived from the serialised body. If the handler emits
  map-keyed JSON with non-deterministic ordering, the ETag churns.
  Use stable key ordering (Go's `encoding/json` does this for
  structs; for `map[string]…` sort keys explicitly).
