package application

import (
	"context"
	"encoding/json"
	"errors"
	pb "github.com/artificialflow/artificialflow/backend/api/v1/go"
	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/domain/repository"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/infrastructure/messaging"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

type txOnlyRepo struct {
	repository.Repository
	txCalls          int
	outbox           map[string]model.OutboxMessage
	idempotency      map[string]model.IdempotencyRecord
	elementInstances map[int64]model.ElementInstance
	jobs             map[int64]model.Job
	processes        map[int64]model.Process
	processInstances map[int64]model.ProcessInstance
	variables        []model.Variable
	processGets      int
	stateGets        int
	typeQueries      [][]string
	outboxCountCalls int
}

func (r *txOnlyRepo) WithTx(_ context.Context, fn func(txRepo repository.Repository) error) error {
	r.txCalls++
	return fn(r)
}

func (r *txOnlyRepo) CreateOutboxMessage(_ context.Context, message *model.OutboxMessage) error {
	if r.outbox == nil {
		r.outbox = make(map[string]model.OutboxMessage)
	}
	r.outbox[message.ID] = *message
	return nil
}

func (r *txOnlyRepo) ListPendingOutboxMessages(_ context.Context, now time.Time, limit int) ([]model.OutboxMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	staleBefore := now.Add(-5 * time.Minute)
	out := make([]model.OutboxMessage, 0, limit)
	for _, msg := range r.outbox {
		pendingDue := msg.Status == "PENDING" && (msg.NextAttempt == nil || !msg.NextAttempt.After(now))
		staleProcessing := msg.Status == "PROCESSING" &&
			((msg.ProcessingStartedAt != nil && !msg.ProcessingStartedAt.After(staleBefore)) ||
				(msg.ProcessingStartedAt == nil && !msg.CreatedAt.After(staleBefore)))
		if !pendingDue && !staleProcessing {
			continue
		}
		out = append(out, msg)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *txOnlyRepo) ClaimOutboxMessage(_ context.Context, id, owner string, ttl time.Duration) (*model.OutboxMessage, error) {
	msg, ok := r.outbox[id]
	if !ok {
		return nil, nil
	}
	claimedAt := time.Now()
	staleBefore := claimedAt.Add(-5 * time.Minute)
	pendingDue := msg.Status == "PENDING" && (msg.NextAttempt == nil || !msg.NextAttempt.After(claimedAt))
	staleProcessing := msg.Status == "PROCESSING" &&
		((msg.ProcessingStartedAt != nil && !msg.ProcessingStartedAt.After(staleBefore)) ||
			(msg.ProcessingStartedAt == nil && !msg.CreatedAt.After(staleBefore)))
	if !pendingDue && !staleProcessing {
		return nil, nil
	}
	processingStartedAt := claimedAt
	msg.Status = "PROCESSING"
	msg.Attempts++
	msg.LastError = ""
	msg.NextAttempt = nil
	msg.ProcessingStartedAt = &processingStartedAt
	msg.ClaimOwner = owner
	expiresAt := claimedAt.Add(ttl)
	msg.ClaimExpiresAt = &expiresAt
	msg.ClaimGeneration++
	r.outbox[id] = msg
	return &msg, nil
}

func (r *txOnlyRepo) ClaimOutboxMessages(ctx context.Context, owner string, ttl time.Duration, limit int) ([]model.OutboxMessage, error) {
	messages, err := r.ListPendingOutboxMessages(ctx, time.Now(), limit)
	if err != nil {
		return nil, err
	}
	claimed := make([]model.OutboxMessage, 0, len(messages))
	for _, message := range messages {
		item, claimErr := r.ClaimOutboxMessage(ctx, message.ID, owner, ttl)
		if claimErr != nil {
			return nil, claimErr
		}
		if item != nil {
			claimed = append(claimed, *item)
		}
	}
	return claimed, nil
}

func (r *txOnlyRepo) MarkOutboxMessagePublishFailed(
	_ context.Context, id, owner string, generation int64, lastError string, retryDelay time.Duration,
) error {
	msg, ok := r.outbox[id]
	if !ok || msg.ClaimOwner != owner || msg.ClaimGeneration != generation {
		return repository.ErrLeaseLost
	}
	nextAttempt := time.Now().Add(retryDelay)
	msg.Status = "PENDING"
	msg.LastError = lastError
	msg.NextAttempt = &nextAttempt
	msg.ProcessingStartedAt = nil
	msg.ClaimOwner = ""
	msg.ClaimExpiresAt = nil
	r.outbox[id] = msg
	return nil
}

func (r *txOnlyRepo) MarkOutboxMessageTerminalFailed(
	_ context.Context, id, owner string, generation int64, lastError string,
) error {
	msg, ok := r.outbox[id]
	if !ok || msg.ClaimOwner != owner || msg.ClaimGeneration != generation {
		return repository.ErrLeaseLost
	}
	failedAt := time.Now()
	msg.Status = "FAILED"
	msg.LastError = lastError
	msg.NextAttempt = nil
	msg.ProcessingStartedAt = nil
	msg.PublishedAt = &failedAt
	msg.ClaimOwner = ""
	msg.ClaimExpiresAt = nil
	r.outbox[id] = msg
	return nil
}

func (r *txOnlyRepo) MarkOutboxMessagePublished(_ context.Context, id, owner string, generation int64) error {
	msg, ok := r.outbox[id]
	if !ok || msg.ClaimOwner != owner || msg.ClaimGeneration != generation {
		return repository.ErrLeaseLost
	}
	publishedAt := time.Now()
	msg.Status = "PUBLISHED"
	msg.ProcessingStartedAt = nil
	msg.PublishedAt = &publishedAt
	msg.ClaimOwner = ""
	msg.ClaimExpiresAt = nil
	r.outbox[id] = msg
	return nil
}

func (r *txOnlyRepo) MarkOutboxMessagesPublished(
	ctx context.Context, owner string, messages []model.OutboxMessage,
) error {
	for _, message := range messages {
		if err := r.MarkOutboxMessagePublished(ctx, message.ID, owner, message.ClaimGeneration); err != nil {
			return err
		}
	}
	return nil
}

func (r *txOnlyRepo) CountPendingOutboxMessages(_ context.Context, now time.Time) (int64, error) {
	r.outboxCountCalls++
	var count int64
	staleBefore := now.Add(-5 * time.Minute)
	for _, msg := range r.outbox {
		pendingDue := msg.Status == "PENDING" && (msg.NextAttempt == nil || !msg.NextAttempt.After(now))
		staleProcessing := msg.Status == "PROCESSING" &&
			((msg.ProcessingStartedAt != nil && !msg.ProcessingStartedAt.After(staleBefore)) ||
				(msg.ProcessingStartedAt == nil && !msg.CreatedAt.After(staleBefore)))
		if !pendingDue && !staleProcessing {
			continue
		}
		count++
	}
	return count, nil
}

func (r *txOnlyRepo) DeletePublishedOutboxMessagesBefore(
	_ context.Context, cutoff time.Time, limit int,
) (int64, error) {
	if limit <= 0 {
		limit = len(r.outbox)
	}
	var deleted int64
	for id, msg := range r.outbox {
		if msg.Status != "PUBLISHED" || msg.PublishedAt == nil || !msg.PublishedAt.Before(cutoff) {
			continue
		}
		delete(r.outbox, id)
		deleted++
		if int(deleted) >= limit {
			break
		}
	}
	return deleted, nil
}

func (r *txOnlyRepo) GetIdempotencyRecord(_ context.Context, key, operation string) (*model.IdempotencyRecord, error) {
	if r.idempotency == nil {
		return nil, nil
	}
	rec, ok := r.idempotency[key+"|"+operation]
	if !ok {
		return nil, nil
	}
	return &rec, nil
}

func (r *txOnlyRepo) CreateIdempotencyRecord(_ context.Context, record *model.IdempotencyRecord) error {
	if r.idempotency == nil {
		r.idempotency = make(map[string]model.IdempotencyRecord)
	}
	r.idempotency[record.Key+"|"+record.Operation] = *record
	return nil
}

func (r *txOnlyRepo) DeleteIdempotencyRecordsBefore(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = len(r.idempotency)
	}

	var deleted int64
	for k, rec := range r.idempotency {
		if !rec.CreatedAt.Before(cutoff) {
			continue
		}
		delete(r.idempotency, k)
		deleted++
		if int(deleted) >= limit {
			break
		}
	}
	return deleted, nil
}

func (r *txOnlyRepo) GetProcess(_ context.Context, key int64) (*model.Process, error) {
	r.processGets++
	process, ok := r.processes[key]
	if !ok {
		return nil, errors.New("process not found")
	}
	return &process, nil
}

func (r *txOnlyRepo) GetProcessInstanceWithState(_ context.Context, key int64) (*model.ProcessInstance, []model.ElementInstance, []model.Variable, error) {
	r.stateGets++
	instance, ok := r.processInstances[key]
	if !ok {
		return nil, nil, nil, errors.New("process instance not found")
	}

	elements := make([]model.ElementInstance, 0)
	for _, element := range r.elementInstances {
		if element.ProcessInstanceKey == key {
			elements = append(elements, element)
		}
	}

	variables := make([]model.Variable, 0)
	for _, variable := range r.variables {
		if variable.ProcessInstanceKey == key {
			variables = append(variables, variable)
		}
	}

	return &instance, elements, variables, nil
}

func (r *txOnlyRepo) ListActiveElementInstances(_ context.Context, processInstanceKey int64) ([]model.ElementInstance, error) {
	return r.listActiveElementInstances(processInstanceKey, nil), nil
}

func (r *txOnlyRepo) ListActiveElementInstancesByTypes(_ context.Context, processInstanceKey int64, elementTypes []string) ([]model.ElementInstance, error) {
	r.typeQueries = append(r.typeQueries, append([]string(nil), elementTypes...))
	return r.listActiveElementInstances(processInstanceKey, elementTypes), nil
}

func (r *txOnlyRepo) listActiveElementInstances(processInstanceKey int64, elementTypes []string) []model.ElementInstance {
	typeAllowed := func(elementType string) bool {
		if len(elementTypes) == 0 {
			return true
		}
		for _, allowed := range elementTypes {
			if elementType == allowed {
				return true
			}
		}
		return false
	}

	elements := make([]model.ElementInstance, 0)
	for _, element := range r.elementInstances {
		if processInstanceKey != 0 && element.ProcessInstanceKey != processInstanceKey {
			continue
		}
		if element.State != "ACTIVATED" && element.State != "ACTIVATING" && element.State != "COMPLETING" {
			continue
		}
		if !typeAllowed(element.BpmnElementType) {
			continue
		}
		elements = append(elements, element)
	}
	return elements
}

func (r *txOnlyRepo) CreateVariable(_ context.Context, variable *model.Variable) error {
	r.variables = append(r.variables, *variable)
	return nil
}

func (r *txOnlyRepo) GetJob(_ context.Context, key int64) (*model.Job, error) {
	job, ok := r.jobs[key]
	if !ok {
		return nil, errors.New("job not found")
	}
	return &job, nil
}

func (r *txOnlyRepo) UpdateJob(_ context.Context, job *model.Job) error {
	if r.jobs == nil {
		r.jobs = make(map[int64]model.Job)
	}
	r.jobs[job.Key] = *job
	return nil
}

func (r *txOnlyRepo) UpdateProcessInstance(_ context.Context, instance *model.ProcessInstance) error {
	if r.processInstances == nil {
		r.processInstances = make(map[int64]model.ProcessInstance)
	}
	r.processInstances[instance.Key] = *instance
	return nil
}

func (r *txOnlyRepo) UpdateElementInstance(_ context.Context, element *model.ElementInstance) error {
	if r.elementInstances == nil {
		r.elementInstances = make(map[int64]model.ElementInstance)
	}
	r.elementInstances[element.Key] = *element
	return nil
}

func (r *txOnlyRepo) ListActiveProcessInstances(_ context.Context) ([]*model.ProcessInstance, error) {
	return nil, nil
}

func (r *txOnlyRepo) ListProcesses(_ context.Context) ([]*model.Process, error) {
	return nil, nil
}

type capturedPublisher struct {
	events   []string
	messages []proto.Message
}

func (p *capturedPublisher) Publish(_ context.Context, event proto.Message, eventType string) error {
	if event == nil {
		return errors.New("event is required")
	}
	p.events = append(p.events, eventType)
	p.messages = append(p.messages, event)
	return nil
}

func (p *capturedPublisher) Close() error {
	return nil
}

type failingPublisher struct{}

func (p *failingPublisher) Publish(_ context.Context, _ proto.Message, _ string) error {
	return errors.New("publish failed")
}

func (p *failingPublisher) Close() error {
	return nil
}

type envelopeCapturePublisher struct {
	envelopes []messaging.EventEnvelope
}

func (p *envelopeCapturePublisher) Publish(_ context.Context, _ proto.Message, _ string) error {
	return nil
}

func (p *envelopeCapturePublisher) PublishEnvelope(_ context.Context, _ proto.Message, envelope messaging.EventEnvelope) error {
	p.envelopes = append(p.envelopes, envelope)
	return nil
}

func (p *envelopeCapturePublisher) Close() error { return nil }

func testJSONValue(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("failed to marshal test value: %v", err)
	}
	return data
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

func TestOutboxRetryReusesImmutableEventEnvelope(t *testing.T) {
	repo := &txOnlyRepo{outbox: map[string]model.OutboxMessage{}}
	publisher := &envelopeCapturePublisher{}
	engine := NewEngine(repo, publisher)
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{TenantID: "tenant-a"})
	event := &pb.ProcessInstanceCreated{Key: 991}
	persisted, err := engine.persistBufferedEventsToOutbox(ctx, repo, []bufferedEvent{{
		ctx: ctx, event: event, eventType: "ProcessInstanceCreated",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 {
		t.Fatal("expected persisted event")
	}
	if err := engine.publishEnvelope(ctx, event, persisted[0].envelope); err != nil {
		t.Fatal(err)
	}
	if err := engine.publishEnvelope(ctx, event, persisted[0].envelope); err != nil {
		t.Fatal(err)
	}
	if len(publisher.envelopes) != 2 {
		t.Fatalf("envelopes=%d", len(publisher.envelopes))
	}
	first, retry := publisher.envelopes[0], publisher.envelopes[1]
	if first.EventID == "" || first.EventID != retry.EventID || first.TenantID != "tenant-a" ||
		first.AggregateID != retry.AggregateID || !first.OccurredAt.Equal(retry.OccurredAt) {
		t.Fatalf("retry changed immutable envelope: first=%#v retry=%#v", first, retry)
	}
}

func TestWithTxFlushesBufferedEventsAfterCommit(t *testing.T) {
	repo := &txOnlyRepo{}
	publisher := &capturedPublisher{}
	engine := NewEngine(repo, publisher)

	err := engine.withTx(context.Background(), func(txEngine *Engine) error {
		return txEngine.eventPublisher.Publish(context.Background(), &pb.ProcessInstanceCreated{}, "ProcessInstanceCreated")
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if repo.txCalls != 1 {
		t.Fatalf("expected one tx call, got %d", repo.txCalls)
	}
	if len(publisher.events) != 1 {
		t.Fatalf("expected 1 flushed event, got %d", len(publisher.events))
	}
	if publisher.events[0] != "ProcessInstanceCreated" {
		t.Fatalf("expected ProcessInstanceCreated event, got %q", publisher.events[0])
	}
}

func TestWithTxCanDeferBufferedEventsToRelay(t *testing.T) {
	repo := &txOnlyRepo{}
	publisher := &capturedPublisher{}
	engine := NewEngine(repo, publisher)
	engine.SetFlushOutboxAfterCommit(false)

	err := engine.withTx(context.Background(), func(txEngine *Engine) error {
		return txEngine.eventPublisher.Publish(context.Background(), &pb.ProcessInstanceCreated{}, "ProcessInstanceCreated")
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(publisher.events) != 0 {
		t.Fatalf("expected delivery to be deferred, got %d published events", len(publisher.events))
	}
	if len(repo.outbox) != 1 {
		t.Fatalf("expected one durable outbox event, got %d", len(repo.outbox))
	}
}

func TestWithTxSkipsBufferedEventsOnRollback(t *testing.T) {
	repo := &txOnlyRepo{}
	publisher := &capturedPublisher{}
	engine := NewEngine(repo, publisher)

	expectedErr := errors.New("force rollback")
	err := engine.withTx(context.Background(), func(txEngine *Engine) error {
		if publishErr := txEngine.eventPublisher.Publish(context.Background(), &pb.JobActivated{}, "JobActivated"); publishErr != nil {
			return publishErr
		}
		return expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected rollback error %v, got %v", expectedErr, err)
	}

	if len(publisher.events) != 0 {
		t.Fatalf("expected no flushed events on rollback, got %d", len(publisher.events))
	}
}

func TestIdempotencyMetricsHitMiss(t *testing.T) {
	repo := &txOnlyRepo{}
	engine := NewEngine(repo, &capturedPublisher{})

	hit, err := engine.HasProcessedIdempotencyKey(context.Background(), "k1", "op1")
	if err != nil {
		t.Fatalf("unexpected error checking idempotency miss: %v", err)
	}
	if hit {
		t.Fatalf("expected miss for unknown idempotency key")
	}

	if err := engine.RecordIdempotencyKey(context.Background(), "k1", "op1"); err != nil {
		t.Fatalf("failed to record idempotency key: %v", err)
	}

	hit, err = engine.HasProcessedIdempotencyKey(context.Background(), "k1", "op1")
	if err != nil {
		t.Fatalf("unexpected error checking idempotency hit: %v", err)
	}
	if !hit {
		t.Fatalf("expected idempotency hit")
	}

	snapshot := engine.MetricsSnapshot()
	if snapshot.IdempotencyMiss != 1 {
		t.Fatalf("expected idempotency miss metric 1, got %d", snapshot.IdempotencyMiss)
	}
	if snapshot.IdempotencyHit != 1 {
		t.Fatalf("expected idempotency hit metric 1, got %d", snapshot.IdempotencyHit)
	}
}

func TestRunOutboxRelayCyclePublishesClaimedMessages(t *testing.T) {
	repo := &txOnlyRepo{outbox: map[string]model.OutboxMessage{}}
	publisher := &capturedPublisher{}
	engine := NewEngine(repo, publisher)

	payload, err := proto.Marshal(&pb.JobActivated{Key: 101})
	if err != nil {
		t.Fatalf("failed to marshal test payload: %v", err)
	}

	repo.outbox["msg-1"] = model.OutboxMessage{
		ID:        "msg-1",
		EventType: "JobActivated",
		Payload:   payload,
		Status:    "PENDING",
		CreatedAt: time.Now().Add(-time.Second),
	}

	result, err := engine.RunOutboxRelayCycle(context.Background(), 10)
	if err != nil {
		t.Fatalf("relay cycle failed: %v", err)
	}
	if result.Claimed != 1 || result.Published != 1 || result.Failed != 0 {
		t.Fatalf("unexpected relay result: %+v", result)
	}

	msg := repo.outbox["msg-1"]
	if msg.Status != "PUBLISHED" {
		t.Fatalf("expected outbox message to be published, got %s", msg.Status)
	}
	if msg.PublishedAt == nil {
		t.Fatalf("expected published_at to be set")
	}

	snapshot := engine.MetricsSnapshot()
	if snapshot.OutboxPending == 0 {
		t.Fatalf("expected outbox pending metric to be set before publish")
	}
	if snapshot.OutboxPublishSuccess == 0 {
		t.Fatalf("expected outbox success metric to increment")
	}
	if snapshot.OutboxPublishLagSec == 0 {
		t.Fatalf("expected outbox publish lag metric to be set")
	}
}

func TestRunOutboxRelayCycleCachesPendingCountWithoutBlockingClaims(t *testing.T) {
	repo := &txOnlyRepo{outbox: map[string]model.OutboxMessage{}}
	engine := NewEngine(repo, &capturedPublisher{})
	payload, err := proto.Marshal(&pb.JobActivated{Key: 102})
	if err != nil {
		t.Fatal(err)
	}
	repo.outbox["msg-cache-1"] = model.OutboxMessage{
		ID: "msg-cache-1", EventType: "JobActivated", Payload: payload,
		Status: "PENDING", CreatedAt: time.Now().Add(-time.Second),
	}
	first, err := engine.RunOutboxRelayCycle(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	repo.outbox["msg-cache-2"] = model.OutboxMessage{
		ID: "msg-cache-2", EventType: "JobActivated", Payload: payload,
		Status: "PENDING", CreatedAt: time.Now(),
	}
	second, err := engine.RunOutboxRelayCycle(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if repo.outboxCountCalls != 1 {
		t.Fatalf("pending count calls = %d, want 1 within refresh interval", repo.outboxCountCalls)
	}
	if first.Pending != 1 || second.Pending != first.Pending {
		t.Fatalf("pending results = %d then %d, want cached exact result", first.Pending, second.Pending)
	}
	if second.Claimed != 1 || second.Published != 1 {
		t.Fatalf("cached count prevented relay progress: %+v", second)
	}
}

func TestRunOutboxCleanupDeletesOnlyRetainedPublishedMessages(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-time.Hour)
	recent := now.Add(-time.Minute)
	repo := &txOnlyRepo{outbox: map[string]model.OutboxMessage{
		"old":    {ID: "old", Status: "PUBLISHED", PublishedAt: &old},
		"recent": {ID: "recent", Status: "PUBLISHED", PublishedAt: &recent},
		"failed": {ID: "failed", Status: "FAILED", PublishedAt: &old},
	}}
	engine := NewEngine(repo, &capturedPublisher{})
	result, err := engine.RunOutboxCleanup(context.Background(), 30*time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 1 || result.Cutoff.IsZero() {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}
	if _, exists := repo.outbox["old"]; exists {
		t.Fatal("old published message was not deleted")
	}
	if _, exists := repo.outbox["recent"]; !exists {
		t.Fatal("recent published message was deleted")
	}
	if _, exists := repo.outbox["failed"]; !exists {
		t.Fatal("non-published message was deleted")
	}
}

func TestRunOutboxRelayCycleReclaimsStaleProcessingMessages(t *testing.T) {
	repo := &txOnlyRepo{outbox: map[string]model.OutboxMessage{}}
	publisher := &capturedPublisher{}
	engine := NewEngine(repo, publisher)
	now := time.Now()
	staleClaim := now.Add(-10 * time.Minute)

	payload, err := proto.Marshal(&pb.JobActivated{Key: 104})
	if err != nil {
		t.Fatalf("failed to marshal test payload: %v", err)
	}

	repo.outbox["msg-stale"] = model.OutboxMessage{
		ID:                  "msg-stale",
		EventType:           "JobActivated",
		Payload:             payload,
		Status:              "PROCESSING",
		Attempts:            1,
		ProcessingStartedAt: &staleClaim,
		CreatedAt:           now.Add(-time.Hour),
	}

	result, err := engine.RunOutboxRelayCycle(context.Background(), 10)
	if err != nil {
		t.Fatalf("relay cycle failed: %v", err)
	}
	if result.Claimed != 1 || result.Published != 1 || result.Failed != 0 {
		t.Fatalf("unexpected relay result: %+v", result)
	}

	msg := repo.outbox["msg-stale"]
	if msg.Status != "PUBLISHED" {
		t.Fatalf("expected reclaimed outbox message to be published, got %s", msg.Status)
	}
	if msg.Attempts != 2 {
		t.Fatalf("expected reclaimed message attempts to increment to 2, got %d", msg.Attempts)
	}
	if msg.ProcessingStartedAt != nil {
		t.Fatalf("expected processing timestamp to be cleared after publish")
	}
}

func TestProceedTokenPublishesElementCompletionWithProcessInstanceKey(t *testing.T) {
	repo := &txOnlyRepo{}
	publisher := &capturedPublisher{}
	engine := NewEngine(repo, publisher)

	instance := &model.WorkflowInstance{
		ID:     "12345",
		Status: model.StatusRunning,
		Executions: []model.Execution{
			{
				ID:                 "exec-end",
				StepID:             "end",
				Status:             "ACTIVE",
				ElementInstanceKey: 67890,
			},
		},
		Context: map[string]any{},
	}
	workflow := &model.WorkflowDefinition{
		ID: 777,
		Steps: []model.StepDefinition{
			{ID: "end", Type: model.StepTypeEnd},
		},
	}

	if err := engine.proceedToken(context.Background(), instance, "exec-end", workflow); err != nil {
		t.Fatalf("proceedToken failed: %v", err)
	}

	var completed *pb.ElementInstanceCompleted
	for _, message := range publisher.messages {
		if event, ok := message.(*pb.ElementInstanceCompleted); ok {
			completed = event
			break
		}
	}
	if completed == nil {
		t.Fatalf("expected ElementInstanceCompleted event, got %v", publisher.events)
	}
	if completed.Key != 67890 {
		t.Fatalf("expected element key 67890, got %d", completed.Key)
	}
	if completed.ProcessInstanceKey != 12345 {
		t.Fatalf("expected process instance key 12345, got %d", completed.ProcessInstanceKey)
	}
}

func TestCompleteJobPublishesOnlyUpdatedVariables(t *testing.T) {
	repo := &txOnlyRepo{
		elementInstances: map[int64]model.ElementInstance{
			456: {
				Key:                  456,
				ProcessInstanceKey:   123,
				ProcessDefinitionKey: 777,
				ElementID:            "end",
				BpmnElementType:      string(model.StepTypeEnd),
				State:                "ACTIVATED",
				CreatedAt:            time.Now().Add(-time.Minute),
			},
		},
		jobs: map[int64]model.Job{
			111: {
				Key:                  111,
				Type:                 "worker-task",
				ProcessInstanceKey:   123,
				ElementInstanceKey:   456,
				ProcessDefinitionKey: 777,
				ElementID:            "end",
				Worker:               "worker-1",
				State:                "ACTIVATED",
				LockExpirationTime:   ptrTime(time.Now().Add(time.Minute)),
			},
		},
		processes: map[int64]model.Process{
			777: {
				Key:           777,
				BpmnProcessID: "test-process",
				Version:       1,
				Resource:      []byte(`[{"id":"end","type":"END"}]`),
			},
		},
		processInstances: map[int64]model.ProcessInstance{
			123: {
				Key:                  123,
				ProcessDefinitionKey: 777,
				Version:              1,
				State:                "ACTIVE",
				CreatedAt:            time.Now().Add(-time.Minute),
			},
		},
		variables: []model.Variable{
			{
				ScopeKey:           1,
				ProcessInstanceKey: 123,
				Name:               "existing",
				Value:              testJSONValue(t, "keep-me"),
			},
			{
				ScopeKey:           2,
				ProcessInstanceKey: 123,
				Name:               "secret",
				Value:              testJSONValue(t, "do-not-republish"),
			},
		},
	}
	publisher := &capturedPublisher{}
	engine := NewEngine(repo, publisher)

	if err := engine.CompleteJob(context.Background(), 111, "worker-1", map[string]any{"approved": true}); err != nil {
		t.Fatalf("CompleteJob failed: %v", err)
	}

	var variableEvents []string
	for _, message := range publisher.messages {
		if event, ok := message.(*pb.VariableUpdated); ok {
			variableEvents = append(variableEvents, event.Name)
		}
	}
	if len(variableEvents) != 1 {
		t.Fatalf("expected one VariableUpdated event, got %v", variableEvents)
	}
	if variableEvents[0] != "approved" {
		t.Fatalf("expected only approved to be published, got %v", variableEvents)
	}
}

func TestPublishSignalUsesScopedCandidateQuery(t *testing.T) {
	repo := &txOnlyRepo{
		elementInstances: map[int64]model.ElementInstance{
			456: {
				Key:                  456,
				ProcessInstanceKey:   123,
				ProcessDefinitionKey: 777,
				ElementID:            "service-task",
				BpmnElementType:      string(model.StepTypeServiceTask),
				State:                "ACTIVATED",
				CreatedAt:            time.Now().Add(-time.Minute),
			},
		},
		processes: map[int64]model.Process{
			777: {
				Key:           777,
				BpmnProcessID: "test-process",
				Version:       1,
				Resource:      []byte(`[{"id":"service-task","type":"SERVICE_TASK"}]`),
			},
		},
	}
	engine := NewEngine(repo, &capturedPublisher{})

	if err := engine.PublishSignal(context.Background(), "approval-received", nil); err != nil {
		t.Fatalf("PublishSignal failed: %v", err)
	}

	if len(repo.typeQueries) != 1 {
		t.Fatalf("expected one type-scoped active element query, got %d", len(repo.typeQueries))
	}
	if repo.txCalls != 0 {
		t.Fatalf("expected no transaction for non-signal element rows, got %d", repo.txCalls)
	}
	// Activity candidates load the definition once to check attached signal boundaries.
	if repo.processGets != 1 {
		t.Fatalf("expected one workflow definition load to inspect activity boundaries, got %d", repo.processGets)
	}
	if repo.stateGets != 0 {
		t.Fatalf("expected no instance hydration for non-signal element rows, got %d", repo.stateGets)
	}
}

func TestPublishSignalMemoizesWorkflowDefinitionsBeforeHydratingInstances(t *testing.T) {
	repo := &txOnlyRepo{
		elementInstances: map[int64]model.ElementInstance{
			456: {
				Key:                  456,
				ProcessInstanceKey:   123,
				ProcessDefinitionKey: 777,
				ElementID:            "catch-a",
				BpmnElementType:      string(model.StepTypeIntermediateCatchEvent),
				State:                "ACTIVATED",
				CreatedAt:            time.Now().Add(-time.Minute),
			},
			457: {
				Key:                  457,
				ProcessInstanceKey:   124,
				ProcessDefinitionKey: 777,
				ElementID:            "catch-b",
				BpmnElementType:      string(model.StepTypeIntermediateCatchEvent),
				State:                "ACTIVATED",
				CreatedAt:            time.Now().Add(-time.Minute),
			},
			458: {
				Key:                  458,
				ProcessInstanceKey:   125,
				ProcessDefinitionKey: 777,
				ElementID:            "catch-c",
				BpmnElementType:      string(model.StepTypeIntermediateCatchEvent),
				State:                "ACTIVATED",
				CreatedAt:            time.Now().Add(-time.Minute),
			},
		},
		processes: map[int64]model.Process{
			777: {
				Key:           777,
				BpmnProcessID: "test-process",
				Version:       1,
				Resource: []byte(`[
					{"id":"catch-a","type":"INTERMEDIATE_CATCH_EVENT","properties":{"signal_ref":"other-signal"}},
					{"id":"catch-b","type":"INTERMEDIATE_CATCH_EVENT","properties":{"signal_ref":"other-signal"}},
					{"id":"catch-c","type":"INTERMEDIATE_CATCH_EVENT","properties":{"signal_ref":"other-signal"}}
				]`),
			},
		},
	}
	engine := NewEngine(repo, &capturedPublisher{})

	if err := engine.PublishSignal(context.Background(), "approval-received", map[string]any{"approved": true}); err != nil {
		t.Fatalf("PublishSignal failed: %v", err)
	}

	if repo.processGets != 1 {
		t.Fatalf("expected one workflow definition load for shared process, got %d", repo.processGets)
	}
	if repo.stateGets != 0 {
		t.Fatalf("expected no instance hydration for non-matching signal, got %d", repo.stateGets)
	}
	if repo.txCalls != 0 {
		t.Fatalf("expected no transaction for non-matching signal, got %d", repo.txCalls)
	}
}

func TestRunOutboxRelayCycleSchedulesRetryOnFailure(t *testing.T) {
	repo := &txOnlyRepo{outbox: map[string]model.OutboxMessage{}}
	engine := NewEngine(repo, &failingPublisher{})

	payload, err := proto.Marshal(&pb.JobActivated{Key: 102})
	if err != nil {
		t.Fatalf("failed to marshal test payload: %v", err)
	}

	repo.outbox["msg-2"] = model.OutboxMessage{
		ID:        "msg-2",
		EventType: "JobActivated",
		Payload:   payload,
		Status:    "PENDING",
		CreatedAt: time.Now().Add(-time.Second),
	}

	result, err := engine.RunOutboxRelayCycle(context.Background(), 10)
	if err != nil {
		t.Fatalf("relay cycle failed: %v", err)
	}
	if result.Failed != 1 {
		t.Fatalf("expected one failed relay, got %+v", result)
	}

	msg := repo.outbox["msg-2"]
	if msg.Status != "PENDING" {
		t.Fatalf("expected failed message to return to pending, got %s", msg.Status)
	}
	if msg.NextAttempt == nil {
		t.Fatalf("expected next attempt to be set for retry")
	}
	if msg.LastError == "" {
		t.Fatalf("expected last_error to be set")
	}

	snapshot := engine.MetricsSnapshot()
	if snapshot.OutboxPublishFailure == 0 {
		t.Fatalf("expected outbox failure metric to increment")
	}
}

type batchPublisher struct {
	calls   int
	failIDs map[string]bool
}

func (p *batchPublisher) Publish(_ context.Context, _ proto.Message, _ string) error {
	return errors.New("single publish must not be used")
}

func (p *batchPublisher) PublishEnvelopes(_ context.Context, messages []messaging.EnvelopeMessage) []error {
	p.calls++
	errs := make([]error, len(messages))
	for i, m := range messages {
		if p.failIDs[m.Envelope.EventID] {
			errs[i] = errors.New("broker rejected")
		}
	}
	return errs
}

func (p *batchPublisher) Close() error { return nil }

func TestRunOutboxRelayCyclePublishesClaimedMessagesInOneBatch(t *testing.T) {
	repo := &txOnlyRepo{outbox: map[string]model.OutboxMessage{}}
	publisher := &batchPublisher{failIDs: map[string]bool{"msg-b": true}}
	engine := NewEngine(repo, publisher)

	for i, msgID := range []string{"msg-a", "msg-b", "msg-c"} {
		payload, err := proto.Marshal(&pb.JobActivated{Key: int64(200 + i)})
		if err != nil {
			t.Fatalf("failed to marshal test payload: %v", err)
		}
		repo.outbox[msgID] = model.OutboxMessage{
			ID: msgID, EventType: "JobActivated", Payload: payload,
			Status: "PENDING", CreatedAt: time.Now().Add(-time.Second),
		}
	}

	result, err := engine.RunOutboxRelayCycle(context.Background(), 10)
	if err != nil {
		t.Fatalf("relay cycle failed: %v", err)
	}
	if publisher.calls != 1 {
		t.Fatalf("expected one batch publish call, got %d", publisher.calls)
	}
	if result.Claimed != 3 || result.Published != 2 || result.Failed != 1 {
		t.Fatalf("unexpected relay result: %+v", result)
	}
	if got := repo.outbox["msg-b"].Status; got != "PENDING" {
		t.Fatalf("rejected message status = %s, want PENDING", got)
	}
	for _, msgID := range []string{"msg-a", "msg-c"} {
		if got := repo.outbox[msgID].Status; got != "PUBLISHED" {
			t.Fatalf("%s status = %s, want PUBLISHED", msgID, got)
		}
	}
}

func TestRunOutboxRelayCycleMarksTerminalFailureAtMaxAttempts(t *testing.T) {
	repo := &txOnlyRepo{outbox: map[string]model.OutboxMessage{}}
	engine := NewEngine(repo, &failingPublisher{})
	engine.SetOutboxMaxAttempts(2)

	payload, err := proto.Marshal(&pb.JobActivated{Key: 103})
	if err != nil {
		t.Fatalf("failed to marshal test payload: %v", err)
	}

	repo.outbox["msg-3"] = model.OutboxMessage{
		ID:        "msg-3",
		EventType: "JobActivated",
		Payload:   payload,
		Status:    "PENDING",
		Attempts:  1,
		CreatedAt: time.Now().Add(-time.Second),
	}

	result, err := engine.RunOutboxRelayCycle(context.Background(), 10)
	if err != nil {
		t.Fatalf("relay cycle failed: %v", err)
	}
	if result.Failed != 1 {
		t.Fatalf("expected one failed relay, got %+v", result)
	}

	msg := repo.outbox["msg-3"]
	if msg.Status != "FAILED" {
		t.Fatalf("expected failed terminal status, got %s", msg.Status)
	}
	if msg.NextAttempt != nil {
		t.Fatalf("expected no next attempt for terminal failed message")
	}
}

func TestRunIdempotencyCleanupDeletesExpiredRecords(t *testing.T) {
	now := time.Now()
	repo := &txOnlyRepo{idempotency: map[string]model.IdempotencyRecord{
		"old|op": {
			Key:       "old",
			Operation: "op",
			CreatedAt: now.Add(-48 * time.Hour),
		},
		"new|op": {
			Key:       "new",
			Operation: "op",
			CreatedAt: now.Add(-2 * time.Hour),
		},
	}}

	engine := NewEngine(repo, &capturedPublisher{})
	result, err := engine.RunIdempotencyCleanup(context.Background(), 24*time.Hour, 10)
	if err != nil {
		t.Fatalf("idempotency cleanup failed: %v", err)
	}
	if result.Deleted != 1 {
		t.Fatalf("expected one deleted record, got %d", result.Deleted)
	}
	if _, ok := repo.idempotency["old|op"]; ok {
		t.Fatalf("expected old idempotency record to be removed")
	}
	if _, ok := repo.idempotency["new|op"]; !ok {
		t.Fatalf("expected new idempotency record to remain")
	}
}
