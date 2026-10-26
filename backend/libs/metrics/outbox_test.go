package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestOutboxCollectorExposesCanonicalMetrics(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	collector := NewUnregisteredOutboxCollector(func() OutboxSnapshot {
		return OutboxSnapshot{
			Pending:        3,
			PublishSuccess: 7,
			PublishFailure: 2,
			Retries:        4,
			DeadLetters:    1,
			PublishLagSec:  11,
		}
	})
	registry.MustRegister(collector)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}
	values := make(map[string]float64, len(families))
	for _, family := range families {
		values[family.GetName()] = metricValue(family)
	}

	expected := map[string]float64{
		"artificialflow_outbox_pending":               3,
		"artificialflow_outbox_publish_success_total": 7,
		"artificialflow_outbox_publish_failure_total": 2,
		"artificialflow_outbox_retries_total":         4,
		"artificialflow_outbox_dead_letters_total":    1,
		"artificialflow_outbox_publish_lag_seconds":   11,
	}
	for name, want := range expected {
		got, ok := values[name]
		if !ok {
			t.Fatalf("missing metric %q in %#v", name, values)
		}
		if got != want {
			t.Fatalf("metric %q = %v, want %v", name, got, want)
		}
	}
}

func metricValue(family *dto.MetricFamily) float64 {
	if len(family.Metric) == 0 {
		return 0
	}
	metric := family.Metric[0]
	if metric.Gauge != nil {
		return metric.Gauge.GetValue()
	}
	return metric.Counter.GetValue()
}
