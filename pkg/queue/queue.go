// Package queue is goforge's external message-queue abstraction,
// complementing pkg/outbox: where the outbox guarantees atomic
// persist-and-publish inside a database transaction, the queue is
// the fan-out pipe that delivers events to independent consumers
// at high throughput.
//
// The default backend is NATS JetStream — it is single-binary,
// embeds cleanly in a k8s cluster, and supports durable consumers,
// at-least-once delivery, per-subject ordering, and server-side
// retry/DLQ semantics without an external orchestrator.
//
// # Delivery contract
//
// Every backend guarantees *at-least-once* delivery. Consumers MUST
// be idempotent. A Handler returning:
//
//   - nil:                 message is acknowledged (removed).
//   - ErrTerminal (or wrapped):  message is terminated (never retried),
//     typical for malformed payloads that will
//     fail forever; caller is expected to have
//     routed the payload somewhere durable first.
//   - any other error:     message is negative-acked and will be
//     redelivered after the backend's AckWait.
//
// # Why not reuse pkg/jobs?
//
// pkg/jobs is a Postgres-backed work queue tuned for "one handler
// per job, retry with exponential backoff, DLQ". The queue package
// is the fan-out pub/sub primitive: one message, many independent
// consumers, subject-based routing, and a transport (NATS/Kafka)
// tuned for high-throughput realtime delivery. The two are
// complements, not competitors — outbox → queue → N consumers
// (pkg/jobs included) is the canonical pattern for this framework.
package queue

import (
	"context"
	"errors"
	"time"
)

// ErrTerminal is returned (or wrapped) by a Handler to signal the
// backend that a message cannot be handled and MUST NOT be retried.
// Typical use: malformed payload that will deterministically fail
// forever. Callers should log / route the offending payload to a
// durable inspection area before returning this — terminating a
// message is data loss from the consumer's point of view.
var ErrTerminal = errors.New("queue: terminal")

// Queue is the bidirectional contract every backend satisfies. It
// is safe for concurrent use by many goroutines once constructed.
type Queue interface {
	// Publish delivers payload under subject. subject follows NATS
	// dot notation (e.g. "orders.created", "analytics.*.click").
	// The publish is synchronous with respect to the broker's
	// durability ack; returning nil means the broker has persisted
	// the message.
	Publish(ctx context.Context, subject string, payload []byte, opts ...PublishOption) error

	// Subscribe attaches a durable consumer to a subject filter.
	// The handler is invoked once per delivery; concurrent
	// deliveries to the same handler are bounded by
	// Subscription.MaxConcurrent. The returned Unsubscribe stops
	// the consumer and releases its resources; Close() on the
	// queue implicitly unsubscribes every active subscription.
	Subscribe(ctx context.Context, sub Subscription, handler Handler) (Unsubscribe, error)

	// Close releases all backend resources (connections, goroutines,
	// in-flight subscriptions). Subsequent Publish/Subscribe calls
	// return an error. Safe to call multiple times.
	Close(ctx context.Context) error
}

// Handler is the consumer callback. Return semantics are documented
// on the package comment.
type Handler func(ctx context.Context, msg Message) error

// Message is a single delivery. It is backend-agnostic — concrete
// implementations satisfy the interface.
type Message interface {
	// Subject returns the subject the message was published under.
	Subject() string

	// Data returns the payload. The returned slice is owned by the
	// message and MUST NOT be retained after the handler returns.
	// Consumers that need to keep the payload should copy it.
	Data() []byte

	// Headers returns a best-effort header map. Backends without
	// header support (e.g. legacy NATS core) return an empty map.
	Headers() map[string][]string

	// Metadata carries delivery-level information useful for
	// retry/idempotency decisions.
	Metadata() MessageMetadata
}

// MessageMetadata carries per-delivery telemetry.
type MessageMetadata struct {
	// Deliveries is the 1-based count of times this message has
	// been delivered; 1 means first attempt.
	Deliveries uint64

	// Sequence is the broker-assigned monotonic sequence number
	// within the stream (where "stream" is the backend's concept
	// of an ordered subject group). 0 when the backend does not
	// expose a sequence.
	Sequence uint64

	// Timestamp is when the broker first received the message.
	// Zero when unknown.
	Timestamp time.Time
}

// Subscription describes a durable consumer. Durable means the
// backend remembers the consumer's position across restarts — a
// subscriber that crashes and reconnects picks up where it left
// off, without redelivering already-acked messages.
type Subscription struct {
	// Stream is the backend's stream / topic. In NATS JetStream
	// this is the stream name (e.g. "EVENTS"); streams are
	// declared separately via DeclareStream.
	Stream string

	// Subject filter selects which subjects on the Stream flow to
	// this consumer. Supports NATS wildcards ("events.*.created",
	// "events.>") on backends that implement them.
	Subject string

	// Consumer is the durable name. Reusing the same Consumer
	// across restarts resumes delivery; using a new name starts a
	// fresh consumer from the stream's replay policy.
	Consumer string

	// MaxDeliver is the maximum delivery attempts before the
	// backend terminates the message. 0 means "unlimited" —
	// messages are redelivered until acked or Term()ed.
	MaxDeliver int

	// AckWait is how long the backend waits for an ack before
	// redelivering. 0 defaults to the backend's sensible default
	// (30s for NATS JetStream).
	AckWait time.Duration

	// MaxConcurrent bounds in-flight messages delivered to this
	// subscription. 0 means 1 (serial delivery); higher values
	// enable parallel handler goroutines.
	MaxConcurrent int
}

// Unsubscribe stops a subscription and releases its resources.
// Calling it more than once is a no-op.
type Unsubscribe func() error

// PublishOption configures a single Publish call. Concrete backends
// may ignore options they do not implement.
type PublishOption func(*PublishOptions)

// PublishOptions is the realised bundle of PublishOption values.
// Exposed so backends can read it; applications use the helpers.
type PublishOptions struct {
	// Headers are best-effort transport headers. NATS JetStream
	// propagates them; legacy transports drop them.
	Headers map[string][]string

	// MessageID, when non-empty, asks the backend to dedupe
	// deliveries with the same ID within its dedupe window.
	// NATS JetStream implements this via the Nats-Msg-Id header.
	MessageID string
}

// WithHeader sets a transport header on the published message. It
// is additive: multiple values for the same key are preserved.
func WithHeader(key, value string) PublishOption {
	return func(o *PublishOptions) {
		if o.Headers == nil {
			o.Headers = make(map[string][]string)
		}
		o.Headers[key] = append(o.Headers[key], value)
	}
}

// WithMessageID tags the publish with a dedupe key. Two publishes
// with the same ID within the backend's dedupe window deliver only
// once.
func WithMessageID(id string) PublishOption {
	return func(o *PublishOptions) { o.MessageID = id }
}
