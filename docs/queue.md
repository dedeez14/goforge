# Message queue (`pkg/queue`)

`pkg/queue` is goforge's external pub/sub abstraction, complementing
`pkg/outbox`: where the outbox guarantees atomic persist-and-publish
inside a database transaction, the queue is the fan-out pipe that
delivers events to independent consumers at high throughput.

The default backend is **NATS JetStream** — single-binary, embeds
cleanly in a k8s cluster, and supports durable consumers,
at-least-once delivery, per-subject ordering, and server-side
retry/DLQ semantics without an external orchestrator.

## When to use `pkg/queue` vs `pkg/jobs` vs `pkg/outbox`

| Use case | Pick |
|---|---|
| "Send this email when the user registers" — one handler, retried with backoff | `pkg/jobs` |
| "Atomically save the order and ensure the event will eventually publish" | `pkg/outbox` |
| "Fan out 50k events/sec to billing, analytics, and search indexer" | `pkg/queue` |
| "Outbox → queue → many consumers" (canonical) | `pkg/outbox` feeds `pkg/queue` |

The three layers compose: a transactional handler writes a row to
the outbox, the outbox dispatcher reads it and calls
`queue.Publish`, JetStream fans the message out to every durable
consumer that has registered interest.

## Delivery contract

Every backend guarantees **at-least-once** delivery. Consumers MUST
be idempotent. A `Handler` returning:

- `nil` — message is acknowledged (removed).
- `queue.ErrTerminal` (or any error wrapping it) — message is
  terminated (never retried). Used for malformed payloads that will
  deterministically fail forever; route the offending payload to
  durable storage before returning this.
- any other error — message is negative-acked and redelivered after
  the backend's AckWait (default 30s for JetStream).

JetStream additionally enforces `Subscription.MaxDeliver`: after the
configured number of attempts the broker terminates the message.
Set `MaxDeliver=0` for "retry forever".

## Wiring

```go
// Connect at boot. Hard-fail if NATS is unreachable.
q, err := queue.NewJetStreamQueue(queue.Config{
    URL:            cfg.NATS.URL,           // nats://nats:4222
    Name:           "goforge-api",
    Logger:         queueLogger{logger},    // implements queue.Logger
    ConnectTimeout: 5 * time.Second,
})
if err != nil {
    return fmt.Errorf("queue: %w", err)
}
defer q.Close(ctx)

// Declare streams once per process. Idempotent.
if err := q.DeclareStream(ctx, jetstream.StreamConfig{
    Name:      "EVENTS",
    Subjects:  []string{"events.>"},
    Retention: jetstream.LimitsPolicy,
    Storage:   jetstream.FileStorage,
    MaxAge:    72 * time.Hour,
}); err != nil {
    return err
}

// Publish (sync, blocks until broker durably acks).
if err := q.Publish(ctx, "events.orders.created", payload,
    queue.WithMessageID(orderID),
    queue.WithHeader("X-Tenant", tenantID),
); err != nil {
    return err
}

// Subscribe with a durable consumer. Reconnects resume from the
// last ack'd position; redeploys do not lose unprocessed messages.
unsub, err := q.Subscribe(ctx, queue.Subscription{
    Stream:        "EVENTS",
    Subject:       "events.orders.>",
    Consumer:      "billing-worker",
    MaxDeliver:    5,
    AckWait:       30 * time.Second,
    MaxConcurrent: 16,
}, func(ctx context.Context, m queue.Message) error {
    return billingService.Handle(ctx, m.Data())
})
if err != nil {
    return err
}
defer unsub()
```

## Outbox → queue bridge

The canonical pattern: transactional writes hit the outbox; a
single dispatcher drains the outbox and publishes onto the queue;
consumers attach to JetStream subjects.

```go
dispatcher := outbox.NewDispatcher(outboxRepo, func(ctx context.Context, ev outbox.Event) error {
    return q.Publish(ctx, ev.Subject, ev.Payload, queue.WithMessageID(ev.ID))
})
```

`WithMessageID(ev.ID)` lets JetStream dedupe duplicate publishes
within its dedupe window (default 2 minutes), so an outbox retry
after a partial network failure does not result in two consumer
deliveries from a single business event.

## Local testing

`pkg/queue` also ships an in-process `MemoryQueue` that satisfies
the same `Queue` interface — application-level tests can exercise
the pub/sub pipeline without running a NATS server in CI.

```go
q := queue.NewMemoryQueue()
defer q.Close(ctx)
```

It supports the same subject wildcards (`*` for one token, `>` for
tail), the same Ack/Nak/Term semantics, and the same backoff on
handler errors. **Do not** use it in production — there is no
broker, no durability, and no inter-process delivery.

## Operational notes

- **Streams are configuration, not application code.** Declare them
  centrally at boot. JetStream rejects publishes against unknown
  streams.
- **Durable consumer names are forever.** Renaming
  `Subscription.Consumer` resets the consumer's ack position to the
  stream's start — old durable consumer state lingers until you
  delete it via `nats consumer rm`.
- **`MaxAckPending` bounds the in-flight window.** Set it to the
  largest number of concurrent handlers you want to allow.
- **Dedupe window is per stream.** Configure
  `StreamConfig.Duplicates` to a value larger than your worst-case
  retry budget.
- **Reconnection is automatic** with exponential backoff capped at
  `ReconnectWait`. The `Logger` you pass to `Config` receives
  disconnect/reconnect events.
- **Cluster deployment**: NATS clusters with 3+ nodes give you
  replicated streams (`StreamConfig.Replicas: 3`). Single-node is
  fine for dev, but production should run a cluster on dedicated
  pods with PVCs.
