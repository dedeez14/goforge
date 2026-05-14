package queue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubjectMatches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		filter, subject string
		want            bool
	}{
		{"orders.created", "orders.created", true},
		{"orders.created", "orders.updated", false},
		{"orders.*", "orders.created", true},
		{"orders.*", "orders.created.v2", false},
		{"orders.>", "orders.created", true},
		{"orders.>", "orders.created.v2", true},
		{"orders.>", "orders", false},
		{"a.b.c", "a.b", false},
		{"*.*.*", "a.b.c", true},
		{"a.>", "a.b.c.d", true},
	}
	for _, c := range cases {
		got := subjectMatches(c.filter, c.subject)
		if got != c.want {
			t.Errorf("subjectMatches(%q,%q)=%v want %v", c.filter, c.subject, got, c.want)
		}
	}
}

func TestMemoryQueue_PublishSubscribe(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewMemoryQueue()
	t.Cleanup(func() { _ = q.Close(ctx) })

	received := make(chan Message, 4)
	unsub, err := q.Subscribe(ctx, Subscription{
		Stream: "events", Subject: "orders.*", Consumer: "c1",
	}, func(_ context.Context, m Message) error {
		received <- m
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = unsub() })

	if err := q.Publish(ctx, "orders.created", []byte("payload-1"), WithHeader("X-Tenant", "t1")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := q.Publish(ctx, "orders.updated", []byte("payload-2")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := q.Publish(ctx, "shipments.created", []byte("not-matched")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := drain(t, received, 2, time.Second)
	if len(got) != 2 {
		t.Fatalf("received %d messages, want 2", len(got))
	}
	// Ordering between two publishes to the same subscription is
	// preserved by the per-subscription channel.
	if string(got[0].Data()) != "payload-1" {
		t.Errorf("first payload = %q", got[0].Data())
	}
	if got[0].Subject() != "orders.created" {
		t.Errorf("first subject = %q", got[0].Subject())
	}
	if v := got[0].Headers()["X-Tenant"]; len(v) != 1 || v[0] != "t1" {
		t.Errorf("first headers = %v", got[0].Headers())
	}
}

func TestMemoryQueue_HandlerErrorRedelivers(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewMemoryQueue()
	t.Cleanup(func() { _ = q.Close(ctx) })

	var deliveries atomic.Int64
	done := make(chan struct{})

	_, err := q.Subscribe(ctx, Subscription{
		Stream: "events", Subject: "orders.created", Consumer: "c1",
		MaxDeliver: 3, AckWait: 50 * time.Millisecond,
	}, func(_ context.Context, m Message) error {
		n := deliveries.Add(1)
		if n < 3 {
			return errors.New("flaky")
		}
		close(done)
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := q.Publish(ctx, "orders.created", []byte("x")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("handler never succeeded; deliveries=%d", deliveries.Load())
	}
	if got := deliveries.Load(); got != 3 {
		t.Errorf("deliveries = %d, want 3", got)
	}
}

func TestMemoryQueue_MaxDeliverStopsRedelivery(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewMemoryQueue()
	t.Cleanup(func() { _ = q.Close(ctx) })

	var deliveries atomic.Int64

	_, err := q.Subscribe(ctx, Subscription{
		Stream: "events", Subject: "orders.created", Consumer: "c1",
		MaxDeliver: 2, AckWait: 30 * time.Millisecond,
	}, func(_ context.Context, _ Message) error {
		deliveries.Add(1)
		return errors.New("always fail")
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := q.Publish(ctx, "orders.created", []byte("x")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Give the queue time to attempt every redelivery + one extra
	// AckWait window to confirm no further deliveries happen.
	time.Sleep(300 * time.Millisecond)
	if got := deliveries.Load(); got != 2 {
		t.Errorf("deliveries = %d, want 2 (MaxDeliver)", got)
	}
}

func TestMemoryQueue_ErrTerminalSkipsRedelivery(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewMemoryQueue()
	t.Cleanup(func() { _ = q.Close(ctx) })

	var deliveries atomic.Int64

	_, err := q.Subscribe(ctx, Subscription{
		Stream: "events", Subject: "orders.created", Consumer: "c1",
		MaxDeliver: 0, AckWait: 30 * time.Millisecond,
	}, func(_ context.Context, _ Message) error {
		deliveries.Add(1)
		return ErrTerminal
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := q.Publish(ctx, "orders.created", []byte("bad")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	time.Sleep(150 * time.Millisecond)
	if got := deliveries.Load(); got != 1 {
		t.Errorf("deliveries = %d, want 1 (ErrTerminal)", got)
	}
}

func TestMemoryQueue_UnsubscribeStopsDelivery(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewMemoryQueue()
	t.Cleanup(func() { _ = q.Close(ctx) })

	var deliveries atomic.Int64
	unsub, err := q.Subscribe(ctx, Subscription{
		Stream: "events", Subject: "orders.*", Consumer: "c1",
	}, func(_ context.Context, _ Message) error {
		deliveries.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := q.Publish(ctx, "orders.created", []byte("x")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := deliveries.Load(); got != 1 {
		t.Fatalf("first delivery missing: %d", got)
	}

	if err := unsub(); err != nil {
		t.Fatalf("unsub: %v", err)
	}

	if err := q.Publish(ctx, "orders.created", []byte("y")); err != nil {
		t.Fatalf("Publish after unsub: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := deliveries.Load(); got != 1 {
		t.Errorf("deliveries after unsub = %d, want 1", got)
	}
}

func TestMemoryQueue_FanOutToMultipleSubscribers(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewMemoryQueue()
	t.Cleanup(func() { _ = q.Close(ctx) })

	var a, b atomic.Int64

	for _, target := range []*atomic.Int64{&a, &b} {
		tgt := target
		if _, err := q.Subscribe(ctx, Subscription{
			Stream: "events", Subject: "orders.>", Consumer: "c-" + iname(tgt),
		}, func(_ context.Context, _ Message) error {
			tgt.Add(1)
			return nil
		}); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
	}

	if err := q.Publish(ctx, "orders.created.v2", []byte("x")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if a.Load() != 1 || b.Load() != 1 {
		t.Errorf("fanout = (%d,%d), want (1,1)", a.Load(), b.Load())
	}
}

func TestMemoryQueue_ClosedQueueRejectsPublishAndSubscribe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := NewMemoryQueue()
	if err := q.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Double-close is a no-op.
	if err := q.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := q.Publish(ctx, "x", []byte("y")); err == nil {
		t.Errorf("Publish after Close should error")
	}
	if _, err := q.Subscribe(ctx, Subscription{Stream: "s", Subject: "x", Consumer: "c"}, func(_ context.Context, _ Message) error { return nil }); err == nil {
		t.Errorf("Subscribe after Close should error")
	}
}

func TestMemoryQueue_PayloadOwnership(t *testing.T) {
	t.Parallel()
	// The queue must copy the publish slice so the caller can
	// safely mutate it after Publish returns.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewMemoryQueue()
	t.Cleanup(func() { _ = q.Close(ctx) })

	received := make(chan []byte, 1)
	_, err := q.Subscribe(ctx, Subscription{Stream: "s", Subject: "x", Consumer: "c"}, func(_ context.Context, m Message) error {
		// Copy into a separate slice — Message.Data() returns the
		// queue's buffer.
		cp := make([]byte, len(m.Data()))
		copy(cp, m.Data())
		received <- cp
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	payload := []byte("original")
	if err := q.Publish(ctx, "x", payload); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Mutate the caller-side slice immediately.
	for i := range payload {
		payload[i] = '!'
	}

	select {
	case got := <-received:
		if string(got) != "original" {
			t.Errorf("payload corrupted: got %q want %q", got, "original")
		}
	case <-time.After(time.Second):
		t.Fatalf("no delivery")
	}
}

// --- helpers ---

func drain(t *testing.T, ch <-chan Message, n int, timeout time.Duration) []Message {
	t.Helper()
	out := make([]Message, 0, n)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for len(out) < n {
		select {
		case m := <-ch:
			out = append(out, m)
		case <-deadline.C:
			return out
		}
	}
	return out
}

var inameSeq atomic.Int64

func iname(_ *atomic.Int64) string {
	return "iname-" + itoa(int(inameSeq.Add(1)))
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg, i = true, -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

var _ sync.Locker = (*sync.Mutex)(nil)
