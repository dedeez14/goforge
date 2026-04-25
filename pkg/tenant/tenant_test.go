package tenant

import (
	"context"
	"errors"
	"testing"
)

func TestWithIDAndRequire(t *testing.T) {
	t.Parallel()
	ctx := WithID(context.Background(), ID("tenant-1"))
	id, err := Require(ctx)
	if err != nil {
		t.Fatalf("require: %v", err)
	}
	if id != "tenant-1" {
		t.Fatalf("unexpected tenant: %q", id)
	}
}

func TestRequireMissing(t *testing.T) {
	t.Parallel()
	_, err := Require(context.Background())
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("expected ErrMissing, got %v", err)
	}
}
