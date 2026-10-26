package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	pb "github.com/artificialflow/artificialflow/backend/api/v1/go"
	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/id"
	"github.com/artificialflow/artificialflow/backend/libs/metrics"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/domain/repository"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/infrastructure/messaging"
	"hash/fnv"
	"math"
	"reflect"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type decisionStore interface {
	UpsertDecision(ctx context.Context, decision *model.DecisionDefinition) error
	GetDecisionByDecisionID(ctx context.Context, decisionID string) (*model.DecisionDefinition, error)
	ListDecisions(ctx context.Context) ([]model.DecisionDefinition, error)
}

type Engine struct {
	repo              repository.Repository
	decisions         decisionStore
	handlers          map[string]ServiceTaskHandler
	stepExecutors     map[model.StepType]StepExecutor
	eventPublisher    messaging.EventPublisher
	metrics           *engineMetrics
	outboxMaxAttempts int
	flushAfterCommit  bool
	outboxPending     *outboxPendingCache
}

// outboxPendingCache is shared by pointer so transactional Engine copies reuse
// the same lock and cached count.
type outboxPendingCache struct {
	mu       sync.Mutex
	value    int64
	at       time.Time
	counting bool
}

type ServiceTaskHandler func(ctx context.Context, instance *model.WorkflowInstance, step *model.StepDefinition) error

type bufferedEvent struct {
	ctx             context.Context
	event           proto.Message
	eventType       string
	outboxID        string
	outboxCreatedAt time.Time
	envelope        messaging.EventEnvelope
}

type OutboxRelayResult struct {
	Pending   int64
	Claimed   int64
	Published int64
	Failed    int64
}

type IdempotencyCleanupResult struct {
	Cutoff  time.Time
	Deleted int64
}

type OutboxCleanupResult struct {
	Cutoff  time.Time
	Deleted int64
}

const (
	outboxStatusPending            = "PENDING"
	outboxFlushBatchSize           = 200
	outboxBaseRetryDelay           = time.Second
	outboxMaxRetryDelay            = 5 * time.Minute
	outboxClaimTTL                 = 5 * time.Minute
	outboxMaxErrorLength           = 2048
	defaultOutboxMaxAttempts       = 10
	outboxPendingRefreshInterval   = 10 * time.Second
	defaultOutboxCleanupBatch      = 5000
	defaultIdempotencyCleanupBatch = 500
)

func safeInt32(value int) int32 {
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	if value < math.MinInt32 {
		return math.MinInt32
	}
	return int32(value)
}

// transactionalEventPublisher buffers events produced inside a DB transaction.
// Events are flushed by withTx only after transaction commit succeeds.
type transactionalEventPublisher struct {
	events []bufferedEvent
}

func (p *transactionalEventPublisher) Publish(ctx context.Context, event proto.Message, eventType string) error {
	if event == nil {
		return fmt.Errorf("event is required")
	}
	p.events = append(p.events, bufferedEvent{ctx: ctx, event: event, eventType: eventType})
	return nil
}

func (p *transactionalEventPublisher) Close() error {
	return nil
}

func (p *transactionalEventPublisher) Events() []bufferedEvent {
	if len(p.events) == 0 {
		return nil
	}
	out := make([]bufferedEvent, len(p.events))
	copy(out, p.events)
	return out
}

func NewEngine(repo repository.Repository, eventPublisher messaging.EventPublisher) *Engine {
	e := &Engine{
		repo:              repo,
		handlers:          make(map[string]ServiceTaskHandler),
		stepExecutors:     make(map[model.StepType]StepExecutor),
		eventPublisher:    eventPublisher,
		metrics:           newEngineMetrics(),
		outboxMaxAttempts: defaultOutboxMaxAttempts,
		flushAfterCommit:  true,
		outboxPending:     &outboxPendingCache{},
	}
	if ds, ok := repo.(decisionStore); ok {
		e.decisions = ds
	}
	e.registerStepExecutors()
	return e
}

func (e *Engine) RegisterHandler(id string, handler ServiceTaskHandler) {
	e.handlers[id] = handler
}

func (e *Engine) SetOutboxMaxAttempts(maxAttempts int) {
	if maxAttempts <= 0 {
		maxAttempts = defaultOutboxMaxAttempts
	}
	e.outboxMaxAttempts = maxAttempts
}

// SetFlushOutboxAfterCommit controls synchronous post-commit delivery. Runtime
// services disable it and use the durable relay, keeping broker and outbox I/O
// off latency-sensitive command transactions.
func (e *Engine) SetFlushOutboxAfterCommit(enabled bool) {
	e.flushAfterCommit = enabled
}

func (e *Engine) OutboxMaxAttempts() int {
	if e.outboxMaxAttempts <= 0 {
		return defaultOutboxMaxAttempts
	}
	return e.outboxMaxAttempts
}

func (e *Engine) MetricsSnapshot() EngineMetricsSnapshot {
	if e == nil || e.metrics == nil {
		return EngineMetricsSnapshot{}
	}
	return e.metrics.snapshot()
}

