package module

import (
	"errors"
	"testing"
)

type stubModule struct {
	BaseModule
	name string
}

func (s stubModule) Name() string { return s.name }

func TestRegistry_RegisterAndEach(t *testing.T) {
	t.Parallel()
	var r Registry
	if err := r.Register(stubModule{name: "b"}); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if err := r.Register(stubModule{name: "a"}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := r.Register(stubModule{name: "a"}); !errors.Is(err, ErrDuplicateModule) {
		t.Fatalf("expected duplicate error, got %v", err)
	}

	names := r.Names()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("expected sorted [a b], got %v", names)
	}
}

func TestValues_GetMustGet(t *testing.T) {
	t.Parallel()
	v := NewValues()
	v.Set("port", 8080)
	if got := MustGet[int](v, "port"); got != 8080 {
		t.Fatalf("expected 8080, got %d", got)
	}
	if _, ok := v.Get("missing"); ok {
		t.Fatalf("expected missing key to report false")
	}
	defer func() {
		if recover() == nil {
			t.Fatalf("expected MustGet to panic on missing key")
		}
	}()
	_ = MustGet[int](v, "missing")
}
