# Platform features

goforge ships with a bundle of opinionated, opt-in capabilities that
are rare to find together in the Go ecosystem. They are all wired into
the running HTTP server by `internal/platform.Build`. Each feature can
be toggled independently via the `platform.*` configuration group
(env: `GOFORGE_PLATFORM_*`).

| Feature | Endpoint(s) | Default | Env toggle |
| --- | --- | --- | --- |
| OpenAPI 3.1 spec + Swagger UI | `/openapi.json`, `/docs` | on | `GOFORGE_PLATFORM_OPENAPI_ENABLED` |
| Server-Sent Events bridge | `/api/v1/stream` | on | `GOFORGE_PLATFORM_REALTIME_ENABLED` |
| Idempotency-Key middleware | (header on POST/PUT/PATCH/DELETE) | on | `GOFORGE_PLATFORM_IDEMPOTENCY_ENABLED` |
| Transactional outbox dispatcher | (background worker) | on | `GOFORGE_PLATFORM_OUTBOX_ENABLED` |
| Prometheus metrics | `/admin/metrics` | on | `GOFORGE_PLATFORM_METRICS_ENABLED` |
| Module registry / health | `/admin/modules`, `/admin/healthz` | always | (admin token) |

Admin endpoints require either the configured `X-Admin-Token`
(`GOFORGE_PLATFORM_ADMIN_TOKEN`) or a request originating from
localhost.

---

## Idempotency-Key middleware

Stripe-style replay protection. Clients send `Idempotency-Key: <uuid>`
on a state-changing request; the framework remembers the response (or
409 if a different body is sent under the same key).

```bash
curl -X POST http://localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: 5e7b…' \
  -d '{"email":"a@b.com","password":"...","name":"A"}'
```

Replay attempts return:

```
HTTP/1.1 201 Created
Idempotency-Key: 5e7b…
Idempotent-Replay: true
```

A different body with the same key returns `409 Conflict` with code
`idempotency.key_reused`. Records expire after
`GOFORGE_PLATFORM_IDEMPOTENCY_TTL` (default `24h`); a 5-minute sweep
worker reclaims storage.

Production-grade Postgres store (`idempotency_keys` table) is shipped
in `migrations/0002_idempotency.up.sql`.

## Transactional outbox

Use this anywhere you would otherwise write to the DB *and* publish an
event. The pattern keeps both writes inside the same transaction so
nothing escapes if either side fails.

```go
err := pool.BeginTxFunc(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
    if err := orderRepo.Insert(ctx, tx, order); err != nil { return err }
    return outbox.Append(ctx, tx, "order.placed", OrderPlacedEvent{ ID: order.ID })
})
```

A background dispatcher (interval = `GOFORGE_PLATFORM_OUTBOX_INTERVAL_MS`,
batch = `GOFORGE_PLATFORM_OUTBOX_BATCH_SIZE`) drains
`outbox_messages`, marks each row `published_at` and forwards it to
the configured Sink. The default sink republishes to the in-process
`events.Bus`; replace it with Kafka/NATS when you outgrow a single
process.

## Domain event bus

Tiny in-process pub/sub for cross-module communication.

```go
bus.Subscribe("order.placed", func(ctx context.Context, raw json.RawMessage) error {
    var ev OrderPlaced
    _ = json.Unmarshal(raw, &ev)
    log.Info().Str("order", ev.ID).Msg("inventory should reserve")
    return nil
})
```

Tenant IDs are propagated automatically when events are published from
a tenant-scoped context. The bus is the binding glue between the
outbox dispatcher and the SSE bridge.

## Realtime SSE bridge

A `text/event-stream` endpoint that mirrors the bus to HTTP clients.
Browsers connect via the standard `EventSource` API:

```js
const es = new EventSource('/api/v1/stream?topics=order.placed,order.shipped');
es.addEventListener('order.placed', e => render(JSON.parse(e.data)));
```

Tenant ID is enforced via the `X-Tenant-ID` header (configurable via
`GOFORGE_PLATFORM_TENANT_HEADER`). Clients that supply the wrong
tenant simply receive nothing - the framework filters server-side.

Heartbeats every 15 seconds keep idle connections alive through
proxies. Slow consumers are dropped per-message rather than blocking
the publisher.

## Auto OpenAPI 3.1

Programmatic registry: handlers register their summary, request DTO,
response DTO and tags via `Document.AddOperation`. The framework
reflects the DTO struct tags into JSON Schema, including
`time.Time -> date-time`, `uuid.UUID -> uuid`, and `validate:"required"`
into `required`.

Hit `/openapi.json` to fetch the spec, `/docs` for Swagger UI.

## Prometheus metrics

`/admin/metrics` serves Go runtime, process, and the following
goforge-specific series:

| Metric | Type | Labels |
| --- | --- | --- |
| `goforge_http_requests_total` | counter | method, route, status |
| `goforge_http_request_duration_seconds` | histogram | method, route |
| `goforge_http_in_flight_requests` | gauge | – |

Add Grafana dashboards on top of those - latency p99 by route,
requests per minute, error rate.

## Multi-tenancy primitives

`pkg/tenant` ships:

- `tenant.ID` named string type
- `tenant.WithID(ctx, id)` / `tenant.FromContext(ctx)` /
  `tenant.Require(ctx)`
- `tenant.Middleware(resolver)` reads the tenant from the request and
  injects it into the context
- Default `HeaderResolver` reads `X-Tenant-ID`; swap in a subdomain or
  JWT-claim resolver in 3 lines.

Repositories should treat the tenant as a hard filter, not an
optional `WHERE` clause - always derive it from `tenant.Require(ctx)`.

## Feature flags

`pkg/flags` evaluates a name through an ordered chain of `Source`s
(env first, static defaults last) with TTL caching. Wire it into your
handlers:

```go
if !flags.Bool(ctx, "checkout.new_payment_flow", false) {
    return legacyCheckout(c)
}
return newCheckout(c)
```

`flags.Refresh()` clears the cache, suitable for SIGHUP or an admin
endpoint.