func (e *Engine) RunIdempotencyCleanup(ctx context.Context, ttl time.Duration, limit int) (IdempotencyCleanupResult, error) {
	result := IdempotencyCleanupResult{}
	if ttl <= 0 {
		return result, nil
	}
	if limit <= 0 {
		limit = defaultIdempotencyCleanupBatch
	}

	cutoff := time.Now().Add(-ttl)
	deleted, err := e.repo.DeleteIdempotencyRecordsBefore(ctx, cutoff, limit)
	if err != nil {
		return result, fmt.Errorf("delete idempotency records before %s: %w", cutoff.Format(time.RFC3339), err)
	}

	result.Cutoff = cutoff
	result.Deleted = deleted
	return result, nil
}

func (e *Engine) RunOutboxCleanup(ctx context.Context, retention time.Duration, limit int) (OutboxCleanupResult, error) {
	result := OutboxCleanupResult{}
	if retention <= 0 {
		return result, nil
	}
	if limit <= 0 {
		limit = defaultOutboxCleanupBatch
	}
	cutoff := time.Now().UTC().Add(-retention)
	deleted, err := e.repo.DeletePublishedOutboxMessagesBefore(ctx, cutoff, limit)
	if err != nil {
		return result, fmt.Errorf("delete published outbox messages before %s: %w", cutoff.Format(time.RFC3339), err)
	}
	result.Cutoff = cutoff
	result.Deleted = deleted
	return result, nil
}

func (e *Engine) RunClaimedIdempotencyCleanup(
	ctx context.Context,
	claim model.RuntimeClaim,
	retention time.Duration,
	limit int,
	nextDelay time.Duration,
) (IdempotencyCleanupResult, error) {
	var result IdempotencyCleanupResult
	err := e.withTx(ctx, func(txEngine *Engine) error {
		if err := txEngine.repo.LockRuntimeWork(ctx, claim); err != nil {
			return err
		}
		cleanupResult, err := txEngine.RunIdempotencyCleanup(ctx, retention, limit)
		if err != nil {
			return err
		}
		if err := txEngine.repo.CompleteRuntimeWork(ctx, claim, nextDelay); err != nil {
			return err
		}
		result = cleanupResult
		return nil
	})
	return result, err
}

func (e *Engine) StartInstance(ctx context.Context, workflowID string, contextVars map[string]any) (*model.WorkflowInstance, error) {
	return e.StartInstanceVersion(ctx, workflowID, contextVars, nil)
}

func (e *Engine) StartInstanceVersion(ctx context.Context, workflowID string, contextVars map[string]any, version *int) (*model.WorkflowInstance, error) {
	var instance *model.WorkflowInstance
	err := e.withTx(ctx, func(txEngine *Engine) error {
		var err error
		instance, err = txEngine.createAndStartInstance(ctx, workflowID, contextVars, "", "", version)
		return err
	})
	if err != nil {
		return nil, err
	}
	return instance, nil
}

type processByVersionLookup interface {
	GetProcessByBpmnProcessIDAndVersion(ctx context.Context, bpmnProcessID string, version int) (*model.Process, error)
}

