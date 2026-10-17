package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

const namespace = "companies"

type Metrics struct {
	Registry *prometheus.Registry

	HTTPRequests *prometheus.CounterVec
	HTTPDuration *prometheus.HistogramVec

	outboxPublished prometheus.Counter
	outboxFailures  prometheus.Counter
	outboxPending   prometheus.Gauge
	outboxOldestAge prometheus.Gauge
}

func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		Registry: reg,
		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "http_requests_total", Help: "HTTP requests by route and status.",
		}, []string{"method", "route", "status"}),
		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "http_request_duration_seconds", Help: "HTTP request latency.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		outboxPublished: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "outbox_published_total", Help: "Events published to Kafka.",
		}),
		outboxFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "outbox_failures_total", Help: "Failed outbox flush attempts.",
		}),
		outboxPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Name: "outbox_pending", Help: "Events waiting in the outbox.",
		}),
		outboxOldestAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Name: "outbox_oldest_age_seconds", Help: "Age of the oldest unpublished event, 0 when empty.",
		}),
	}
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.HTTPRequests, m.HTTPDuration, m.outboxPublished, m.outboxFailures, m.outboxPending, m.outboxOldestAge,
	)
	return m
}

func (m *Metrics) Published(n int) { m.outboxPublished.Add(float64(n)) }

func (m *Metrics) Failed() { m.outboxFailures.Inc() }

func (m *Metrics) Backlog(pending int, oldestAge time.Duration) {
	m.outboxPending.Set(float64(pending))
	m.outboxOldestAge.Set(oldestAge.Seconds())
}
