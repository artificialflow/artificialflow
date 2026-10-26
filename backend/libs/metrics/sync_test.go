package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestSyncCollectorExportsBoundedProjectionMetrics(t *testing.T) {
	collector := NewSyncCollector(func() SyncSnapshot {
		return SyncSnapshot{
			Processed: 7, Succeeded: 5, Failed: 2, Retried: 3, DeadLetters: 1,
			LastProcessedAt: time.Now().Add(-2 * time.Second),
		}
	})
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(families) != 6 {
		t.Fatalf("metric families = %d, want 6", len(families))
	}
	for _, family := range families {
		if family.GetName() == "artificialflow_projection_messages_total" &&
			family.Metric[0].Counter.GetValue() != 7 {
			t.Fatalf("processed = %v, want 7", family.Metric[0].Counter.GetValue())
		}
	}
}

func TestSyncCollectorUsesMinusOneBeforeFirstMessage(t *testing.T) {
	collector := NewSyncCollector(func() SyncSnapshot { return SyncSnapshot{} })
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() == "artificialflow_projection_freshness_seconds" &&
			family.Metric[0].Gauge.GetValue() != -1 {
			t.Fatalf("freshness = %v, want -1", family.Metric[0].Gauge.GetValue())
		}
	}
}