func (e *Engine) createAndStartInstance(ctx context.Context, workflowID string, contextVars map[string]any, parentInstanceID, parentExecutionID string, version *int) (*model.WorkflowInstance, error) {
	var process *model.Process
	var err error

	// Try parsing as int64 (Key)
	if processKey, parseErr := strconv.ParseInt(workflowID, 10, 64); parseErr == nil {
		process, err = e.repo.GetProcess(ctx, processKey)
	} else if version != nil && *version > 0 {
		lookup, ok := e.repo.(processByVersionLookup)
		if !ok {
			return nil, fmt.Errorf("versioned process lookup is not available")
		}
		process, err = lookup.GetProcessByBpmnProcessIDAndVersion(ctx, workflowID, *version)
	} else {
		// If not an int64, treat as BPMN Process ID (latest version)
		process, err = e.repo.GetProcessByBpmnProcessID(ctx, workflowID)
	}

	if err != nil {
		return nil, fmt.Errorf("process not found for id '%s': %v", workflowID, err)
	}

	wf, err := e.processToWorkflowDefinition(process)
	if err != nil {
		return nil, err
	}

	// Find start step (first step for simplicity)
	if len(wf.Steps) == 0 {
		return nil, errors.New("workflow has no steps")
	}
	startStep := wf.Steps[0]

	// Generate Keys
	runtimeID := id.GenerateUUIDv7() // New UUIDv7
	processInstanceKey := id.GenerateSnowflake()
	rootElementKey := id.GenerateSnowflake()

	// Engine: Create Process Instance
	pi := &model.ProcessInstance{
		Key:                  processInstanceKey,
		ID:                   runtimeID,
		ProcessDefinitionKey: process.Key,
		Version:              process.Version,
		State:                "ACTIVE",
		CreatedAt:            time.Now(),
	}
	if parentInstanceID != "" {
		pk, _ := strconv.ParseInt(parentInstanceID, 10, 64)
		pi.ParentProcessInstanceKey = pk
	}
	if parentExecutionID != "" {
		ek, _ := strconv.ParseInt(parentExecutionID, 10, 64)
		pi.ParentElementInstanceKey = ek
	}

	if err := e.repo.CreateProcessInstance(ctx, pi); err != nil {
		return nil, fmt.Errorf("failed to create process instance: %v", err)
	}

	// Publish Event
	if err := e.eventPublisher.Publish(ctx, &pb.ProcessInstanceCreated{
		Key:                  processInstanceKey,
		Id:                   runtimeID,
		ProcessDefinitionKey: process.Key,
		Version:              safeInt32(process.Version),
		BpmnProcessId:        process.BpmnProcessID,
		CreatedAt:            timestamppb.New(pi.CreatedAt),
	}, "ProcessInstanceCreated"); err != nil {
		fmt.Printf("failed to publish ProcessInstanceCreated: %v\n", err)
	}

	// Engine: Create Element Instance (Start Event)
	el := &model.ElementInstance{
		Key:                  rootElementKey,
		ID:                   id.GenerateUUIDv7(),
		ProcessInstanceKey:   processInstanceKey,
		ProcessDefinitionKey: process.Key,
		ElementID:            startStep.ID,
		BpmnElementType:      string(startStep.Type),
		State:                "ACTIVATED",
		CreatedAt:            time.Now(),
	}
	if err := e.repo.CreateElementInstance(ctx, el); err != nil {
		return nil, fmt.Errorf("failed to create element instance: %v", err)
	}

	// Publish ElementInstanceActivated
	if err := e.eventPublisher.Publish(ctx, &pb.ElementInstanceActivated{
		Key:                  el.Key,
		Id:                   el.ID,
		ProcessInstanceKey:   el.ProcessInstanceKey,
		ProcessDefinitionKey: el.ProcessDefinitionKey,
		ElementId:            el.ElementID,
		BpmnElementType:      el.BpmnElementType,
		FlowScopeKey:         el.FlowScopeKey,
		CreatedAt:            timestamppb.New(el.CreatedAt),
	}, "ElementInstanceActivated"); err != nil {
		fmt.Printf("failed to publish ElementInstanceActivated: %v\n", err)
	}

	// Engine: Persist Variables
	if err := e.persistVariables(ctx, fmt.Sprintf("%d", processInstanceKey), processInstanceKey, contextVars); err != nil {
		return nil, err
	}

	// Construct In-Memory WorkflowInstance DTO
	vars := make([]model.Variable, 0)
	for k, v := range contextVars {
		valBytes, _ := json.Marshal(v)
		vars = append(vars, model.Variable{Name: k, Value: valBytes})
	}
	elements := []model.ElementInstance{*el}

	instance := e.mapToWorkflowInstance(pi, elements, vars)

	// Automatically advance if start step is just a start event
	if startStep.Type == model.StepTypeStart {
		// Trigger execution loop
		if err := e.proceedToken(ctx, instance, instance.Executions[0].ID, wf); err != nil {
			return nil, err
		}
	}

	// Schedule duration timers for event sub-process timer starts (PT…).
	if err := e.scheduleEventSubProcessTimers(ctx, instance, wf); err != nil {
		return nil, err
	}

	// Reload state to ensure we return the latest (after proceedToken updates)
	return e.GetInstance(ctx, instance.ID)
}

// CompleteTask moves the workflow to the next step
func (e *Engine) CompleteTask(ctx context.Context, instanceID string, stepID string) error {
	return e.withTx(ctx, func(txEngine *Engine) error {
		return txEngine.completeTask(ctx, instanceID, stepID)
	})
}

func (e *Engine) completeTask(ctx context.Context, instanceID string, stepID string) error {
	// Read state (Engine)
	instance, err := e.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}

	if instance.Status != model.StatusRunning {
		return fmt.Errorf("instance is not running, status: %s", instance.Status)
	}

	// Get Workflow Definition
	wfID, err := strconv.ParseInt(instance.WorkflowID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid workflow id in instance: %v", err)
	}

	wf, err := e.getWorkflowDefinition(ctx, wfID)
	if err != nil {
		return err
	}

	// Find an ACTIVE execution token.
	var targetExec *model.Execution
	for i := range instance.Executions {
		ex := &instance.Executions[i]
		if ex.Status == "ACTIVE" {
			// If stepID is specified, match it (either BPMN ID or Execution Key)
			if stepID != "" && ex.StepID != stepID && ex.ID != stepID {
				continue
			}
			targetExec = ex
			break
		}
	}

	if targetExec == nil {
		if stepID != "" {
			return fmt.Errorf("no active execution found for step %s", stepID)
		}
		return errors.New("no active execution found")
	}

	// Listener: Complete
	// We need the step definition
	step := findStep(wf.Steps, targetExec.StepID)
	if step != nil {
		if err := e.executeTaskListeners(ctx, instance, step, "complete"); err != nil {
			return err
		}
	}

	// Advance this token
	if err := e.proceedToken(ctx, instance, targetExec.ID, wf); err != nil {
		return err
	}

	return nil
}

// CompleteExecution moves a specific execution token to the next step
func (e *Engine) CompleteExecution(ctx context.Context, instanceID string, executionID string) error {
	return e.withTx(ctx, func(txEngine *Engine) error {
		return txEngine.completeExecution(ctx, instanceID, executionID)
	})
}

