// Package realtime exposes the in-process events.Bus to HTTP clients
// over Server-Sent Events.
//
// Clients open `GET /api/v1/stream` (or whatever path the application
// mounts the handler on) and receive a continuous text/event-stream of
// JSON-encoded Event envelopes. Reconnection is handled by the browser
// EventSource API for free; tenant scoping is enforced server-side by
// reusing the value injected by pkg/tenant middleware.
//
// Why SSE instead of WebSocket? SSE is one direction (server -> client),
// uses plain HTTP, traverses every HTTP/2-aware proxy in existence and
// needs zero extra dependencies in Go. It is perfect for live order
// boards, dashboards and notification feeds. WebSocket support can be
// layered on top later if bidirectional traffic is needed.
package realtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/valyala/fasthttp"

	"github.com/dedeez14/goforge/pkg/events"
	"github.com/dedeez14/goforge/pkg/tenant"
)

// Hub manages active SSE subscribers and forwards events from the bus
// to each one. It is safe to call from multiple goroutines.
type Hub struct {
	bus    *events.Bus
	log    zerolog.Logger
	mu     sync.RWMutex
	subs   map[*subscriber]struct{}
	count  atomic.Int64
}

type subscriber struct {
	tenant ID
	topics map[string]struct{}
	send   chan events.Event
	closed atomic.Bool
}

// ID is the tenant ID type alias used by the hub. We retype it so
// pkg/realtime doesn't have to import pkg/tenant just to declare the
// field shape on Subscribe.
type ID = tenant.ID

// NewHub returns a hub bound to the given bus. The hub installs a
// catch-all subscriber on the bus immediately so events emitted before
// the first HTTP client connects are not lost on this side - they are
// simply fanned out to zero subscribers.
func NewHub(bus *events.Bus, log zerolog.Logger) *Hub {
	h := &Hub{
		bus:  bus,
		log:  log,
		subs: make(map[*subscriber]struct{}),
	}
	bus.SubscribeEvent("", h.onEvent)
	return h
}

// Active reports the number of currently connected SSE subscribers.
// Useful for the /readyz signal and metrics.
func (h *Hub) Active() int64 { return h.count.Load() }

func (h *Hub) onEvent(ctx context.Context, ev events.Event) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subs {
		if s.tenant != "" && ev.TenantID != "" && string(s.tenant) != ev.TenantID {
			continue
		}
		if len(s.topics) > 0 {
			if _, ok := s.topics[ev.Topic]; !ok {
				continue
			}
		}
		select {
		case s.send <- ev:
		default:
			// Slow consumer; drop event so the publisher is not blocked.
			h.log.Warn().Str("topic", ev.Topic).Msg("SSE subscriber slow; dropping event")
		}
	}
	return nil
}

// Handler returns a Fiber handler that streams events to the client.
// It honours the `topics` query parameter (comma-separated list of
// topic names) for filtering and adopts the tenant ID from the request
// context when present.
func (h *Hub) Handler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		topics := parseTopics(c.Query("topics"))
		tid, _ := tenant.FromContext(c.UserContext())

		s := &subscriber{
			tenant: tid,
			topics: topics,
			send:   make(chan events.Event, 64),
		}
		h.register(s)

		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")
		c.Set("X-Accel-Buffering", "no")

		c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
			defer h.unregister(s)
			heartbeat := time.NewTicker(15 * time.Second)
			defer heartbeat.Stop()

			// Initial comment line lets clients know the stream is alive.
			fmt.Fprintf(w, ": connected\n\n")
			if err := w.Flush(); err != nil {
				return
			}

			for {
				select {
				case ev, ok := <-s.send:
					if !ok {
						return
					}
					if err := writeEvent(w, ev); err != nil {
						return
					}
				case <-heartbeat.C:
					if _, err := fmt.Fprintf(w, ": ping %d\n\n", time.Now().Unix()); err != nil {
						return
					}
					if err := w.Flush(); err != nil {
						return
					}
				}
			}
		}))
		return nil
	}
}

func (h *Hub) register(s *subscriber) {
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	h.count.Add(1)
}

func (h *Hub) unregister(s *subscriber) {
	if s.closed.Swap(true) {
		return
	}
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
	close(s.send)
	h.count.Add(-1)
}

func parseTopics(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := make(map[string]struct{})
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			out[t] = struct{}{}
		}
	}
	return out
}

func writeEvent(w *bufio.Writer, ev events.Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	// SSE frame: id, event, data, blank line.
	if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", ev.ID, ev.Topic, body); err != nil {
		return err
	}
	return w.Flush()
}
