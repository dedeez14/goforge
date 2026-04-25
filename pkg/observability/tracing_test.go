package observability

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
)

func TestInitTracing_NoEndpointInstallsNoOp(t *testing.T) {
	t.Parallel()
	shutdown, err := InitTracing(context.Background(), TracingConfig{})
	if err != nil {
		t.Fatalf("InitTracing: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	tr := otel.Tracer("test")
	_, span := tr.Start(context.Background(), "noop")
	defer span.End()
	if span.SpanContext().IsValid() {
		t.Fatal("noop tracer should not produce a valid span context")
	}
}

func TestTracer_AccessibleViaPackageHelper(t *testing.T) {
	t.Parallel()
	if Tracer("x") == nil {
		t.Fatal("Tracer must never return nil")
	}
}
