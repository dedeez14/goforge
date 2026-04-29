package lifecycle

import (
	"sync"
	"testing"
)

func TestDrainer_InitiallyServing(t *testing.T) {
	d := NewDrainer()
	if d.IsDraining() {
		t.Fatal("new Drainer must start in the serving state")
	}
}

func TestDrainer_StartDraining(t *testing.T) {
	d := NewDrainer()
	d.StartDraining()
	if !d.IsDraining() {
		t.Fatal("StartDraining must flip IsDraining to true")
	}
	// Idempotent: second call still true, no panic.
	d.StartDraining()
	if !d.IsDraining() {
		t.Fatal("StartDraining must remain true after second call")
	}
}

func TestDrainer_ConcurrentSafe(t *testing.T) {
	d := NewDrainer()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); d.StartDraining() }()
		go func() { defer wg.Done(); _ = d.IsDraining() }()
	}
	wg.Wait()
	if !d.IsDraining() {
		t.Fatal("after concurrent calls, Drainer must be draining")
	}
}
