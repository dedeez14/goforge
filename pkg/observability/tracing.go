package observability

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// TracingConfig describes how the application should ship spans.
// Endpoint is an OTLP/HTTP receiver such as an OpenTelemetry
// Collector (`otel-collector:4318`); when blank, a no-op tracer is
// installed and the rest of the framework can use the same code path
// without conditional checks.
type TracingConfig struct {
	Endpoint    string
	Insecure    bool // skip TLS verification (set false in prod)
	ServiceName string
	Version     string
	Environment string
	// SampleRatio is the fraction of spans to record. 0.0 = never sample,
	// 1.0 = always sample. Negative values are coerced to 1.0 (treated as
	// "unset"). The application is expected to set a sensible default
	// (e.g. 0.1 in production, 1.0 elsewhere) — InitTracing does not
	// silently override an explicit 0.
	SampleRatio float64
	Headers     map[string]string
}

// Shutdown is returned by InitTracing and MUST be deferred from main.
// It flushes pending spans with a hard deadline so the binary cannot
// hang during graceful shutdown.
type Shutdown func(ctx context.Context) error

// InitTracing wires the global OpenTelemetry TracerProvider for the
// process. If Endpoint is empty the provider is a no-op — application
// code keeps calling otel.Tracer(...) and the framework keeps emitting
// spans, they simply go nowhere. That keeps the conditional logic out
// of every call site.
func InitTracing(ctx context.Context, cfg TracingConfig) (Shutdown, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		// Install a no-op provider so otel.Tracer never returns
		// nil even when tracing is disabled.
		otel.SetTracerProvider(noop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
		return func(context.Context) error { return nil }, nil
	}

	options := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(cfg.Endpoint),
	}
	if cfg.Insecure {
		options = append(options, otlptracehttp.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		options = append(options, otlptracehttp.WithHeaders(cfg.Headers))
	}

	exp, err := otlptracehttp.New(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(orDefault(cfg.ServiceName, "goforge")),
			semconv.ServiceVersion(orDefault(cfg.Version, "dev")),
			attribute.String("deployment.environment", orDefault(cfg.Environment, "development")),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	ratio := cfg.SampleRatio
	if ratio < 0 {
		ratio = 1.0
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp,
			sdktrace.WithMaxQueueSize(2048),
			sdktrace.WithBatchTimeout(2*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func(shutdownCtx context.Context) error {
		// Bound the shutdown so a slow collector cannot freeze
		// the process at exit.
		ctx, cancel := context.WithTimeout(shutdownCtx, 5*time.Second)
		defer cancel()
		var errs []string
		if err := tp.ForceFlush(ctx); err != nil {
			errs = append(errs, fmt.Sprintf("flush: %v", err))
		}
		if err := tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Sprintf("shutdown: %v", err))
		}
		if len(errs) > 0 {
			return errors.New(strings.Join(errs, "; "))
		}
		return nil
	}, nil
}

// Tracer returns a named tracer; package-level helper so call sites
// don't import otel directly.
func Tracer(name string) trace.Tracer { return otel.Tracer(name) }

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
