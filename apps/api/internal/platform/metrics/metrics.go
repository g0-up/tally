// Package metrics owns the Prometheus registry. Its handler is served on an internal listener
// (METRICS_ADDR), never on the public API port. All methods are safe on a nil *Metrics so tests
// may skip instrumentation.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics groups every collector of the process.
type Metrics struct {
	registry         *prometheus.Registry
	httpRequests     *prometheus.CounterVec
	httpDuration     *prometheus.HistogramVec
	postingDuration  *prometheus.HistogramVec
	outboxLag        prometheus.Gauge
	outboxDispatched *prometheus.CounterVec
}

// New creates a registry with the Go runtime and process collectors plus the Tally metrics.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		registry: reg,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tally_http_requests_total",
			Help: "HTTP requests by method, route template and status code.",
		}, []string{"method", "route", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "tally_http_request_duration_seconds",
			Help:    "HTTP request latency by method and route template.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"method", "route"}),
		postingDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "tally_posting_duration_seconds",
			Help:    "Duration of posting transactions by command scope and outcome.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		}, []string{"scope", "outcome"}),
		outboxLag: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tally_outbox_lag_seconds",
			Help: "Age of the oldest pending outbox event seen by the last dispatch pass.",
		}),
		outboxDispatched: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tally_outbox_events_total",
			Help: "Outbox events handled by topic and outcome (processed, retry, dead).",
		}, []string{"topic", "outcome"}),
	}
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.httpRequests, m.httpDuration, m.postingDuration, m.outboxLag, m.outboxDispatched,
	)
	return m
}

// Handler serves the exposition format.
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

// ObserveHTTP records one request. route is the route template ("/api/v1/skus/:id"), or
// "unmatched" for 404s so arbitrary paths cannot explode label cardinality.
func (m *Metrics) ObserveHTTP(method, route string, status int, d time.Duration) {
	if m == nil {
		return
	}
	if route == "" {
		route = "unmatched"
	}
	m.httpRequests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.httpDuration.WithLabelValues(method, route).Observe(d.Seconds())
}

// ObservePosting records one posting transaction; outcome is "applied", "replayed", "rejected"
// (a client-facing error) or "error".
func (m *Metrics) ObservePosting(scope, outcome string, d time.Duration) {
	if m == nil {
		return
	}
	m.postingDuration.WithLabelValues(scope, outcome).Observe(d.Seconds())
}

// SetOutboxLag sets the age of the oldest pending outbox event (0 when none).
func (m *Metrics) SetOutboxLag(d time.Duration) {
	if m == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	m.outboxLag.Set(d.Seconds())
}

// CountOutbox counts one handled outbox event.
func (m *Metrics) CountOutbox(topic, outcome string) {
	if m == nil {
		return
	}
	m.outboxDispatched.WithLabelValues(topic, outcome).Inc()
}
