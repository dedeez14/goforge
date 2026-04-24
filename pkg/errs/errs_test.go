package errs

import (
	"errors"
	"testing"
)

func TestError_ErrorString(t *testing.T) {
	e := New(KindNotFound, "user.not_found", "user not found")
	if got := e.Error(); got != "user.not_found: user not found" {
		t.Fatalf("unexpected error string: %q", got)
	}
}

func TestError_WrapUnwrap(t *testing.T) {
	root := errors.New("root cause")
	e := Wrap(KindInternal, "x", "y", root)
	if !errors.Is(e, root) {
		t.Fatal("wrapped error should match root cause via errors.Is")
	}
}

func TestError_IsAs(t *testing.T) {
	e := Conflict("x.y", "z")
	if !Is(e, KindConflict) {
		t.Fatal("Is(KindConflict) must be true")
	}
	got, ok := As(e)
	if !ok || got != e {
		t.Fatal("As must recover the *Error")
	}
}

func TestError_With(t *testing.T) {
	e := InvalidInput("v", "bad").With("fields", map[string]any{"email": "is required"})
	if _, ok := e.Meta["fields"]; !ok {
		t.Fatal("With must attach meta")
	}
}

func TestKind_String(t *testing.T) {
	cases := map[Kind]string{
		KindInvalidInput: "invalid_input",
		KindUnauthorized: "unauthorized",
		KindForbidden:    "forbidden",
		KindNotFound:     "not_found",
		KindConflict:     "conflict",
		KindRateLimited:  "rate_limited",
		KindInternal:     "internal",
		KindUnavailable:  "unavailable",
		KindUnknown:      "unknown",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}
