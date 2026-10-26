package messaging

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/services/sync-worker/internal/domain/repository"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	maxBatchMessages = 500
	// Bounds the extra latency a lone message waits for companions.
	batchFillWindow = 20 * time.Millisecond
	// Redeliveries arrive within a rebalance or retry window, so a bounded
	// recent-ID set catches them without growing for the life of the process.
	dedupeWindowSize = 200_000
)

type recentEventSet struct {
	mu   sync.Mutex
	keys map[string]struct{}
	ring []string
	next int
}

func newRecentEventSet(capacity int) *recentEventSet {
	return &recentEventSet{keys: make(map[string]struct{}, capacity), ring: make([]string, capacity)}
}

func (s *recentEventSet) contains(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.keys[key]
	return ok
}

func (s *recentEventSet) add(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[key]; ok {
		return
	}
	if evicted := s.ring[s.next]; evicted != "" {
		delete(s.keys, evicted)
	}
	s.ring[s.next] = key
	s.keys[key] = struct{}{}
	s.next = (s.next + 1) % len(s.ring)
}

type plannedMessage struct {
	msg       kafka.Message
	ops       []repository.WriteOp
	dedupeKey string
}

// fetchBatch blocks for the first message, then drains whatever arrives within
// batchFillWindow. A timed-out FetchMessage leaves unread messages in the reader.
func (c *KafkaConsumer) fetchBatch(ctx context.Context, r *kafka.Reader) ([]kafka.Message, error) {
	first, err := r.FetchMessage(ctx)
	if err != nil {
		return nil, err
	}
	batch := []kafka.Message{first}
	fillCtx, cancel := context.WithTimeout(ctx, batchFillWindow)
	defer cancel()
	for len(batch) < maxBatchMessages {
		m, err := r.FetchMessage(fillCtx)
		if err != nil {
			break
		}
		batch = append(batch, m)
	}
	return batch, nil
}

// processBatch projects protobuf events through one bulk write per run of
// plannable messages, preserving fetch order. Anything that cannot be planned
// or fails in bulk goes through the per-message retry and DLQ path.
func (c *KafkaConsumer) processBatch(ctx context.Context, batch []kafka.Message) {
	pending := make([]plannedMessage, 0, len(batch))
	for _, m := range batch {
		if len(m.Value) == 0 {
			continue
		}
		c.processedCount.Add(1)
		c.lastProcessed.Store(time.Now().UnixNano())

		if planned, ok := c.planMessage(ctx, m); ok {
			pending = append(pending, planned)
			continue
		}
		c.flushPlanned(ctx, pending)
		pending = pending[:0]
		c.processSingle(ctx, m)
	}
	c.flushPlanned(ctx, pending)
}

func (c *KafkaConsumer) planMessage(ctx context.Context, m kafka.Message) (plannedMessage, bool) {
	carrier := ConsumerKafkaCarrier{headers: m.Headers}
	eventType := carrier.Get("type")
	if eventType == "" {
		return plannedMessage{}, false
	}
	tenantID, err := resolveEventTenant(carrier.Get("tenant_id"), c.cfg.TenantMode)
	if err != nil {
		return plannedMessage{}, false
	}
	planned := plannedMessage{msg: m}
	if eventID := strings.TrimSpace(carrier.Get("event_id")); eventID != "" {
		key := tenantID + "\x00" + eventID
		if c.eventAlreadyProcessed(key) {
			return planned, true
		}
		planned.dedupeKey = key
	}
	msgCtx := auth.WithPrincipal(ctx, auth.Principal{TenantID: tenantID, Subject: "sync-worker"})
	ops, err := c.service.PlanEvent(msgCtx, eventType, m.Value)
	if err != nil {
		return plannedMessage{}, false
	}
	planned.ops = ops
	return planned, true
}

func (c *KafkaConsumer) flushPlanned(ctx context.Context, planned []plannedMessage) {
	if len(planned) == 0 {
		return
	}
	ops := make([]repository.WriteOp, 0, len(planned)*2)
	for _, p := range planned {
		ops = append(ops, p.ops...)
	}

	spanCtx, span := c.tracer.Start(ctx, "process_batch",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.operation", "process"),
			attribute.Int("messaging.batch.message_count", len(planned)),
			attribute.Int("artificialflow.projection.write_count", len(ops)),
		))
	defer span.End()

	items, err := c.service.ApplyWrites(spanCtx, ops)
	if err == nil && len(items) != len(ops) {
		err = errBulkItemMismatch
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "bulk projection write failed")
		c.log.Warn(spanCtx, "bulk projection write failed; falling back to per-message processing", map[string]any{
			"messages": len(planned),
			"error":    err.Error(),
		})
		for _, p := range planned {
			c.processSingle(spanCtx, p.msg)
		}
		return
	}

	offset := 0
	for _, p := range planned {
		results := items[offset : offset+len(p.ops)]
		offset += len(p.ops)
		failed := false
		for _, item := range results {
			if item.Error != nil {
				failed = true
				break
			}
		}
		if failed {
			c.processSingle(spanCtx, p.msg)
			continue
		}
		if p.dedupeKey != "" {
			c.markEventProcessed(p.dedupeKey)
		}
		c.successCount.Add(1)
		c.recordTopicOutcome(p.msg.Topic, p.msg.Offset, nil)
	}
}

func (c *KafkaConsumer) processSingle(ctx context.Context, m kafka.Message) {
	processErr := c.processMessageWithRetry(ctx, m)
	if processErr == nil {
		return
	}
	c.failureCount.Add(1)
	c.recordTopicOutcome(m.Topic, m.Offset, processErr)
	if err := c.publishDLQ(ctx, m, processErr); err != nil {
		c.log.Error(ctx, "failed to publish message to dlq", map[string]any{
			"topic":     m.Topic,
			"partition": m.Partition,
			"offset":    m.Offset,
			"error":     err.Error(),
		})
	}
}
