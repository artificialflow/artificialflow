package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// SyncSnapshot is the bounded projection-consumer state exposed to Prometheus.
type SyncSnapshot struct {
	Processed       int64
	Succeeded       int64
	Failed          int64
	Retried         int64
	DeadLetters     int64
	LastProcessedAt time.Time
}

// SyncCollector exports projection outcomes and freshness without tenant,
// event, offset, or error labels.
type SyncCollector struct {
	snapshot    func() SyncSnapshot
	processed   *prometheus.Desc
	succeeded   *prometheus.Desc
	failed      *prometheus.Desc
	retried     *prometheus.Desc
	deadLetters *prometheus.Desc
	freshness   *prometheus.Desc
}

func NewSyncCollector(snapshot func() SyncSnapshot) *SyncCollector {
	return &SyncCollector{
		snapshot: snapshot,
		processed: prometheus.NewDesc("artificialflow_projection_messages_total",
			"Projection messages fetched from the broker.", nil, nil),
		succeeded: prometheus.NewDesc("artificialflow_projection_success_total",
			"Projection messages applied successfully.", nil, nil),
		failed: prometheus.NewDesc("artificialflow_projection_failure_total",
			"Projection messages that exhausted processing attempts.", nil, nil),
		retried: prometheus.NewDesc("artificialflow_projection_retries_total",
			"Projection processing retries.", nil, nil),
		deadLetters: prometheus.NewDesc("artificialflow_projection_dead_letters_total",
			"Projection messages published to the dead-letter topic.", nil, nil),
		freshness: prometheus.NewDesc("artificialflow_projection_freshness_seconds",
			"Seconds since the last consumed projection message; -1 before the first message.", nil, nil),
	}
}

func (c *SyncCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.processed
	ch <- c.succeeded
	ch <- c.failed
	ch <- c.retried
	ch <- c.deadLetters
	ch <- c.freshness
}

func (c *SyncCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.snapshot()
	freshness := -1.0
	if !s.LastProcessedAt.IsZero() {
		freshness = time.Since(s.LastProcessedAt).Seconds()
		if freshness < 0 {
			freshness = 0
		}
	}
	ch <- prometheus.MustNewConstMetric(c.processed, prometheus.CounterValue, float64(s.Processed))
	ch <- prometheus.MustNewConstMetric(c.succeeded, prometheus.CounterValue, float64(s.Succeeded))
	ch <- prometheus.MustNewConstMetric(c.failed, prometheus.CounterValue, float64(s.Failed))
	ch <- prometheus.MustNewConstMetric(c.retried, prometheus.CounterValue, float64(s.Retried))
	ch <- prometheus.MustNewConstMetric(c.deadLetters, prometheus.CounterValue, float64(s.DeadLetters))
	ch <- prometheus.MustNewConstMetric(c.freshness, prometheus.GaugeValue, freshness)
}
