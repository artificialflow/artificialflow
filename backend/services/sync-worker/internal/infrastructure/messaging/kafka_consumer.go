package messaging

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/logger"
	"github.com/artificialflow/artificialflow/backend/services/sync-worker/internal/application"
	"github.com/artificialflow/artificialflow/backend/services/sync-worker/internal/domain/model"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	Brokers           []string
	GroupID           string
	Topics            []string
	DLQTopic          string
	MaxProcessRetries int
	RetryBackoff      time.Duration
	TenantMode        string
}

type KafkaConsumer struct {
	cfg       Config
	service   *application.SyncService
	readers   []*kafka.Reader
	dlqWriter *kafka.Writer
	log       *logger.Logger
	tracer    trace.Tracer

	processedCount atomic.Int64
	successCount   atomic.Int64
	failureCount   atomic.Int64
	retryCount     atomic.Int64
	dlqCount       atomic.Int64
	lastProcessed  atomic.Int64

	topicMu         sync.RWMutex
	topicStats      map[string]TopicStatsSnapshot
	processedEvents *recentEventSet
}

var errBulkItemMismatch = errors.New("bulk response item count does not match planned writes")

func NewKafkaConsumer(cfg Config, service *application.SyncService) *KafkaConsumer {
	if cfg.MaxProcessRetries < 0 {
		cfg.MaxProcessRetries = 0
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = 250 * time.Millisecond
	}
	return &KafkaConsumer{
		cfg:             cfg,
		service:         service,
		log:             logger.New("kafka-consumer"),
		tracer:          otel.Tracer("sync-worker/kafka-consumer"),
		topicStats:      make(map[string]TopicStatsSnapshot),
		processedEvents: newRecentEventSet(dedupeWindowSize),
	}
}

func (c *KafkaConsumer) Ready(ctx context.Context) error {
	if len(c.cfg.Brokers) == 0 {
		return fmt.Errorf("kafka brokers are not configured")
	}
	conn, err := kafka.DialContext(ctx, "tcp", c.cfg.Brokers[0])
	if err != nil {
		return fmt.Errorf("connect to kafka: %w", err)
	}
	return conn.Close()
}

var errConsumerNeedsRestart = errors.New("kafka consumer needs restart after empty assignment")

const (
	assignmentRecoveryTimeout = 15 * time.Second
	assignmentRecoveryBackoff = 2 * time.Second
)

func (c *KafkaConsumer) Start(ctx context.Context) error {
	if len(c.cfg.Brokers) == 0 {
		return fmt.Errorf("kafka brokers are required")
	}
	if c.cfg.GroupID == "" {
		return fmt.Errorf("kafka group id is required")
	}
	if len(c.cfg.Topics) == 0 {
		return fmt.Errorf("kafka topics are required")
	}

	if c.cfg.DLQTopic != "" {
		c.dlqWriter = &kafka.Writer{
			Addr:         kafka.TCP(c.cfg.Brokers...),
			Topic:        c.cfg.DLQTopic,
			Balancer:     &kafka.LeastBytes{},
			BatchTimeout: 25 * time.Millisecond,
		}
		defer func() {
			if err := c.dlqWriter.Close(); err != nil {
				c.log.Warn(ctx, "failed to close dlq writer", map[string]any{"error": err.Error()})
			}
		}()
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := c.runGeneration(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			return err
		}
		if !errors.Is(err, errConsumerNeedsRestart) {
			return err
		}
		c.log.Warn(ctx, "recreating kafka consumer after empty group assignment", map[string]any{
			"group":  c.cfg.GroupID,
			"topics": c.cfg.Topics,
		})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(assignmentRecoveryBackoff):
		}
	}
}

