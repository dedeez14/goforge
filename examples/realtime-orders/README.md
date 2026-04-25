# Realtime orders example

Restaurant-style order board: kitchen staff watch orders flow in via
Server-Sent Events as customers submit them. Demonstrates the full
event pipeline: HTTP write -> outbox -> bus -> SSE.

## Stack

- `pkg/outbox` - orders are inserted in the same transaction that
  appends `order.placed` to the outbox.
- `pkg/events` - outbox dispatcher fans out into the bus.
- `pkg/realtime` - SSE handler streams events to connected dashboards.
- `pkg/idempotency` - prevents duplicate orders on flaky networks.

## Endpoints

| Method | Path | Description |
| --- | --- | --- |
| POST | /api/v1/orders | Place an order (idempotent) |
| GET  | /api/v1/orders/:id | Fetch an order |
| GET  | /api/v1/stream?topics=order.placed,order.shipped | Kitchen dashboard |

## Why "realtime via the outbox"?

An obvious alternative is publishing to the bus directly from the
handler. That works until the DB transaction rolls back: the event is
out, the order isn't. The outbox keeps both writes atomic and the
dispatcher catches up at-least-once - the dashboard is always
eventually consistent with the database.

## Try it

```bash
# place an order
curl -X POST http://localhost:8080/api/v1/orders \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: ord-001' \
  -d '{"items":[{"sku":"latte","qty":1}]}'

# in another terminal, watch kitchen events
curl -N 'http://localhost:8080/api/v1/stream?topics=order.placed'
```

## Status

This directory currently holds only the README. The full sample app
will land in [#TODO]. Track ROADMAP.md for status.
