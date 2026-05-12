package queue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// JetStreamQueue is the production Queue backed by a NATS JetStream
// cluster. It is safe for concurrent use; one instance per process
// is the norm.
//
// JetStream provides:
//   - At-least-once delivery via explicit ack/nak/term.
//   - Per-stream replication and on-disk durability.
//   - Server-side dedupe (Nats-Msg-Id header) within the stream's
//     dedupe window — usually 2 minutes.
//   - Durable consumers whose ack position persists across restarts.
//
// Streams are declared explicitly with DeclareStream because their
// retention policy is an operational decision, not a per-subscriber
// one: multiple subscribers attach to the same stream and share
// replay history. Treat DeclareStream like a migration — call it
// at boot, on every node, and let JetStream's idempotent
// CreateOrUpdateStream sort it out.
type JetStreamQueue struct {
	nc   *nats.Conn
	js   jetstream.JetStream
	logr Logger

	mu     sync.Mutex
	subs   []func() // closer per active subscription
	closed bool
}

// Logger is the minimal logging contract JetStreamQueue uses for
// async events (delivery callbacks, consumer errors) that have no
// natural caller frame. Pass NopLogger to silence them.
type Logger interface {
	Errorf(format string, args ...any)
	Warnf(format string, args ...any)
}

// NopLogger discards every message.
type NopLogger struct{}

func (NopLogger) Errorf(string, ...any) {}
func (NopLogger) Warnf(string, ...any)  {}

// Config bundles the connection options for NewJetStreamQueue.
type Config struct {
	// URL is the NATS server URL, e.g. "nats://nats:4222". A
	// comma-separated list selects from a cluster on connect.
	URL string

	// Name is an identifier for this client connection. Surfaces
	// in `nats server report connz` and helps operators correlate
	// traffic to processes.
	Name string

	// Credentials is an optional path to a NATS .creds file for
	// NGS / NATS auth callout. Empty disables auth.
	Credentials string

	// ConnectTimeout caps how long Dial waits for the first
	// connection. Defaults to 5 seconds.
	ConnectTimeout time.Duration

	// MaxReconnects caps reconnect attempts before connection is
	// abandoned. Negative = unlimited (NATS default).
	MaxReconnects int

	// ReconnectWait sets the backoff between reconnect attempts.
	// Defaults to 2 seconds.
	ReconnectWait time.Duration

	// Logger receives async events. nil falls back to NopLogger.
	Logger Logger
}

// NewJetStreamQueue dials NATS and returns a ready-to-use queue.
// Returns a non-nil error if the connection cannot be established
// within ConnectTimeout — operators should treat that as a hard
// failure and refuse to start the process.
func NewJetStreamQueue(cfg Config) (*JetStreamQueue, error) {
	if cfg.URL == "" {
		return nil, errors.New("queue: Config.URL is required")
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 5 * time.Second
	}
	if cfg.ReconnectWait <= 0 {
		cfg.ReconnectWait = 2 * time.Second
	}
	logr := cfg.Logger
	if logr == nil {
		logr = NopLogger{}
	}

	opts := []nats.Option{
		nats.Timeout(cfg.ConnectTimeout),
		nats.ReconnectWait(cfg.ReconnectWait),
		nats.MaxReconnects(cfg.MaxReconnects),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				logr.Warnf("queue: disconnected: %v", err)
			}
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			logr.Warnf("queue: reconnected to %s", c.ConnectedUrl())
		}),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			if err != nil {
				logr.Errorf("queue: async error: %v", err)
			}
		}),
	}
	if cfg.Name != "" {
		opts = append(opts, nats.Name(cfg.Name))
	}
	if cfg.Credentials != "" {
		opts = append(opts, nats.UserCredentials(cfg.Credentials))
	}

	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("queue: nats connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("queue: jetstream init: %w", err)
	}
	return &JetStreamQueue{nc: nc, js: js, logr: logr}, nil
}

// DeclareStream creates or updates a JetStream stream. Call this at
// boot for every stream the application consumes from or publishes
// to — JetStream rejects publishes/subscribes against unknown
// streams. The call is idempotent.
func (q *JetStreamQueue) DeclareStream(ctx context.Context, cfg jetstream.StreamConfig) error {
	if _, err := q.js.CreateOrUpdateStream(ctx, cfg); err != nil {
		return fmt.Errorf("queue: declare stream %q: %w", cfg.Name, err)
	}
	return nil
}