func (c *KafkaConsumer) runGeneration(ctx context.Context) error {
	generationCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	reader := kafka.NewReader(c.readerConfig())
	c.readers = []*kafka.Reader{reader}

	startProcessed := c.processedCount.Load()
	watchdogDone := make(chan struct{})
	go func() {
		timer := time.NewTimer(assignmentRecoveryTimeout)
		defer timer.Stop()
		select {
		case <-generationCtx.Done():
		case <-watchdogDone:
		case <-timer.C:
			if c.processedCount.Load() != startProcessed {
				return
			}
			hasBacklog, err := TopicsHaveBacklog(generationCtx, c.cfg.Brokers, c.cfg.Topics)
			if err != nil {
				c.log.Warn(generationCtx, "failed to inspect kafka backlog for assignment recovery", map[string]any{
					"error": err.Error(),
				})
				return
			}
			if hasBacklog {
				c.log.Warn(generationCtx, "kafka consumer has backlog but no messages processed; forcing rejoin", map[string]any{
					"group":  c.cfg.GroupID,
					"topics": c.cfg.Topics,
				})
				cancel()
			}
		}
	}()

	err := c.consumeLoop(generationCtx, reader)
	close(watchdogDone)
	if errors.Is(err, context.Canceled) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if c.processedCount.Load() == startProcessed {
			return errConsumerNeedsRestart
		}
		return nil
	}
	return err
}

func (c *KafkaConsumer) readerConfig() kafka.ReaderConfig {
	return kafka.ReaderConfig{
		Brokers:     c.cfg.Brokers,
		GroupID:     c.cfg.GroupID,
		GroupTopics: c.cfg.Topics,
		MinBytes:    1,
		MaxBytes:    10e6,
		MaxWait:     500 * time.Millisecond,
		StartOffset: kafka.FirstOffset,
		// Synchronous per-message commits cap a replica at a few hundred messages/s.
		// Periodic commits keep at-least-once delivery; replays re-apply in order.
		CommitInterval: time.Second,
		// Debezium creates CDC topics asynchronously after the connector reports
		// RUNNING. On a fresh cluster the first group assignment can therefore be
		// empty. Keep watching so topic/partition creation triggers a rebalance
		// without requiring a sync-worker restart or manual connector bootstrap.
		WatchPartitionChanges:  true,
		PartitionWatchInterval: 2 * time.Second,
	}
}

// ConsumerKafkaCarrier adapts kafka.Header to otel TextMapCarrier for extraction
type ConsumerKafkaCarrier struct {
	headers []kafka.Header
}

func (c ConsumerKafkaCarrier) Get(key string) string {
	for _, h := range c.headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func (c ConsumerKafkaCarrier) Set(key string, value string) {
	// Consumer only reads
}

func (c ConsumerKafkaCarrier) Keys() []string {
	keys := make([]string, 0, len(c.headers))
	for _, h := range c.headers {
		keys = append(keys, h.Key)
	}
	return keys
}

func (c *KafkaConsumer) consumeLoop(ctx context.Context, r *kafka.Reader) error {
	defer r.Close()

	readerTopics := r.Config().GroupTopics
	if len(readerTopics) == 0 && r.Config().Topic != "" {
		readerTopics = []string{r.Config().Topic}
	}

	c.log.Info(ctx, "consumer loop started", map[string]any{
		"topics": readerTopics,
	})

	for {
		batch, err := c.fetchBatch(ctx, r)
		if err != nil {
			// Check if context is done (graceful shutdown)
			if ctx.Err() != nil {
				return ctx.Err()
			}

			c.log.Warn(ctx, "failed to read message, retrying in 1s", map[string]any{
				"error": err.Error(),
				"topic": r.Config().Topic,
			})

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
				continue
			}
		}

		c.processBatch(ctx, batch)
		if err := r.CommitMessages(ctx, batch...); err != nil {
			last := batch[len(batch)-1]
			c.log.Warn(ctx, "failed to commit processed batch", map[string]any{
				"messages":  len(batch),
				"topic":     last.Topic,
				"partition": last.Partition,
				"offset":    last.Offset,
				"error":     err.Error(),
			})
		}
	}
}

func (c *KafkaConsumer) processMessageWithRetry(ctx context.Context, m kafka.Message) error {
	maxAttempts := c.cfg.MaxProcessRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := c.processMessageOnce(ctx, m)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt >= maxAttempts {
			break
		}
		c.retryCount.Add(1)
		c.log.Warn(ctx, "message processing failed; retrying", map[string]any{
			"topic":     m.Topic,
			"partition": m.Partition,
			"offset":    m.Offset,
			"attempt":   attempt,
			"error":     err.Error(),
		})

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.cfg.RetryBackoff):
		}
	}

	return lastErr
}

