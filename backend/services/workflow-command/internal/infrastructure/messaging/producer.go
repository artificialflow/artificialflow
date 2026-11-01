package messaging

import (
	"context"
	"errors"
	"fmt"
	workflowapi "github.com/artificialflow/artificialflow/backend/api/v1/go"
	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/id"
	"time"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

type EventPublisher interface {
	Publish(ctx context.Context, event proto.Message, eventType string) error
	Close() error
}

type EventEnvelope struct {
	EventID       string
	TenantID      string
	EventType     string
	AggregateType string
	AggregateID   string
	SchemaVersion int
	OccurredAt    time.Time
	TraceContext  map[string]string
}

type EnvelopePublisher interface {
	PublishEnvelope(ctx context.Context, event proto.Message, envelope EventEnvelope) error
}

type EnvelopeMessage struct {
	Event    proto.Message
	Envelope EventEnvelope
}

// BatchEnvelopePublisher returns one error slot per input message, in order.
type BatchEnvelopePublisher interface {
	PublishEnvelopes(ctx context.Context, messages []EnvelopeMessage) []error
}

type KafkaPublisher struct {
	writer  *kafka.Writer
	brokers []string
}

func NewKafkaPublisher(brokers []string, topic string) *KafkaPublisher {
	writer := &kafka.Writer{
		Addr:  kafka.TCP(brokers...),
		Topic: topic,
		// Per-aggregate ordering across partitions depends on key hashing.
		Balancer: &kafka.Hash{},
		// The outbox marks rows published on success, so writes must be acknowledged.
		RequiredAcks: kafka.RequireAll,
		BatchSize:    1000,
		BatchTimeout: 10 * time.Millisecond,
	}
	return &KafkaPublisher{writer: writer, brokers: append([]string(nil), brokers...)}
}

func (p *KafkaPublisher) Ready(ctx context.Context) error {
	if len(p.brokers) == 0 {
		return fmt.Errorf("kafka brokers are not configured")
	}
	conn, err := kafka.DialContext(ctx, "tcp", p.brokers[0])
	if err != nil {
		return fmt.Errorf("connect to kafka: %w", err)
	}
	return conn.Close()
}

func (p *KafkaPublisher) Publish(ctx context.Context, event proto.Message, eventType string) error {
	return p.PublishEnvelope(ctx, event, newEventEnvelope(ctx, event, eventType))
}

func newEventEnvelope(ctx context.Context, event proto.Message, eventType string) EventEnvelope {
	aggregateType, aggregateID := AggregateIdentity(event)
	return EventEnvelope{
		EventID: id.GenerateUUIDv7(), TenantID: auth.TenantIDFromContext(ctx), EventType: eventType,
		AggregateType: aggregateType, AggregateID: aggregateID, SchemaVersion: 1, OccurredAt: time.Now().UTC(),
	}
}

func (p *KafkaPublisher) PublishEnvelope(ctx context.Context, event proto.Message, envelope EventEnvelope) error {
	return p.PublishEnvelopes(ctx, []EnvelopeMessage{{Event: event, Envelope: envelope}})[0]
}

func (p *KafkaPublisher) PublishEnvelopes(ctx context.Context, messages []EnvelopeMessage) []error {
	errs := make([]error, len(messages))
	batch := make([]kafka.Message, 0, len(messages))
	positions := make([]int, 0, len(messages))
	spans := make([]trace.Span, 0, len(messages))
	for i, m := range messages {
		msg, span, err := buildKafkaMessage(ctx, m.Event, m.Envelope)
		if err != nil {
			errs[i] = err
			continue
		}
		batch = append(batch, msg)
		positions = append(positions, i)
		spans = append(spans, span)
	}
	if len(batch) == 0 {
		return errs
	}

	writeErr := p.writer.WriteMessages(ctx, batch...)
	var perMessage kafka.WriteErrors
	hasPerMessage := errors.As(writeErr, &perMessage) && len(perMessage) == len(batch)
	for j, span := range spans {
		err := writeErr
		if hasPerMessage {
			err = perMessage[j]
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "publish failed")
			errs[positions[j]] = err
		}
		span.End()
	}
	return errs
}