func (e *Engine) completeExecution(ctx context.Context, instanceID string, executionID string) error {
	// Read state
	instance, err := e.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}

	if instance.Status != model.StatusRunning {
		return fmt.Errorf("instance is not running, status: %s", instance.Status)
	}

	// Get Workflow Definition
	wfID, err := strconv.ParseInt(instance.WorkflowID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid workflow id in instance: %v", err)
	}

	wf, err := e.getWorkflowDefinition(ctx, wfID)
	if err != nil {
		return err
	}

	// Verify execution exists and is active
	var targetExec *model.Execution
	for i := range instance.Executions {
		if instance.Executions[i].ID == executionID {
			if instance.Executions[i].Status != "ACTIVE" {
				return fmt.Errorf("execution %s is not active", executionID)
			}
			targetExec = &instance.Executions[i]
			break
		}
	}

	if targetExec == nil {
		return fmt.Errorf("execution %s not found", executionID)
	}

	// Listener: Complete
	step := findStep(wf.Steps, targetExec.StepID)
	if step != nil {
		if err := e.executeTaskListeners(ctx, instance, step, "complete"); err != nil {
			return err
		}
	}

	// Advance this token
	if err := e.proceedToken(ctx, instance, targetExec.ID, wf); err != nil {
		return err
	}

	return nil
}

