# goforge roadmap

> A living document. Items move freely between sections; check the git
> log for canonical history.

## Now (in this milestone)

- [x] Clean architecture scaffold (Fiber + pgx + zerolog)
- [x] 200k / 500k benchmark harness with reports
- [x] Module system (`pkg/module`)
- [x] In-process domain event bus (`pkg/events`)
- [x] Multi-tenancy primitives (`pkg/tenant`)
- [x] Idempotency-Key middleware (memory + Postgres stores)
- [x] Transactional outbox table + dispatcher (`pkg/outbox`)
- [x] Server-Sent Events bridge (`pkg/realtime`)
- [x] Auto OpenAPI 3.1 generator (`pkg/openapi`)
- [x] Prometheus metrics + admin endpoints
- [x] Feature flags with TTL cache (`pkg/flags`)
- [x] `forge` CLI (doctor / scaffold / migrate / openapi / bench / module / version)

## Next

- [ ] OpenTelemetry tracing module (`pkg/otel`)
- [ ] WebSocket bridge alongside SSE
- [ ] Outbox sinks for Kafka / NATS / Redis Streams
- [ ] Built-in audit log module (`pkg/audit`)
- [ ] CSRF middleware (opt-in for cookie-auth deployments)
- [ ] Admin dashboard at `/admin` rendering modules / outbox / flags
- [ ] `examples/saas-multitenant` complete walkthrough
- [ ] `examples/realtime-orders` complete walkthrough
- [ ] Postgres LISTEN/NOTIFY adapter for the outbox

## Later

- [ ] Pluggable cache layer (Redis / in-memory) with tag invalidation
- [ ] Background job runner with cron + retry policies
- [ ] Generator for typed gRPC service from OpenAPI spec
- [ ] Templates for Cloud Run / Fly.io / Kubernetes deploy
- [ ] Per-tenant rate limiting plugged into events.Bus

## Non-goals

- A full ORM. We prefer raw SQL via pgx and embrace the type-safety of
  generated query helpers (sqlc) when needed.
- A web UI framework. goforge is server-side only.
- Reflection-driven validation. We use `validator` tags on DTOs.
