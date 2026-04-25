package observability

import (
	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// FiberTracing returns a Fiber middleware that opens an OpenTelemetry
// span around every request. The middleware:
//
//   - extracts a parent span from incoming W3C TraceContext / B3
//     headers so tracing follows requests across services,
//   - names the span `<METHOD> <route>` (route, not raw path, so
//     `/users/:id` doesn't explode cardinality),
//   - records http.method / http.route / http.status_code per
//     OpenTelemetry HTTP semantic conventions,
//   - flips the span status to Error for any 5xx response so
//     downstream alerting can rely on a single bit.
//
// The middleware is safe to install when tracing is disabled — the
// global no-op TracerProvider returns no-op spans and the cost is a
// few nanoseconds per request.
func FiberTracing(serviceName string) fiber.Handler {
	tracer := Tracer(serviceName)
	propagator := propagationOnce()
	return func(c *fiber.Ctx) error {
		// Extract upstream span from request headers.
		ctx := propagator.Extract(c.UserContext(), fiberHeaderCarrier{c: c})

		spanName := c.Method() + " " + c.Route().Path
		ctx, span := tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(c.Method()),
				semconv.URLPath(c.Path()),
				semconv.URLScheme(c.Protocol()),
				attribute.String("http.route", c.Route().Path),
				attribute.String("net.peer.ip", c.IP()),
				attribute.String("user_agent.original", string(c.Request().Header.UserAgent())),
			),
		)
		defer span.End()

		c.SetUserContext(ctx)
		err := c.Next()

		status := c.Response().StatusCode()
		span.SetAttributes(semconv.HTTPResponseStatusCode(status))
		if err != nil {
			span.RecordError(err)
		}
		if status >= 500 {
			span.SetStatus(codes.Error, "5xx")
		}
		return err
	}
}

func propagationOnce() propagation.TextMapPropagator {
	// otel.GetTextMapPropagator returns the global propagator
	// configured by InitTracing. Cache it once per middleware.
	return propagationGlobal()
}

// fiberHeaderCarrier adapts *fiber.Ctx to TextMapCarrier without
// allocating a map per request.
type fiberHeaderCarrier struct{ c *fiber.Ctx }

func (h fiberHeaderCarrier) Get(key string) string { return h.c.Get(key) }
func (h fiberHeaderCarrier) Set(key, val string)   { h.c.Set(key, val) }
func (h fiberHeaderCarrier) Keys() []string {
	keys := make([]string, 0)
	h.c.Request().Header.VisitAll(func(k, _ []byte) {
		keys = append(keys, string(k))
	})
	return keys
}