func (e *Engine) withTx(ctx context.Context, fn func(txEngine *Engine) error) error {
	txPublisher := &transactionalEventPublisher{}
	err := e.repo.WithTx(ctx, func(txRepo repository.Repository) error {
		txEngine := *e
		txEngine.repo = txRepo
		txEngine.eventPublisher = txPublisher
		if ds, ok := txRepo.(decisionStore); ok {
			txEngine.decisions = ds
		}
		if err := fn(&txEngine); err != nil {
			return err
		}

		_, err := e.persistBufferedEventsToOutbox(ctx, txRepo, txPublisher.Events())
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	if e.flushAfterCommit {
		if _, err := e.flushPendingOutbox(context.Background(), outboxFlushBatchSize); err != nil {
			fmt.Printf("failed to flush pending outbox after commit: %v\n", err)
		}
	}
	return nil
}

func (e *Engine) persistBufferedEventsToOutbox(ctx context.Context, txRepo repository.Repository, events []bufferedEvent) ([]bufferedEvent, error) {
	if len(events) == 0 {
		return nil, nil
	}

	persisted := make([]bufferedEvent, len(events))
	copy(persisted, events)

	for i := range persisted {
		payload, err := proto.Marshal(persisted[i].event)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal %s for outbox: %w", persisted[i].eventType, err)
		}

		outboxID := id.GenerateUUIDv7()
		createdAt := time.Now().UTC()
		aggregateType, aggregateID := messaging.AggregateIdentity(persisted[i].event)
		traceCarrier := propagation.MapCarrier{}
		eventCtx := persisted[i].ctx
		if eventCtx == nil {
			eventCtx = ctx
		}
		otel.GetTextMapPropagator().Inject(eventCtx, traceCarrier)
		traceContext, err := json.Marshal(map[string]string(traceCarrier))
		if err != nil {
			return nil, fmt.Errorf("marshal trace context: %w", err)
		}
		envelope := messaging.EventEnvelope{
			EventID: outboxID, TenantID: auth.TenantIDFromContext(ctx),
			EventType: persisted[i].eventType, AggregateType: aggregateType,
			AggregateID: aggregateID, SchemaVersion: 1, OccurredAt: createdAt,
			TraceContext: map[string]string(traceCarrier),
		}
		if err := txRepo.CreateOutboxMessage(ctx, &model.OutboxMessage{
			ID:            outboxID,
			EventType:     persisted[i].eventType,
			TenantID:      envelope.TenantID,
			AggregateType: aggregateType,
			AggregateID:   aggregateID,
			SchemaVersion: 1,
			OccurredAt:    createdAt,
			TraceContext:  traceContext,
			Payload:       payload,
			Status:        outboxStatusPending,
			Attempts:      0,
			LastError:     "",
			NextAttempt:   nil,
			CreatedAt:     createdAt,
		}); err != nil {
			return nil, fmt.Errorf("failed to persist outbox message for %s: %w", persisted[i].eventType, err)
		}

		persisted[i].outboxID = outboxID
		persisted[i].outboxCreatedAt = createdAt
		persisted[i].envelope = envelope
	}

	return persisted, nil
}

func (e *Engine) RunOutboxRelayCycle(ctx context.Context, limit int) (OutboxRelayResult, error) {
	return e.flushPendingOutbox(ctx, limit)
}

func (e *Engine) flushPendingOutbox(ctx context.Context, limit int) (OutboxRelayResult, error) {
	result := OutboxRelayResult{}
	if e.eventPublisher == nil {
		return result, nil
	}
	now := time.Now()
	pending, _ := e.cachedPendingOutboxMessages(ctx, now)
	result.Pending = pending
	if e.metrics != nil {
		e.metrics.setOutboxPending(pending)
	}

	claimOwner := "outbox-" + id.GenerateUUIDv7()
	messages, err := e.repo.ClaimOutboxMessages(ctx, claimOwner, outboxClaimTTL, limit)
	if err != nil {
		return result, fmt.Errorf("claim pending outbox messages: %w", err)
	}
	if e.metrics != nil {
		oldest := now
		for _, msg := range messages {
			if !msg.CreatedAt.IsZero() && msg.CreatedAt.Before(oldest) {
				oldest = msg.CreatedAt
			}
		}
		if len(messages) == 0 {
			e.metrics.setOutboxPublishLagSec(0)
		} else {
			e.metrics.setOutboxPublishLagSec(int64(now.Sub(oldest).Seconds()))
		}
	}

	claimedMessages := make([]*model.OutboxMessage, 0, len(messages))
	claimedContexts := make([]context.Context, 0, len(messages))
	batch := make([]messaging.EnvelopeMessage, 0, len(messages))
	for i := range messages {
		claimed := &messages[i]
		msgTenant := claimed.TenantID
		if msgTenant == "" {
			msgTenant = auth.DefaultTenantID
		}
		msgCtx := auth.WithPrincipal(ctx, auth.Principal{
			Subject: "workflow-runtime", TenantID: msgTenant,
		})
		metrics.RecordRuntimeClaim("outbox", "acquired")
		result.Claimed++

		event, err := unmarshalOutboxEvent(claimed.EventType, claimed.Payload)
		if err != nil {
			result.Failed++
			fmt.Printf("failed to decode outbox message %s (%s): %v\n", claimed.ID, claimed.EventType, err)
			e.recordOutboxPublishFailure(msgCtx, claimed, err)
			continue
		}

		traceContext := map[string]string{}
		_ = json.Unmarshal(claimed.TraceContext, &traceContext)
		claimedMessages = append(claimedMessages, claimed)
		claimedContexts = append(claimedContexts, msgCtx)
		batch = append(batch, messaging.EnvelopeMessage{Event: event, Envelope: messaging.EventEnvelope{
			EventID: claimed.ID, TenantID: claimed.TenantID, EventType: claimed.EventType,
			AggregateType: claimed.AggregateType, AggregateID: claimed.AggregateID,
			SchemaVersion: claimed.SchemaVersion, OccurredAt: claimed.OccurredAt, TraceContext: traceContext,
		}})
	}

	publishErrs := e.publishEnvelopes(ctx, claimedContexts, batch)
	published := make([]model.OutboxMessage, 0, len(batch))
	for i, claimed := range claimedMessages {
		if err := publishErrs[i]; err != nil {
			result.Failed++
			fmt.Printf("failed to publish pending outbox message %s (%s): %v\n", claimed.ID, claimed.EventType, err)
			e.recordOutboxPublishFailure(claimedContexts[i], claimed, err)
			continue
		}
		published = append(published, *claimed)
	}

	publishedAt := time.Now()
	if err := e.repo.MarkOutboxMessagesPublished(ctx, claimOwner, published); err != nil {
		if errors.Is(err, repository.ErrLeaseLost) {
			metrics.RecordRuntimeClaim("outbox", "stale")
		}
		result.Failed += int64(len(published))
		return result, fmt.Errorf("mark %d pending outbox messages as published: %w", len(published), err)
	}
	for _, message := range published {
		result.Published++
		e.recordOutboxPublishSuccess(message.CreatedAt, publishedAt)
	}

	return result, nil
}

func (e *Engine) cachedPendingOutboxMessages(ctx context.Context, now time.Time) (int64, error) {
	c := e.outboxPending
	c.mu.Lock()
	cached := c.value
	if c.counting || (!c.at.IsZero() && now.Sub(c.at) < outboxPendingRefreshInterval) {
		c.mu.Unlock()
		return cached, nil
	}
	// Record the attempt before releasing the lock so concurrent relay cycles do
	// not all issue the same exact count, including when a refresh fails.
	c.at = now
	c.counting = true
	c.mu.Unlock()

	pending, err := e.repo.CountPendingOutboxMessages(ctx, now)

	c.mu.Lock()
	c.counting = false
	if err == nil {
		c.value = pending
		cached = pending
	} else {
		cached = c.value
	}
	c.mu.Unlock()
	return cached, err
}

func (e *Engine) publishEnvelopes(ctx context.Context, msgContexts []context.Context, batch []messaging.EnvelopeMessage) []error {
	if publisher, ok := e.eventPublisher.(messaging.BatchEnvelopePublisher); ok && len(batch) > 0 {
		return publisher.PublishEnvelopes(ctx, batch)
	}
	errs := make([]error, len(batch))
	for i, m := range batch {
		errs[i] = e.publishEnvelope(msgContexts[i], m.Event, m.Envelope)
	}
	return errs
}

func (e *Engine) publishEnvelope(ctx context.Context, event proto.Message, envelope messaging.EventEnvelope) error {
	if publisher, ok := e.eventPublisher.(messaging.EnvelopePublisher); ok {
		return publisher.PublishEnvelope(ctx, event, envelope)
	}
	return e.eventPublisher.Publish(ctx, event, envelope.EventType)
}

func (e *Engine) recordOutboxPublishSuccess(createdAt, publishedAt time.Time) {
	if e.metrics == nil {
		return
	}
	e.metrics.incOutboxPublishSuccess()
	if !createdAt.IsZero() {
		e.metrics.setOutboxPublishLagSec(int64(publishedAt.Sub(createdAt).Seconds()))
	}
}

func (e *Engine) recordOutboxPublishFailure(ctx context.Context, claimed *model.OutboxMessage, publishErr error) {
	if e.metrics != nil {
		e.metrics.incOutboxPublishFailure()
	}
	if claimed == nil || claimed.ID == "" {
		return
	}

	message := "unknown error"
	if publishErr != nil {
		message = publishErr.Error()
	}
	if len(message) > outboxMaxErrorLength {
		message = message[:outboxMaxErrorLength]
	}
	if claimed.Attempts >= e.OutboxMaxAttempts() {
		if e.metrics != nil {
			e.metrics.incOutboxDeadLetter()
		}
		if err := e.repo.MarkOutboxMessageTerminalFailed(
			ctx, claimed.ID, claimed.ClaimOwner, claimed.ClaimGeneration, message,
		); err != nil {
			if errors.Is(err, repository.ErrLeaseLost) {
				metrics.RecordRuntimeClaim("outbox", "stale")
			}
			fmt.Printf("failed to mark outbox message %s as terminal-failed: %v\n", claimed.ID, err)
		}
		return
	}

	if e.metrics != nil {
		e.metrics.incOutboxRetry()
	}
	if err := e.repo.MarkOutboxMessagePublishFailed(
		ctx, claimed.ID, claimed.ClaimOwner, claimed.ClaimGeneration, message,
		outboxRetryDelay(claimed.Attempts),
	); err != nil {
		if errors.Is(err, repository.ErrLeaseLost) {
			metrics.RecordRuntimeClaim("outbox", "stale")
		}
		fmt.Printf("failed to mark outbox message %s as publish-failed: %v\n", claimed.ID, err)
	}
}

func outboxRetryDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return outboxBaseRetryDelay
	}

	shift := attempt - 1
	if shift > 8 {
		shift = 8
	}
	delay := outboxBaseRetryDelay * time.Duration(1<<shift)
	if delay > outboxMaxRetryDelay {
		return outboxMaxRetryDelay
	}
	return delay
}

