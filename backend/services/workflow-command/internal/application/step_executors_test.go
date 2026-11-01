package application

import (
	"context"
	"errors"
	"testing"

	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/domain/repository"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/infrastructure/messaging"
)

// MockRepository for testing
type MockRepository struct {
	repository.Repository
	createdIncidents []*model.Incident
	createdJobs      []*model.Job
}

func (m *MockRepository) CreateIncident(ctx context.Context, incident *model.Incident) error {
	m.createdIncidents = append(m.createdIncidents, incident)
	return nil
}

// Implement other methods as needed (dummies)
func (m *MockRepository) CreateElementInstance(ctx context.Context, element *model.ElementInstance) error {
	return nil
}
func (m *MockRepository) UpdateElementInstance(ctx context.Context, element *model.ElementInstance) error {
	return nil
}
func (m *MockRepository) GetJob(ctx context.Context, key int64) (*model.Job, error) {
	return nil, errors.New("not found")
}
func (m *MockRepository) CreateJob(ctx context.Context, job *model.Job) error {
	m.createdJobs = append(m.createdJobs, job)
	return nil
}
func (m *MockRepository) UpdateJob(ctx context.Context, job *model.Job) error { return nil }

// Add more if needed by ScriptTaskExecutor. Wait, it calls persistVariables?
func (m *MockRepository) UpdateProcessInstance(ctx context.Context, instance *model.ProcessInstance) error {
	return nil
}
func (m *MockRepository) CreateVariable(ctx context.Context, variable *model.Variable) error {
	return nil
}
func (m *MockRepository) UpdateVariable(ctx context.Context, variable *model.Variable) error {
	return nil
}
func (m *MockRepository) GetVariable(ctx context.Context, scopeKey int64, name string) (*model.Variable, error) {
	return nil, nil
}

func TestScriptTaskExecutorCreatesIncidentOnFailure(t *testing.T) {
	mockRepo := &MockRepository{}
	engine := NewEngine(mockRepo, &messaging.NoOpPublisher{})

	executor := &ScriptTaskExecutor{}

	// Setup Step Definition with failing script
	step := &model.StepDefinition{
		ID:   "script_step",
		Type: model.StepTypeScriptTask,
		Properties: map[string]any{
			"script": "throw new Error('boom')",
		},
	}

	// Setup Workflow Instance
	instance := &model.WorkflowInstance{
		ID: "123",
		Executions: []model.Execution{
			{
				ID:                 "exec_1",
				StepID:             "script_step",
				Status:             "ACTIVE",
				ElementInstanceKey: 100,
			},
		},
		Context: make(map[string]any),
	}

	execID := "exec_1"
	wf := &model.WorkflowDefinition{ID: 1}

	// Execute
	err := executor.Execute(context.Background(), engine, instance, step, execID, wf)

	// Verify Error Returned matches incident creation?
	// The implementation returns wrapped error. verify it's the script error.
	if err == nil {
		t.Fatalf("expected error from failing script")
	}

	// Verify Incident Created
	if len(mockRepo.createdIncidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(mockRepo.createdIncidents))
	}

	incident := mockRepo.createdIncidents[0]
	if incident.ErrorType != "SCRIPT_ERROR" {
		t.Errorf("expected error type SCRIPT_ERROR, got %s", incident.ErrorType)
	}
	if incident.ErrorMessage != "Error: boom at <eval>:1:7(1)" { // Goja error format varies
		// Just check contains "boom"
		if !contains(incident.ErrorMessage, "boom") {
			t.Errorf("expected error message to contain 'boom', got %s", incident.ErrorMessage)
		}
	}
	if incident.ElementInstanceKey != 100 {
		t.Errorf("expected ElementInstanceKey 100, got %d", incident.ElementInstanceKey)
	}
}

func TestServiceTaskExecutorPublishesJobCreated(t *testing.T) {
	mockRepo := &MockRepository{}
	publisher := &capturedPublisher{}
	engine := NewEngine(mockRepo, publisher)
	executor := &ServiceTaskExecutor{}
	step := &model.StepDefinition{
		ID: "service_step", Type: model.StepTypeServiceTask, Implementation: "external-work",
	}
	instance := &model.WorkflowInstance{
		ID: "123",
		Executions: []model.Execution{{
			ID: "exec_1", StepID: step.ID, Status: "ACTIVE", ElementInstanceKey: 100,
		}},
		Context: map[string]any{},
	}

	if err := executor.Execute(context.Background(), engine, instance, step, "exec_1", &model.WorkflowDefinition{ID: 1}); err != nil {
		t.Fatal(err)
	}
	if len(mockRepo.createdJobs) != 1 {
		t.Fatalf("created jobs = %d, want 1", len(mockRepo.createdJobs))
	}
	if len(publisher.events) != 1 || publisher.events[0] != "JobCreated" {
		t.Fatalf("published events = %v, want [JobCreated]", publisher.events)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > len(substr) && (s[:len(substr)] == substr || contains(s[1:], substr))))
}
