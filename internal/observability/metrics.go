package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics bundles the Prometheus collectors and their registry. Using a private
// registry (rather than the global default) keeps tests isolated and the exported
// surface explicit.
type Metrics struct {
	reg *prometheus.Registry

	HTTPRequests  *prometheus.CounterVec
	HTTPDuration  *prometheus.HistogramVec
	CacheHits     prometheus.Counter
	CacheMisses   prometheus.Counter
	ClicksDropped prometheus.Counter
	ClicksQueued  prometheus.Gauge
}

// NewMetrics constructs and registers all collectors.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		reg: reg,
		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests by route, method and status code.",
		}, []string{"route", "method", "status"}),
		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency by route.",
			Buckets: []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1},
		}, []string{"route"}),
		CacheHits: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cache_hits_total", Help: "Redirect cache hits.",
		}),
		CacheMisses: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cache_misses_total", Help: "Redirect cache misses.",
		}),
		ClicksDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "clicks_dropped_total", Help: "Click events dropped due to a full analytics buffer.",
		}),
		ClicksQueued: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "clicks_queue_depth", Help: "Current depth of the analytics buffer.",
		}),
	}
	reg.MustRegister(
		m.HTTPRequests, m.HTTPDuration, m.CacheHits, m.CacheMisses,
		m.ClicksDropped, m.ClicksQueued,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// Handler returns the /metrics HTTP handler for this registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
