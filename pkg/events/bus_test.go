package events

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestBus_PublishFansOutToSubscribers(t *testing.T) {
	t.Parallel()
	bus := NewBus(zerolog.Nop())

	var (
		mu       sync.Mutex
		received []string
	)
	wg := sync.WaitGroup{}
	wg.Add(2)

	for i := 0; i < 2; i++ {
		bus.SubscribeEvent("user.registered", func(_ context.Context, ev Event) error {
			defer wg.Done()
			mu.Lock()
			received = append(received, ev.Topic)
			mu.Unlock()
			return nil
		})
	}

	if err := bus.Publish(context.Background(), "user.registered", map[string]string{"id": "u1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("subscribers did not run within timeout")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("expected 2 received events, got %d", len(received))
	}
}

func TestBus_TenantPropagation(t *testing.T) {
	t.Parallel()
	bus := NewBus(zerolog.Nop())
	got := make(chan string, 1)
	bus.SubscribeEvent("ping", func(_ context.Context, ev Event) error {
		got <- ev.TenantID
		return nil
	})
	ctx := WithTenant(context.Background(), "tenant-42")
	if err := bus.Publish(ctx, "ping", struct{}{}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case tid := <-got:
		if tid != "tenant-42" {
			t.Fatalf("expected tenant-42, got %q", tid)
		}
	case <-time.After(time.Second):
		t.Fatalf("no event received")
	}
}
