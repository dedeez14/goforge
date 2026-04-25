// Package observability wires Prometheus metrics into a goforge HTTP
// server. The middleware records per-request counters and a latency
// histogram and exposes them on `/metrics`.
//
// OpenTelemetry tracing is intentionally not pulled in by default to
// keep the dependency footprint small; the `forge module add otel`
// command (planned) will introduce a tracing module that satisfies
// the same Module interface.
package observability

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
)

// Metrics is the bundle of Prometheus collectors used by the
// middleware. Application code rarely touches the fields directly;
// passing the bundle around keeps the wiring explicit at the
// composition root.
type Metrics struct {
	Registry        *prometheus.Registry
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	InFlight        prometheus.Gauge
}

// New builds a Metrics bundle with the standard Go and process
// collectors pre-registered. Pass the returned Metrics to Middleware()
// and Handler() to wire it into a Fiber app.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := &Metrics{
		Registry: reg,
		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "goforge_http_requests_total",
			Help: "HTTP requests counted by method, path template and status class.",
		}, []string{"method", "route", "status"}),
		RequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "goforge_http_request_duration_seconds",
			Help:    "HTTP request latency in seconds, by method and route template.",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		}, []string{"method", "route"}),
		InFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "goforge_http_in_flight_requests",
			Help: "Number of HTTP requests currently being served.",
		}),
	}
	reg.MustRegister(m.RequestsTotal, m.RequestDuration, m.InFlight)
	return m
}

// Middleware records metrics for every request that flows through it.
// Mount it after RequestID and Recover so panics still increment the
// counters before bubbling up.
func Middleware(m *Metrics) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		m.InFlight.Inc()
		err := c.Next()
		m.InFlight.Dec()
		route := c.Route().Path
		if route == "" {
			route = c.Path()
		}
		status := strconv.Itoa(c.Response().StatusCode())
		m.RequestsTotal.WithLabelValues(c.Method(), route, status).Inc()
		m.RequestDuration.WithLabelValues(c.Method(), route).Observe(time.Since(start).Seconds())
		return err
	}
}

// Handler returns a Fiber handler serving the registry on /metrics.
// It uses fasthttpadaptor so we can reuse the official promhttp
// handler unchanged.
func Handler(m *Metrics) fiber.Handler {
	std := promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
	adapter := fasthttpadaptor.NewFastHTTPHandler(std)
	return func(c *fiber.Ctx) error {
		adapter(c.Context())
		return nil
	}
}
