package http

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/services/workflow-query/internal/application"
	"github.com/artificialflow/artificialflow/backend/services/workflow-query/internal/domain/repository"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// MockRepository implements repository.QueryRepository for testing
type MockRepository struct {
	instances []model.ProcessInstance
	workflows []model.Process
}

func (m *MockRepository) SearchInstances(ctx context.Context, filter repository.InstanceFilter) (*repository.InstanceSearchResult, error) {
	instances := m.instances
	if filter.State != "" {
		filtered := make([]model.ProcessInstance, 0)
		for _, instance := range m.instances {
			if string(instance.State) == filter.State {
				filtered = append(filtered, instance)
			}
		}
		instances = filtered
	}

	return &repository.InstanceSearchResult{
		Instances: instances,
		Total:     int64(len(instances)),
	}, nil
}

func (m *MockRepository) SearchWorkflows(ctx context.Context, filter repository.WorkflowFilter) (*repository.WorkflowSearchResult, error) {
	return &repository.WorkflowSearchResult{
		Workflows: m.workflows,
		Total:     int64(len(m.workflows)),
	}, nil
}

func (m *MockRepository) GetInstance(ctx context.Context, id string) (*model.ProcessInstance, error) {
	for _, instance := range m.instances {
		if fmt.Sprintf("%d", instance.Key) == id {
			return &instance, nil
		}
	}
	return nil, fmt.Errorf("instance not found")
}

func setupTestHandler() *Handler {
	repo := &MockRepository{
		instances: []model.ProcessInstance{
			{
				Key:                  123,
				ProcessDefinitionKey: 456,
				State:                "ACTIVE",
				CreatedAt:            time.Now(),
			},
		},
		workflows: []model.Process{
			{
				Key:           456,
				BpmnProcessID: "process-1",
				Version:       1,
				CreatedAt:     time.Now(),
			},
		},
	}
	service := application.NewQueryService(repo)
	return NewHandler(service)
}

func registerTestRoutes(r *mux.Router, h *Handler) {
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if _, ok := auth.PrincipalFromContext(req.Context()); !ok {
				principal := auth.Principal{
					Subject: "test-admin",
					Roles:   []string{auth.RoleAdmin},
				}
				req = req.WithContext(auth.WithPrincipal(req.Context(), principal))
			}
			next.ServeHTTP(w, req)
		})
	})
	h.RegisterRoutes(r)
}

func TestSearchInstances(t *testing.T) {
	h := setupTestHandler()
	r := mux.NewRouter()
	registerTestRoutes(r, h)

	req, _ := http.NewRequest("GET", "/instances", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	var result InstanceSearchResponse
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if result.Total != 1 {
		t.Errorf("Expected 1 instance, got %d", result.Total)
	}
	if result.Instances[0].ID != "123" {
		t.Errorf("Expected projected instance id 123, got %s", result.Instances[0].ID)
	}
	if result.Instances[0].WorkflowID != "456" {
		t.Errorf("Expected projected workflow id 456, got %s", result.Instances[0].WorkflowID)
	}
	if result.Instances[0].Status != "RUNNING" {
		t.Errorf("Expected projected status RUNNING, got %s", result.Instances[0].Status)
	}
}

func TestSearchInstancesRejectsCustomRoleProjectionRead(t *testing.T) {
	h := setupTestHandler()
	r := mux.NewRouter()
	registerTestRoutes(r, h)

	req, _ := http.NewRequest("GET", "/instances", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{Subject: "accountant", Roles: []string{"accountant"}}))
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusForbidden {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusForbidden)
	}
}

func TestSearchWorkflowsRejectsCustomRoleProjectionRead(t *testing.T) {
	h := setupTestHandler()
	r := mux.NewRouter()
	registerTestRoutes(r, h)

	req, _ := http.NewRequest("GET", "/workflows", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{Subject: "accountant", Roles: []string{"accountant"}}))
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusForbidden {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusForbidden)
	}
}

