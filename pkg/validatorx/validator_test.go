package validatorx

import (
	"testing"

	"github.com/dedeez14/goforge/pkg/errs"
)

type sample struct {
	Email string `json:"email" validate:"required,email"`
	Age   int    `json:"age"   validate:"min=0,max=120"`
}

func TestStruct_OK(t *testing.T) {
	if err := Struct(&sample{Email: "a@b.co", Age: 1}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestStruct_Invalid(t *testing.T) {
	err := Struct(&sample{Email: "nope", Age: -1})
	if err == nil {
		t.Fatal("expected error")
	}
	e, ok := errs.As(err)
	if !ok {
		t.Fatalf("expected *errs.Error, got %T", err)
	}
	if e.Kind != errs.KindInvalidInput {
		t.Fatalf("expected invalid_input kind, got %s", e.Kind)
	}
	fields, ok := e.Meta["fields"].(map[string]any)
	if !ok {
		t.Fatalf("expected meta.fields map, got %T", e.Meta["fields"])
	}
	if _, ok := fields["email"]; !ok {
		t.Fatal("expected email field error")
	}
	if _, ok := fields["age"]; !ok {
		t.Fatal("expected age field error")
	}
}
