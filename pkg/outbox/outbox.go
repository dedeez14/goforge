// Package outbox implements the transactional outbox pattern.
//
// The pattern solves the "dual write" problem: when a use-case both
// writes business data and publishes a domain event, doing those in
// two systems can leave them inconsistent (write succeeds, publish
// fails, or vice versa). The outbox keeps both writes inside the same
// database transaction by appending events to a table; a separate
// dispatcher then drains the table and publishes the events to the
// in-process bus (or an external broker via a Sink) at-least-once.
//
// goforge ships this as a first-class feature because event-driven
// systems are otherwise hard to bootstrap in Go - most apps end up
// hand-rolling a brittle equivalent. Wiring the outbox module pulls in
// the table, the writer helpers and a background dispatcher loop.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/dedeez14/goforge/pkg/events"
)

// Message is the on-disk representation of an outbox row. Producers
// build one inside their transaction; consumers receive the same shape
// after the dispatcher loads it.
type Message struct {
	ID         string
	Topic      string
	TenantID   string
	Payload    json.RawMessage
	OccurredAt time.Time
	Attempts   int
}

// Append writes a message into the outbox table inside the supplied
// transaction. Calling Append() commits nothing on its own - it must
// run inside a `BEGIN ... COMMIT` block where the rest of the use-case
// also writes its data.
func Append(ctx context.Context, tx pgx.Tx, topic string, payload any, opts ...AppendOption) error {
	cfg := appendConfig{occurredAt: time.Now().UTC()}
	for _, opt := range opts {
		opt(&cfg)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox: marshal payload for %q: %w", topic, err)
	}
	if cfg.id == "" {
		cfg.id = uuid.NewString()
	}
	if cfg.tenantID == "" {
		// The tenant ID is propagated through the events package to
		// avoid an import cycle with pkg/tenant. Re-using a private
		// key declared locally here would never match because Go
		// considers unexported types from different packages distinct.
		cfg.tenantID = events.TenantFromContext(ctx)
	}
	const q = `
		INSERT INTO outbox_messages (id, topic, tenant_id, payload, occurred_at)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5)
	`
	_, err = tx.Exec(ctx, q, cfg.id, topic, cfg.tenantID, raw, cfg.occurredAt)
	return err
}

// AppendOption tweaks an Append call. The defaults are good for the
// common case; options exist mostly for tests and replay tooling.
type AppendOption func(*appendConfig)

type appendConfig struct {
	id         string
	tenantID   string
	occurredAt time.Time
}

// WithID forces a specific message ID. Useful in tests; rarely needed
// in production where uuid.NewString is fine.
func WithID(id string) AppendOption { return func(c *appendConfig) { c.id = id } }

// WithTenantID overrides the tenant ID derived from the context. Use
// this for system-emitted events that aren't tied to a request.
func WithTenantID(tid string) AppendOption { return func(c *appendConfig) { c.tenantID = tid } }

// WithOccurredAt overrides the occurred-at timestamp.
func WithOccurredAt(t time.Time) AppendOption { return func(c *appendConfig) { c.occurredAt = t } }

// Sink is the destination for dispatched messages. The default sink
// publishes onto the in-process events.Bus; production deployments
// typically swap in a Kafka/NATS/Redis Streams sink.
type Sink interface {
	Publish(ctx context.Context, msg Message) error
}

// BusSink republishes messages onto an events.Bus.
type BusSink struct{ Bus *events.Bus }

// Publish implements Sink.
func (b BusSink) Publish(ctx context.Context, msg Message) error {
	if b.Bus == nil {
		return errors.New("outbox: bus is nil")
	}
	b.Bus.PublishEvent(ctx, events.Event{
		ID:         msg.ID,
		Topic:      msg.Topic,
		OccurredAt: msg.OccurredAt,
		TenantID:   msg.TenantID,
		Payload:    msg.Payload,
	})
	return nil
}

// Dispatcher drains outbox_messages on a loop and forwards them to the
// configured Sink. It claims rows with `FOR UPDATE SKIP LOCKED` so
// multiple replicas can run dispatchers in parallel without conflict.
type Dispatcher struct {
	Pool        *pgxpool.Pool
	Sink        Sink
	Logger      zerolog.Logger
	BatchSize   int
	Interval    time.Duration
	MaxAttempts int
}

// Run blocks until ctx is cancelled, draining the outbox in batches.
// Errors during a single message are logged but do not stop the loop;
// rows that exceed MaxAttempts are left in the table for human
// triage so payloads are never silently dropped.
func (d *Dispatcher) Run(ctx context.Context) error {
	if d.BatchSize <= 0 {
		d.BatchSize = 100
	}
	if d.Interval <= 0 {
		d.Interval = time.Second
	}
	if d.MaxAttempts <= 0 {
		d.MaxAttempts = 12
	}
	t := time.NewTicker(d.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if _, err := d.tick(ctx); err != nil {
				d.Logger.Warn().Err(err).Msg("outbox dispatch tick failed")
			}
		}
	}
}

// tick processes one batch and returns the number of messages
// dispatched. Exposed for tests.
func (d *Dispatcher) tick(ctx context.Context) (int, error) {
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const claim = `
		SELECT id, topic, COALESCE(tenant_id, ''), payload, occurred_at, attempts
		FROM outbox_messages
		WHERE published_at IS NULL AND attempts < $2
		ORDER BY occurred_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`
	rows, err := tx.Query(ctx, claim, d.BatchSize, d.MaxAttempts)
	if err != nil {
		return 0, err
	}
	var batch []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Topic, &m.TenantID, &m.Payload, &m.OccurredAt, &m.Attempts); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(batch) == 0 {
		_ = tx.Commit(ctx)
		return 0, nil
	}

	var (
		published []string
		failed    []string
	)
	tracer := otel.Tracer("goforge.outbox")
	for _, m := range batch {
		// Each dispatch gets its own span so an external collector
		// can connect "event published at T+1s" to "tx that wrote
		// it at T". We make it a producer span because, as far as
		// downstream subscribers are concerned, this is where the
		// event enters the wire.
		mctx, span := tracer.Start(ctx, "outbox.dispatch",
			trace.WithSpanKind(trace.SpanKindProducer),
			trace.WithAttributes(
				attribute.String("messaging.outbox.id", m.ID),
				attribute.String("messaging.destination.name", m.Topic),
				attribute.String("messaging.outbox.tenant_id", m.TenantID),
				attribute.Int("messaging.outbox.attempts", m.Attempts),
			),
		)
		if err := d.Sink.Publish(mctx, m); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			span.End()
			d.Logger.Warn().
				Err(err).
				Str("outbox_id", m.ID).
				Str("topic", m.Topic).
				Int("attempts", m.Attempts+1).
				Msg("outbox publish failed; will retry")
			failed = append(failed, m.ID)
			continue
		}
		span.End()
		published = append(published, m.ID)
	}

	if len(published) > 0 {
		_, err = tx.Exec(ctx, `UPDATE outbox_messages SET published_at = now(), attempts = attempts + 1 WHERE id = ANY($1)`, published)
		if err != nil {
			return 0, err
		}
	}
	if len(failed) > 0 {
		_, err = tx.Exec(ctx, `UPDATE outbox_messages SET attempts = attempts + 1 WHERE id = ANY($1)`, failed)
		if err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(published), nil
}
