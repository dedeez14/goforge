// Package resilience provides small, composable primitives for calling
// unreliable downstream services: circuit breakers, retries with
// backoff+jitter, and request hedging.
//
// The package deliberately has no dependency on any HTTP client,
// queue, or logger: every call takes a plain `func(ctx) (T, error)`
// so it can wrap HTTP requests, database queries, webhook deliveries,
// IdP token exchanges, SMTP sends — anything that returns an error
// and might be slow or flaky.
//
// # Why this package exists
//
// A goforge app typically fans out to several third parties:
// webhooks, SMTP/SES, OAuth IdPs, object storage, external APIs. A
// single slow provider can hold a goroutine (and a DB connection, if
// the caller was mid-transaction) for as long as the client timeout
// allows — and once enough goroutines pile up, the whole process
// falls over. The primitives here bound that blast radius:
//
//   - CircuitBreaker trips after repeated failures and sheds load
//     with an immediate error instead of waiting on a dead peer.
//   - Retry re-runs idempotent calls with exponential backoff and
//     full jitter so retries don't synchronise into a thundering herd.
//   - Hedge starts a second attempt after a delay and returns
//     whichever responds first, trading a small amount of extra
//     work for a big tail-latency reduction.
//
// # Composition
//
// These primitives compose by wrapping func values, so callers can
// stack them in whatever order makes sense:
//
//	cb := resilience.NewCircuitBreaker("mailer", resilience.CBConfig{...})
//	_, err := cb.Execute(ctx, func(ctx context.Context) (struct{}, error) {
//	    return resilience.Retry(ctx, resilience.RetryConfig{MaxAttempts: 3}, send)
//	})
//
// Circuit breaker on the outside sheds load during an outage; retry
// on the inside smooths over transient blips.
//
// # No global state
//
// Every primitive is constructed per callsite. A circuit breaker for
// "mailer" should be a distinct value from one for "webhooks" —
// otherwise one slow dependency can trip the other's breaker.
package resilience