// Publish sends a message to JetStream. The call blocks until the
// broker has durably ack'd the message; returning nil means the
// message is on disk on the configured replication set.
func (q *JetStreamQueue) Publish(ctx context.Context, subject string, payload []byte, opts ...PublishOption) error {
	cfg := PublishOptions{}
	for _, o := range opts {
		o(&cfg)
	}
	var jsOpts []jetstream.PublishOpt
	if cfg.MessageID != "" {
		jsOpts = append(jsOpts, jetstream.WithMsgID(cfg.MessageID))
	}
	if len(cfg.Headers) == 0 {
		if _, err := q.js.Publish(ctx, subject, payload, jsOpts...); err != nil {
			return fmt.Errorf("queue: publish %q: %w", subject, err)
		}
		return nil
	}
	msg := &nats.Msg{Subject: subject, Data: payload, Header: nats.Header{}}
	for k, vs := range cfg.Headers {
		for _, v := range vs {
			msg.Header.Add(k, v)
		}
	}
	if cfg.MessageID != "" {
		msg.Header.Set(jetstream.MsgIDHeader, cfg.MessageID)
	}
	if _, err := q.js.PublishMsg(ctx, msg, jsOpts...); err != nil {
		return fmt.Errorf("queue: publish %q: %w", subject, err)
	}
	return nil
}

// Subscribe attaches a durable consumer to a stream. The Subscription
// fields map onto jetstream.ConsumerConfig:
//
//	Stream         -> stream name (must exist; declare with DeclareStream)
//	Subject        -> ConsumerConfig.FilterSubject
//	Consumer       -> ConsumerConfig.Durable + Name
//	MaxDeliver     -> ConsumerConfig.MaxDeliver
//	AckWait        -> ConsumerConfig.AckWait
//	MaxConcurrent  -> ConsumerConfig.MaxAckPending
//
// AckPolicy is forced to AckExplicit so handler errors translate to
// redelivery; a future option could expose AckAll for high-throughput
// fire-and-forget consumers.
func (q *JetStreamQueue) Subscribe(ctx context.Context, sub Subscription, handler Handler) (Unsubscribe, error) {
	if handler == nil {
		return nil, errors.New("queue: nil handler")
	}
	if sub.Stream == "" {
		return nil, errors.New("queue: Subscription.Stream is required")
	}
	if sub.Subject == "" {
		return nil, errors.New("queue: Subscription.Subject is required")
	}
	if sub.Consumer == "" {
		return nil, errors.New("queue: Subscription.Consumer is required")
	}

	cfg := jetstream.ConsumerConfig{
		Name:          sub.Consumer,
		Durable:       sub.Consumer,
		FilterSubject: sub.Subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    sub.MaxDeliver,
		AckWait:       sub.AckWait,
	}
	if sub.MaxConcurrent > 0 {
		cfg.MaxAckPending = sub.MaxConcurrent
	}

	consumer, err := q.js.CreateOrUpdateConsumer(ctx, sub.Stream, cfg)
	if err != nil {
		return nil, fmt.Errorf("queue: create consumer %s/%s: %w", sub.Stream, sub.Consumer, err)
	}

	cc, err := consumer.Consume(func(m jetstream.Msg) {
		q.dispatch(ctx, m, handler)
	}, jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
		if err != nil {
			q.logr.Errorf("queue: consume %s/%s: %v", sub.Stream, sub.Consumer, err)
		}
	}))
	if err != nil {
		return nil, fmt.Errorf("queue: consume %s/%s: %w", sub.Stream, sub.Consumer, err)
	}

	stop := func() { cc.Stop() }
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		cc.Stop()
		return nil, errors.New("queue: JetStreamQueue is closed")
	}
	q.subs = append(q.subs, stop)
	q.mu.Unlock()

	return func() error {
		stop()
		return nil
	}, nil
}

// dispatch wraps a single JetStream delivery in our Handler contract.
// Ack/Nak/Term errors are logged but not propagated — the broker
// will redeliver on its own AckWait expiry if we fail to ack.
func (q *JetStreamQueue) dispatch(ctx context.Context, m jetstream.Msg, handler Handler) {
	msg := jsMessage{m: m}
	err := handler(ctx, msg)
	switch {
	case err == nil:
		if ackErr := m.Ack(); ackErr != nil {
			q.logr.Errorf("queue: ack: %v", ackErr)
		}
	case errors.Is(err, ErrTerminal):
		if termErr := m.Term(); termErr != nil {
			q.logr.Errorf("queue: term: %v", termErr)
		}
	default:
		if nakErr := m.Nak(); nakErr != nil {
			q.logr.Errorf("queue: nak: %v", nakErr)
		}
	}
}

// Close drains every subscription and tears down the NATS connection.
func (q *JetStreamQueue) Close(_ context.Context) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	q.closed = true
	for _, stop := range q.subs {
		stop()
	}
	q.subs = nil
	q.nc.Close()
	return nil
}

// jsMessage adapts a jetstream.Msg to our Message interface.
type jsMessage struct {
	m jetstream.Msg
}

func (j jsMessage) Subject() string              { return j.m.Subject() }
func (j jsMessage) Data() []byte                 { return j.m.Data() }
func (j jsMessage) Headers() map[string][]string { return map[string][]string(j.m.Headers()) }
func (j jsMessage) Metadata() MessageMetadata {
	md, err := j.m.Metadata()
	if err != nil || md == nil {
		return MessageMetadata{}
	}
	return MessageMetadata{
		Deliveries: md.NumDelivered,
		Sequence:   md.Sequence.Stream,
		Timestamp:  md.Timestamp,
	}
}