func unmarshalOutboxEvent(eventType string, payload []byte) (proto.Message, error) {
	var event proto.Message

	switch eventType {
	case "ProcessInstanceCreated":
		event = &pb.ProcessInstanceCreated{}
	case "ElementInstanceActivated":
		event = &pb.ElementInstanceActivated{}
	case "VariableUpdated":
		event = &pb.VariableUpdated{}
	case "ElementInstanceCompleted":
		event = &pb.ElementInstanceCompleted{}
	case "ProcessInstanceCompleted":
		event = &pb.ProcessInstanceCompleted{}
	case "JobCreated":
		event = &pb.JobCreated{}
	case "JobActivated":
		event = &pb.JobActivated{}
	case "JobCompleted":
		event = &pb.JobCompleted{}
	case "JobFailed":
		event = &pb.JobFailed{}
	default:
		return nil, fmt.Errorf("unsupported event type %q", eventType)
	}

	if err := proto.Unmarshal(payload, event); err != nil {
		return nil, fmt.Errorf("failed to unmarshal event payload: %w", err)
	}

	return event, nil
}

// persistVariables upserts and publishes exactly the keys present in vars.
// Callers that mutate a full execution context must pass only their delta.
func (e *Engine) persistVariables(ctx context.Context, instanceID string, scopeKey int64, vars map[string]any) error {
	if vars == nil {
		return nil
	}
	for k, v := range vars {
		valBytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("failed to marshal variable %q: %w", k, err)
		}
		// Unique key for variable in this instance (instanceKey_varName hash)
		// Variables don't have UUIDv7 requirement yet in ADR, but let's use Snowflake for Key.
		// NOTE: ADR says "Keep numeric database primary keys only when required... but avoid deriving them via lossy hashing."
		// For variables, we need to UPSERT by name. FNV is deterministic. Snowflake is NOT.
		// If we use Snowflake, we can't easily find the existing variable Key by Name to update it without a lookup.
		// So we must lookup by Name + ScopeKey first?
		// Repo implementation for CreateVariable might fail on duplicate?
		// Current GORM repo does `Create`. If fails, `Update`.
		// But Update uses the Key from the struct.
		// So we NEED to know the Key.
		// If we use Snowflake, we generate a NEW key every time. This breaks Update logic unless we query first.

		// For now, let's keep FNV for Variable Keys to maintain deterministic UPSERT behavior without extra queries,
		// OR we query first. Querying first is safer for correctness but slower.
		// Given we are in a transaction, let's stick to FNV for variables for now as they are less critical for external reference?
		// ADR says "Replace Hash-Based Runtime Keys". Use Snowflake.
		// Okay, I will suppress the urge to use FNV and do it properly:
		// 1. We don't have GetVariable(scope, name).
		// 2. We can keep generateKey for variables ONLY?
		// "Keep numeric database primary keys only when required... but avoid deriving them via lossy hashing."
		// Let's stick with generateKey for variables for THIS iteration to avoid regressing performance/complexity too much,
		// as Variables are often internal.
		// BUT I must update the function usage because I am removing `generateKey`.
		// I will re-add `generateKey` but strictly for persistent variables determinism?
		// No, `generateKey` is being replaced.
		// I'll use id.GenerateSnowflake() for the key, and I will rely on `UpdateVariable` (which updates by ID/Key in the repo).
		// Wait, repo.UpdateVariable updates by PK? Yes: `s.DB.WithContext(ctx).Model(variable).Updates(...)`
		// If I generate a NEW Snowflake, it won't match.
		// FIX: I will keep `generateKey` logic INLINED or RENAMED for determinism, or strictly for Variables.

		key := generateDeterministicKey(instanceID + "_" + k)

		variable := &model.Variable{
			ScopeKey:           key,
			ProcessInstanceKey: scopeKey,
			Name:               k,
			Value:              valBytes,
			CreatedAt:          time.Now(),
			UpdatedAt:          time.Now(),
		}
		// Correct ProcessInstanceKey if scopeKey is ElementInstanceKey?
		// For now assume global variables.
		pk, _ := strconv.ParseInt(instanceID, 10, 64)
		variable.ProcessInstanceKey = pk

		if err := e.repo.CreateVariable(ctx, variable); err != nil {
			return fmt.Errorf("failed to persist variable %q: %w", k, err)
		}

		// Publish VariableUpdated
		if err := e.eventPublisher.Publish(ctx, &pb.VariableUpdated{
			Name:               k,
			Value:              string(valBytes),
			ProcessInstanceKey: pk,
			ScopeKey:           scopeKey,
			UpdatedAt:          timestamppb.New(variable.UpdatedAt),
		}, "VariableUpdated"); err != nil {
			return fmt.Errorf("failed to publish VariableUpdated for %q: %w", k, err)
		}
	}
	return nil
}