func buildKafkaMessage(ctx context.Context, event proto.Message, envelope EventEnvelope) (kafka.Message, trace.Span, error) {
	parentCtx := otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(envelope.TraceContext))
	publishCtx, span := otel.Tracer("workflow-command/kafka-publisher").Start(parentCtx, "publish_event",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.operation", "publish"),
		))
	data, err := proto.Marshal(event)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "marshal event failed")
		span.End()
		return kafka.Message{}, nil, fmt.Errorf("failed to marshal event: %w", err)
	}

	aggregateID := envelope.AggregateID
	if aggregateID == "" {
		aggregateID = "event:" + envelope.EventType
	}
	headers := []kafka.Header{
		{Key: "type", Value: []byte(envelope.EventType)},
		{Key: "event_id", Value: []byte(envelope.EventID)},
		{Key: "tenant_id", Value: []byte(envelope.TenantID)},
		{Key: "aggregate_type", Value: []byte(envelope.AggregateType)},
		{Key: "aggregate_id", Value: []byte(aggregateID)},
		{Key: "occurred_at", Value: []byte(envelope.OccurredAt.UTC().Format(time.RFC3339Nano))},
		{Key: "schema_version", Value: []byte(fmt.Sprintf("%d", envelope.SchemaVersion))},
		{Key: "source", Value: []byte("workflow-command")},
	}
	traceContext := propagation.MapCarrier{}
	for key, value := range envelope.TraceContext {
		traceContext[key] = value
	}
	otel.GetTextMapPropagator().Inject(publishCtx, traceContext)
	for key, value := range traceContext {
		headers = append(headers, kafka.Header{Key: key, Value: []byte(value)})
	}
	return kafka.Message{Key: []byte(aggregateID), Value: data, Headers: headers}, span, nil
}

func AggregateIdentity(event proto.Message) (string, string) {
	switch e := event.(type) {
	case *workflowapi.ProcessInstanceCreated:
		return "process-instance", fmt.Sprintf("%d", e.Key)
	case *workflowapi.ProcessInstanceCompleted:
		return "process-instance", fmt.Sprintf("%d", e.Key)
	case *workflowapi.ProcessInstanceTerminated:
		return "process-instance", fmt.Sprintf("%d", e.Key)
	case *workflowapi.ElementInstanceActivated:
		if e.ProcessInstanceKey > 0 {
			return "process-instance", fmt.Sprintf("%d", e.ProcessInstanceKey)
		}
		return "element-instance", fmt.Sprintf("%d", e.Key)
	case *workflowapi.ElementInstanceCompleted:
		if e.ProcessInstanceKey > 0 {
			return "process-instance", fmt.Sprintf("%d", e.ProcessInstanceKey)
		}
		return "element-instance", fmt.Sprintf("%d", e.Key)
	case *workflowapi.ElementInstanceTerminated:
		if e.ProcessInstanceKey > 0 {
			return "process-instance", fmt.Sprintf("%d", e.ProcessInstanceKey)
		}
		return "element-instance", fmt.Sprintf("%d", e.Key)
	case *workflowapi.JobCreated:
		if e.ProcessInstanceKey > 0 {
			return "process-instance", fmt.Sprintf("%d", e.ProcessInstanceKey)
		}
		return "job", fmt.Sprintf("%d", e.Key)
	case *workflowapi.JobActivated:
		return "job", fmt.Sprintf("%d", e.Key)
	case *workflowapi.JobCompleted:
		return "job", fmt.Sprintf("%d", e.Key)
	case *workflowapi.JobFailed:
		return "job", fmt.Sprintf("%d", e.Key)
	case *workflowapi.VariableUpdated:
		if e.ProcessInstanceKey > 0 {
			return "process-instance", fmt.Sprintf("%d", e.ProcessInstanceKey)
		}
		return "scope", fmt.Sprintf("%d", e.ScopeKey)
	case *workflowapi.IncidentCreated:
		if e.ProcessInstanceKey > 0 {
			return "process-instance", fmt.Sprintf("%d", e.ProcessInstanceKey)
		}
		return "incident", fmt.Sprintf("%d", e.Key)
	case *workflowapi.IncidentResolved:
		if e.ProcessInstanceKey > 0 {
			return "process-instance", fmt.Sprintf("%d", e.ProcessInstanceKey)
		}
		return "incident", fmt.Sprintf("%d", e.Key)
	default:
		return "event", ""
	}
}

func aggregateIDForEvent(event proto.Message) string {
	aggregateType, aggregateID := AggregateIdentity(event)
	if aggregateID == "" {
		return ""
	}
	return aggregateType + ":" + aggregateID
}

func (p *KafkaPublisher) Close() error {
	return p.writer.Close()
}

// NoOpPublisher for tests
type NoOpPublisher struct{}

func (p *NoOpPublisher) Publish(ctx context.Context, event proto.Message, eventType string) error {
	return nil
}

func (p *NoOpPublisher) Close() error {
	return nil
}
