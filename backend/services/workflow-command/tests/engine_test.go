package tests

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/application"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/infrastructure/messaging"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/infrastructure/persistence"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestEngine(t *testing.T) *application.Engine {
	// Use unique in-memory DB for each test to ensure isolation
	// SQLite in-memory mode with shared cache
	uid := uuid.New().String()
	dsn := fmt.Sprintf("file:memdb%s?mode=memory&cache=shared", uid)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open sqlite db: %v", err)
	}

	// Auto-migrate
	if err := db.AutoMigrate(
		&model.Process{},
		&model.ProcessInstance{},
		&model.ElementInstance{},
		&model.Variable{},
		&model.Job{},
		&model.Incident{},
		&model.Timer{},
		&model.MessageSubscription{},
		&model.IdempotencyRecord{},
		&model.OutboxMessage{},
		&model.DecisionDefinition{},
	); err != nil {
		t.Fatalf("Failed to migrate schema: %v", err)
	}

	repo := persistence.NewGormRepository(db)
	return application.NewEngine(repo, &messaging.NoOpPublisher{})
}

func TestDeployWorkflow(t *testing.T) {
	e := setupTestEngine(t)

	steps := []model.StepDefinition{
		{
			ID:   "step1",
			Type: model.StepTypeUserTask,
			Name: "Step 1",
			Outgoing: []model.Transition{
				{TargetRef: "step2"},
			},
		},
		{
			ID:       "step2",
			Type:     model.StepTypeEnd,
			Name:     "Step 2",
			Outgoing: []model.Transition{},
		},
	}

	wf, err := e.DeployWorkflow(context.Background(), "Test Workflow", steps)
	if err != nil {
		t.Fatalf("DeployWorkflow failed: %v", err)
	}

	if wf.ID == 0 {
		t.Error("Expected Workflow ID to be generated")
	}
	if wf.Name != "Test Workflow" {
		t.Errorf("Expected Name 'Test Workflow', got '%s'", wf.Name)
	}
	if len(wf.Steps) != 2 {
		t.Errorf("Expected 2 steps, got %d", len(wf.Steps))
	}
}

func TestDeployWorkflowUsesContextTenant(t *testing.T) {
	e := setupTestEngine(t)
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{TenantID: "tenant-a"})
	wf, err := e.DeployWorkflow(ctx, "Tenant workflow", []model.StepDefinition{{
		ID: "end", Type: model.StepTypeEnd,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if wf.TenantID != "tenant-a" {
		t.Fatalf("tenant = %q, want tenant-a", wf.TenantID)
	}
}

func TestWorkflowExecution(t *testing.T) {
	e := setupTestEngine(t)

	// Deploy
	steps := []model.StepDefinition{
		{
			ID:   "start",
			Type: model.StepTypeStart,
			Name: "Start",
			Outgoing: []model.Transition{
				{TargetRef: "task1"},
			},
		},
		{
			ID:   "task1",
			Type: model.StepTypeUserTask,
			Name: "Task 1",
			Outgoing: []model.Transition{
				{TargetRef: "end"},
			},
		},
		{
			ID:       "end",
			Type:     model.StepTypeEnd,
			Name:     "End",
			Outgoing: []model.Transition{},
		},
	}

	wf, err := e.DeployWorkflow(context.Background(), "Execution Test", steps)
	if err != nil {
		t.Fatalf("Deploy failed: %v", err)
	}

	// Start Instance
	ctx := map[string]any{"foo": "bar"}
	instance, err := e.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), ctx)
	if err != nil {
		t.Fatalf("StartInstance failed: %v", err)
	}

	if instance.Status != model.StatusRunning {
		t.Errorf("Expected status RUNNING, got %s", instance.Status)
	}

	currentSteps := getCurrentSteps(instance)
	if !contains(currentSteps, "task1") {
		// Start event should have auto-advanced to task1
		t.Errorf("Expected current step 'task1', got %v", currentSteps)
	}

	// Complete Task 1
	if err := e.CompleteTask(context.Background(), instance.ID, ""); err != nil {
		t.Fatalf("CompleteTask(task1) failed: %v", err)
	}

	// Check State (End)
	instance, err = e.GetInstance(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetInstance failed: %v", err)
	}

	// Task 1 -> End. End is automatic. So Status should be COMPLETED.
	if instance.Status != model.StatusCompleted {
		t.Errorf("Expected status COMPLETED, got %s. Executions: %+v", instance.Status, instance.Executions)
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func getCurrentSteps(instance *model.WorkflowInstance) []string {
	var steps []string
	for _, ex := range instance.Executions {
		if ex.Status == "ACTIVE" {
			steps = append(steps, ex.StepID)
		}
	}
	return steps
}

func TestWorkflowExecution_InvalidStep(t *testing.T) {
	e := setupTestEngine(t)

	// Deploy workflow with broken link
	steps := []model.StepDefinition{
		{
			ID:   "step1",
			Type: model.StepTypeTask,
			Name: "Step 1",
			Outgoing: []model.Transition{
				{TargetRef: "non_existent_step"},
			},
		},
	}

	wf, err := e.DeployWorkflow(context.Background(), "Broken Workflow", steps)
	if err != nil {
		t.Fatalf("Deploy failed: %v", err)
	}

	instance, err := e.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)
	if err != nil {
		t.Fatalf("StartInstance failed: %v", err)
	}

	// Try to complete step1, should fail because next step doesn't exist
	err = e.CompleteTask(context.Background(), instance.ID, "")
	if err == nil {
		t.Error("Expected error due to missing next step, got nil")
	}
}

func TestDeployWorkflowFromBPMN(t *testing.T) {
	e := setupTestEngine(t)

	xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="Definitions_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_1" name="BPMN Test" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1" name="Start">
      <bpmn:outgoing>Flow_1</bpmn:outgoing>
    </bpmn:startEvent>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="EndEvent_1"/>
    <bpmn:endEvent id="EndEvent_1" name="End">
      <bpmn:incoming>Flow_1</bpmn:incoming>
    </bpmn:endEvent>
  </bpmn:process>
</bpmn:definitions>`)

	wf, err := e.DeployWorkflowFromBPMN(context.Background(), xmlData)
	if err != nil {
		t.Fatalf("DeployWorkflowFromBPMN failed: %v", err)
	}

	if wf.Name != "BPMN Test" {
		t.Errorf("Expected name 'BPMN Test', got '%s'", wf.Name)
	}
	if len(wf.Steps) != 2 {
		t.Errorf("Expected 2 steps, got %d", len(wf.Steps))
	}
}