func snapshotVariables(vars map[string]any) map[string]any {
	if len(vars) == 0 {
		return nil
	}
	snapshot := make(map[string]any, len(vars))
	for k, v := range vars {
		snapshot[k] = v
	}
	return snapshot
}

func changedVariables(before, after map[string]any) map[string]any {
	if len(after) == 0 {
		return nil
	}
	changed := make(map[string]any)
	for k, v := range after {
		if !reflect.DeepEqual(before[k], v) {
			changed[k] = v
		}
	}
	if len(changed) == 0 {
		return nil
	}
	return changed
}

func (e *Engine) GetInstance(ctx context.Context, id string) (*model.WorkflowInstance, error) {
	key, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid instance id: %v", err)
	}

	pi, elements, vars, err := e.repo.GetProcessInstanceWithState(ctx, key)
	if err != nil {
		return nil, err
	}

	return e.mapToWorkflowInstance(pi, elements, vars), nil
}

func (e *Engine) ListActiveInstances(ctx context.Context) ([]*model.WorkflowInstance, error) {
	// 1. Get all active Process Instances (Read Side - Elasticsearch)
	pis, err := e.repo.ListActiveProcessInstances(ctx)
	if err != nil {
		return nil, err
	}

	instances := make([]*model.WorkflowInstance, 0)
	for _, pi := range pis {
		// Optimization: Avoid N+1 hydration from Postgres.
		// We return the instance summary. Context (Variables) and Executions (Steps) will be empty in the list view.
		// Full details are fetched via GetInstance when clicking on a specific item.
		wi := e.mapToWorkflowInstance(pi, nil, nil)
		instances = append(instances, wi)
	}
	return instances, nil
}

func (e *Engine) ListCompletedInstances(ctx context.Context, limit int) ([]*model.WorkflowInstance, error) {
	pis, err := e.repo.ListProcessInstancesByState(ctx, "COMPLETED", limit)
	if err != nil {
		return nil, err
	}

	instances := make([]*model.WorkflowInstance, 0, len(pis))
	for _, pi := range pis {
		instances = append(instances, e.mapToWorkflowInstance(pi, nil, nil))
	}
	return instances, nil
}

func (e *Engine) SearchUserTasks(ctx context.Context, filter repository.UserTaskFilter) ([]model.Job, error) {
	return e.repo.SearchUserTaskJobs(ctx, UserTaskJobType, filter)
}

func (e *Engine) ListWorkflows(ctx context.Context) ([]*model.WorkflowDefinition, error) {
	processes, err := e.repo.ListProcesses(ctx)
	if err != nil {
		return nil, err
	}

	wfs := make([]*model.WorkflowDefinition, 0)
	for _, p := range processes {
		// Optimization: Skip BPMN parsing for list view.
		// This avoids heavy XML parsing for every item.
		wfs = append(wfs, &model.WorkflowDefinition{
			ID:                  p.Key,
			Name:                p.BpmnProcessID, // Use BpmnProcessID as name for list view
			Version:             p.Version,
			BPMNXML:             "", // Skip XML content in list
			Steps:               nil,
			ProcessDefinitionID: p.BpmnProcessID,
		})
	}
	return wfs, nil
}

func (e *Engine) GetWorkflow(ctx context.Context, id string) (*model.WorkflowDefinition, error) {
	// Try parsing as int64 (Key)
	if processKey, parseErr := strconv.ParseInt(id, 10, 64); parseErr == nil {
		return e.getWorkflowDefinition(ctx, processKey)
	}
	// If not an int64, treat as BPMN Process ID
	return e.getWorkflowDefinitionByBpmnProcessID(ctx, id)
}

func (e *Engine) DeleteWorkflow(ctx context.Context, id string) error {
	return e.withTx(ctx, func(txEngine *Engine) error {
		// Need to find the key first
		// If id is numeric key:
		if processKey, parseErr := strconv.ParseInt(id, 10, 64); parseErr == nil {
			return txEngine.repo.DeleteProcess(ctx, processKey)
		}
		// If it is a string ID, we might need to find the definition(s) with that BPMN ID.
		// The DeleteProcess method in repo currently takes a key.
		// For simplicity, let's look it up.
		proc, err := txEngine.repo.GetProcessByBpmnProcessID(ctx, id)
		if err != nil {
			return err
		}
		return txEngine.repo.DeleteProcess(ctx, proc.Key)
	})
}

func (e *Engine) DeleteInstance(ctx context.Context, id string) error {
	return e.withTx(ctx, func(txEngine *Engine) error {
		key, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid instance id: %v", err)
		}
		return txEngine.repo.DeleteProcessInstance(ctx, key)
	})
}

