# Graceful Shutdown & Drain

goforge terminates in three phases so rolling deploys on Kubernetes (or
any load-balanced topology) never drop in-flight requests.

## Timeline

```
SIGTERM ──▶ Phase 1: drain        ──▶ Phase 2: close HTTP ──▶ Phase 3: workers
            /readyz → 503              app.Shutdown(ctx)       ctx cancel + wg.Wait
            keep serving
            for DrainGracePeriod
            (default 5s)
```

| Phase | Duration | What changes | What keeps working |
|-------|----------|--------------|--------------------|
| 1 — drain | `http.drain_grace_period` (default `5s`) | `/readyz` returns `503 {"status":"draining"}`. Kubernetes removes the pod from Service endpoints. | HTTP listener still accepts requests. Liveness stays 200. Requests that land still return 2xx. |
| 2 — close | up to `http.shutdown_timeout` (default `15s`) | Fiber's `ShutdownWithContext` refuses new connections and waits for in-flight handlers. | In-flight handlers run to completion or hit the shutdown-context deadline. |
| 3 — workers | up to `http.shutdown_timeout` (shared budget) | Worker context cancelled; each worker observes `ctx.Done()` and exits. | Nothing new scheduled; in-flight jobs commit or bail out. |

A second SIGTERM while in phase 1 skips the remaining grace window -
useful for CI and for operators who want to cut a hang short.

## Configuration

| Env var | Default | Meaning |
|---------|---------|---------|
| `GOFORGE_HTTP_DRAIN_GRACE_PERIOD` | `5s` | Time `/readyz` reports 503 before the HTTP listener closes. Set `0` to disable drain (same as pre-drain behaviour). |
| `GOFORGE_HTTP_SHUTDOWN_TIMEOUT` | `15s` | Hard upper bound on how long phases 2 + 3 may take. |

### Kubernetes

Make sure `DrainGracePeriod` is **strictly greater** than your
readiness probe's `periodSeconds + iptables propagation`:

```yaml
readinessProbe:
  httpGet: { path: /readyz, port: 8080 }
  periodSeconds: 2          # ← drain must exceed this
  failureThreshold: 1
livenessProbe:
  httpGet: { path: /healthz, port: 8080 }
  periodSeconds: 10

terminationGracePeriodSeconds: 30  # ≥ drainGrace + shutdownTimeout
```

## How `/readyz` works

- **Serving** state → `200 {"status":"ready","has_replica":bool}`
- **Draining** state → `503 {"status":"draining","reason":"shutdown in progress"}`
- **Dependency down** → `503 {"status":"degraded","reason":"database unavailable"}`

Liveness (`/healthz`) is deliberately dumb: it returns 200 as long as
the process is up. Tying liveness to dependencies risks cascading
restarts when Postgres hiccups.

## Observability

Each phase logs a single structured line (`level=info`):

```
{"msg":"shutdown signal received","signal":"terminated"}
{"msg":"draining; readiness reporting 503","grace":"5s"}
{"msg":"shutdown complete"}
```

OpenTelemetry traces are flushed by the deferred `tracingShutdown` at
the top of `Run` - this runs *after* phase 3 because of `defer` LIFO
ordering, so the last request's span is in the exporter before the
process exits.