func (c *KafkaConsumer) processMessageOnce(ctx context.Context, m kafka.Message) error {
	// Extract OpenTelemetry Context from Kafka Headers
	carrier := ConsumerKafkaCarrier{headers: m.Headers}
	traceCtx := otel.GetTextMapPropagator().Extract(ctx, carrier)

	// Start Span
	spanCtx, span := c.tracer.Start(traceCtx, "process_message",
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.destination", m.Topic),
			attribute.String("messaging.destination_kind", "topic"),
			attribute.Int("messaging.kafka.partition", m.Partition),
			attribute.Int64("messaging.kafka.offset", m.Offset),
			attribute.String("messaging.operation", "process"),
		),
		trace.WithSpanKind(trace.SpanKindConsumer),
	)
	defer span.End()

	// Generate correlation ID for this message (Logger)
	correlationID := logger.GenerateMessageCorrelationID(m.Topic, m.Partition, m.Offset)
	// Combine Span Context with Logger Correlation Context
	msgCtx := logger.ContextWithCorrelationID(spanCtx, correlationID)

	start := time.Now()

	c.log.Debug(msgCtx, "received message", map[string]any{
		"topic":     m.Topic,
		"partition": m.Partition,
		"offset":    m.Offset,
		"size":      len(m.Value),
	})

	// Check for 'type' header which indicates Protobuf Event
	eventType := carrier.Get("type")

	if eventType != "" {
		tenantID, err := resolveEventTenant(carrier.Get("tenant_id"), c.cfg.TenantMode)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "tenant resolution failed")
			return err
		}
		eventID := strings.TrimSpace(carrier.Get("event_id"))
		dedupeKey := ""
		if eventID != "" {
			dedupeKey = tenantID + "\x00" + eventID
			if c.eventAlreadyProcessed(dedupeKey) {
				return nil
			}
		}
		msgCtx = auth.WithPrincipal(msgCtx, auth.Principal{TenantID: tenantID, Subject: "sync-worker"})
		// Protobuf Event Path
		c.log.Debug(msgCtx, "received protobuf event", map[string]any{
			"type":  eventType,
			"topic": m.Topic,
		})

		if err := c.service.ProcessEvent(msgCtx, eventType, m.Value); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "process event failed")
			c.log.Error(msgCtx, "process event failed", map[string]any{
				"error": err.Error(),
				"type":  eventType,
			})
			c.recordTopicOutcome(m.Topic, m.Offset, err)
			return err
		}
		if dedupeKey != "" {
			c.markEventProcessed(dedupeKey)
		}
	} else {
		// Legacy Debezium Path
		var msg model.DebeziumMessage
		if err := json.Unmarshal(m.Value, &msg); err != nil {
			errAttr := attribute.String("error.type", "unmarshal_error")
			span.RecordError(err, trace.WithAttributes(errAttr))
			span.SetStatus(codes.Error, "failed to unmarshal")

			c.log.Error(msgCtx, "failed to unmarshal debezium message", map[string]any{
				"error":     err.Error(),
				"topic":     m.Topic,
				"partition": m.Partition,
				"offset":    m.Offset,
			})
			c.recordTopicOutcome(m.Topic, m.Offset, err)
			return err
		}

		if err := c.service.ProcessMessage(msgCtx, m.Topic, msg); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "process message failed")

			c.log.Error(msgCtx, "process message failed", map[string]any{
				"error":       err.Error(),
				"topic":       m.Topic,
				"partition":   m.Partition,
				"offset":      m.Offset,
				"duration_ms": time.Since(start).Milliseconds(),
			})
			c.recordTopicOutcome(m.Topic, m.Offset, err)
			return err
		}
	}

	c.successCount.Add(1)
	c.recordTopicOutcome(m.Topic, m.Offset, nil)

	c.log.Debug(msgCtx, "message processed successfully", map[string]any{
		"topic":       m.Topic,
		"partition":   m.Partition,
		"offset":      m.Offset,
		"duration_ms": time.Since(start).Milliseconds(),
	})

	return nil
}

