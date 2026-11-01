package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/domain/repository"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupRuntimeMigrationRepository(t *testing.T) *GormRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.Process{}, &model.ProcessInstance{}, &model.ElementInstance{}, &model.Variable{},
		&model.Job{}, &model.Incident{}, &model.Timer{}, &model.MessageSubscription{},
		&model.ProcessInstanceMigration{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE process_instance_migration_audit (
		tenant_id text, event_id text, operation_id text, instance_key integer,
		source_definition_key integer, target_definition_key integer, mapping blob,
		actor text, occurred_at datetime
	)`).Error; err != nil {
		t.Fatal(err)
	}
	return NewGormRepository(db)
}

func tenantContext(tenant string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{Subject: "admin", TenantID: tenant})
}

func seedMigratableInstance(t *testing.T, repo *GormRepository, ctx context.Context, targetResource string) {
	t.Helper()
	sourceXML := `<definitions><process id="orders"><userTask id="review"/><boundaryEvent id="review-timeout" attachedToRef="review"><timerEventDefinition/></boundaryEvent><intermediateCatchEvent id="review-message"><messageEventDefinition/></intermediateCatchEvent></process></definitions>`
	if err := repo.CreateProcess(ctx, &model.Process{Key: 10, BpmnProcessID: "orders", Version: 1, Resource: []byte(sourceXML)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateProcess(ctx, &model.Process{Key: 20, BpmnProcessID: "orders", Version: 2, Resource: []byte(targetResource)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateProcessInstance(ctx, &model.ProcessInstance{Key: 100, ProcessDefinitionKey: 10, Version: 1, State: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateElementInstance(ctx, &model.ElementInstance{
		Key: 101, ProcessInstanceKey: 100, ProcessDefinitionKey: 10,
		ElementID: "review", BpmnElementType: "USER_TASK", State: "ACTIVATED",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateJob(ctx, &model.Job{
		Key: 102, ProcessInstanceKey: 100, ProcessDefinitionKey: 10,
		ElementInstanceKey: 101, ElementID: "review", Type: canonicalUserTaskJobType, State: "CREATED",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInstanceMigrationDryRunAndApplySupportedFixture(t *testing.T) {
	repo := setupRuntimeMigrationRepository(t)
	ctx := tenantContext("tenant-a")
	seedMigratableInstance(t, repo, ctx, `<definitions><process id="orders"><userTask id="approve"/></process></definitions>`)
	request := model.InstanceMigrationRequest{
		InstanceKey: 100, SourceDefinitionKey: 10, TargetDefinitionKey: 20,
		ElementMapping: map[string]string{"review": "approve"}, Actor: "admin",
	}
	request.DryRun = true
	dryRun, err := repo.MigrateProcessInstance(ctx, request)
	if err != nil || dryRun.Status != "VALIDATED" {
		t.Fatalf("dry-run = %#v err=%v", dryRun, err)
	}
	instance, _ := repo.GetProcessInstance(ctx, 100)
	if instance.ProcessDefinitionKey != 10 {
		t.Fatal("dry-run mutated source")
	}
	request.DryRun = false
	applied, err := repo.MigrateProcessInstance(ctx, request)
	if err != nil || applied.Status != "APPLIED" {
		t.Fatalf("apply = %#v err=%v", applied, err)
	}
	instance, _ = repo.GetProcessInstance(ctx, 100)
	if instance.ProcessDefinitionKey != 20 || instance.Version != 2 {
		t.Fatalf("instance not migrated: %#v", instance)
	}
	job, _ := repo.GetJob(ctx, 102)
	if job.ElementID != "approve" || job.ProcessDefinitionKey != 20 {
		t.Fatalf("job not mapped atomically: %#v", job)
	}
	var operation model.ProcessInstanceMigration
	if err := repo.DB.WithContext(ctx).Where("operation_id = ?", applied.OperationID).First(&operation).Error; err != nil {
		t.Fatalf("migration operation was not recorded: %v", err)
	}
	if operation.Status != model.MigrationStatusApplied || operation.Actor != "admin" {
		t.Fatalf("migration operation = %#v", operation)
	}
	var audit struct {
		OperationID         string
		InstanceKey         int64
		SourceDefinitionKey int64
		TargetDefinitionKey int64
		Actor               string
	}
	if err := repo.DB.WithContext(ctx).Table("process_instance_migration_audit").
		Where("operation_id = ?", applied.OperationID).Take(&audit).Error; err != nil {
		t.Fatalf("migration audit was not recorded: %v", err)
	}
	if audit.InstanceKey != 100 || audit.SourceDefinitionKey != 10 || audit.TargetDefinitionKey != 20 || audit.Actor != "admin" {
		t.Fatalf("migration audit = %#v", audit)
	}
}

func TestInstanceMigrationMapsCreatedUserTaskTimerAndMessageWaits(t *testing.T) {
	repo := setupRuntimeMigrationRepository(t)
	ctx := tenantContext("tenant-a")
	target := `<definitions><process id="orders"><userTask id="approve"/><userTask id="approve-second"/><boundaryEvent id="approve-timeout" attachedToRef="approve"><timerEventDefinition/></boundaryEvent><boundaryEvent id="approve-boundary-message" attachedToRef="approve"><messageEventDefinition/></boundaryEvent><intermediateCatchEvent id="approve-timer"><timerEventDefinition/></intermediateCatchEvent><intermediateCatchEvent id="approve-message"><messageEventDefinition/></intermediateCatchEvent></process></definitions>`
	seedMigratableInstance(t, repo, ctx, target)
	source := `<definitions><process id="orders"><userTask id="review"/><userTask id="review-second"/><boundaryEvent id="review-timeout" attachedToRef="review"><timerEventDefinition/></boundaryEvent><boundaryEvent id="review-boundary-message" attachedToRef="review"><messageEventDefinition/></boundaryEvent><intermediateCatchEvent id="review-timer"><timerEventDefinition/></intermediateCatchEvent><intermediateCatchEvent id="review-message"><messageEventDefinition/></intermediateCatchEvent></process></definitions>`
	if err := repo.DB.WithContext(ctx).Model(&model.Process{}).Where("key = ?", 10).Update("resource", []byte(source)).Error; err != nil {
		t.Fatal(err)
	}
	activeElements := []model.ElementInstance{
		{Key: 105, ProcessInstanceKey: 100, ProcessDefinitionKey: 10, ElementID: "review-second", BpmnElementType: "USER_TASK", State: model.ElementInstanceStateActivated},
		{Key: 106, ProcessInstanceKey: 100, ProcessDefinitionKey: 10, ElementID: "review-timeout", BpmnElementType: "BOUNDARY_EVENT", State: model.ElementInstanceStateActivated},
		{Key: 107, ProcessInstanceKey: 100, ProcessDefinitionKey: 10, ElementID: "review-boundary-message", BpmnElementType: "BOUNDARY_EVENT", State: model.ElementInstanceStateActivated},
		{Key: 108, ProcessInstanceKey: 100, ProcessDefinitionKey: 10, ElementID: "review-timer", BpmnElementType: "INTERMEDIATE_CATCH_EVENT", State: model.ElementInstanceStateActivated},
		{Key: 109, ProcessInstanceKey: 100, ProcessDefinitionKey: 10, ElementID: "review-message", BpmnElementType: "INTERMEDIATE_CATCH_EVENT", State: model.ElementInstanceStateActivated},
	}
	for index := range activeElements {
		if err := repo.CreateElementInstance(ctx, &activeElements[index]); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.CreateJob(ctx, &model.Job{
		Key: 110, ProcessInstanceKey: 100, ProcessDefinitionKey: 10,
		ElementInstanceKey: 105, ElementID: "review-second", Type: canonicalUserTaskJobType, State: model.JobStateCreated,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTimer(ctx, &model.Timer{
		Key: 103, ProcessInstanceKey: 100, ElementInstanceKey: 101,
		ElementID: "review-timeout", State: "CREATED", DueDate: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTimer(ctx, &model.Timer{
		Key: 111, ProcessInstanceKey: 100, ElementInstanceKey: 108,
		ElementID: "review-timer", State: model.TimerStateCreated, DueDate: time.Now().Add(2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateMessageSubscription(ctx, &model.MessageSubscription{
		Key: 104, ProcessInstanceKey: 100, ElementInstanceKey: 101,
		ElementID: "review-message", MessageName: "approved", State: "OPEN",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateMessageSubscription(ctx, &model.MessageSubscription{
		Key: 112, ProcessInstanceKey: 100, ElementInstanceKey: 107,
		ElementID: "review-boundary-message", MessageName: "cancel", State: model.MessageSubscriptionStateOpen,
	}); err != nil {
		t.Fatal(err)
	}
	request := model.InstanceMigrationRequest{
		InstanceKey: 100, SourceDefinitionKey: 10, TargetDefinitionKey: 20,
		ElementMapping: map[string]string{
			"review":                  "approve",
			"review-second":           "approve-second",
			"review-timeout":          "approve-timeout",
			"review-boundary-message": "approve-boundary-message",
			"review-timer":            "approve-timer",
			"review-message":          "approve-message",
		},
		Actor: "admin",
	}
	dryRun := request
	dryRun.DryRun = true
	if result, err := repo.MigrateProcessInstance(ctx, dryRun); err != nil || result.Status != "VALIDATED" {
		t.Fatalf("dry-run = %#v err=%v", result, err)
	}
	if result, err := repo.MigrateProcessInstance(ctx, request); err != nil || result.Status != "APPLIED" {
		t.Fatalf("apply = %#v err=%v", result, err)
	}
	timer, err := repo.GetTimer(ctx, 103)
	if err != nil || timer.ElementID != "approve-timeout" {
		t.Fatalf("timer = %#v err=%v", timer, err)
	}
	subscription, err := repo.GetMessageSubscription(ctx, 104)
	if err != nil || subscription.ElementID != "approve-message" {
		t.Fatalf("subscription = %#v err=%v", subscription, err)
	}
	secondJob, err := repo.GetJob(ctx, 110)
	if err != nil || secondJob.ElementID != "approve-second" {
		t.Fatalf("parallel user task job = %#v err=%v", secondJob, err)
	}
	intermediateTimer, err := repo.GetTimer(ctx, 111)
	if err != nil || intermediateTimer.ElementID != "approve-timer" {
		t.Fatalf("intermediate timer = %#v err=%v", intermediateTimer, err)
	}
	boundarySubscription, err := repo.GetMessageSubscription(ctx, 112)
	if err != nil || boundarySubscription.ElementID != "approve-boundary-message" {
		t.Fatalf("boundary subscription = %#v err=%v", boundarySubscription, err)
	}
	for key, want := range map[int64]string{
		101: "approve",
		105: "approve-second",
		106: "approve-timeout",
		107: "approve-boundary-message",
		108: "approve-timer",
		109: "approve-message",
	} {
		var element model.ElementInstance
		if err := repo.DB.WithContext(ctx).First(&element, "key = ?", key).Error; err != nil {
			t.Fatal(err)
		}
		if element.ElementID != want || element.ProcessDefinitionKey != 20 {
			t.Fatalf("active element %d = %#v, want id=%q definition=20", key, element, want)
		}
	}
}

func TestInstanceMigrationLeavesCompletedHistoryUntouched(t *testing.T) {
	repo := setupRuntimeMigrationRepository(t)
	ctx := tenantContext("tenant-a")
	seedMigratableInstance(t, repo, ctx, `<definitions><process id="orders"><userTask id="approve"/></process></definitions>`)
	completedElement := model.ElementInstance{
		Key: 120, ProcessInstanceKey: 100, ProcessDefinitionKey: 10,
		ElementID: "review", BpmnElementType: "USER_TASK", State: model.ElementInstanceStateCompleted,
	}
	if err := repo.CreateElementInstance(ctx, &completedElement); err != nil {
		t.Fatal(err)
	}
	completedJob := model.Job{
		Key: 121, ProcessInstanceKey: 100, ProcessDefinitionKey: 10,
		ElementInstanceKey: 120, ElementID: "review", Type: canonicalUserTaskJobType, State: model.JobStateCompleted,
	}
	if err := repo.CreateJob(ctx, &completedJob); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTimer(ctx, &model.Timer{
		Key: 122, ProcessInstanceKey: 100, ElementInstanceKey: 120,
		ElementID: "review", State: model.TimerStateTriggered, DueDate: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateMessageSubscription(ctx, &model.MessageSubscription{
		Key: 123, ProcessInstanceKey: 100, ElementInstanceKey: 120,
		ElementID: "review", MessageName: "past", State: model.MessageSubscriptionStateCorrelated,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := repo.MigrateProcessInstance(ctx, model.InstanceMigrationRequest{
		InstanceKey: 100, SourceDefinitionKey: 10, TargetDefinitionKey: 20,
		ElementMapping: map[string]string{"review": "approve"}, Actor: "admin",
	})
	if err != nil || result.Status != model.MigrationStatusApplied {
		t.Fatalf("apply = %#v err=%v", result, err)
	}

	var element model.ElementInstance
	if err := repo.DB.WithContext(ctx).First(&element, "key = ?", 120).Error; err != nil {
		t.Fatal(err)
	}
	job, jobErr := repo.GetJob(ctx, 121)
	timer, timerErr := repo.GetTimer(ctx, 122)
	subscription, subscriptionErr := repo.GetMessageSubscription(ctx, 123)
	if element.ElementID != "review" || element.ProcessDefinitionKey != 10 {
		t.Fatalf("completed element history changed: %#v", element)
	}
	if jobErr != nil || job.ElementID != "review" || job.ProcessDefinitionKey != 10 {
		t.Fatalf("completed job history changed: %#v err=%v", job, jobErr)
	}
	if timerErr != nil || timer.ElementID != "review" {
		t.Fatalf("triggered timer history changed: %#v err=%v", timer, timerErr)
	}
	if subscriptionErr != nil || subscription.ElementID != "review" {
		t.Fatalf("correlated subscription history changed: %#v err=%v", subscription, subscriptionErr)
	}
}

func TestInstanceMigrationRejectsTransitionalElementState(t *testing.T) {
	for _, state := range []model.ElementInstanceState{
		model.ElementInstanceStateActivating,
		model.ElementInstanceStateCompleting,
	} {
		t.Run(string(state), func(t *testing.T) {
			repo := setupRuntimeMigrationRepository(t)
			ctx := tenantContext("tenant-a")
			seedMigratableInstance(t, repo, ctx, `<definitions><process id="orders"><userTask id="approve"/></process></definitions>`)
			if err := repo.DB.WithContext(ctx).Model(&model.ElementInstance{}).
				Where("key = ?", 101).Update("state", state).Error; err != nil {
				t.Fatal(err)
			}

			result, err := repo.MigrateProcessInstance(ctx, model.InstanceMigrationRequest{
				InstanceKey: 100, SourceDefinitionKey: 10, TargetDefinitionKey: 20,
				ElementMapping: map[string]string{"review": "approve"}, Actor: "admin",
			})
			if err != nil || result.Status != model.MigrationStatusRejected || result.ReasonCode != "UNSTABLE_ACTIVE_STATE" {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestInstanceMigrationOperationIDRejectsChangedRequest(t *testing.T) {
	repo := setupRuntimeMigrationRepository(t)
	ctx := tenantContext("tenant-a")
	seedMigratableInstance(t, repo, ctx, `<definitions><process id="orders"><userTask id="approve"/><userTask id="approve-other"/></process></definitions>`)
	request := model.InstanceMigrationRequest{
		OperationID: "migration-operation", InstanceKey: 100,
		SourceDefinitionKey: 10, TargetDefinitionKey: 20,
		ElementMapping: map[string]string{"review": "approve"}, Actor: "admin",
	}
	first, err := repo.MigrateProcessInstance(ctx, request)
	if err != nil || first.Status != "APPLIED" {
		t.Fatalf("first apply = %#v err=%v", first, err)
	}
	retry, err := repo.MigrateProcessInstance(ctx, request)
	if err != nil || retry.Status != "APPLIED" {
		t.Fatalf("idempotent retry = %#v err=%v", retry, err)
	}
	tests := []struct {
		name   string
		change func(*model.InstanceMigrationRequest)
	}{
		{"instance", func(changed *model.InstanceMigrationRequest) { changed.InstanceKey++ }},
		{"source", func(changed *model.InstanceMigrationRequest) { changed.SourceDefinitionKey++ }},
		{"target", func(changed *model.InstanceMigrationRequest) { changed.TargetDefinitionKey++ }},
		{"mapping", func(changed *model.InstanceMigrationRequest) {
			changed.ElementMapping = map[string]string{"review": "approve-other"}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			changed := request
			tc.change(&changed)
			conflict, err := repo.MigrateProcessInstance(ctx, changed)
			if err != nil || conflict.ReasonCode != "OPERATION_CONFLICT" {
				t.Fatalf("changed retry = %#v err=%v", conflict, err)
			}
		})
	}
}

func TestInstanceMigrationRollsBackWhenAuditWriteFails(t *testing.T) {
	repo := setupRuntimeMigrationRepository(t)
	ctx := tenantContext("tenant-a")
	target := `<definitions><process id="orders"><userTask id="approve"/><boundaryEvent id="approve-timeout"><timerEventDefinition/></boundaryEvent><intermediateCatchEvent id="approve-message"><messageEventDefinition/></intermediateCatchEvent></process></definitions>`
	seedMigratableInstance(t, repo, ctx, target)
	if err := repo.CreateTimer(ctx, &model.Timer{
		Key: 103, ProcessInstanceKey: 100, ElementInstanceKey: 101,
		ElementID: "review-timeout", State: model.TimerStateCreated, DueDate: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateMessageSubscription(ctx, &model.MessageSubscription{
		Key: 104, ProcessInstanceKey: 100, ElementInstanceKey: 101,
		ElementID: "review-message", MessageName: "approved", State: model.MessageSubscriptionStateOpen,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DB.Exec(`CREATE TRIGGER reject_migration_audit
		BEFORE INSERT ON process_instance_migration_audit
		BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`).Error; err != nil {
		t.Fatal(err)
	}
	_, err := repo.MigrateProcessInstance(ctx, model.InstanceMigrationRequest{
		InstanceKey: 100, SourceDefinitionKey: 10, TargetDefinitionKey: 20,
		ElementMapping: map[string]string{
			"review": "approve", "review-timeout": "approve-timeout", "review-message": "approve-message",
		},
		OperationID: "atomic-operation", Actor: "admin",
	})
	if err == nil {
		t.Fatal("migration unexpectedly committed without its audit record")
	}
	instance, getErr := repo.GetProcessInstance(ctx, 100)
	if getErr != nil || instance.ProcessDefinitionKey != 10 || instance.Version != 1 {
		t.Fatalf("instance changed after rollback: %#v err=%v", instance, getErr)
	}
	job, getErr := repo.GetJob(ctx, 102)
	if getErr != nil || job.ElementID != "review" || job.ProcessDefinitionKey != 10 {
		t.Fatalf("job changed after rollback: %#v err=%v", job, getErr)
	}
	var element model.ElementInstance
	if getErr := repo.DB.WithContext(ctx).First(&element, "key = ?", 101).Error; getErr != nil ||
		element.ElementID != "review" || element.ProcessDefinitionKey != 10 {
		t.Fatalf("element changed after rollback: %#v err=%v", element, getErr)
	}
	timer, getErr := repo.GetTimer(ctx, 103)
	if getErr != nil || timer.ElementID != "review-timeout" {
		t.Fatalf("timer changed after rollback: %#v err=%v", timer, getErr)
	}
	subscription, getErr := repo.GetMessageSubscription(ctx, 104)
	if getErr != nil || subscription.ElementID != "review-message" {
		t.Fatalf("subscription changed after rollback: %#v err=%v", subscription, getErr)
	}
	var operationCount, auditCount int64
	if err := repo.DB.WithContext(ctx).Model(&model.ProcessInstanceMigration{}).
		Where("operation_id = ?", "atomic-operation").Count(&operationCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.DB.WithContext(ctx).Table("process_instance_migration_audit").
		Where("operation_id = ?", "atomic-operation").Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if operationCount != 0 || auditCount != 0 {
		t.Fatalf("atomic rollback retained operation=%d audit=%d", operationCount, auditCount)
	}
}

func TestInstanceMigrationRejectedFixtures(t *testing.T) {
	tests := []struct {
		name, target, reason string
		mapping              map[string]string
	}{
		{"incomplete mapping", `<definitions><process id="orders"><userTask id="approve"/></process></definitions>`, "INCOMPLETE_MAPPING", map[string]string{}},
		{"missing source", `<definitions><process id="orders"><userTask id="approve"/></process></definitions>`, "SOURCE_ELEMENT_NOT_FOUND", map[string]string{"other": "approve"}},
		{"missing target", `<definitions><process id="orders"><userTask id="approve"/></process></definitions>`, "TARGET_ELEMENT_NOT_FOUND", map[string]string{"review": "absent"}},
		{"incompatible type", `<definitions><process id="orders"><exclusiveGateway id="approve"/></process></definitions>`, "INCOMPATIBLE_ELEMENT_TYPE", map[string]string{"review": "approve"}},
		{"multi instance", `<definitions><process id="orders"><userTask id="approve"><multiInstanceLoopCharacteristics/></userTask></process></definitions>`, "UNSUPPORTED_MULTI_INSTANCE", map[string]string{"review": "approve"}},
		{"nested scope", `<definitions><process id="orders"><subProcess id="nested"/></process></definitions>`, "UNSUPPORTED_NESTED_SCOPE", map[string]string{"review": "approve"}},
		{"event subprocess", `<definitions><process id="orders"><subProcess id="event" triggeredByEvent="true"/></process></definitions>`, "UNSUPPORTED_EVENT_SUBPROCESS", map[string]string{"review": "approve"}},
		{"compensation single quotes", `<definitions><process id="orders"><serviceTask id="undo" isForCompensation='true'/></process></definitions>`, "UNSUPPORTED_COMPENSATION", map[string]string{"review": "approve"}},
		{"compensation double quotes", `<definitions><process id="orders"><serviceTask id="undo" isForCompensation="true"/></process></definitions>`, "UNSUPPORTED_COMPENSATION", map[string]string{"review": "approve"}},
		{"compensation event", `<definitions><process id="orders"><boundaryEvent id="undo"><compensateEventDefinition/></boundaryEvent></process></definitions>`, "UNSUPPORTED_COMPENSATION", map[string]string{"review": "approve"}},
		{"call activity", `<definitions><process id="orders"><callActivity id="called"/></process></definitions>`, "UNSUPPORTED_NESTED_SCOPE", map[string]string{"review": "approve"}},
		{"transaction", `<definitions><process id="orders"><transaction id="tx"/></process></definitions>`, "UNSUPPORTED_TRANSACTION", map[string]string{"review": "approve"}},
		{"ad hoc subprocess", `<definitions><process id="orders"><adHocSubProcess id="ad-hoc"/></process></definitions>`, "UNSUPPORTED_AD_HOC_SUBPROCESS", map[string]string{"review": "approve"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := setupRuntimeMigrationRepository(t)
			ctx := tenantContext("tenant-a")
			seedMigratableInstance(t, repo, ctx, tc.target)
			result, err := repo.MigrateProcessInstance(ctx, model.InstanceMigrationRequest{
				InstanceKey: 100, SourceDefinitionKey: 10, TargetDefinitionKey: 20,
				ElementMapping: tc.mapping, DryRun: true, Actor: "admin",
			})
			if err != nil || result.Status != model.MigrationStatusRejected || string(result.ReasonCode) != tc.reason {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestInstanceMigrationRejectsUnsupportedConstructsInSource(t *testing.T) {
	tests := []struct {
		name, source, reason string
	}{
		{"multi instance", `<definitions><process id="orders"><userTask id="review"><multiInstanceLoopCharacteristics/></userTask></process></definitions>`, "UNSUPPORTED_MULTI_INSTANCE"},
		{"nested scope", `<definitions><process id="orders"><subProcess id="nested"/></process></definitions>`, "UNSUPPORTED_NESTED_SCOPE"},
		{"call activity", `<definitions><process id="orders"><callActivity id="called"/></process></definitions>`, "UNSUPPORTED_NESTED_SCOPE"},
		{"event subprocess", `<definitions><process id="orders"><subProcess id="event" triggeredByEvent='true'/></process></definitions>`, "UNSUPPORTED_EVENT_SUBPROCESS"},
		{"compensation single quotes", `<definitions><process id="orders"><serviceTask id="undo" isForCompensation='TRUE'/></process></definitions>`, "UNSUPPORTED_COMPENSATION"},
		{"compensation double quotes", `<definitions><process id="orders"><serviceTask id="undo" isForCompensation="true"/></process></definitions>`, "UNSUPPORTED_COMPENSATION"},
		{"transaction", `<definitions><process id="orders"><transaction id="tx"/></process></definitions>`, "UNSUPPORTED_TRANSACTION"},
		{"ad hoc subprocess", `<definitions><process id="orders"><adHocSubProcess id="ad-hoc"/></process></definitions>`, "UNSUPPORTED_AD_HOC_SUBPROCESS"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := setupRuntimeMigrationRepository(t)
			ctx := tenantContext("tenant-a")
			seedMigratableInstance(t, repo, ctx, `<definitions><process id="orders"><userTask id="approve"/></process></definitions>`)
			if err := repo.DB.WithContext(ctx).Model(&model.Process{}).Where("key = ?", 10).
				Update("resource", []byte(tc.source)).Error; err != nil {
				t.Fatal(err)
			}
			result, err := repo.MigrateProcessInstance(ctx, model.InstanceMigrationRequest{
				InstanceKey: 100, SourceDefinitionKey: 10, TargetDefinitionKey: 20,
				ElementMapping: map[string]string{"review": "approve"}, DryRun: true, Actor: "admin",
			})
			if err != nil || result.Status != model.MigrationStatusRejected || string(result.ReasonCode) != tc.reason {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestInstanceMigrationConcurrentCompletionWins(t *testing.T) {
	repo := setupRuntimeMigrationRepository(t)
	ctx := tenantContext("tenant-a")
	seedMigratableInstance(t, repo, ctx, `<definitions><process id="orders"><userTask id="approve"/></process></definitions>`)
	if err := repo.DB.WithContext(ctx).Model(&model.ProcessInstance{}).Where("key = ?", 100).Update("state", "COMPLETED").Error; err != nil {
		t.Fatal(err)
	}
	result, err := repo.MigrateProcessInstance(ctx, model.InstanceMigrationRequest{
		InstanceKey: 100, SourceDefinitionKey: 10, TargetDefinitionKey: 20,
		ElementMapping: map[string]string{"review": "approve"}, Actor: "admin",
	})
	if err != nil || result.ReasonCode != "SOURCE_STATE_CHANGED" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestTenantIsolationAndStaleFenceAcrossThreeOwners(t *testing.T) {
	repo := setupRuntimeMigrationRepository(t)
	a := tenantContext("tenant-a")
	b := tenantContext("tenant-b")
	if err := repo.CreateProcessInstance(a, &model.ProcessInstance{Key: 500, State: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetProcessInstance(b, 500); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign tenant read disclosed row: %v", err)
	}

	expires := time.Now().Add(time.Minute)
	timer := &model.Timer{Key: 700, State: "CREATED", DueDate: time.Now(), ClaimOwner: "runtime-a", ClaimGeneration: 1, ClaimExpiresAt: &expires}
	if err := repo.CreateTimer(a, timer); err != nil {
		t.Fatal(err)
	}
	// Simulate two successive expiry/reclamations across three active replicas.
	if err := repo.DB.WithContext(a).Model(&model.Timer{}).Where("key = ?", 700).
		Updates(map[string]any{"claim_owner": "runtime-b", "claim_generation": 2, "claim_expires_at": expires}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.DB.WithContext(a).Model(&model.Timer{}).Where("key = ?", 700).
		Updates(map[string]any{"claim_owner": "runtime-c", "claim_generation": 3, "claim_expires_at": expires}).Error; err != nil {
		t.Fatal(err)
	}
	stale := *timer
	stale.State = "TRIGGERED"
	if err := repo.UpdateClaimedTimer(a, &stale, "runtime-a", 1); !errors.Is(err, repository.ErrLeaseLost) {
		t.Fatalf("stale runtime-a completion = %v", err)
	}
	current := stale
	if err := repo.UpdateClaimedTimer(a, &current, "runtime-c", 3); err != nil {
		t.Fatalf("current runtime-c completion failed: %v", err)
	}
}

func TestMessageCorrelationIsTenantScoped(t *testing.T) {
	repo := setupRuntimeMigrationRepository(t)
	a, b := tenantContext("tenant-a"), tenantContext("tenant-b")
	if err := repo.CreateMessageSubscription(a, &model.MessageSubscription{
		Key: 801, MessageName: "payment", CorrelationKey: "order-1", State: "OPEN",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateMessageSubscription(b, &model.MessageSubscription{
		Key: 802, MessageName: "payment", CorrelationKey: "order-1", State: "OPEN",
	}); err != nil {
		t.Fatal(err)
	}
	matches, err := repo.ListMessageSubscriptions(a, "payment", "order-1")
	if err != nil || len(matches) != 1 || matches[0].Key != 801 {
		t.Fatalf("tenant-a correlation leaked: %#v err=%v", matches, err)
	}
}
