package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MemoryQueue is an in-process Queue for tests and single-node
// deployments. It is not durable across restarts and not safe to
// share across processes — there is no broker. Its delivery
// semantics match the Queue contract: at-least-once (with explicit
// Nak/Term redelivery), subject wildcards (`*` matches one token,
// `>` matches tail), best-effort ordering per subscription.
//
// Production systems should use JetStreamQueue. MemoryQueue exists
// so application-level tests can exercise publish / subscribe
// pipelines without running a NATS server in CI.
type MemoryQueue struct {
	mu            sync.Mutex
	subscriptions []*memSubscription
	closed        bool
}

// NewMemoryQueue constructs an empty MemoryQueue.
func NewMemoryQueue() *MemoryQueue { return &MemoryQueue{} }

type memSubscription struct {
	sub       Subscription
	handler   Handler
	queue     chan *memoryMsg
	done      chan struct{}
	closeOnce sync.Once
}

type memoryMsg struct {
	subject    string
	data       []byte
	headers    map[string][]string
	deliveries atomic.Uint64
	sequence   uint64
	timestamp  time.Time

	// ackState controls redelivery: 0=pending, 1=acked, 2=nak, 3=term.
	ackState atomic.Int32
}

func (m *memoryMsg) Subject() string              { return m.subject }
func (m *memoryMsg) Data() []byte                 { return m.data }
func (m *memoryMsg) Headers() map[string][]string { return m.headers }
func (m *memoryMsg) Metadata() MessageMetadata {
	return MessageMetadata{
		Deliveries: m.deliveries.Load(),
		Sequence:   m.sequence,
		Timestamp:  m.timestamp,
	}
}

// Publish delivers to every matching subscription. If none matches
// the publish succeeds silently (matches NATS semantics: publishing
// to a subject nobody listens on is not an error).
func (q *MemoryQueue) Publish(ctx context.Context, subject string, payload []byte, opts ...PublishOption) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg := PublishOptions{}
	for _, o := range opts {
		o(&cfg)
	}

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return errors.New("queue: MemoryQueue is closed")
	}
	// Copy payload so callers can reuse the slice after Publish
	// returns (matches the JetStream backend's behaviour).
	buf := make([]byte, len(payload))
	copy(buf, payload)

	targets := make([]*memSubscription, 0, len(q.subscriptions))
	for _, s := range q.subscriptions {
		if subjectMatches(s.sub.Subject, subject) {
			targets = append(targets, s)
		}
	}
	seq := uint64(time.Now().UnixNano())
	q.mu.Unlock()

	for _, s := range targets {
		msg := &memoryMsg{
			subject:   subject,
			data:      buf,
			headers:   copyHeaders(cfg.Headers),
			sequence:  seq,
			timestamp: time.Now(),
		}
		select {
		case s.queue <- msg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Subscribe registers a handler. The handler runs in its own
// goroutine pool sized by sub.MaxConcurrent (min 1). Returning
// ErrTerminal or nil from the handler completes the delivery;
// any other error is treated as a Nak and the message is
// redelivered after max(AckWait, 10ms).
func (q *MemoryQueue) Subscribe(ctx context.Context, sub Subscription, handler Handler) (Unsubscribe, error) {
	if handler == nil {
		return nil, errors.New("queue: nil handler")
	}
	if sub.Subject == "" {
		return nil, errors.New("queue: Subscription.Subject is required")
	}
	if sub.MaxConcurrent < 1 {
		sub.MaxConcurrent = 1
	}
	if sub.AckWait <= 0 {
		sub.AckWait = 30 * time.Second
	}

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil, errors.New("queue: MemoryQueue is closed")
	}
	s := &memSubscription{
		sub:     sub,
		handler: handler,
		queue:   make(chan *memoryMsg, 1024),
		done:    make(chan struct{}),
	}
	q.subscriptions = append(q.subscriptions, s)
	q.mu.Unlock()

	for i := 0; i < sub.MaxConcurrent; i++ {
		go q.runWorker(ctx, s)
	}

	return func() error {
		q.removeSubscription(s)
		return nil
	}, nil
}

func (q *MemoryQueue) runWorker(ctx context.Context, s *memSubscription) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case msg, ok := <-s.queue:
			if !ok {
				return
			}
			q.deliver(ctx, s, msg)
		}
	}
}

func (q *MemoryQueue) deliver(ctx context.Context, s *memSubscription, msg *memoryMsg) {
	msg.deliveries.Add(1)
	err := s.handler(ctx, msg)

	switch {
	case err == nil:
		msg.ackState.Store(1)
	case errors.Is(err, ErrTerminal):
		msg.ackState.Store(3)
	default:
		msg.ackState.Store(2)
		// Respect MaxDeliver; 0 means unlimited.
		if s.sub.MaxDeliver > 0 && int(msg.deliveries.Load()) >= s.sub.MaxDeliver {
			msg.ackState.Store(3)
			return
		}
		// Schedule a redelivery. We bound the delay so tests do not
		// wait a full 30s; AckWait is the ceiling.
		delay := s.sub.AckWait
		if delay > 100*time.Millisecond {
			delay = 100 * time.Millisecond
		}
		go func() {
			t := time.NewTimer(delay)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return
			case <-s.done:
				return
			case <-t.C:
			}
			select {
			case s.queue <- msg:
			case <-ctx.Done():
			case <-s.done:
			}
		}()
	}
}

func (q *MemoryQueue) removeSubscription(target *memSubscription) {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.subscriptions[:0]
	for _, s := range q.subscriptions {
		if s != target {
			out = append(out, s)
			continue
		}
		target.closeOnce.Do(func() { close(target.done) })
	}
	q.subscriptions = out
}

// Close stops every subscription. Subsequent Publish/Subscribe calls
// return an error.
func (q *MemoryQueue) Close(_ context.Context) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	q.closed = true
	for _, s := range q.subscriptions {
		s.closeOnce.Do(func() { close(s.done) })
	}
	q.subscriptions = nil
	return nil
}

// subjectMatches implements NATS-style subject filtering.
// `*` matches exactly one token; `>` matches one-or-more tokens.
// Empty filter (`""`) is rejected earlier, so this never sees one.
func subjectMatches(filter, subject string) bool {
	f := strings.Split(filter, ".")
	s := strings.Split(subject, ".")
	for i, token := range f {
		if token == ">" {
			return i < len(s)
		}
		if i >= len(s) {
			return false
		}
		if token != "*" && token != s[i] {
			return false
		}
	}
	return len(f) == len(s)
}

func copyHeaders(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		cp := make([]string, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// String is a convenience for tests / diagnostics.
func (q *MemoryQueue) String() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return fmt.Sprintf("MemoryQueue{subs=%d closed=%v}", len(q.subscriptions), q.closed)
}
