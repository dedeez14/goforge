package resilience

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// State is the current disposition of a CircuitBreaker.
type State int

const (
	// StateClosed is the normal operating state: calls pass through
	// and failures are counted.
	StateClosed State = iota
	// StateOpen means the breaker has tripped: calls short-circuit
	// with ErrOpen until the cooldown elapses.
	StateOpen
	// StateHalfOpen means a bounded number of probe calls are
	// allowed through to test whether the downstream has recovered.
	StateHalfOpen
)

// String returns the canonical lowercase name of a state, suitable
// for logs and Prometheus labels.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// ErrOpen is returned by (CircuitBreaker).Execute when the breaker
// is open and the call was short-circuited. Callers should treat
// this as a temporary unavailability, distinct from a downstream
// error: retrying immediately will not help, and the failure should
// usually be surfaced to the client as 503.
var ErrOpen = errors.New("resilience: circuit breaker is open")

// ErrTooManyProbes is returned when a call arrives while the breaker
// is half-open and the probe budget is already allocated to other
// in-flight calls. Treat the same as ErrOpen from the caller's
// perspective.
var ErrTooManyProbes = errors.New("resilience: too many half-open probes")

// CBConfig configures a CircuitBreaker.
//
// Zero values are filled in with sensible defaults by
// NewCircuitBreaker, so an empty CBConfig produces a usable breaker.
type CBConfig struct {
	// FailureThreshold is the number of consecutive failures that
	// trip the breaker from closed to open. Default: 5.
	FailureThreshold int

	// SuccessThreshold is the number of consecutive successes in
	// half-open that close the breaker. Default: 1.
	SuccessThreshold int

	// HalfOpenMaxProbes caps how many probe calls may be in flight
	// at once while half-open; others get ErrTooManyProbes until the
	// in-flight probes finish. Default: 1.
	HalfOpenMaxProbes int

	// CooldownPeriod is how long the breaker stays open before
	// transitioning to half-open. Default: 30s.
	CooldownPeriod time.Duration

	// IsFailure classifies an error. If nil, every non-nil error is
	// treated as a failure. Return false for errors that should not
	// count against the breaker (e.g. context.Canceled from the
	// caller abandoning the request — see DefaultIsFailure).
	IsFailure func(error) bool

	// OnStateChange is called whenever the breaker transitions. It
	// runs on the goroutine that triggered the change; keep it cheap
	// and non-blocking (metrics increment, structured log line).
	OnStateChange func(name string, from, to State)

	// Clock lets tests substitute a fake clock. Production code
	// leaves this nil (time.Now() is used).
	Clock func() time.Time
}

// DefaultIsFailure is a reasonable default classifier: a non-nil
// error is a failure unless it's context.Canceled (the caller gave
// up, which is not the downstream's fault).
func DefaultIsFailure(err error) bool {
	if err == nil {
		return false
	}
	return !errors.Is(err, context.Canceled)
}

// CircuitBreaker is a thread-safe three-state breaker.
//
// A breaker should be constructed once per distinct downstream and
// reused across calls. Do not copy the zero value — always construct
// via NewCircuitBreaker.
type CircuitBreaker struct {
	name string
	cfg  CBConfig

	mu               sync.Mutex
	state            State
	consecFailures   int
	consecSuccesses  int
	openedAt         time.Time
	halfOpenInFlight int
}