func resolveEventTenant(raw, mode string) (string, error) {
	if tenantID := strings.TrimSpace(raw); tenantID != "" {
		return tenantID, nil
	}
	if strings.EqualFold(strings.TrimSpace(mode), "strict") {
		return "", fmt.Errorf("event tenant_id is required in strict tenant mode")
	}
	return auth.DefaultTenantID, nil
}

func (c *KafkaConsumer) eventAlreadyProcessed(key string) bool {
	return c.processedEvents.contains(key)
}

func (c *KafkaConsumer) markEventProcessed(key string) {
	c.processedEvents.add(key)
}

func (c *KafkaConsumer) publishDLQ(ctx context.Context, m kafka.Message, processErr error) error {
	if c.dlqWriter == nil {
		return fmt.Errorf("dlq writer is not configured")
	}

	headers := make(map[string]string, len(m.Headers))
	for _, h := range m.Headers {
		headers[h.Key] = string(h.Value)
	}

	payload := map[string]any{
		"failedAt":        time.Now().UTC().Format(time.RFC3339Nano),
		"sourceTopic":     m.Topic,
		"sourcePartition": m.Partition,
		"sourceOffset":    m.Offset,
		"error":           processErr.Error(),
		"keyBase64":       base64.StdEncoding.EncodeToString(m.Key),
		"valueBase64":     base64.StdEncoding.EncodeToString(m.Value),
		"headers":         headers,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal dlq payload: %w", err)
	}

	dlqMsg := kafka.Message{
		Key:   []byte(fmt.Sprintf("%s:%d:%d", m.Topic, m.Partition, m.Offset)),
		Value: body,
		Headers: []kafka.Header{
			{Key: "source_topic", Value: []byte(m.Topic)},
			{Key: "source_partition", Value: []byte(fmt.Sprintf("%d", m.Partition))},
			{Key: "source_offset", Value: []byte(fmt.Sprintf("%d", m.Offset))},
			{Key: "error", Value: []byte(strings.TrimSpace(processErr.Error()))},
		},
	}

	if err := c.dlqWriter.WriteMessages(ctx, dlqMsg); err != nil {
		return fmt.Errorf("write dlq message: %w", err)
	}

	c.dlqCount.Add(1)
	c.log.Warn(ctx, "message moved to dlq", map[string]any{
		"dlq_topic": c.cfg.DLQTopic,
		"topic":     m.Topic,
		"partition": m.Partition,
		"offset":    m.Offset,
		"error":     processErr.Error(),
	})

	return nil
}

func (c *KafkaConsumer) recordTopicOutcome(topic string, offset int64, err error) {
	now := time.Now().UTC()
	c.topicMu.Lock()
	defer c.topicMu.Unlock()

	entry := c.topicStats[topic]
	entry.LastOffset = offset
	entry.LastProcessedAt = now
	if err != nil {
		entry.LastError = err.Error()
	} else {
		entry.LastError = ""
	}
	c.topicStats[topic] = entry
}

func (c *KafkaConsumer) Snapshot() ConsumerStatsSnapshot {
	last := c.lastProcessed.Load()
	lastProcessedAt := time.Time{}
	if last > 0 {
		lastProcessedAt = time.Unix(0, last).UTC()
	}

	c.topicMu.RLock()
	topics := make(map[string]TopicStatsSnapshot, len(c.topicStats))
	for topic, stats := range c.topicStats {
		topics[topic] = stats
	}
	c.topicMu.RUnlock()

	return ConsumerStatsSnapshot{
		Processed:       c.processedCount.Load(),
		Succeeded:       c.successCount.Load(),
		Failed:          c.failureCount.Load(),
		Retried:         c.retryCount.Load(),
		DLQPublished:    c.dlqCount.Load(),
		LastProcessedAt: lastProcessedAt,
		Topics:          topics,
	}
}
