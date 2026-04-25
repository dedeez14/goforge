# SaaS multi-tenant example

Demonstrates how to build a multi-tenant SaaS API on top of goforge:

- Every request carries `X-Tenant-ID`.
- Repositories filter by tenant automatically.
- Domain events inherit the tenant ID and only stream to clients of
  the same tenant.
- Idempotency-Key enforces safe retries on `POST /workspaces`.

## Stack

- `pkg/tenant` for context propagation
- `pkg/idempotency` for replay protection
- `pkg/events` + `pkg/realtime` for live updates
- `pkg/outbox` to keep DB writes and events consistent

## Outline

```
internal/domain/workspace        # Workspace entity + repo iface (no tenant arg in API; comes from ctx)
internal/usecase/workspace       # Create/List with tenant.Require(ctx)
internal/adapter/repository/pg   # Workspaces table with tenant_id FK
internal/adapter/http            # Routes mounted under tenant.Middleware
```

## Endpoints

| Method | Path | Description |
| --- | --- | --- |
| POST | /api/v1/workspaces | Create workspace (idempotent via `Idempotency-Key`) |
| GET  | /api/v1/workspaces | List my workspaces (scoped to my tenant) |
| GET  | /api/v1/stream?topics=workspace.created | Live updates for my tenant |

## Try it

```bash
# Create a workspace twice with the same Idempotency-Key, get the same ID.
curl -X POST http://localhost:8080/api/v1/workspaces \
  -H 'X-Tenant-ID: tenant-A' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: 9e1c…' \
  -d '{"name":"Engineering"}'

# Subscribe to the live stream from another shell
curl -N -H 'X-Tenant-ID: tenant-A' \
     'http://localhost:8080/api/v1/stream?topics=workspace.created'
```

The stream client only sees events from `tenant-A`; spinning up a
second client with `X-Tenant-ID: tenant-B` proves the isolation.

## Status

This directory currently holds only the README. The full sample app
will land in [#TODO]. Track ROADMAP.md for status.
