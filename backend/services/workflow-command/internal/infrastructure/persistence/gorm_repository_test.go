package persistence

import (
	"context"
	"errors"
	"fmt"
	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/application"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/domain/repository"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupGormRepositoryTest(t *testing.T) *GormRepository {
	t.Helper()

	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}

	if err := db.AutoMigrate(
		&model.Process{},
		&model.ProcessInstance{},
		&model.Variable{},
		&model.Job{},
		&model.Timer{},
		&model.MessageSubscription{},
		&model.IdempotencyRecord{},
		&model.OutboxMessage{},
		&model.RuntimeSchedulerLease{},
		&model.RuntimeWorkItem{},
		&model.DecisionDefinition{},
	); err != nil {
		t.Fatalf("failed to migrate schema: %v", err)
	}

	return NewGormRepository(db)
}

func TestRuntimeWorkIsSeededForEveryDiscoveredTenant(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	tenantA := auth.WithPrincipal(context.Background(), auth.Principal{Subject: "a", TenantID: "tenant-a"})
	tenantB := auth.WithPrincipal(context.Background(), auth.Principal{Subject: "b", TenantID: "tenant-b"})
	if err := repo.CreateProcessInstance(tenantA, &model.ProcessInstance{Key: 1001, State: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	// Simulate pre-existing data created before runtime work seeding existed.
	if err := repo.DB.Exec(
		"INSERT INTO job (key, state, retries, tenant_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		1002, "CREATED", 1, "tenant-b", time.Now(), time.Now(),
	).Error; err != nil {
		t.Fatal(err)
	}
	tenants, err := repo.EnsureRuntimeWorkTenants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(tenants, ",") != "default,tenant-a,tenant-b" {
		t.Fatalf("discovered tenants = %v", tenants)
	}
	if err := repo.CreateProcessInstance(
		auth.WithPrincipal(context.Background(), auth.Principal{Subject: "c", TenantID: "tenant-c"}),
		&model.ProcessInstance{Key: 1003, State: "ACTIVE"},
	); err != nil {
		t.Fatal(err)
	}
	cached, err := repo.EnsureRuntimeWorkTenants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cached, ",") != "default,tenant-a,tenant-b" {
		t.Fatalf("cached tenants = %v, want previous discovery", cached)
	}
	for _, fixture := range []struct {
		ctx    context.Context
		tenant string
	}{
		{tenantA, "tenant-a"},
		{tenantB, "tenant-b"},
	} {
		var count int64
		if err := repo.DB.WithContext(fixture.ctx).Model(&model.RuntimeWorkItem{}).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 3 {
			t.Fatalf("%s runtime work rows = %d, want 3", fixture.tenant, count)
		}
	}
}

func TestVariableUpsertConflictIsTenantScoped(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	a := auth.WithPrincipal(context.Background(), auth.Principal{TenantID: "tenant-a"})
	b := auth.WithPrincipal(context.Background(), auth.Principal{TenantID: "tenant-b"})
	for _, fixture := range []struct {
		ctx   context.Context
		value string
	}{
		{a, `{"owner":"a"}`},
		{b, `{"owner":"b"}`},
	} {
		if err := repo.CreateVariable(fixture.ctx, &model.Variable{
			ScopeKey: 77, ProcessInstanceKey: 77, Name: "context",
			Value: []byte(fixture.value), UpdatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, fixture := range []struct {
		ctx  context.Context
		want string
	}{
		{a, `{"owner":"a"}`},
		{b, `{"owner":"b"}`},
	} {
		var variable model.Variable
		if err := repo.DB.WithContext(fixture.ctx).First(&variable, "scope_key = ?", 77).Error; err != nil {
			t.Fatal(err)
		}
		if string(variable.Value) != fixture.want {
			t.Fatalf("tenant variable = %s, want %s", variable.Value, fixture.want)
		}
	}
}

func TestWorkerMutationsCannotCrossTenant(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	a := auth.WithPrincipal(context.Background(), auth.Principal{TenantID: "tenant-a"})
	b := auth.WithPrincipal(context.Background(), auth.Principal{TenantID: "tenant-b"})
	if err := repo.CreateJob(a, &model.Job{
		Key: 901, Type: "worker", Worker: "worker-a", Retries: 3,
		State: "ACTIVATED", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"complete", "fail", "extend-lock"} {
		t.Run(operation, func(t *testing.T) {
			foreign, err := repo.GetJob(b, 901)
			if !errors.Is(err, gorm.ErrRecordNotFound) || foreign != nil {
				t.Fatalf("foreign tenant resolved worker job: job=%#v err=%v", foreign, err)
			}
		})
	}
}

func TestIdempotencyIdentityIsTenantScoped(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	a := auth.WithPrincipal(context.Background(), auth.Principal{TenantID: "tenant-a"})
	b := auth.WithPrincipal(context.Background(), auth.Principal{TenantID: "tenant-b"})
	for _, ctx := range []context.Context{a, b} {
		if err := repo.CreateIdempotencyRecord(ctx, &model.IdempotencyRecord{
			Key: "same-key", Operation: "jobs.complete:901", CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, ctx := range []context.Context{a, b} {
		record, err := repo.GetIdempotencyRecord(ctx, "same-key", "jobs.complete:901")
		if err != nil || record == nil {
			t.Fatalf("tenant idempotency lookup: record=%#v err=%v", record, err)
		}
	}
}

func TestRuntimeSchedulerLeaseFailover(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	ctx := context.Background()
	ttl := 50 * time.Millisecond

	ok, err := repo.TryAcquireRuntimeSchedulerLease(ctx, "runtime-a", ttl)
	if err != nil || !ok {
		t.Fatalf("holder A should acquire lease: ok=%v err=%v", ok, err)
	}
	ok, err = repo.TryAcquireRuntimeSchedulerLease(ctx, "runtime-b", ttl)
	if err != nil {
		t.Fatalf("holder B acquire error: %v", err)
	}
	if ok {
		t.Fatalf("holder B must not steal an unexpired lease")
	}
	ok, err = repo.TryAcquireRuntimeSchedulerLease(ctx, "runtime-a", ttl)
	if err != nil || !ok {
		t.Fatalf("holder A should renew: ok=%v err=%v", ok, err)
	}

	time.Sleep(ttl + 30*time.Millisecond)
	ok, err = repo.TryAcquireRuntimeSchedulerLease(ctx, "runtime-b", ttl)
	if err != nil || !ok {
		t.Fatalf("holder B should acquire after expiry: ok=%v err=%v", ok, err)
	}
}

func TestOutboxMutationsAreTenantScoped(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	tenantA := auth.WithPrincipal(context.Background(), auth.Principal{Subject: "a", TenantID: "tenant-a"})
	tenantB := auth.WithPrincipal(context.Background(), auth.Principal{Subject: "b", TenantID: "tenant-b"})
	message := &model.OutboxMessage{
		ID: "tenant-a-event", EventType: "ProcessStarted", Status: "PENDING",
		CreatedAt: time.Now().Add(-time.Second),
	}
	if err := repo.CreateOutboxMessage(tenantA, message); err != nil {
		t.Fatal(err)
	}
	if claimed, err := repo.ClaimOutboxMessage(tenantB, message.ID, "owner-b", time.Minute); err != nil || claimed != nil {
		t.Fatalf("foreign tenant claim: claimed=%v err=%v", claimed, err)
	}
	if err := repo.MarkOutboxMessagePublished(tenantB, message.ID, "owner-b", 1); !errors.Is(err, repository.ErrLeaseLost) {
		t.Fatalf("foreign tenant completion = %v, want ErrLeaseLost", err)
	}
	var unchanged model.OutboxMessage
	if err := repo.DB.WithContext(tenantA).Where("id = ?", message.ID).First(&unchanged).Error; err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "PENDING" {
		t.Fatalf("foreign tenant changed status to %q", unchanged.Status)
	}
	claimed, err := repo.ClaimOutboxMessage(tenantA, message.ID, "owner-a", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("own tenant claim: claimed=%v err=%v", claimed, err)
	}
	if err := repo.MarkOutboxMessagePublished(
		tenantA, message.ID, claimed.ClaimOwner, claimed.ClaimGeneration,
	); err != nil {
		t.Fatal(err)
	}
}

func TestDecisionUpsertAndList(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	ctx := context.Background()
	resource := []byte(`{"id":"invoice_decision","rules":[{"when":{},"then":{"result":"auto"}}]}`)
	if err := repo.UpsertDecision(ctx, &model.DecisionDefinition{
		DecisionID: "invoice_decision",
		Name:       "Invoice",
		Resource:   resource,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := repo.UpsertDecision(ctx, &model.DecisionDefinition{
		DecisionID: "invoice_decision",
		Name:       "Invoice v2",
		Resource:   resource,
	}); err != nil {
		t.Fatalf("upsert v2: %v", err)
	}
	got, err := repo.GetDecisionByDecisionID(ctx, "invoice_decision")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Version != 2 || got.Name != "Invoice v2" {
		t.Fatalf("unexpected decision: version=%d name=%q", got.Version, got.Name)
	}
	list, err := repo.ListDecisions(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: len=%d err=%v", len(list), err)
	}
}

func TestDecisionIdentityIsTenantScoped(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	tenantA := auth.WithPrincipal(context.Background(), auth.Principal{Subject: "a", TenantID: "tenant-a"})
	tenantB := auth.WithPrincipal(context.Background(), auth.Principal{Subject: "b", TenantID: "tenant-b"})
	for _, fixture := range []struct {
		ctx  context.Context
		name string
	}{
		{tenantA, "Tenant A decision"},
		{tenantB, "Tenant B decision"},
	} {
		if err := repo.UpsertDecision(fixture.ctx, &model.DecisionDefinition{
			DecisionID: "shared-id", Name: fixture.name, Resource: []byte(`{"rules":[]}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	a, err := repo.GetDecisionByDecisionID(tenantA, "shared-id")
	if err != nil || a.Name != "Tenant A decision" {
		t.Fatalf("tenant A decision = %#v, err=%v", a, err)
	}
	b, err := repo.GetDecisionByDecisionID(tenantB, "shared-id")
	if err != nil || b.Name != "Tenant B decision" {
		t.Fatalf("tenant B decision = %#v, err=%v", b, err)
	}
}

func TestDeleteIdempotencyRecordsBeforeRespectsLimit(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	now := time.Now()

	records := []model.IdempotencyRecord{
		{Key: "k-old-1", Operation: "jobs.complete:1", CreatedAt: now.Add(-72 * time.Hour)},
		{Key: "k-old-2", Operation: "jobs.complete:2", CreatedAt: now.Add(-48 * time.Hour)},
		{Key: "k-new", Operation: "jobs.complete:3", CreatedAt: now.Add(-2 * time.Hour)},
	}
	for i := range records {
		if err := repo.CreateIdempotencyRecord(context.Background(), &records[i]); err != nil {
			t.Fatalf("failed to seed idempotency record: %v", err)
		}
	}

	deleted, err := repo.DeleteIdempotencyRecordsBefore(context.Background(), now.Add(-24*time.Hour), 1)
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected one deleted record, got %d", deleted)
	}

	var oldCount int64
	if err := repo.DB.WithContext(context.Background()).
		Model(&model.IdempotencyRecord{}).
		Where("created_at < ?", now.Add(-24*time.Hour)).
		Count(&oldCount).Error; err != nil {
		t.Fatalf("failed to query remaining old records: %v", err)
	}
	if oldCount != 1 {
		t.Fatalf("expected one old record to remain due to limit, got %d", oldCount)
	}
}

func TestMarkOutboxMessageTerminalFailed(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	now := time.Now()

	message := &model.OutboxMessage{
		ID:        "outbox-terminal-1",
		EventType: "JobActivated",
		Payload:   []byte("{}"),
		Status:    "PENDING",
		CreatedAt: now,
	}
	if err := repo.CreateOutboxMessage(context.Background(), message); err != nil {
		t.Fatalf("failed to seed outbox message: %v", err)
	}

	claimed, err := repo.ClaimOutboxMessage(context.Background(), message.ID, "terminal-owner", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("failed to claim outbox message: %v", err)
	}
	if err := repo.MarkOutboxMessageTerminalFailed(
		context.Background(), message.ID, claimed.ClaimOwner, claimed.ClaimGeneration, "publish failed",
	); err != nil {
		t.Fatalf("failed to mark terminal failure: %v", err)
	}

	var stored model.OutboxMessage
	if err := repo.DB.WithContext(context.Background()).First(&stored, "id = ?", message.ID).Error; err != nil {
		t.Fatalf("failed to load outbox message: %v", err)
	}
	if stored.Status != "FAILED" {
		t.Fatalf("expected status FAILED, got %s", stored.Status)
	}
	if stored.LastError != "publish failed" {
		t.Fatalf("expected last_error to be persisted, got %q", stored.LastError)
	}
	if stored.NextAttempt != nil {
		t.Fatalf("expected next_attempt to be nil for terminal failure")
	}
}

func TestSearchUserTaskJobs(t *testing.T) {
	runSearchUserTaskJobsCases(t, setupGormRepositoryTest(t), context.Background(), 0)
}

// runSearchUserTaskJobsCases seeds keys base+201..base+208 and instances
// base+1..base+4; callers sharing a database must isolate them by tenant.
func runSearchUserTaskJobsCases(t *testing.T, repo *GormRepository, ctx context.Context, base int64) {
	t.Helper()
	now := time.Now().UTC()

	ut := application.UserTaskJobType
	jobs := []model.Job{
		{Key: 201, Type: ut, ProcessInstanceKey: 1, ElementInstanceKey: 2001, Assignee: "Alice@Example.com", State: "CREATED", Retries: 1, CreatedAt: now.Add(-6 * time.Minute)},
		{Key: 202, Type: ut, ProcessInstanceKey: 1, ElementInstanceKey: 2002, CandidateUsers: "bob, alice@example.com", State: "CREATED", Retries: 1, CreatedAt: now.Add(-5 * time.Minute)},
		{Key: 203, Type: ut, ProcessInstanceKey: 2, ElementInstanceKey: 2003, CandidateGroups: "Finance Reviewer, ops", State: "CREATED", Retries: 1, CreatedAt: now.Add(-4 * time.Minute)},
		{Key: 204, Type: ut, ProcessInstanceKey: 2, ElementInstanceKey: 2004, Worker: "alice@example.com", CandidateGroups: "sales", State: "ACTIVATED", Retries: 1, CreatedAt: now.Add(-3 * time.Minute)},
		{Key: 205, Type: ut, ProcessInstanceKey: 3, ElementInstanceKey: 2005, State: "CREATED", Retries: 1, CreatedAt: now.Add(-2 * time.Minute)},
		{Key: 206, Type: ut, ProcessInstanceKey: 3, ElementInstanceKey: 2006, Assignee: "alice@example.com", Worker: "alice@example.com", State: "COMPLETED", Retries: 1, CreatedAt: now.Add(-time.Minute)},
		{Key: 207, Type: "service", ProcessInstanceKey: 3, ElementInstanceKey: 2007, Assignee: "alice@example.com", State: "CREATED", Retries: 1, CreatedAt: now},
		{Key: 208, Type: ut, ProcessInstanceKey: 4, ElementInstanceKey: 2008, CandidateGroups: "fin_ops", State: "CREATED", Retries: 1, CreatedAt: now},
	}
	for i := range jobs {
		jobs[i].Key += base
		jobs[i].ProcessInstanceKey += base
		jobs[i].ElementInstanceKey += base
		if err := repo.CreateJob(ctx, &jobs[i]); err != nil {
			t.Fatalf("failed to seed job %d: %v", jobs[i].Key, err)
		}
	}

	keys := func(filter repository.UserTaskFilter) []int64 {
		t.Helper()
		found, err := repo.SearchUserTaskJobs(ctx, ut, filter)
		if err != nil {
			t.Fatalf("search %+v: %v", filter, err)
		}
		out := make([]int64, 0, len(found))
		for _, job := range found {
			out = append(out, job.Key-base)
		}
		return out
	}
	tests := []struct {
		name   string
		filter repository.UserTaskFilter
		want   []int64
	}{
		{"no filter returns every open user task", repository.UserTaskFilter{}, []int64{201, 202, 203, 204, 205, 208}},
		{"assignee matches assignee or claim owner", repository.UserTaskFilter{Assignee: "ALICE@example.com"}, []int64{201, 204}},
		{"candidate user matches lists, assignee and claim", repository.UserTaskFilter{CandidateUser: "alice@example.com"}, []int64{201, 202, 204}},
		{"candidate group matches comma lists with spaces", repository.UserTaskFilter{CandidateGroups: []string{"finance reviewer"}}, []int64{203}},
		{"identity filters are OR-combined", repository.UserTaskFilter{CandidateUser: "bob", CandidateGroups: []string{"sales"}}, []int64{202, 204}},
		{"underscore is not a wildcard", repository.UserTaskFilter{CandidateGroups: []string{"fin_ops"}}, []int64{208}},
		{"include unassigned adds open unrestricted tasks", repository.UserTaskFilter{CandidateGroups: []string{"ops"}, IncludeUnassigned: true}, []int64{203, 205}},
		{"completed state", repository.UserTaskFilter{Assignee: "alice@example.com", State: repository.UserTaskStateCompleted}, []int64{206}},
		{"all states", repository.UserTaskFilter{Assignee: "alice@example.com", State: repository.UserTaskStateAll}, []int64{201, 204, 206}},
		{"process instance scope", repository.UserTaskFilter{ProcessInstanceKey: base + 2}, []int64{203, 204}},
		{"limit", repository.UserTaskFilter{Limit: 2}, []int64{201, 202}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keys(tt.filter)
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUserTaskQueriesIncludeCanonicalAndLegacyTypes(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	ctx := context.Background()
	now := time.Now().UTC()

	jobs := []model.Job{
		{Key: 201, Type: application.UserTaskJobType, ProcessInstanceKey: 20, ElementInstanceKey: 2001, Retries: 1, State: "CREATED", CreatedAt: now},
		{Key: 202, Type: application.UserTaskJobType, ProcessInstanceKey: 20, ElementInstanceKey: 2002, Retries: 1, State: "CREATED", CreatedAt: now.Add(time.Second)},
		{Key: 203, Type: "service", ProcessInstanceKey: 20, ElementInstanceKey: 2003, Retries: 1, State: "CREATED", CreatedAt: now.Add(2 * time.Second)},
	}
	for i := range jobs {
		if err := repo.CreateJob(ctx, &jobs[i]); err != nil {
			t.Fatalf("failed to seed job %d: %v", jobs[i].Key, err)
		}
	}

	for _, requestedType := range []string{application.UserTaskJobType, application.UserTaskJobType} {
		listed, err := repo.ListJobsByProcessInstanceAndType(ctx, 20, requestedType)
		if err != nil {
			t.Fatalf("failed to list %q jobs: %v", requestedType, err)
		}
		if len(listed) != 2 || listed[0].Key != 201 || listed[1].Key != 202 {
			t.Fatalf("expected both user-task types for %q, got %#v", requestedType, listed)
		}

		activatable, err := repo.ListActivatableJobs(ctx, requestedType, 10)
		if err != nil {
			t.Fatalf("failed to activate %q jobs: %v", requestedType, err)
		}
		if len(activatable) != 2 {
			t.Fatalf("expected both user-task types to be activatable for %q, got %#v", requestedType, activatable)
		}
	}
}

func TestOutboxClaimReclaimsStaleProcessingMessages(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	ctx := context.Background()
	now := time.Now()
	staleClaim := now.Add(-10 * time.Minute)
	freshClaim := now.Add(-time.Minute)
	staleExpiry := now.Add(-time.Minute)
	freshExpiry := now.Add(time.Minute)

	messages := []model.OutboxMessage{
		{
			ID:        "pending",
			EventType: "JobActivated",
			Payload:   []byte("{}"),
			Status:    "PENDING",
			CreatedAt: now.Add(-time.Hour),
		},
		{
			ID:                  "stale-processing",
			EventType:           "JobActivated",
			Payload:             []byte("{}"),
			Status:              "PROCESSING",
			Attempts:            1,
			ProcessingStartedAt: &staleClaim,
			ClaimOwner:          "stale-owner",
			ClaimExpiresAt:      &staleExpiry,
			ClaimGeneration:     1,
			CreatedAt:           now.Add(-time.Hour),
		},
		{
			ID:                  "fresh-processing",
			EventType:           "JobActivated",
			Payload:             []byte("{}"),
			Status:              "PROCESSING",
			Attempts:            1,
			ProcessingStartedAt: &freshClaim,
			ClaimOwner:          "fresh-owner",
			ClaimExpiresAt:      &freshExpiry,
			ClaimGeneration:     1,
			CreatedAt:           now.Add(-time.Hour),
		},
	}
	for i := range messages {
		if err := repo.CreateOutboxMessage(ctx, &messages[i]); err != nil {
			t.Fatalf("failed to seed outbox message %s: %v", messages[i].ID, err)
		}
	}
	var seededStale model.OutboxMessage
	if err := repo.DB.Raw("SELECT * FROM outbox_message WHERE id = ?", "stale-processing").
		Scan(&seededStale).Error; err != nil {
		t.Fatal(err)
	}
	if seededStale.ProcessingStartedAt == nil || seededStale.ClaimExpiresAt == nil {
		t.Fatalf("stale seed lost claim timestamps: %#v", seededStale)
	}
	if seededStale.TenantID != auth.DefaultTenantID || seededStale.Status != "PROCESSING" ||
		seededStale.ClaimExpiresAt.After(now) || seededStale.ProcessingStartedAt.After(now.Add(-5*time.Minute)) {
		t.Fatalf("stale seed is not claimable: %#v", seededStale)
	}

	count, err := repo.CountPendingOutboxMessages(ctx, now)
	if err != nil {
		t.Fatalf("failed to count claimable messages: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected pending plus stale processing to be claimable, got %d", count)
	}

	listed, err := repo.ListPendingOutboxMessages(ctx, now, 10)
	if err != nil {
		t.Fatalf("failed to list claimable messages: %v", err)
	}
	seen := map[string]bool{}
	for _, message := range listed {
		seen[message.ID] = true
	}
	if !seen["pending"] || !seen["stale-processing"] {
		t.Fatalf("expected pending and stale processing messages to be listed, got %v", seen)
	}
	if seen["fresh-processing"] {
		t.Fatalf("did not expect fresh processing message to be listed")
	}

	claimed, err := repo.ClaimOutboxMessage(ctx, "stale-processing", "replacement-owner", time.Minute)
	if err != nil {
		t.Fatalf("failed to claim stale processing message: %v", err)
	}
	if claimed == nil {
		t.Fatalf("expected stale processing message to be claimed")
	}

	claimedFresh, err := repo.ClaimOutboxMessage(ctx, "fresh-processing", "other-owner", time.Minute)
	if err != nil {
		t.Fatalf("failed to attempt fresh processing claim: %v", err)
	}
	if claimedFresh != nil {
		t.Fatalf("did not expect fresh processing message to be claimed")
	}

	var stored model.OutboxMessage
	if err := repo.DB.WithContext(ctx).First(&stored, "id = ?", "stale-processing").Error; err != nil {
		t.Fatalf("failed to load claimed message: %v", err)
	}
	if stored.Status != "PROCESSING" {
		t.Fatalf("expected stale message to remain PROCESSING after claim, got %s", stored.Status)
	}
	if stored.Attempts != 2 {
		t.Fatalf("expected attempts to increment to 2, got %d", stored.Attempts)
	}
	if stored.ProcessingStartedAt == nil {
		t.Fatalf("expected processing_started_at to be refreshed")
	}
}

func TestOutboxCompletionRejectsStaleGeneration(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	ctx := context.Background()
	message := &model.OutboxMessage{
		ID: "outbox-stale-generation", EventType: "JobActivated",
		Status: "PENDING", CreatedAt: time.Now().Add(-time.Minute),
	}
	if err := repo.CreateOutboxMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimOutboxMessage(ctx, message.ID, "former-owner", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim = %#v, err=%v", claimed, err)
	}
	if err := repo.DB.Model(&model.OutboxMessage{}).Where("id = ?", message.ID).Updates(map[string]any{
		"claim_owner": "takeover-owner", "claim_generation": claimed.ClaimGeneration + 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkOutboxMessagePublished(
		ctx, message.ID, claimed.ClaimOwner, claimed.ClaimGeneration,
	); !errors.Is(err, repository.ErrLeaseLost) {
		t.Fatalf("stale publish completion = %v, want ErrLeaseLost", err)
	}
	var stored model.OutboxMessage
	if err := repo.DB.First(&stored, "id = ?", message.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "PROCESSING" || stored.ClaimOwner != "takeover-owner" {
		t.Fatalf("stale completion mutated takeover claim: %#v", stored)
	}
}

func TestClaimOutboxMessagesClaimsBoundedDisjointBatches(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, messageID := range []string{"batch-1", "batch-2", "batch-3"} {
		if err := repo.CreateOutboxMessage(ctx, &model.OutboxMessage{
			ID: messageID, EventType: "JobCreated", Payload: []byte("{}"),
			Status: "PENDING", CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := repo.ClaimOutboxMessages(ctx, "owner-a", time.Minute, 2)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.ClaimOutboxMessages(ctx, "owner-b", time.Minute, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(second) != 1 {
		t.Fatalf("batch sizes = %d and %d, want 2 and 1", len(first), len(second))
	}
	seen := map[string]bool{}
	for _, message := range append(first, second...) {
		if seen[message.ID] {
			t.Fatalf("message %s was claimed twice", message.ID)
		}
		seen[message.ID] = true
		if message.ClaimGeneration != 1 || message.Status != "PROCESSING" {
			t.Fatalf("invalid claim state: %#v", message)
		}
	}
	if err := repo.MarkOutboxMessagesPublished(ctx, "owner-a", first); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkOutboxMessagesPublished(ctx, "owner-b", second); err != nil {
		t.Fatal(err)
	}
	var remaining int64
	if err := repo.DB.Model(&model.OutboxMessage{}).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("completed outbox messages remaining = %d, want 0", remaining)
	}
}

func TestDeletePublishedOutboxMessagesBeforeHonorsCutoffStatusAndLimit(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	oldPublished := now.Add(-2 * time.Hour)
	recentPublished := now.Add(-time.Minute)
	fixtures := []model.OutboxMessage{
		{ID: "published-old-1", EventType: "JobCreated", Status: "PUBLISHED", PublishedAt: &oldPublished, CreatedAt: oldPublished},
		{ID: "published-old-2", EventType: "JobCreated", Status: "PUBLISHED", PublishedAt: &oldPublished, CreatedAt: oldPublished},
		{ID: "published-recent", EventType: "JobCreated", Status: "PUBLISHED", PublishedAt: &recentPublished, CreatedAt: oldPublished},
		{ID: "pending-old", EventType: "JobCreated", Status: "PENDING", PublishedAt: &oldPublished, CreatedAt: oldPublished},
	}
	for index := range fixtures {
		if err := repo.CreateOutboxMessage(ctx, &fixtures[index]); err != nil {
			t.Fatal(err)
		}
	}

	cutoff := now.Add(-30 * time.Minute)
	deleted, err := repo.DeletePublishedOutboxMessagesBefore(ctx, cutoff, 1)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want bounded batch of 1", deleted)
	}

	deleted, err = repo.DeletePublishedOutboxMessagesBefore(ctx, cutoff, 10)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("second deleted = %d, want remaining old published row", deleted)
	}
	var remaining []model.OutboxMessage
	if err := repo.DB.Order("id").Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 || remaining[0].ID != "pending-old" || remaining[1].ID != "published-recent" {
		t.Fatalf("remaining rows = %#v, want pending and recent published rows", remaining)
	}
}

func TestCancelUnclaimedTimersPreservesLiveSiblingClaim(t *testing.T) {
	repo := setupGormRepositoryTest(t)
	ctx := context.Background()
	expiresAt := time.Now().Add(time.Minute)
	timers := []model.Timer{
		{
			Key: 91, ElementInstanceKey: 900, State: "CREATED", DueDate: time.Now(),
			ClaimOwner: "sibling-owner", ClaimGeneration: 1, ClaimExpiresAt: &expiresAt,
		},
		{Key: 92, ElementInstanceKey: 900, State: "CREATED", DueDate: time.Now()},
	}
	for index := range timers {
		if err := repo.CreateTimer(ctx, &timers[index]); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.CancelUnclaimedTimersForElement(ctx, 900, 90); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[int64]model.TimerState{91: "CREATED", 92: "CANCELED"} {
		timer, err := repo.GetTimer(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if timer.State != want {
			t.Fatalf("timer %d state = %s, want %s", key, timer.State, want)
		}
	}
}
