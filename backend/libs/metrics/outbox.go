package metrics

import "github.com/prometheus/client_golang/prometheus"

// OutboxSnapshot is the subset of engine metrics needed by the outbox collector.
type OutboxSnapshot struct {
	Pending        int64
	PublishSuccess int64
	PublishFailure int64
	Retries        int64
	DeadLetters    int64
	PublishLagSec  int64
}

// OutboxSnapshotFn is a function that returns the current outbox snapshot.
type OutboxSnapshotFn func() OutboxSnapshot

// OutboxCollector is a Prometheus Collector that exposes outbox health metrics.
type OutboxCollector struct {
	snapshot    OutboxSnapshotFn
	pending     *prometheus.Desc
	success     *prometheus.Desc
	failure     *prometheus.Desc
	retries     *prometheus.Desc
	deadLetters *prometheus.Desc
	lagSeconds  *prometheus.Desc
}

// NewOutboxCollector creates and registers a new OutboxCollector.
func NewOutboxCollector(snapshotFn OutboxSnapshotFn) *OutboxCollector {
	c := NewUnregisteredOutboxCollector(snapshotFn)
	prometheus.MustRegister(c)
	return c
}

// NewUnregisteredOutboxCollector creates a collector for use with a caller-owned registry.
func NewUnregisteredOutboxCollector(snapshotFn OutboxSnapshotFn) *OutboxCollector {
	return &OutboxCollector{
		snapshot: snapshotFn,
		pending: prometheus.NewDesc(
			"artificialflow_outbox_pending",
			"Number of outbox events pending publication.",
			nil, nil,
		),
		success: prometheus.NewDesc(
			"artificialflow_outbox_publish_success_total",
			"Total outbox events successfully published.",
			nil, nil,
		),
		failure: prometheus.NewDesc(
			"artificialflow_outbox_publish_failure_total",
			"Total outbox events that failed to publish.",
			nil, nil,
		),
		retries: prometheus.NewDesc(
			"artificialflow_outbox_retries_total",
			"Total outbox publication attempts scheduled for retry.",
			nil, nil,
		),
		deadLetters: prometheus.NewDesc(
			"artificialflow_outbox_dead_letters_total",
			"Total outbox messages terminally failed after exhausting attempts.",
			nil, nil,
		),
		lagSeconds: prometheus.NewDesc(
			"artificialflow_outbox_publish_lag_seconds",
			"Age in seconds of the oldest unpublished outbox event.",
			nil, nil,
		),
	}
}

func (c *OutboxCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.pending
	ch <- c.success
	ch <- c.failure
	ch <- c.retries
	ch <- c.deadLetters
	ch <- c.lagSeconds
}

func (c *OutboxCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.snapshot()
	ch <- prometheus.MustNewConstMetric(c.pending, prometheus.GaugeValue, float64(s.Pending))
	ch <- prometheus.MustNewConstMetric(c.success, prometheus.CounterValue, float64(s.PublishSuccess))
	ch <- prometheus.MustNewConstMetric(c.failure, prometheus.CounterValue, float64(s.PublishFailure))
	ch <- prometheus.MustNewConstMetric(c.retries, prometheus.CounterValue, float64(s.Retries))
	ch <- prometheus.MustNewConstMetric(c.deadLetters, prometheus.CounterValue, float64(s.DeadLetters))
	ch <- prometheus.MustNewConstMetric(c.lagSeconds, prometheus.GaugeValue, float64(s.PublishLagSec))
}