// NewCircuitBreaker returns a breaker starting in the closed state.
// name is used only in logs/metrics and must be non-empty.
func NewCircuitBreaker(name string, cfg CBConfig) *CircuitBreaker {
	if name == "" {
		panic("resilience: circuit breaker name must not be empty")
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 1
	}
	if cfg.HalfOpenMaxProbes <= 0 {
		cfg.HalfOpenMaxProbes = 1
	}
	if cfg.CooldownPeriod <= 0 {
		cfg.CooldownPeriod = 30 * time.Second
	}
	if cfg.IsFailure == nil {
		cfg.IsFailure = DefaultIsFailure
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &CircuitBreaker{name: name, cfg: cfg}
}

// Name returns the identifier passed to NewCircuitBreaker.
func (b *CircuitBreaker) Name() string { return b.name }

// State returns the breaker's current state. It may transition
// open → half-open on read if the cooldown has elapsed.
func (b *CircuitBreaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybePromoteToHalfOpen()
	return b.state
}

// Execute runs fn if the breaker permits, records the outcome, and
// returns fn's result. If the breaker is open (or its probe budget
// is exhausted in half-open), Execute returns immediately with
// ErrOpen / ErrTooManyProbes and does NOT call fn.
//
// Callers should not retry ErrOpen in a tight loop: the whole point
// is to give the downstream time to recover. Retry at a higher
// level (e.g. surface a 503 and let the client back off).
func Execute[T any](b *CircuitBreaker, ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := b.admit(); err != nil {
		return zero, err
	}
	result, err := fn(ctx)
	b.record(err)
	return result, err
}

// Execute is the non-generic convenience entry-point used by tests
// and callers that don't need a typed return value. It mirrors
// Execute[T] but discards the return value.
func (b *CircuitBreaker) Execute(ctx context.Context, fn func(context.Context) error) error {
	_, err := Execute(b, ctx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return err
}

// admit decides whether a call should be allowed through, and
// accounts for half-open probe slots if so.
func (b *CircuitBreaker) admit() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybePromoteToHalfOpen()
	switch b.state {
	case StateClosed:
		return nil
	case StateOpen:
		return ErrOpen
	case StateHalfOpen:
		if b.halfOpenInFlight >= b.cfg.HalfOpenMaxProbes {
			return ErrTooManyProbes
		}
		b.halfOpenInFlight++
		return nil
	default:
		return fmt.Errorf("resilience: breaker %q in unknown state", b.name)
	}
}

// record updates counters after a completed call.
func (b *CircuitBreaker) record(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	failed := b.cfg.IsFailure(err)

	if b.state == StateHalfOpen {
		b.halfOpenInFlight--
		if b.halfOpenInFlight < 0 {
			// Defensive: should never happen, but never let the
			// counter drift negative.
			b.halfOpenInFlight = 0
		}
	}

	if failed {
		b.consecFailures++
		b.consecSuccesses = 0
		switch b.state {
		case StateClosed:
			if b.consecFailures >= b.cfg.FailureThreshold {
				b.transitionLocked(StateOpen)
			}
		case StateHalfOpen:
			// any failure in half-open re-opens immediately
			b.transitionLocked(StateOpen)
		}
		return
	}

	// success
	b.consecSuccesses++
	b.consecFailures = 0
	if b.state == StateHalfOpen && b.consecSuccesses >= b.cfg.SuccessThreshold {
		b.transitionLocked(StateClosed)
	}
}

// maybePromoteToHalfOpen transitions an open breaker to half-open
// if the cooldown has elapsed. Must be called with b.mu held.
func (b *CircuitBreaker) maybePromoteToHalfOpen() {
	if b.state != StateOpen {
		return
	}
	if b.cfg.Clock().Sub(b.openedAt) < b.cfg.CooldownPeriod {
		return
	}
	b.transitionLocked(StateHalfOpen)
}

// transitionLocked changes state and calls the OnStateChange hook.
// Must be called with b.mu held. The hook runs under the lock; keep
// it cheap.
func (b *CircuitBreaker) transitionLocked(to State) {
	if b.state == to {
		return
	}
	from := b.state
	b.state = to
	switch to {
	case StateOpen:
		b.openedAt = b.cfg.Clock()
		b.halfOpenInFlight = 0
	case StateClosed:
		b.consecFailures = 0
		b.consecSuccesses = 0
		b.halfOpenInFlight = 0
	case StateHalfOpen:
		b.consecSuccesses = 0
		b.halfOpenInFlight = 0
	}
	if b.cfg.OnStateChange != nil {
		b.cfg.OnStateChange(b.name, from, to)
	}
}
