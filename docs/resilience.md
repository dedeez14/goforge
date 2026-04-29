# Resilience (circuit breaker · retry · hedge)

`pkg/resilience` provides three small, composable primitives for
calling flaky downstream services (webhooks, mailer, OAuth IdPs,
object storage, third-party APIs):

- **CircuitBreaker** — trips after repeated failures and sheds load
  with `ErrOpen` until a cooldown has elapsed.
- **Retry** — re-runs idempotent calls with exponential backoff and
  full jitter.
- **Hedge** — runs multiple attempts staggered by a delay and
  returns whichever succeeds first; cancels the losers.

None of them has package-level state. Each call site constructs its
own breaker/config, so a slow mailer cannot trip the webhooks breaker.

## Quick start

### Circuit breaker

```go
cb := resilience.NewCircuitBreaker("mailer", resilience.CBConfig{
    FailureThreshold: 5,
    CooldownPeriod:   30 * time.Second,
})

err := cb.Execute(ctx, func(ctx context.Context) error {
    return smtp.Send(ctx, msg)
})
if errors.Is(err, resilience.ErrOpen) {
    // Breaker is open: surface 503 to the caller and back off.
}
```

`Execute[T]` is generic for typed returns:

```go
out, err := resilience.Execute(cb, ctx,
    func(ctx context.Context) (*http.Response, error) {
        return httpClient.Do(req)
    })
```

### Retry

```go
body, err := resilience.Retry(ctx,
    resilience.RetryConfig{MaxAttempts: 3, BaseDelay: 100 * time.Millisecond},
    func(ctx context.Context) ([]byte, error) {
        return fetchProfile(ctx, userID)
    })
```

By default `DefaultShouldRetry` retries everything except context
cancellation and `ErrOpen` / `ErrTooManyProbes`. Pass your own
`ShouldRetry` to distinguish 4xx (do not retry) from 5xx (retry).

### Hedge

```go
// Fire a secondary if the primary hasn't responded in 50ms. Whichever
// returns first wins; the other's context is cancelled.
rec, err := resilience.Hedge(ctx,
    resilience.HedgeConfig{Count: 2, Delay: 50 * time.Millisecond},
    func(ctx context.Context) (*Record, error) {
        return store.Get(ctx, id)
    })
```

**Only hedge read-only / idempotent calls** — for non-idempotent
writes you will duplicate side-effects roughly `Count` times.

## Composition

Breaker on the outside, retry on the inside is the common pattern:

```go
cb := resilience.NewCircuitBreaker("mailer", resilience.CBConfig{})

err := cb.Execute(ctx, func(ctx context.Context) error {
    return resilience.RetryVoid(ctx,
        resilience.RetryConfig{MaxAttempts: 3},
        func(ctx context.Context) error { return smtp.Send(ctx, m) })
})
```

- The retry smooths over transient blips.
- The breaker kicks in only when retries have been failing repeatedly
  and still not succeeding — i.e. the downstream is actually sick.

## Breaker per downstream

A single breaker shared across heterogeneous downstreams is wrong:
one slow provider would trip traffic for all the others. For a
service with many peers (e.g. webhook subscribers, tenant-specific
OAuth IdPs), use `BreakerGroup` to memoise one breaker per key:

```go
grp := resilience.NewBreakerGroup(func(key string) resilience.CBConfig {
    return resilience.CBConfig{FailureThreshold: 5, CooldownPeriod: time.Minute}
})

dispatcher := &webhooks.Dispatcher{
    ...
    BreakerFor: grp.Get,
}
```

`Dispatcher.Deliver` then routes every delivery for endpoint `E`
through `grp.Get(E)`. A flaky subscriber trips its own breaker and
stops getting delivery attempts without affecting healthy peers.

## Observability

Circuit breakers fire `OnStateChange(name, from, to)` on every
transition. Wire it to your metrics:

```go
cb := resilience.NewCircuitBreaker("webhooks:"+epID, resilience.CBConfig{
    OnStateChange: func(name string, from, to resilience.State) {
        circuitState.WithLabelValues(name, to.String()).Set(1)
        log.Info().Str("breaker", name).Str("from", from.String()).Str("to", to.String()).Msg("breaker state change")
    },
})
```

Retry's `OnRetry(attempt, delay, err)` hook fires before each sleep
— useful for structured logs.

## When NOT to use these

- **Write operations without an idempotency key**: don't retry, don't
  hedge.
- **Very cheap in-process calls**: the breaker/retry overhead is
  tiny, but adds indirection and lock contention. Save it for
  network-bound calls.
- **Calls protected by the queue**: `pkg/jobs` already retries with
  backoff across attempts. Adding in-process retry to a job handler
  double-counts. Use the breaker to shed load instead.

## Defaults

| Setting | Default | Notes |
|---|---|---|
| `CBConfig.FailureThreshold` | 5 | Consecutive failures to trip. |
| `CBConfig.SuccessThreshold` | 1 | Successes in half-open to close. |
| `CBConfig.HalfOpenMaxProbes` | 1 | Concurrent probes while half-open. |
| `CBConfig.CooldownPeriod` | 30s | Open → half-open delay. |
| `CBConfig.IsFailure` | `DefaultIsFailure` | Counts non-nil non-`context.Canceled`. |
| `RetryConfig.MaxAttempts` | 3 | Includes the first attempt. |
| `RetryConfig.BaseDelay` | 100ms | |
| `RetryConfig.MaxDelay` | 10s | Cap for exponential backoff. |
| `RetryConfig.Multiplier` | 2.0 | |
| `RetryConfig.JitterFraction` | 1.0 | Full jitter (AWS-recommended). |
| `HedgeConfig.Count` | 2 | |
| `HedgeConfig.Delay` | 50ms | Stagger between attempts. |