func TestModelerReadsWorkflowProjectionOnly(t *testing.T) {
	h := setupTestHandler()
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	modeler := auth.Principal{Subject: "modeler", Roles: []string{auth.RoleBusiness}}

	workflowsReq, _ := http.NewRequest("GET", "/workflows", nil)
	workflowsReq = workflowsReq.WithContext(auth.WithPrincipal(workflowsReq.Context(), modeler))
	workflowsRec := httptest.NewRecorder()
	r.ServeHTTP(workflowsRec, workflowsReq)
	if status := workflowsRec.Code; status != http.StatusOK {
		t.Fatalf("workflow handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	instancesReq, _ := http.NewRequest("GET", "/instances", nil)
	instancesReq = instancesReq.WithContext(auth.WithPrincipal(instancesReq.Context(), modeler))
	instancesRec := httptest.NewRecorder()
	r.ServeHTTP(instancesRec, instancesReq)
	if status := instancesRec.Code; status != http.StatusForbidden {
		t.Fatalf("instance handler returned wrong status code: got %v want %v", status, http.StatusForbidden)
	}
}

func TestSearchWorkflows(t *testing.T) {
	h := setupTestHandler()
	r := mux.NewRouter()
	registerTestRoutes(r, h)

	req, _ := http.NewRequest("GET", "/workflows", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	var result WorkflowSearchResponse
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if result.Total != 1 {
		t.Errorf("Expected 1 workflow, got %d", result.Total)
	}
	if result.Workflows[0].ID != "456" {
		t.Errorf("Expected projected workflow id 456, got %s", result.Workflows[0].ID)
	}
	if result.Workflows[0].ProcessDefinitionID != "process-1" {
		t.Errorf("Expected projected process_definition_id process-1, got %s", result.Workflows[0].ProcessDefinitionID)
	}
}

func TestSearchInstances_RunningStateNormalizedToActive(t *testing.T) {
	h := setupTestHandler()
	r := mux.NewRouter()
	registerTestRoutes(r, h)

	req, _ := http.NewRequest("GET", "/instances?state=RUNNING", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var result InstanceSearchResponse
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if result.Total != 1 {
		t.Errorf("Expected 1 ACTIVE instance for RUNNING filter, got %d", result.Total)
	}
}

func TestGetInstance_ReturnsProjectedResponse(t *testing.T) {
	h := setupTestHandler()
	r := mux.NewRouter()
	registerTestRoutes(r, h)

	req, _ := http.NewRequest("GET", "/instances/123", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var response InstanceResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.ID != "123" {
		t.Fatalf("expected id 123, got %s", response.ID)
	}
	if response.WorkflowID != "456" {
		t.Fatalf("expected workflow_id 456, got %s", response.WorkflowID)
	}
	if response.Status != "RUNNING" {
		t.Fatalf("expected status RUNNING, got %s", response.Status)
	}
}

func TestSearchInstances_InvalidWorkflowIDReturnsBadRequest(t *testing.T) {
	h := setupTestHandler()
	r := mux.NewRouter()
	registerTestRoutes(r, h)

	req, _ := http.NewRequest("GET", "/instances?workflowId=not-a-number", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}
}

func TestGetInstance_InvalidIDReturnsBadRequest(t *testing.T) {
	h := setupTestHandler()
	r := mux.NewRouter()
	registerTestRoutes(r, h)

	req, _ := http.NewRequest("GET", "/instances/not-a-number", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}
}

func TestNormalizeInstanceStateFilter(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "running uppercase", input: "RUNNING", want: "ACTIVE"},
		{name: "running lowercase", input: "running", want: "ACTIVE"},
		{name: "trim whitespace", input: " active ", want: "ACTIVE"},
		{name: "completed passthrough", input: "COMPLETED", want: "COMPLETED"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeInstanceStateFilter(tc.input)
			if got != tc.want {
				t.Fatalf("normalizeInstanceStateFilter(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestMapInstanceStatus(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "active maps to running", input: "ACTIVE", want: "RUNNING"},
		{name: "activated maps to running", input: "ACTIVATED", want: "RUNNING"},
		{name: "completed maps to completed", input: "COMPLETED", want: "COMPLETED"},
		{name: "terminated maps to failed", input: "TERMINATED", want: "FAILED"},
		{name: "unknown maps to pending", input: "UNKNOWN", want: "PENDING"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapInstanceStatus(tc.input); got != tc.want {
				t.Fatalf("mapInstanceStatus(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
