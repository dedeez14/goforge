package observability

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// propagationGlobal returns the process-wide TextMapPropagator
// installed by InitTracing. Splitting it into its own file keeps the
// fiber middleware import-light.
func propagationGlobal() propagation.TextMapPropagator {
	return otel.GetTextMapPropagator()
}
