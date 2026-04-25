package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dedeez14/goforge/pkg/jobs"
)

// JobKind is the canonical name of the webhook delivery job. Wire
// this string in your jobs.Runner so deliveries are routed to
// (Dispatcher).Deliver.
const JobKind = "webhooks.deliver"

// Endpoint is a registered webhook destination. Most apps store
// these in their own table; the Dispatcher only needs the URL and
// the signing secret at delivery time.
type Endpoint struct {
	ID     string
	URL    string
	Secret string
}

// EndpointStore loads endpoints by ID. Implementations typically wrap
// a database table.
type EndpointStore interface {
	Get(ctx context.Context, id string) (*Endpoint, error)
}

// Dispatcher fans out an event to one or more endpoints by enqueueing
// a delivery job per (event, endpoint) pair. The actual HTTP POST is
// performed by the jobs.Runner via Dispatcher.Deliver.
type Dispatcher struct {
	Queue     jobs.Queue
	Store     EndpointStore
	HTTP      *http.Client
	UserAgent string
}

// NewDispatcher returns a Dispatcher with sensible defaults.
func NewDispatcher(q jobs.Queue, s EndpointStore) *Dispatcher {
	return &Dispatcher{
		Queue:     q,
		Store:     s,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: "goforge-webhooks/1",
	}
}

// deliveryPayload is the JSON shape we pass between Enqueue and
// Deliver. It carries everything the worker needs to recompute the
// signature without another DB roundtrip.
type deliveryPayload struct {
	EventID    string          `json:"event_id"`
	EndpointID string          `json:"endpoint_id"`
	Body       json.RawMessage `json:"body"`
}

// Enqueue schedules a single (event, endpoint) delivery. The body is
// stored verbatim on the job; the worker re-signs at send time so
// secret rotations during retries take effect immediately.
func (d *Dispatcher) Enqueue(ctx context.Context, eventID, endpointID string, body json.RawMessage) error {
	_, err := d.Queue.Enqueue(ctx, JobKind, deliveryPayload{
		EventID: eventID, EndpointID: endpointID, Body: body,
	}, jobs.EnqueueOptions{
		// dedupe so we don't enqueue the same delivery twice if
		// the caller retries.
		DedupeKey:   "wh:" + eventID + ":" + endpointID,
		MaxAttempts: 8,
	})
	if err != nil && !errors.Is(err, jobs.ErrDuplicate) {
		return err
	}
	return nil
}

// Deliver is the jobs.Handler. Register it on a Runner with kind
// JobKind. It signs and POSTs the body; non-2xx responses (or
// network errors) propagate as an error so jobs.Runner reschedules
// with backoff.
func (d *Dispatcher) Deliver(ctx context.Context, payload json.RawMessage) error {
	var p deliveryPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("webhooks: bad payload: %w", err)
	}
	ep, err := d.Store.Get(ctx, p.EndpointID)
	if err != nil {
		return fmt.Errorf("webhooks: load endpoint: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(p.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", d.UserAgent)
	req.Header.Set("Webhook-Event", p.EventID)
	req.Header.Set(SignatureHeader, Sign(ep.Secret, p.EventID, p.Body, time.Time{}))

	resp, err := d.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("webhooks: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain so HTTP/1.1 keep-alive can reuse the connection.
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhooks: %s -> HTTP %d", ep.URL, resp.StatusCode)
	}
	return nil
}