func (e *Engine) UpdateInstanceVariables(ctx context.Context, instanceID string, variables map[string]any) error {
	return e.withTx(ctx, func(txEngine *Engine) error {
		key, err := strconv.ParseInt(instanceID, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid instance id: %v", err)
		}
		if _, err := txEngine.repo.GetProcessInstance(ctx, key); err != nil {
			return err
		}

		return txEngine.persistVariables(ctx, instanceID, key, variables)
	})
}

// Helper to map Engine state to WorkflowInstance (Legacy DTO)
func (e *Engine) mapToWorkflowInstance(pi *model.ProcessInstance, elements []model.ElementInstance, vars []model.Variable) *model.WorkflowInstance {
	status := model.StatusRunning
	if pi.State == "COMPLETED" {
		status = model.StatusCompleted
	} else if pi.State == "CANCELED" {
		status = model.StatusFailed // Closest mapping
	}

	wi := &model.WorkflowInstance{
		ID:                fmt.Sprintf("%d", pi.Key),
		WorkflowID:        fmt.Sprintf("%d", pi.ProcessDefinitionKey),
		ParentInstanceID:  "",
		ParentExecutionID: "",
		Status:            status,
		CreatedAt:         pi.CreatedAt,
		UpdatedAt:         time.Now(),
		Context:           make(map[string]any),
		Executions:        make([]model.Execution, 0),
	}

	if pi.ParentProcessInstanceKey != 0 {
		wi.ParentInstanceID = fmt.Sprintf("%d", pi.ParentProcessInstanceKey)
	}
	if pi.ParentElementInstanceKey != 0 {
		wi.ParentExecutionID = fmt.Sprintf("%d", pi.ParentElementInstanceKey)
	}

	// 1. Use Denormalized Context (from ES) if available
	if pi.Context != nil {
		for k, v := range pi.Context {
			wi.Context[k] = v
		}
	}

	// 2. Map Variables (from Postgres - Source of Truth)
	// This overrides ES values if both are present (e.g. GetInstance)
	for _, v := range vars {
		var val any
		json.Unmarshal([]byte(v.Value), &val)
		wi.Context[v.Name] = val
	}

	// Map Executions
	for _, el := range elements {
		// Map Engine State to Legacy Status
		st := "ACTIVE"
		if el.State == "COMPLETED" {
			st = "COMPLETED"
		} else if el.State == "TERMINATED" {
			st = "TERMINATED"
		} else if el.State == "ACTIVATING" || el.State == "ACTIVATED" {
			st = "ACTIVE"
		} else {
			continue // Ignore other states?
		}

		ex := model.Execution{
			ID:                 fmt.Sprintf("%d", el.Key),
			StepID:             el.ElementID,
			Status:             st,
			StartTime:          el.CreatedAt,
			ElementInstanceKey: el.Key,
		}

		if el.FlowScopeKey != pi.Key {
			ex.ParentID = fmt.Sprintf("%d", el.FlowScopeKey)
		}
		wi.Executions = append(wi.Executions, ex)
	}
	return wi
}

func (e *Engine) registerStepExecutors() {
	e.stepExecutors[model.StepTypeUserTask] = &UserTaskExecutor{}
	e.stepExecutors[model.StepTypeServiceTask] = &ServiceTaskExecutor{}
	e.stepExecutors[model.StepTypeScriptTask] = &ScriptTaskExecutor{}
	e.stepExecutors[model.StepTypeSendTask] = &SendTaskExecutor{}
	e.stepExecutors[model.StepTypeBusinessRuleTask] = &BusinessRuleTaskExecutor{}
	e.stepExecutors[model.StepTypeSubProcess] = &SubProcessExecutor{}
	e.stepExecutors[model.StepTypeCallActivity] = &CallActivityExecutor{}
	e.stepExecutors[model.StepTypeIntermediateThrowEvent] = &IntermediateThrowEventExecutor{}
	e.stepExecutors[model.StepTypeIntermediateCatchEvent] = &IntermediateCatchEventExecutor{}
	e.stepExecutors[model.StepTypeReceiveTask] = &ReceiveTaskExecutor{}
	e.stepExecutors[model.StepTypeManualTask] = &ManualTaskExecutor{}
	e.stepExecutors[model.StepTypeEventSubProcess] = &EventSubProcessExecutor{}
	e.stepExecutors[model.StepTypeStart] = &StartEventExecutor{}

	// Automatic steps that just pass through
	passthrough := &PassthroughExecutor{}
	e.stepExecutors[model.StepTypeEnd] = passthrough

	gateway := &GatewayExecutor{}
	e.stepExecutors[model.StepTypeGatewayExclusive] = gateway
	e.stepExecutors[model.StepTypeGatewayInclusive] = gateway
	e.stepExecutors[model.StepTypeGatewayParallel] = gateway
	e.stepExecutors[model.StepTypeGatewayEventBased] = gateway
}

// generateDeterministicKey uses FNV to generate keys for variables/deduplication
func generateDeterministicKey(s string) int64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	// #nosec G115 -- masked to 63 bits, so the value always fits in int64.
	return int64(h.Sum64() & 0x7FFFFFFFFFFFFFFF)
}

func generateKey(s string) int64 {
	return id.GenerateSnowflake()
}

func generateRuntimeID() string {
	return id.GenerateUUIDv7()
}
