package resilience

import (
	"sync"
	"testing"
)

func TestBreakerGroup_ReusesPerKey(t *testing.T) {
	g := NewBreakerGroup(func(key string) CBConfig { return CBConfig{} })
	a1 := g.Get("a")
	a2 := g.Get("a")
	b := g.Get("b")
	if a1 != a2 {
		t.Fatal("same key returned different breakers")
	}
	if a1 == b {
		t.Fatal("different keys returned same breaker")
	}
	if g.Len() != 2 {
		t.Fatalf("len = %d, want 2", g.Len())
	}
}

func TestBreakerGroup_ConcurrentGet(t *testing.T) {
	g := NewBreakerGroup(func(key string) CBConfig { return CBConfig{} })
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = g.Get("shared")
		}()
	}
	wg.Wait()
	if g.Len() != 1 {
		t.Fatalf("len = %d, want 1", g.Len())
	}
}

func TestBreakerGroup_PanicsWithoutFactory(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("no panic")
		}
	}()
	NewBreakerGroup(nil)
}
