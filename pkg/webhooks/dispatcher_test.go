package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dedeez14/goforge/pkg/resilience"
)

type staticStore struct{ ep *Endpoint }

func (s staticStore) Get(context.Context, string) (*Endpoint, error) {
	return s.ep, nil
}

func TestDispatcher_Deliver_NoBreaker(t *testing.T) {
	var got int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := &Dispatcher{
		Store:     staticStore{ep: &Endpoint{ID: "e1", URL: srv.URL, Secret: "s"}},
		HTTP:      srv.Client(),
		UserAgent: "t",
	}
	body, _ := json.Marshal(deliveryPayload{EventID: "evt", EndpointID: "e1", Body: json.RawMessage(`{}`)})
	if err := d.Deliver(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("got = %d, want 1", got)
	}
}

func TestDispatcher_Deliver_BreakerOpenShortCircuits(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(500)
	}))
	defer srv.Close()

	g := resilience.NewBreakerGroup(func(key string) resilience.CBConfig {
		return resilience.CBConfig{
			FailureThreshold: 2,
			CooldownPeriod:   time.Hour,
		}
	})
	d := &Dispatcher{
		Store:      staticStore{ep: &Endpoint{ID: "flaky", URL: srv.URL, Secret: "s"}},
		HTTP:       srv.Client(),
		UserAgent:  "t",
		BreakerFor: g.Get,
	}
	body, _ := json.Marshal(deliveryPayload{EventID: "evt", EndpointID: "flaky", Body: json.RawMessage(`{}`)})

	// Two 500s trip the breaker…
	for i := 0; i < 2; i++ {
		if err := d.Deliver(context.Background(), body); err == nil {
			t.Fatalf("attempt %d: want error", i+1)
		}
	}
	// …third attempt short-circuits with ErrOpen without hitting the server.
	hitsBefore := hits
	err := d.Deliver(context.Background(), body)
	if !errors.Is(err, resilience.ErrOpen) {
		t.Fatalf("third err = %v, want ErrOpen", err)
	}
	if hits != hitsBefore {
		t.Fatalf("breaker-open call still reached server: hits %d -> %d", hitsBefore, hits)
	}
}

func TestDispatcher_Deliver_BreakerForReturnsNil_Bypass(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := &Dispatcher{
		Store:      staticStore{ep: &Endpoint{ID: "opt-out", URL: srv.URL, Secret: "s"}},
		HTTP:       srv.Client(),
		UserAgent:  "t",
		BreakerFor: func(string) *resilience.CircuitBreaker { return nil },
	}
	body, _ := json.Marshal(deliveryPayload{EventID: "evt", EndpointID: "opt-out", Body: json.RawMessage(`{}`)})
	if err := d.Deliver(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("hits = %d, want 1", hits)
	}
}
