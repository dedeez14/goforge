// Package events implements the in-process domain event bus that powers
// goforge's modular architecture. It is intentionally small: a topic is
// a string, a payload is opaque bytes, and subscribers run in their
// own goroutines so a slow consumer cannot block the publisher.
//
// The bus is the integration seam for cross-module communication. The
// outbox dispatcher feeds it from durable storage; the realtime module
// drains it onto SSE/WebSocket connections. Modules never call each
// other directly - they publish and subscribe.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Event is the canonical envelope flowing through the bus. Topic is a
// dotted name (e.g. "user.registered"); ID is unique per event;
// OccurredAt is set by the publisher; Payload is JSON-encodable.
type Event struct {
	ID         string          `json:"id"`
	Topic      string          `json:"topic"`
	OccurredAt time.Time       `json:"occurred_at"`
	TenantID   string          `json:"tenant_id,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

// Handler is the function shape for subscribers. Returning an error is
// logged but does not stop other subscribers from running.
type Handler func(ctx context.Context, ev Event) error

// Bus is an asynchronous, in-process publish/subscribe bus. Zero value
// is not usable; construct with NewBus.
type Bus struct {
	log zerolog.Logger
	mu  sync.RWMutex
	// subs maps topic -> list of subscribers. The empty topic ("") is
	// reserved for catch-all subscribers (used by the outbox writer
	// to record every event).
	subs map[string][]Handler
}

// NewBus returns a ready-to-use bus that emits diagnostic logs through
// the given logger.
func NewBus(log zerolog.Logger) *Bus {
	return &Bus{log: log, subs: make(map[string][]Handler)}
}

// Subscribe registers handler for topic. Multiple handlers may be
// registered for the same topic; they execute concurrently. An empty
// topic registers a catch-all subscriber.
func (b *Bus) Subscribe(topic string, handler func(ctx context.Context, payload []byte) error) {
	wrapped := func(ctx context.Context, ev Event) error {
		return handler(ctx, ev.Payload)
	}
	b.SubscribeEvent(topic, wrapped)
}

// SubscribeEvent is the typed variant of Subscribe that hands the full
// envelope to the subscriber instead of just the payload bytes.
func (b *Bus) SubscribeEvent(topic string, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = append(b.subs[topic], handler)
}

// Publish fans payload out to every subscriber on topic. Payload may be
// any JSON-encodable value; it is marshalled once and reused across
// subscribers to keep allocation bounded. Errors during marshalling
// fail the call; subscriber errors are logged.
func (b *Bus) Publish(ctx context.Context, topic string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("events: marshal payload for %q: %w", topic, err)
	}
	tenantID, _ := ctx.Value(tenantKey{}).(string)
	ev := Event{
		ID:         uuid.NewString(),
		Topic:      topic,
		OccurredAt: time.Now().UTC(),
		TenantID:   tenantID,
		Payload:    raw,
	}
	b.dispatch(ctx, ev)
	return nil
}

// PublishEvent puts a pre-built event onto the bus. Used by the outbox
// dispatcher to replay durably-stored events without re-marshalling.
func (b *Bus) PublishEvent(ctx context.Context, ev Event) {
	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now().UTC()
	}
	b.dispatch(ctx, ev)
}

func (b *Bus) dispatch(ctx context.Context, ev Event) {
	b.mu.RLock()
	subs := append([]Handler(nil), b.subs[ev.Topic]...)
	subs = append(subs, b.subs[""]...)
	b.mu.RUnlock()
	if len(subs) == 0 {
		return
	}
	for _, h := range subs {
		go b.run(ctx, ev, h)
	}
}

func (b *Bus) run(ctx context.Context, ev Event, h Handler) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error().
				Str("topic", ev.Topic).
				Str("event_id", ev.ID).
				Interface("panic", r).
				Msg("event handler panicked")
		}
	}()
	if err := h(ctx, ev); err != nil {
		b.log.Warn().
			Err(err).
			Str("topic", ev.Topic).
			Str("event_id", ev.ID).
			Msg("event handler returned error")
	}
}

// tenantKey is a private context key shared with pkg/tenant via the
// WithTenant helper. Defined here to avoid an import cycle.
type tenantKey struct{}

// WithTenant returns a derived context that carries the given tenant
// ID. Events published from this context will have TenantID set in the
// envelope automatically.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantKey{}, tenantID)
}

// TenantFromContext returns the tenant ID that pkg/tenant.WithID (or a
// direct call to WithTenant) stored on ctx, or the empty string when
// none is present. Packages that need the current tenant for a write
// (e.g. the outbox) should call this helper instead of redeclaring a
// private context key, which would never match the one used here.
func TenantFromContext(ctx context.Context) string {
	v, _ := ctx.Value(tenantKey{}).(string)
	return v
}
