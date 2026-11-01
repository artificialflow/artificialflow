package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/iam"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	workerSDK "github.com/artificialflow/artificialflow/backend/libs/worker"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/application"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/infrastructure/messaging"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/infrastructure/persistence"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/interfaces/http/dto"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestHandler(t *testing.T) *Handler {
	return setupTestHandlerWithIdentityConfig(t, iam.DeploymentConfig{
		IAMEnabled:   true,
		ProviderName: "Corporate OIDC",
		AuthConfig: auth.Config{
			InternalIssuerURL:   "http://identity.internal/realms/artificialflow",
			ExternalIssuerURL:   "https://identity.example.com/realms/artificialflow",
			ClientID:            "workflow-backend",
			TokenValidationMode: auth.TokenModeJWT,
			EnforceAudience:     false,
			AllowInsecureIssuer: true,
			ClaimSubjectPath:    "sub",
			ClaimRolesPath:      "roles",
			ClaimTenantPath:     "tenant_id",
			ClaimEmailPath:      "email",
			ClaimNamePath:       "name",
		},
		FrontendConfig: iam.FrontendAuthConfig{
			Enabled:       true,
			OIDCAuthority: "https://identity.example.com/realms/artificialflow",
			OIDCClientID:  "workflow-frontend",
		},
	})
}

func setupTestHandlerWithIdentityConfig(t *testing.T, identityConfig iam.DeploymentConfig) *Handler {
	h, _ := setupTestHandlerWithRepository(t, identityConfig)
	return h
}

func setupTestHandlerWithRepository(t *testing.T, identityConfig iam.DeploymentConfig) (*Handler, *persistence.GormRepository) {
	// Use in-memory SQLite for testing
	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open sqlite db: %v", err)
	}

	// Auto-migrate schema using the helper from persistence if we made it public,
	// or just manual migration here since we are using GormRepository.
	// Actually, NewGormRepository doesn't migrate, NewPostgresRepository does.
	// We should duplicate migration logic here for tests.
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
		&model.ProcessInstanceMigration{},
	); err != nil {
		t.Fatalf("Failed to migrate schema: %v", err)
	}

	repo := persistence.NewGormRepository(db)
	e := application.NewEngine(repo, &messaging.NoOpPublisher{})
	return NewHandler(e, identityConfig), repo
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

func TestIdentityConfigAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	configResp, err := http.Get(ts.URL + "/identity/config")
	if err != nil {
		t.Fatalf("Failed to fetch identity config: %v", err)
	}
	if configResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(configResp.Body)
		t.Fatalf("Expected 200 OK for initial config, got %d: %s", configResp.StatusCode, string(body))
	}
	var config dto.IdentityConfigResponse
	if err := json.NewDecoder(configResp.Body).Decode(&config); err != nil {
		t.Fatalf("Failed to decode initial identity config: %v", err)
	}
	if !config.DeploymentMode {
		t.Fatal("Expected deployment_mode true")
	}
	if config.ProviderName != "Corporate OIDC" {
		t.Fatalf("Expected provider name Corporate OIDC, got %q", config.ProviderName)
	}
	if !config.AuthEnabled {
		t.Fatalf("Expected backend auth to be enabled")
	}
	if !config.FrontendAuthEnabled {
		t.Fatalf("Expected frontend auth to be enabled")
	}
	if config.TokenValidationMode != auth.TokenModeJWT {
		t.Fatalf("Expected token mode %q, got %q", auth.TokenModeJWT, config.TokenValidationMode)
	}
	if config.InternalIssuerURL != "http://identity.internal/realms/artificialflow" {
		t.Fatalf("Unexpected internal issuer %q", config.InternalIssuerURL)
	}
	if config.ExternalIssuerURL != "https://identity.example.com/realms/artificialflow" {
		t.Fatalf("Unexpected external issuer %q", config.ExternalIssuerURL)
	}
	if config.ClientID != "workflow-backend" {
		t.Fatalf("Expected client ID workflow-backend, got %q", config.ClientID)
	}
	if config.FrontendOIDCAuthority != "https://identity.example.com/realms/artificialflow" {
		t.Fatalf("Unexpected frontend authority %q", config.FrontendOIDCAuthority)
	}
	if config.FrontendOIDCClientID != "workflow-frontend" {
		t.Fatalf("Unexpected frontend client id %q", config.FrontendOIDCClientID)
	}
	expectedRoles := []string{auth.RoleAdmin, auth.RoleBusiness, auth.RoleClient, auth.RoleAnalytics}
	if len(config.StandardRoles) != len(expectedRoles) {
		t.Fatalf("Expected %d standard roles, got %#v", len(expectedRoles), config.StandardRoles)
	}
	for i, role := range expectedRoles {
		if config.StandardRoles[i] != role {
			t.Fatalf("Expected standard role %q at index %d, got %q", role, i, config.StandardRoles[i])
		}
	}
}

func TestIdentityManagementRoutesExternalModeReturnNotFound(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	req := httptest.NewRequest(http.MethodGet, "/identity/management/users", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{Subject: "admin", Roles: []string{auth.RoleAdmin}}))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("Expected 404 for external IAM management route, got %d", rec.Code)
	}
}

func TestCompleteTaskAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)

	// Deploy Workflow
	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "task1"}}},
		{ID: "task1", Type: model.StepTypeUserTask, Outgoing: []model.Transition{{TargetRef: "end"}}},
		{ID: "end", Type: model.StepTypeEnd},
	}
	wf, err := h.engine.DeployWorkflow(context.Background(), "API Test", steps)
	if err != nil {
		t.Fatalf("Failed to deploy: %v", err)
	}

	// Start Instance
	instance, err := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)
	if err != nil {
		t.Fatalf("Failed to start instance: %v", err)
	}

	// Complete Task without step_id
	ts := httptest.NewServer(r)
	defer ts.Close()

	url := ts.URL + "/instances/" + instance.ID + "/complete"
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		t.Fatalf("Failed to call complete: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", resp.StatusCode)
	}

	// Verify completion
	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	if instance.Status != model.StatusCompleted {
		t.Errorf("Expected instance completed, got %s", instance.Status)
	}
}

func TestCompleteTaskByExecutionIDAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	// Deploy
	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "task1"}}},
		{ID: "task1", Type: model.StepTypeUserTask, Outgoing: []model.Transition{{TargetRef: "end"}}},
		{ID: "end", Type: model.StepTypeEnd},
	}
	wf, _ := h.engine.DeployWorkflow(context.Background(), "ExecID Test", steps)
	instance, _ := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)

	// Find Active Execution ID
	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	var execID string
	for _, ex := range instance.Executions {
		if ex.Status == "ACTIVE" {
			execID = ex.ID
			break
		}
	}

	// Complete using Execution ID
	url := ts.URL + "/instances/" + instance.ID + "/complete"
	reqBody := dto.CompleteTaskRequest{StepID: execID}
	jsonBody, _ := json.Marshal(reqBody)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		t.Fatalf("Failed to call complete: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("Expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	// Verify
	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	if instance.Status != model.StatusCompleted {
		t.Errorf("Expected completed, got %s", instance.Status)
	}
}

func TestCompleteParallelTaskAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	// Deploy Parallel Workflow
	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "split"}}},
		{ID: "split", Type: model.StepTypeGatewayParallel, Outgoing: []model.Transition{
			{TargetRef: "taskA"},
			{TargetRef: "taskB"},
		}},
		{ID: "taskA", Type: model.StepTypeUserTask, Outgoing: []model.Transition{{TargetRef: "join"}}},
		{ID: "taskB", Type: model.StepTypeUserTask, Outgoing: []model.Transition{{TargetRef: "join"}}},
		{ID: "join", Type: model.StepTypeGatewayParallel, Incoming: []string{"taskA", "taskB"}, Outgoing: []model.Transition{{TargetRef: "end"}}},
		{ID: "end", Type: model.StepTypeEnd},
	}
	wf, err := h.engine.DeployWorkflow(context.Background(), "Parallel API Test", steps)
	if err != nil {
		t.Fatalf("Failed to deploy: %v", err)
	}

	// Start Instance
	instance, err := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)
	if err != nil {
		t.Fatalf("Failed to start instance: %v", err)
	}

	// Complete Task A explicitly using step_id
	reqBody := dto.CompleteTaskRequest{StepID: "taskA"}
	jsonBody, _ := json.Marshal(reqBody)
	url := ts.URL + "/instances/" + instance.ID + "/complete"

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		t.Fatalf("Failed to call complete for taskA: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK for taskA, got %d", resp.StatusCode)
	}

	// Reload instance
	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	// Status should still be running
	if instance.Status != model.StatusRunning {
		t.Errorf("Expected running, got %s", instance.Status)
	}

	// Complete Task B explicitly using step_id
	reqBody = dto.CompleteTaskRequest{StepID: "taskB"}
	jsonBody, _ = json.Marshal(reqBody)
	resp, err = http.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		t.Fatalf("Failed to call complete for taskB: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK for taskB, got %d", resp.StatusCode)
	}

	// Reload instance -> Should be completed
	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	if instance.Status != model.StatusCompleted {
		t.Errorf("Expected completed, got %s", instance.Status)
	}
}

func TestDeployBPMNAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="Definitions_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_1" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="EndEvent_1"/>
  </bpmn:process>
</bpmn:definitions>`

	url := ts.URL + "/workflows"
	resp, err := http.Post(url, "application/xml", bytes.NewBufferString(bpmnXML))
	if err != nil {
		t.Fatalf("Failed to deploy BPMN: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", resp.StatusCode)
	}

	var wf dto.WorkflowDefinitionResponse
	if err := json.NewDecoder(resp.Body).Decode(&wf); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if wf.ProcessDefinitionID != "Process_1" {
		t.Errorf("Expected Process_1, got %s", wf.ProcessDefinitionID)
	}
}

func TestDeployBPMNRequestBodyTooLarge(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	before, err := h.engine.ListWorkflows(context.Background())
	if err != nil {
		t.Fatalf("Failed to list workflows before oversized deploy: %v", err)
	}

	oversizedBody := bytes.Repeat([]byte("x"), int(maxBPMNDeployBodyBytes)+1)
	resp, err := http.Post(ts.URL+"/workflows", "application/xml", bytes.NewReader(oversizedBody))
	if err != nil {
		t.Fatalf("Failed to deploy oversized BPMN: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 413 Request Entity Too Large, got %d: %s", resp.StatusCode, string(body))
	}

	after, err := h.engine.ListWorkflows(context.Background())
	if err != nil {
		t.Fatalf("Failed to list workflows after oversized deploy: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("Expected oversized deploy to create no workflows, got before=%d after=%d", len(before), len(after))
	}
}

func TestDeployBPMNInvalidXMLReturnsBadRequest(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	before, err := h.engine.ListWorkflows(context.Background())
	if err != nil {
		t.Fatalf("Failed to list workflows before invalid deploy: %v", err)
	}

	resp, err := http.Post(ts.URL+"/workflows", "application/xml", bytes.NewBufferString("not valid xml"))
	if err != nil {
		t.Fatalf("Failed to deploy invalid BPMN: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request, got %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), application.ErrBPMNValidation.Error()) {
		t.Fatalf("Expected response body to include validation detail, got %q", string(body))
	}

	after, err := h.engine.ListWorkflows(context.Background())
	if err != nil {
		t.Fatalf("Failed to list workflows after invalid deploy: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("Expected invalid deploy to create no workflows, got before=%d after=%d", len(before), len(after))
	}
}

func TestModelerRoleCanDeployAndReadProcessesOnly(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)

	modeler := auth.Principal{Subject: "modeler", Roles: []string{auth.RoleBusiness}}
	bpmnXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="Definitions_ModelerOnly" targetNamespace="http://artificialflow.local/test">
  <bpmn:process id="ModelerOnly" name="Modeler Only" isExecutable="true">
    <bpmn:startEvent id="start"><bpmn:outgoing>f1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:endEvent id="end"><bpmn:incoming>f1</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

	deployReq := requestAs(http.MethodPost, "/workflows", strings.NewReader(bpmnXML), modeler)
	deployReq.Header.Set("Content-Type", "application/xml")
	deployRec := httptest.NewRecorder()
	r.ServeHTTP(deployRec, deployReq)
	if deployRec.Code != http.StatusOK {
		t.Fatalf("expected modeler deploy 200, got %d: %s", deployRec.Code, deployRec.Body.String())
	}
	var workflow dto.WorkflowDefinitionResponse
	if err := json.NewDecoder(deployRec.Body).Decode(&workflow); err != nil {
		t.Fatalf("failed to decode workflow: %v", err)
	}

	listReq := requestAs(http.MethodGet, "/workflows", nil, modeler)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected modeler list workflows 200, got %d: %s", listRec.Code, listRec.Body.String())
	}

	getReq := requestAs(http.MethodGet, "/workflows/"+workflow.ID, nil, modeler)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected modeler get workflow 200, got %d: %s", getRec.Code, getRec.Body.String())
	}

	startBody, _ := json.Marshal(dto.StartInstanceRequest{WorkflowID: workflow.ID, Context: map[string]any{"source": "modeler-test"}})
	startReq := requestAs(http.MethodPost, "/instances", bytes.NewBuffer(startBody), modeler)
	startReq.Header.Set("Content-Type", "application/json")
	startRec := httptest.NewRecorder()
	r.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusForbidden {
		t.Fatalf("expected modeler start instance 403, got %d: %s", startRec.Code, startRec.Body.String())
	}

	instancesReq := requestAs(http.MethodGet, "/instances", nil, modeler)
	instancesRec := httptest.NewRecorder()
	r.ServeHTTP(instancesRec, instancesReq)
	if instancesRec.Code != http.StatusForbidden {
		t.Fatalf("expected modeler list instances 403, got %d: %s", instancesRec.Code, instancesRec.Body.String())
	}
}

func TestPublishSignalAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	// Deploy Workflow with Signal
	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "catch"}}},
		{
			ID:   "catch",
			Type: model.StepTypeIntermediateCatchEvent,
			Properties: map[string]any{
				"signal_ref": "MySignal",
			},
			Incoming: []string{"start"},
			Outgoing: []model.Transition{{TargetRef: "end"}},
		},
		{ID: "end", Type: model.StepTypeEnd, Incoming: []string{"catch"}},
	}
	wf, _ := h.engine.DeployWorkflow(context.Background(), "Signal Test", steps)
	instance, _ := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)

	// Publish Signal
	url := ts.URL + "/signals"
	reqBody := dto.PublishSignalRequest{
		SignalName: "MySignal",
		Payload:    map[string]any{"api_trigger": true},
	}
	jsonBody, _ := json.Marshal(reqBody)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		t.Fatalf("Failed to publish signal: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", resp.StatusCode)
	}

	// Verify
	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	if instance.Status != model.StatusCompleted {
		t.Errorf("Expected instance completed, got %s", instance.Status)
	}
	if val, ok := instance.Context["api_trigger"]; !ok || val != true {
		t.Errorf("Expected context api_trigger=true, got %v", val)
	}
}

func TestPublishMessageAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	// Deploy Workflow with Message
	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "catch"}}},
		{
			ID:   "catch",
			Type: model.StepTypeIntermediateCatchEvent,
			Properties: map[string]any{
				"message_ref": "MsgOrderPlaced",
			},
			Incoming: []string{"start"},
			Outgoing: []model.Transition{{TargetRef: "end"}},
		},
		{ID: "end", Type: model.StepTypeEnd, Incoming: []string{"catch"}},
	}
	wf, _ := h.engine.DeployWorkflow(context.Background(), "Message Test", steps)
	instance, _ := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)

	// Publish Message
	url := ts.URL + "/messages"
	reqBody := dto.PublishMessageRequest{
		MessageName: "MsgOrderPlaced",
		Payload:     map[string]any{"order_id": "999"},
	}
	jsonBody, _ := json.Marshal(reqBody)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		t.Fatalf("Failed to publish message: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", resp.StatusCode)
	}

	// Verify
	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	if instance.Status != model.StatusCompleted {
		t.Errorf("Expected instance completed, got %s", instance.Status)
	}
	if val, ok := instance.Context["order_id"]; !ok || val != "999" {
		t.Errorf("Expected context order_id=999, got %v", val)
	}
}

func TestPublishEscalationAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "work"}}},
		{
			ID:                "work",
			Type:              model.StepTypeUserTask,
			Incoming:          []string{"start"},
			Outgoing:          []model.Transition{{TargetRef: "endOk"}},
			BoundaryEventRefs: []string{"escBound"},
		},
		{
			ID:   "escBound",
			Type: model.StepTypeBoundaryEvent,
			Properties: map[string]any{
				"event_definition_type": "escalation",
				"escalation_code":       "NEED_HELP",
				"cancel_activity":       true,
				"attached_to":           "work",
			},
			Outgoing: []model.Transition{{TargetRef: "endEsc"}},
		},
		{ID: "endOk", Type: model.StepTypeEnd, Incoming: []string{"work"}},
		{ID: "endEsc", Type: model.StepTypeEnd, Incoming: []string{"escBound"}},
	}
	wf, err := h.engine.DeployWorkflow(context.Background(), "Escalation API Test", steps)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	instance, err := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	url := ts.URL + "/escalations"
	reqBody := dto.PublishEscalationRequest{
		EscalationCode: "NEED_HELP",
		Payload:        map[string]any{"api_escalation": true},
	}
	jsonBody, _ := json.Marshal(reqBody)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		t.Fatalf("Failed to publish escalation: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	instance, err = h.engine.GetInstance(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if instance.Status != model.StatusCompleted {
		t.Errorf("Expected instance completed, got %s", instance.Status)
	}
	if val, ok := instance.Context["api_escalation"]; !ok || val != true {
		t.Errorf("Expected context api_escalation=true, got %v", val)
	}

	// Missing code → 400
	badBody, _ := json.Marshal(dto.PublishEscalationRequest{Payload: map[string]any{}})
	badResp, err := http.Post(url, "application/json", bytes.NewBuffer(badBody))
	if err != nil {
		t.Fatalf("bad request post: %v", err)
	}
	defer badResp.Body.Close()
	if badResp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected 400 for empty escalation_code, got %d", badResp.StatusCode)
	}
}

func TestServiceTaskAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	// Register Service Handler
	h.engine.RegisterHandler("paymentService", func(ctx context.Context, instance *model.WorkflowInstance, step *model.StepDefinition) error {
		if instance.Context == nil {
			instance.Context = make(map[string]any)
		}
		instance.Context["payment_processed"] = true
		instance.Context["amount"] = 100
		return nil
	})

	// Deploy Workflow with Service Task
	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "service"}}},
		{
			ID:             "service",
			Type:           model.StepTypeServiceTask,
			Implementation: "paymentService",
			Incoming:       []string{"start"},
			Outgoing:       []model.Transition{{TargetRef: "end"}},
		},
		{ID: "end", Type: model.StepTypeEnd, Incoming: []string{"service"}},
	}
	wf, err := h.engine.DeployWorkflow(context.Background(), "Service API Test", steps)
	if err != nil {
		t.Fatalf("Failed to deploy: %v", err)
	}

	// Start Instance
	instance, err := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)
	if err != nil {
		t.Fatalf("Failed to start instance: %v", err)
	}

	// Wait briefly for async execution (if any) or check immediately if sync
	// Service tasks are currently executed synchronously in autoAdvance
	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)

	if instance.Status != model.StatusCompleted {
		t.Errorf("Expected completed, got %s", instance.Status)
	}

	if val, ok := instance.Context["payment_processed"]; !ok || val != true {
		t.Errorf("Expected payment_processed=true, got %v", val)
	}
}

func TestExternalWorkerActivateAndCompleteJobAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "service"}}},
		{
			ID:             "service",
			Type:           model.StepTypeServiceTask,
			Implementation: "external-payment",
			Incoming:       []string{"start"},
			Outgoing:       []model.Transition{{TargetRef: "end"}},
		},
		{ID: "end", Type: model.StepTypeEnd, Incoming: []string{"service"}},
	}

	wf, err := h.engine.DeployWorkflow(context.Background(), "External Worker Test", steps)
	if err != nil {
		t.Fatalf("Failed to deploy: %v", err)
	}

	instance, err := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)
	if err != nil {
		t.Fatalf("Failed to start instance: %v", err)
	}

	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	if instance.Status != model.StatusRunning {
		t.Fatalf("expected running instance before external completion, got %s", instance.Status)
	}

	activateURL := ts.URL + "/jobs/activate"
	activateBody, _ := json.Marshal(dto.ActivateJobsRequest{Type: "external-payment", Worker: "worker-1", MaxJobs: 1})
	activateResp, err := http.Post(activateURL, "application/json", bytes.NewBuffer(activateBody))
	if err != nil {
		t.Fatalf("Failed to activate jobs: %v", err)
	}
	if activateResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(activateResp.Body)
		t.Fatalf("Expected 200 OK, got %d: %s", activateResp.StatusCode, string(body))
	}

	var activated dto.ActivateJobsResponse
	if err := json.NewDecoder(activateResp.Body).Decode(&activated); err != nil {
		t.Fatalf("Failed to decode activate response: %v", err)
	}
	if len(activated.Jobs) != 1 {
		t.Fatalf("expected 1 activated job, got %d", len(activated.Jobs))
	}
	job := activated.Jobs[0]
	if job.State != "ACTIVATED" {
		t.Fatalf("expected ACTIVATED job state, got %s", job.State)
	}

	completeURL := fmt.Sprintf("%s/jobs/%s/complete", ts.URL, job.Key)
	completeBody, _ := json.Marshal(dto.CompleteJobRequest{
		Worker: "worker-1",
		Variables: map[string]any{
			"approved": true,
		},
	})
	completeResp, err := http.Post(completeURL, "application/json", bytes.NewBuffer(completeBody))
	if err != nil {
		t.Fatalf("Failed to complete job: %v", err)
	}
	if completeResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(completeResp.Body)
		t.Fatalf("Expected 200 OK, got %d: %s", completeResp.StatusCode, string(body))
	}

	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	if instance.Status != model.StatusCompleted {
		t.Fatalf("expected completed instance after job completion, got %s", instance.Status)
	}
	if val, ok := instance.Context["approved"]; !ok || val != true {
		t.Fatalf("expected approved=true in context, got %v", val)
	}
}

func TestRoleMatrix(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)

	admin := auth.Principal{Subject: "admin", Roles: []string{auth.RoleAdmin}}
	business := auth.Principal{Subject: "business", Roles: []string{auth.RoleBusiness}}
	client := auth.Principal{Subject: "sdk", Roles: []string{auth.RoleClient}}
	analytics := auth.Principal{Subject: "analytics", Roles: []string{auth.RoleAnalytics}}
	endUser := auth.Principal{Subject: "accountant", Roles: []string{"accountant"}}

	routes := []struct {
		method, path string
		allowed      []auth.Principal
	}{
		{http.MethodGet, "/workflows", []auth.Principal{admin, business, client}},
		{http.MethodGet, "/workflows/1", []auth.Principal{admin, business, client}},
		{http.MethodDelete, "/workflows/1", []auth.Principal{admin}},
		{http.MethodGet, "/decisions", []auth.Principal{admin, business, client}},
		{http.MethodPost, "/decisions/d1/evaluate", []auth.Principal{admin, client}},
		{http.MethodGet, "/tasks", []auth.Principal{admin, client}},
		{http.MethodGet, "/instances", []auth.Principal{admin, client}},
		{http.MethodGet, "/instances/history/completed", []auth.Principal{admin, client}},
		{http.MethodGet, "/instances/1", []auth.Principal{admin, client}},
		{http.MethodPost, "/instances/1/variables", []auth.Principal{admin, client}},
		{http.MethodGet, "/instances/1/tasks", []auth.Principal{admin, client}},
		{http.MethodPost, "/instances/1/tasks/2/claim", []auth.Principal{admin, client}},
		{http.MethodPost, "/instances/1/tasks/2/complete", []auth.Principal{admin, client}},
		{http.MethodDelete, "/instances/1", []auth.Principal{admin}},
		{http.MethodGet, "/instances/1/jobs", []auth.Principal{admin}},
		{http.MethodGet, "/incidents", []auth.Principal{admin}},
		{http.MethodPost, "/jobs/1/retry", []auth.Principal{admin}},
		{http.MethodPost, "/instances/1/migration/dry-run", []auth.Principal{admin}},
		{http.MethodPost, "/jobs/activate", []auth.Principal{admin, client}},
		{http.MethodGet, "/internal/metrics", []auth.Principal{admin}},
	}
	everyone := []auth.Principal{admin, business, client, analytics, endUser}
	for _, route := range routes {
		for _, principal := range everyone {
			allowed := false
			for _, candidate := range route.allowed {
				if candidate.Subject == principal.Subject {
					allowed = true
				}
			}
			req := requestAs(route.method, route.path, strings.NewReader("{}"), principal)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if allowed && rec.Code == http.StatusForbidden {
				t.Errorf("%s %s as %s: expected access, got 403", route.method, route.path, principal.Subject)
			}
			if !allowed && rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s: expected 403, got %d", route.method, route.path, principal.Subject, rec.Code)
			}
		}
	}
}

func TestClientTaskSearchClaimAndComplete(t *testing.T) {
	h, repo := setupTestHandlerWithRepository(t, iam.DeploymentConfig{IAMEnabled: true})
	r := mux.NewRouter()
	registerTestRoutes(r, h)

	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:artificialflow="http://artificialflow.io/schema/1.0/bpmn" id="Definitions_TaskSearch" targetNamespace="http://artificialflow.local/test">
  <bpmn:process id="TaskSearch" name="Task Search" isExecutable="true">
    <bpmn:startEvent id="start"><bpmn:outgoing>f1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="accountantReview" name="Accountant Review" artificialflow:candidateGroups="accountant"><bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing></bpmn:userTask>
    <bpmn:endEvent id="end"><bpmn:incoming>f2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="accountantReview"/>
    <bpmn:sequenceFlow id="f2" sourceRef="accountantReview" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

	deployReq := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(bpmn))
	deployReq.Header.Set("Content-Type", "application/xml")
	deployRec := httptest.NewRecorder()
	r.ServeHTTP(deployRec, deployReq)
	if deployRec.Code != http.StatusOK {
		t.Fatalf("expected deploy 200, got %d: %s", deployRec.Code, deployRec.Body.String())
	}
	var workflow dto.WorkflowDefinitionResponse
	if err := json.NewDecoder(deployRec.Body).Decode(&workflow); err != nil {
		t.Fatalf("failed to decode workflow: %v", err)
	}

	client := auth.Principal{Subject: "sdk-client", Roles: []string{auth.RoleClient}}
	serve := func(method, target, body string, principal auth.Principal) *httptest.ResponseRecorder {
		t.Helper()
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := requestAs(method, target, reader, principal)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	search := func(query string) []dto.UserTaskResponse {
		t.Helper()
		rec := serve(http.MethodGet, "/tasks"+query, "", client)
		if rec.Code != http.StatusOK {
			t.Fatalf("search %s: expected 200, got %d: %s", query, rec.Code, rec.Body.String())
		}
		var resp dto.ListUserTasksResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode search: %v", err)
		}
		return resp.Tasks
	}

	startBody, _ := json.Marshal(dto.StartInstanceRequest{WorkflowID: workflow.ID})
	startRec := serve(http.MethodPost, "/instances", string(startBody), client)
	if startRec.Code != http.StatusOK {
		t.Fatalf("expected start 200, got %d: %s", startRec.Code, startRec.Body.String())
	}
	var instance dto.WorkflowInstanceResponse
	if err := json.NewDecoder(startRec.Body).Decode(&instance); err != nil {
		t.Fatalf("failed to decode instance: %v", err)
	}
	instanceKey, _ := strconv.ParseInt(instance.ID, 10, 64)
	createdJobs, err := repo.ListJobsByProcessInstanceAndType(context.Background(), instanceKey, application.UserTaskJobType)
	if err != nil || len(createdJobs) != 1 || createdJobs[0].Type != application.UserTaskJobType {
		t.Fatalf("expected one user task of type %q, got jobs=%#v err=%v", application.UserTaskJobType, createdJobs, err)
	}

	if tasks := search("?candidateGroup=reviewer"); len(tasks) != 0 {
		t.Fatalf("reviewer group should not match accountant task, got %#v", tasks)
	}
	tasks := search("?candidateGroup=reviewer,Accountant&processInstanceKey=" + instance.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected accountant task, got %#v", tasks)
	}
	task := tasks[0]
	if task.ProcessInstanceKey != instance.ID || task.ProcessDefinitionKey == "" || !task.CanClaim || task.CanComplete {
		t.Fatalf("unexpected task facts before claim: %#v", task)
	}
	taskPath := "/instances/" + task.ProcessInstanceKey + "/tasks/" + task.ExecutionID

	if rec := serve(http.MethodPost, taskPath+"/claim", "", client); rec.Code != http.StatusBadRequest {
		t.Fatalf("client claim without assignee: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := serve(http.MethodPost, taskPath+"/claim", `{"assignee":"x","extra":1}`, client); rec.Code != http.StatusBadRequest {
		t.Fatalf("claim with unknown field: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	claimRec := serve(http.MethodPost, taskPath+"/claim", `{"assignee":"accountant@example.com"}`, client)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("client claim: expected 200, got %d: %s", claimRec.Code, claimRec.Body.String())
	}
	var claimed dto.UserTaskResponse
	if err := json.NewDecoder(claimRec.Body).Decode(&claimed); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	if claimed.ClaimedBy != "accountant@example.com" || !claimed.CanComplete {
		t.Fatalf("unexpected claimed task: %#v", claimed)
	}
	if rec := serve(http.MethodPost, taskPath+"/claim", `{"assignee":"someone-else"}`, client); rec.Code != http.StatusConflict {
		t.Fatalf("claim of task held by another user: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if tasks := search("?assignee=ACCOUNTANT@example.com"); len(tasks) != 1 {
		t.Fatalf("assignee search should match claim owner, got %#v", tasks)
	}
	if rec := serve(http.MethodPost, taskPath+"/complete", `{"assignee":"someone-else"}`, client); rec.Code != http.StatusConflict {
		t.Fatalf("complete by non-owner: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := serve(http.MethodPost, taskPath+"/complete", `{"assignee":"accountant@example.com"}`, client); rec.Code != http.StatusOK {
		t.Fatalf("complete by owner: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if tasks := search("?candidateGroup=accountant"); len(tasks) != 0 {
		t.Fatalf("completed task must leave the active search, got %#v", tasks)
	}
	completed := search("?assignee=accountant@example.com&state=completed")
	if len(completed) != 1 || completed[0].State != "COMPLETED" || completed[0].ClaimedBy != "accountant@example.com" {
		t.Fatalf("expected completed task history for the user, got %#v", completed)
	}

	for _, query := range []string{"?state=open", "?limit=0", "?limit=501", "?processInstanceKey=abc", "?includeUnassigned=maybe"} {
		if rec := serve(http.MethodGet, "/tasks"+query, "", client); rec.Code != http.StatusBadRequest {
			t.Fatalf("search %s: expected 400, got %d", query, rec.Code)
		}
	}
}

func TestAdminClaimDefaultsToOwnIdentityAndCanReassign(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)

	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:artificialflow="http://artificialflow.io/schema/1.0/bpmn" id="Definitions_AdminClaim" targetNamespace="http://artificialflow.local/test">
  <bpmn:process id="AdminClaim" name="Admin Claim" isExecutable="true">
    <bpmn:startEvent id="start"><bpmn:outgoing>f1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="review" name="Review" artificialflow:assignee="accountant"><bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing></bpmn:userTask>
    <bpmn:endEvent id="end"><bpmn:incoming>f2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
    <bpmn:sequenceFlow id="f2" sourceRef="review" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`
	deployReq := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(bpmn))
	deployReq.Header.Set("Content-Type", "application/xml")
	deployRec := httptest.NewRecorder()
	r.ServeHTTP(deployRec, deployReq)
	var workflow dto.WorkflowDefinitionResponse
	if err := json.NewDecoder(deployRec.Body).Decode(&workflow); err != nil {
		t.Fatalf("failed to decode workflow: %v", err)
	}
	startBody, _ := json.Marshal(dto.StartInstanceRequest{WorkflowID: workflow.ID})
	startReq := httptest.NewRequest(http.MethodPost, "/instances", bytes.NewBuffer(startBody))
	startRec := httptest.NewRecorder()
	r.ServeHTTP(startRec, startReq)
	var instance dto.WorkflowInstanceResponse
	if err := json.NewDecoder(startRec.Body).Decode(&instance); err != nil {
		t.Fatalf("failed to decode instance: %v", err)
	}
	tasksRec := httptest.NewRecorder()
	r.ServeHTTP(tasksRec, httptest.NewRequest(http.MethodGet, "/instances/"+instance.ID+"/tasks", nil))
	var tasks dto.ListUserTasksResponse
	if err := json.NewDecoder(tasksRec.Body).Decode(&tasks); err != nil || len(tasks.Tasks) != 1 {
		t.Fatalf("expected one task, got %#v err=%v", tasks, err)
	}
	taskPath := "/instances/" + instance.ID + "/tasks/" + tasks.Tasks[0].ExecutionID

	admin := auth.Principal{Subject: "admin-sub", Email: "admin@example.com", Roles: []string{auth.RoleAdmin}}
	claimRec := httptest.NewRecorder()
	r.ServeHTTP(claimRec, requestAs(http.MethodPost, taskPath+"/claim", nil, admin))
	var claimed dto.UserTaskResponse
	if err := json.NewDecoder(claimRec.Body).Decode(&claimed); err != nil || claimed.ClaimedBy != "admin@example.com" {
		t.Fatalf("admin claim should default to own identity, got %d %#v err=%v", claimRec.Code, claimed, err)
	}
	reassignRec := httptest.NewRecorder()
	r.ServeHTTP(reassignRec, requestAs(http.MethodPost, taskPath+"/claim", strings.NewReader(`{"assignee":"accountant"}`), admin))
	if reassignRec.Code != http.StatusOK {
		t.Fatalf("admin reassign: expected 200, got %d: %s", reassignRec.Code, reassignRec.Body.String())
	}
	completeRec := httptest.NewRecorder()
	r.ServeHTTP(completeRec, requestAs(http.MethodPost, taskPath+"/complete", nil, admin))
	if completeRec.Code != http.StatusOK {
		t.Fatalf("admin complete: expected 200, got %d: %s", completeRec.Code, completeRec.Body.String())
	}
}

func requestAs(method, target string, body io.Reader, principal auth.Principal) *http.Request {
	req := httptest.NewRequest(method, target, body)
	return req.WithContext(auth.WithPrincipal(req.Context(), principal))
}

func containsWorkflowInstance(instances []dto.WorkflowInstanceResponse, id string) bool {
	for _, instance := range instances {
		if instance.ID == id {
			return true
		}
	}
	return false
}

func containsUserTask(tasks []dto.UserTaskResponse, elementID string) bool {
	for _, task := range tasks {
		if task.ElementID == elementID {
			return true
		}
	}
	return false
}

func userTasksFromInstance(instance dto.WorkflowInstanceResponse) []dto.UserTaskResponse {
	tasks := make([]dto.UserTaskResponse, 0)
	for _, execution := range instance.Executions {
		if execution.Task != nil {
			tasks = append(tasks, *execution.Task)
		}
	}
	return tasks
}

func TestExternalWorkerFailJobAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "service"}}},
		{
			ID:             "service",
			Type:           model.StepTypeServiceTask,
			Implementation: "external-fail",
			Incoming:       []string{"start"},
			Outgoing:       []model.Transition{{TargetRef: "end"}},
		},
		{ID: "end", Type: model.StepTypeEnd, Incoming: []string{"service"}},
	}

	wf, err := h.engine.DeployWorkflow(context.Background(), "External Worker Fail Test", steps)
	if err != nil {
		t.Fatalf("Failed to deploy: %v", err)
	}

	instance, err := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)
	if err != nil {
		t.Fatalf("Failed to start instance: %v", err)
	}

	activateURL := ts.URL + "/jobs/activate"
	activateBody, _ := json.Marshal(dto.ActivateJobsRequest{Type: "external-fail", Worker: "worker-1", MaxJobs: 1})
	activateResp, err := http.Post(activateURL, "application/json", bytes.NewBuffer(activateBody))
	if err != nil {
		t.Fatalf("Failed to activate jobs: %v", err)
	}

	var activated dto.ActivateJobsResponse
	if err := json.NewDecoder(activateResp.Body).Decode(&activated); err != nil {
		t.Fatalf("Failed to decode activate response: %v", err)
	}
	if len(activated.Jobs) != 1 {
		t.Fatalf("expected 1 activated job, got %d", len(activated.Jobs))
	}
	job := activated.Jobs[0]

	retries := 1
	failURL := fmt.Sprintf("%s/jobs/%s/fail", ts.URL, job.Key)
	failBody, _ := json.Marshal(dto.FailJobRequest{Worker: "worker-1", ErrorMessage: "temporary error", Retries: &retries})
	failResp, err := http.Post(failURL, "application/json", bytes.NewBuffer(failBody))
	if err != nil {
		t.Fatalf("Failed to fail job: %v", err)
	}
	if failResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(failResp.Body)
		t.Fatalf("Expected 200 OK, got %d: %s", failResp.StatusCode, string(body))
	}

	reactivateBody, _ := json.Marshal(dto.ActivateJobsRequest{Type: "external-fail", Worker: "worker-2", MaxJobs: 1})
	reactivateResp, err := http.Post(activateURL, "application/json", bytes.NewBuffer(reactivateBody))
	if err != nil {
		t.Fatalf("Failed to re-activate jobs: %v", err)
	}

	var reactivated dto.ActivateJobsResponse
	if err := json.NewDecoder(reactivateResp.Body).Decode(&reactivated); err != nil {
		t.Fatalf("Failed to decode re-activate response: %v", err)
	}
	if len(reactivated.Jobs) != 1 {
		t.Fatalf("expected 1 re-activated job, got %d", len(reactivated.Jobs))
	}

	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	if instance.Status != model.StatusRunning {
		t.Fatalf("expected running instance after failed job, got %s", instance.Status)
	}
}

func TestExternalWorkerJobReactivationAfterLockExpiry(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "service"}}},
		{
			ID:             "service",
			Type:           model.StepTypeServiceTask,
			Implementation: "external-lock-expiry",
			Incoming:       []string{"start"},
			Outgoing:       []model.Transition{{TargetRef: "end"}},
		},
		{ID: "end", Type: model.StepTypeEnd, Incoming: []string{"service"}},
	}

	wf, err := h.engine.DeployWorkflow(context.Background(), "External Worker Lock Expiry", steps)
	if err != nil {
		t.Fatalf("Failed to deploy: %v", err)
	}

	instance, err := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)
	if err != nil {
		t.Fatalf("Failed to start instance: %v", err)
	}

	activateURL := ts.URL + "/jobs/activate"
	activateBody, _ := json.Marshal(dto.ActivateJobsRequest{Type: "external-lock-expiry", Worker: "worker-1", MaxJobs: 1, LockDurationMs: 80})
	activateResp, err := http.Post(activateURL, "application/json", bytes.NewBuffer(activateBody))
	if err != nil {
		t.Fatalf("Failed to activate jobs: %v", err)
	}

	var firstActivation dto.ActivateJobsResponse
	if err := json.NewDecoder(activateResp.Body).Decode(&firstActivation); err != nil {
		t.Fatalf("Failed to decode activation response: %v", err)
	}
	if len(firstActivation.Jobs) != 1 {
		t.Fatalf("expected 1 activated job, got %d", len(firstActivation.Jobs))
	}

	time.Sleep(150 * time.Millisecond)

	reactivateBody, _ := json.Marshal(dto.ActivateJobsRequest{Type: "external-lock-expiry", Worker: "worker-2", MaxJobs: 1, LockDurationMs: 200})
	reactivateResp, err := http.Post(activateURL, "application/json", bytes.NewBuffer(reactivateBody))
	if err != nil {
		t.Fatalf("Failed to re-activate jobs: %v", err)
	}

	var secondActivation dto.ActivateJobsResponse
	if err := json.NewDecoder(reactivateResp.Body).Decode(&secondActivation); err != nil {
		t.Fatalf("Failed to decode re-activation response: %v", err)
	}
	if len(secondActivation.Jobs) != 1 {
		t.Fatalf("expected 1 re-activated job, got %d", len(secondActivation.Jobs))
	}

	if secondActivation.Jobs[0].Key != firstActivation.Jobs[0].Key {
		t.Fatalf("expected same job key to be re-activated")
	}
	if secondActivation.Jobs[0].Worker != "worker-2" {
		t.Fatalf("expected worker-2 to own re-activated job, got %s", secondActivation.Jobs[0].Worker)
	}

	completeURL := fmt.Sprintf("%s/jobs/%s/complete", ts.URL, secondActivation.Jobs[0].Key)
	completeBody, _ := json.Marshal(dto.CompleteJobRequest{Worker: "worker-2"})
	completeResp, err := http.Post(completeURL, "application/json", bytes.NewBuffer(completeBody))
	if err != nil {
		t.Fatalf("Failed to complete re-activated job: %v", err)
	}
	if completeResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(completeResp.Body)
		t.Fatalf("Expected 200 OK, got %d: %s", completeResp.StatusCode, string(body))
	}

	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	if instance.Status != model.StatusCompleted {
		t.Fatalf("expected completed instance, got %s", instance.Status)
	}
}

func TestExternalWorkerExtendLockAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "service"}}},
		{
			ID:             "service",
			Type:           model.StepTypeServiceTask,
			Implementation: "external-extend-lock",
			Incoming:       []string{"start"},
			Outgoing:       []model.Transition{{TargetRef: "end"}},
		},
		{ID: "end", Type: model.StepTypeEnd, Incoming: []string{"service"}},
	}

	wf, err := h.engine.DeployWorkflow(context.Background(), "External Worker Extend Lock", steps)
	if err != nil {
		t.Fatalf("Failed to deploy: %v", err)
	}

	instance, err := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)
	if err != nil {
		t.Fatalf("Failed to start instance: %v", err)
	}

	activateURL := ts.URL + "/jobs/activate"
	activateBody, _ := json.Marshal(dto.ActivateJobsRequest{Type: "external-extend-lock", Worker: "worker-1", MaxJobs: 1, LockDurationMs: 100})
	activateResp, err := http.Post(activateURL, "application/json", bytes.NewBuffer(activateBody))
	if err != nil {
		t.Fatalf("Failed to activate jobs: %v", err)
	}

	var activation dto.ActivateJobsResponse
	if err := json.NewDecoder(activateResp.Body).Decode(&activation); err != nil {
		t.Fatalf("Failed to decode activation response: %v", err)
	}
	if len(activation.Jobs) != 1 {
		t.Fatalf("expected 1 activated job, got %d", len(activation.Jobs))
	}
	jobKey := activation.Jobs[0].Key

	extendURL := fmt.Sprintf("%s/jobs/%s/extend-lock", ts.URL, jobKey)
	extendBody, _ := json.Marshal(dto.ExtendJobLockRequest{Worker: "worker-1", LockDurationMs: 300})
	extendResp, err := http.Post(extendURL, "application/json", bytes.NewBuffer(extendBody))
	if err != nil {
		t.Fatalf("Failed to extend job lock: %v", err)
	}
	if extendResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(extendResp.Body)
		t.Fatalf("Expected 200 OK, got %d: %s", extendResp.StatusCode, string(body))
	}

	time.Sleep(150 * time.Millisecond)

	completeURL := fmt.Sprintf("%s/jobs/%s/complete", ts.URL, jobKey)
	completeBody, _ := json.Marshal(dto.CompleteJobRequest{Worker: "worker-1"})
	completeResp, err := http.Post(completeURL, "application/json", bytes.NewBuffer(completeBody))
	if err != nil {
		t.Fatalf("Failed to complete job: %v", err)
	}
	if completeResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(completeResp.Body)
		t.Fatalf("Expected 200 OK, got %d: %s", completeResp.StatusCode, string(body))
	}

	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	if instance.Status != model.StatusCompleted {
		t.Fatalf("expected completed instance after extend-lock + complete, got %s", instance.Status)
	}
}

func TestExternalWorkerActivateLongPollTimeout(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	activateURL := ts.URL + "/jobs/activate"
	activateBody, _ := json.Marshal(dto.ActivateJobsRequest{Type: "no-jobs-of-this-type", Worker: "worker-1", MaxJobs: 1, TimeoutMs: 300})

	start := time.Now()
	activateResp, err := http.Post(activateURL, "application/json", bytes.NewBuffer(activateBody))
	if err != nil {
		t.Fatalf("Failed to call activate jobs: %v", err)
	}
	elapsed := time.Since(start)

	if activateResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(activateResp.Body)
		t.Fatalf("Expected 200 OK, got %d: %s", activateResp.StatusCode, string(body))
	}

	var activated dto.ActivateJobsResponse
	if err := json.NewDecoder(activateResp.Body).Decode(&activated); err != nil {
		t.Fatalf("Failed to decode activate response: %v", err)
	}
	if len(activated.Jobs) != 0 {
		t.Fatalf("expected 0 jobs from long poll timeout, got %d", len(activated.Jobs))
	}

	if elapsed < 250*time.Millisecond {
		t.Fatalf("expected long poll to wait near timeout, waited only %v", elapsed)
	}
}

func TestJobsCapabilitiesAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/jobs/capabilities")
	if err != nil {
		t.Fatalf("failed to get capabilities: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	if got := resp.Header.Get(workerSDK.HeaderEngineProtocolVersion); got != workerSDK.WorkerProtocolVersion {
		t.Fatalf("expected protocol response header %q, got %q", workerSDK.WorkerProtocolVersion, got)
	}

	var payload dto.WorkerCapabilitiesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode capabilities response: %v", err)
	}

	if payload.ProtocolVersion != workerSDK.WorkerProtocolVersion {
		t.Fatalf("expected protocol version %q, got %q", workerSDK.WorkerProtocolVersion, payload.ProtocolVersion)
	}
	if len(payload.Capabilities) == 0 {
		t.Fatalf("expected non-empty capabilities list")
	}
}

func TestActivateJobsRejectsUnsupportedWorkerProtocol(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/jobs/activate", bytes.NewBufferString(`{"type":"any","worker":"w"}`))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(workerSDK.HeaderWorkerProtocolVersion, "v999")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("failed to call activate jobs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 400 Bad Request, got %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("unsupported worker protocol version")) {
		t.Fatalf("expected protocol validation error, got %q", string(body))
	}
}

func TestCompleteJobRejectsOversizedIdempotencyKey(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/jobs/1/complete", bytes.NewBufferString(`{"worker":"w"}`))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(idempotencyKeyHeader, strings.Repeat("x", maxIdempotencyKeyLength+1))

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("failed to call complete job: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 400 Bad Request, got %d: %s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("Idempotency-Key exceeds")) {
		t.Fatalf("expected Idempotency-Key validation error, got %q", string(body))
	}
}

func TestEngineMetricsEndpointIncludesIdempotencyCounters(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	if _, err := h.engine.HasProcessedIdempotencyKey(context.Background(), "missing", "jobs.complete:10"); err != nil {
		t.Fatalf("failed idempotency miss check: %v", err)
	}
	if err := h.engine.RecordIdempotencyKey(context.Background(), "metrics-hit", "jobs.complete:10"); err != nil {
		t.Fatalf("failed to record idempotency key: %v", err)
	}
	if _, err := h.engine.HasProcessedIdempotencyKey(context.Background(), "metrics-hit", "jobs.complete:10"); err != nil {
		t.Fatalf("failed idempotency hit check: %v", err)
	}

	resp, err := http.Get(ts.URL + "/internal/metrics")
	if err != nil {
		t.Fatalf("failed to call metrics endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read metrics response body: %v", err)
	}
	if !bytes.Contains(rawBody, []byte("outboxPublishLagSec")) {
		t.Fatalf("expected metrics payload to include outboxPublishLagSec, got %s", string(rawBody))
	}

	var payload dto.EngineMetricsResponse
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		t.Fatalf("failed to decode metrics response: %v", err)
	}

	if payload.IdempotencyMiss < 1 {
		t.Fatalf("expected idempotency miss counter >=1, got %d", payload.IdempotencyMiss)
	}
	if payload.IdempotencyHit < 1 {
		t.Fatalf("expected idempotency hit counter >=1, got %d", payload.IdempotencyHit)
	}
	if payload.OutboxMaxAttempts <= 0 {
		t.Fatalf("expected outbox max attempts to be exposed, got %d", payload.OutboxMaxAttempts)
	}
	if payload.OutboxPublishLagSec < 0 {
		t.Fatalf("expected outbox publish lag metric to be non-negative, got %d", payload.OutboxPublishLagSec)
	}
}

func TestCompleteJobIdempotencyReplayBypassesWorkerMismatch(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "service"}}},
		{
			ID:             "service",
			Type:           model.StepTypeServiceTask,
			Implementation: "external-idempotent-complete",
			Incoming:       []string{"start"},
			Outgoing:       []model.Transition{{TargetRef: "end"}},
		},
		{ID: "end", Type: model.StepTypeEnd, Incoming: []string{"service"}},
	}

	wf, err := h.engine.DeployWorkflow(context.Background(), "External Worker Idempotent Complete", steps)
	if err != nil {
		t.Fatalf("Failed to deploy: %v", err)
	}

	instance, err := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)
	if err != nil {
		t.Fatalf("Failed to start instance: %v", err)
	}

	activateBody, _ := json.Marshal(dto.ActivateJobsRequest{Type: "external-idempotent-complete", Worker: "worker-1", MaxJobs: 1, LockDurationMs: 200})
	activateResp, err := http.Post(ts.URL+"/jobs/activate", "application/json", bytes.NewBuffer(activateBody))
	if err != nil {
		t.Fatalf("Failed to activate jobs: %v", err)
	}
	defer activateResp.Body.Close()

	var activation dto.ActivateJobsResponse
	if err := json.NewDecoder(activateResp.Body).Decode(&activation); err != nil {
		t.Fatalf("Failed to decode activation response: %v", err)
	}
	if len(activation.Jobs) != 1 {
		t.Fatalf("expected 1 activated job, got %d", len(activation.Jobs))
	}

	jobKey := activation.Jobs[0].Key
	idempotencyKey := "idem-complete-1"

	completeURL := fmt.Sprintf("%s/jobs/%s/complete", ts.URL, jobKey)
	firstReqBody, _ := json.Marshal(dto.CompleteJobRequest{Worker: "worker-1", Variables: map[string]any{"approved": true}})
	req1, err := http.NewRequest(http.MethodPost, completeURL, bytes.NewBuffer(firstReqBody))
	if err != nil {
		t.Fatalf("failed to create first complete request: %v", err)
	}
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set(idempotencyKeyHeader, idempotencyKey)

	resp1, err := ts.Client().Do(req1)
	if err != nil {
		t.Fatalf("failed to call first complete job: %v", err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp1.Body)
		t.Fatalf("Expected first complete 200 OK, got %d: %s", resp1.StatusCode, string(body))
	}

	replayReqBody, _ := json.Marshal(dto.CompleteJobRequest{Worker: "worker-2", Variables: map[string]any{"approved": false}})
	req2, err := http.NewRequest(http.MethodPost, completeURL, bytes.NewBuffer(replayReqBody))
	if err != nil {
		t.Fatalf("failed to create replay complete request: %v", err)
	}
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set(idempotencyKeyHeader, idempotencyKey)

	resp2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatalf("failed to call replay complete job: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("Expected replay complete 200 OK, got %d: %s", resp2.StatusCode, string(body))
	}

	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	if instance.Status != model.StatusCompleted {
		t.Fatalf("expected completed instance after idempotent complete replay, got %s", instance.Status)
	}
	if val, ok := instance.Context["approved"]; !ok || val != true {
		t.Fatalf("expected approved=true in context after replay, got %v", val)
	}
}

func TestFailJobIdempotencyReplayBypassesWorkerMismatch(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "service"}}},
		{
			ID:             "service",
			Type:           model.StepTypeServiceTask,
			Implementation: "external-idempotent-fail",
			Incoming:       []string{"start"},
			Outgoing:       []model.Transition{{TargetRef: "end"}},
		},
		{ID: "end", Type: model.StepTypeEnd, Incoming: []string{"service"}},
	}

	wf, err := h.engine.DeployWorkflow(context.Background(), "External Worker Idempotent Fail", steps)
	if err != nil {
		t.Fatalf("Failed to deploy: %v", err)
	}

	instance, err := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)
	if err != nil {
		t.Fatalf("Failed to start instance: %v", err)
	}

	activateBody, _ := json.Marshal(dto.ActivateJobsRequest{Type: "external-idempotent-fail", Worker: "worker-1", MaxJobs: 1, LockDurationMs: 200})
	activateResp, err := http.Post(ts.URL+"/jobs/activate", "application/json", bytes.NewBuffer(activateBody))
	if err != nil {
		t.Fatalf("Failed to activate jobs: %v", err)
	}
	defer activateResp.Body.Close()

	var activation dto.ActivateJobsResponse
	if err := json.NewDecoder(activateResp.Body).Decode(&activation); err != nil {
		t.Fatalf("Failed to decode activation response: %v", err)
	}
	if len(activation.Jobs) != 1 {
		t.Fatalf("expected 1 activated job, got %d", len(activation.Jobs))
	}

	jobKey := activation.Jobs[0].Key
	idempotencyKey := "idem-fail-1"

	failURL := fmt.Sprintf("%s/jobs/%s/fail", ts.URL, jobKey)
	retries := 1
	firstReqBody, _ := json.Marshal(dto.FailJobRequest{Worker: "worker-1", ErrorMessage: "temporary failure", Retries: &retries})
	req1, err := http.NewRequest(http.MethodPost, failURL, bytes.NewBuffer(firstReqBody))
	if err != nil {
		t.Fatalf("failed to create first fail request: %v", err)
	}
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set(idempotencyKeyHeader, idempotencyKey)

	resp1, err := ts.Client().Do(req1)
	if err != nil {
		t.Fatalf("failed to call first fail job: %v", err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp1.Body)
		t.Fatalf("Expected first fail 200 OK, got %d: %s", resp1.StatusCode, string(body))
	}

	replayRetries := 0
	replayReqBody, _ := json.Marshal(dto.FailJobRequest{Worker: "worker-2", ErrorMessage: "should be ignored by replay", Retries: &replayRetries})
	req2, err := http.NewRequest(http.MethodPost, failURL, bytes.NewBuffer(replayReqBody))
	if err != nil {
		t.Fatalf("failed to create replay fail request: %v", err)
	}
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set(idempotencyKeyHeader, idempotencyKey)

	resp2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatalf("failed to call replay fail job: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("Expected replay fail 200 OK, got %d: %s", resp2.StatusCode, string(body))
	}

	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	if instance.Status != model.StatusRunning {
		t.Fatalf("expected running instance after idempotent fail replay, got %s", instance.Status)
	}
}

func TestExtendLockIdempotencyReplayBypassesWorkerMismatch(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "service"}}},
		{
			ID:             "service",
			Type:           model.StepTypeServiceTask,
			Implementation: "external-idempotent-lock",
			Incoming:       []string{"start"},
			Outgoing:       []model.Transition{{TargetRef: "end"}},
		},
		{ID: "end", Type: model.StepTypeEnd, Incoming: []string{"service"}},
	}

	wf, err := h.engine.DeployWorkflow(context.Background(), "External Worker Idempotent Lock", steps)
	if err != nil {
		t.Fatalf("Failed to deploy: %v", err)
	}

	_, err = h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)
	if err != nil {
		t.Fatalf("Failed to start instance: %v", err)
	}

	activateBody, _ := json.Marshal(dto.ActivateJobsRequest{Type: "external-idempotent-lock", Worker: "worker-1", MaxJobs: 1, LockDurationMs: 200})
	activateResp, err := http.Post(ts.URL+"/jobs/activate", "application/json", bytes.NewBuffer(activateBody))
	if err != nil {
		t.Fatalf("Failed to activate jobs: %v", err)
	}
	defer activateResp.Body.Close()

	var activation dto.ActivateJobsResponse
	if err := json.NewDecoder(activateResp.Body).Decode(&activation); err != nil {
		t.Fatalf("Failed to decode activation response: %v", err)
	}
	if len(activation.Jobs) != 1 {
		t.Fatalf("expected 1 activated job, got %d", len(activation.Jobs))
	}

	jobKey := activation.Jobs[0].Key
	idempotencyKey := "idem-lock-1"

	extendURL := fmt.Sprintf("%s/jobs/%s/extend-lock", ts.URL, jobKey)
	goodReqBody, _ := json.Marshal(dto.ExtendJobLockRequest{Worker: "worker-1", LockDurationMs: 500})
	req1, err := http.NewRequest(http.MethodPost, extendURL, bytes.NewBuffer(goodReqBody))
	if err != nil {
		t.Fatalf("failed to create first extend request: %v", err)
	}
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set(idempotencyKeyHeader, idempotencyKey)

	resp1, err := ts.Client().Do(req1)
	if err != nil {
		t.Fatalf("failed to call first extend lock: %v", err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp1.Body)
		t.Fatalf("Expected first extend 200 OK, got %d: %s", resp1.StatusCode, string(body))
	}

	badReqBody, _ := json.Marshal(dto.ExtendJobLockRequest{Worker: "worker-2", LockDurationMs: 500})
	req2, err := http.NewRequest(http.MethodPost, extendURL, bytes.NewBuffer(badReqBody))
	if err != nil {
		t.Fatalf("failed to create replay extend request: %v", err)
	}
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set(idempotencyKeyHeader, idempotencyKey)

	resp2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatalf("failed to call replay extend lock: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("Expected replay extend 200 OK, got %d: %s", resp2.StatusCode, string(body))
	}
}

func TestExternalWorkerSDKLockRenewalAgainstAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "service"}}},
		{
			ID:             "service",
			Type:           model.StepTypeServiceTask,
			Implementation: "external-sdk-renew",
			Incoming:       []string{"start"},
			Outgoing:       []model.Transition{{TargetRef: "end"}},
		},
		{ID: "end", Type: model.StepTypeEnd, Incoming: []string{"service"}},
	}

	wf, err := h.engine.DeployWorkflow(context.Background(), "External Worker SDK Renew", steps)
	if err != nil {
		t.Fatalf("Failed to deploy: %v", err)
	}

	instance, err := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), nil)
	if err != nil {
		t.Fatalf("Failed to start instance: %v", err)
	}

	client, err := workerSDK.NewClient(workerSDK.ClientConfig{BaseURL: ts.URL, HTTPClient: ts.Client()})
	if err != nil {
		t.Fatalf("failed to create SDK client: %v", err)
	}

	worker, err := workerSDK.NewWorker(client, workerSDK.WorkerConfig{
		JobType:           "external-sdk-renew",
		WorkerName:        "sdk-worker-1",
		MaxJobs:           1,
		ActivateTimeout:   100 * time.Millisecond,
		LockDuration:      100 * time.Millisecond,
		LockRenewInterval: 25 * time.Millisecond,
		Handler: func(ctx context.Context, job model.Job) (map[string]any, error) {
			timer := time.NewTimer(220 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
			}
			return map[string]any{"sdkRenewed": true}, nil
		},
	})
	if err != nil {
		t.Fatalf("failed to create SDK worker: %v", err)
	}

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("SDK worker run failed: %v", err)
	}

	instance, _ = h.engine.GetInstance(context.Background(), instance.ID)
	if instance.Status != model.StatusCompleted {
		t.Fatalf("expected completed instance after SDK lock renewal, got %s", instance.Status)
	}
	if val, ok := instance.Context["sdkRenewed"]; !ok || val != true {
		t.Fatalf("expected sdkRenewed=true in context, got %v", val)
	}
}

func TestStartInstanceAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	// Deploy a simple workflow
	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "end"}}},
		{ID: "end", Type: model.StepTypeEnd},
	}
	wf, err := h.engine.DeployWorkflow(context.Background(), "Start API Test", steps)
	if err != nil {
		t.Fatalf("Failed to deploy: %v", err)
	}

	// Call Start Instance API
	url := ts.URL + "/instances"
	reqBody := dto.StartInstanceRequest{
		WorkflowID: strconv.FormatInt(wf.ID, 10),
		Context:    map[string]any{"foo": "bar"},
	}
	jsonBody, _ := json.Marshal(reqBody)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		t.Fatalf("Failed to call start instance: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", resp.StatusCode)
	}

	var instance dto.WorkflowInstanceResponse
	if err := json.NewDecoder(resp.Body).Decode(&instance); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if instance.WorkflowID != reqBody.WorkflowID {
		t.Errorf("Expected WorkflowID %s, got %s", reqBody.WorkflowID, instance.WorkflowID)
	}
	if val, ok := instance.Context["foo"]; !ok || val != "bar" {
		t.Errorf("Expected context foo=bar, got %v", val)
	}
}

func TestGetInstanceAPI(t *testing.T) {
	h := setupTestHandler(t)
	r := mux.NewRouter()
	registerTestRoutes(r, h)
	ts := httptest.NewServer(r)
	defer ts.Close()

	// Deploy and Start manually
	steps := []model.StepDefinition{
		{ID: "start", Type: model.StepTypeStart, Outgoing: []model.Transition{{TargetRef: "end"}}},
		{ID: "end", Type: model.StepTypeEnd},
	}
	wf, _ := h.engine.DeployWorkflow(context.Background(), "Get API Test", steps)
	instance, _ := h.engine.StartInstance(context.Background(), strconv.FormatInt(wf.ID, 10), map[string]any{"key": "val"})

	// Call Get Instance API
	url := ts.URL + "/instances/" + instance.ID
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("Failed to call get instance: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", resp.StatusCode)
	}

	var fetchedInstance dto.WorkflowInstanceResponse
	if err := json.NewDecoder(resp.Body).Decode(&fetchedInstance); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if fetchedInstance.ID != instance.ID {
		t.Errorf("Expected ID %s, got %s", instance.ID, fetchedInstance.ID)
	}
	if val, ok := fetchedInstance.Context["key"]; !ok || val != "val" {
		t.Errorf("Expected context key=val, got %v", val)
	}
}

func TestConsoleLoginOnlyInDisabledMode(t *testing.T) {
	disabled := setupTestHandlerWithIdentityConfig(t, iam.DeploymentConfig{
		IAMEnabled:   false,
		ConsoleAdmin: iam.ConsoleAdminCredentials{Username: "admin", Password: "admin"},
	})
	r := mux.NewRouter()
	registerTestRoutes(r, disabled)
	login := func(router *mux.Router, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/identity/login", strings.NewReader(body)))
		return rec
	}

	ok := login(r, `{"username":"admin","password":"admin"}`)
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), auth.RoleAdmin) {
		t.Fatalf("expected admin login to succeed, got %d %s", ok.Code, ok.Body.String())
	}
	for body, want := range map[string]int{
		`{"username":"admin","password":"wrong"}`: http.StatusUnauthorized,
		`{"username":"root","password":"admin"}`:  http.StatusUnauthorized,
		`{"username":"admin","extra":true}`:       http.StatusBadRequest,
	} {
		if got := login(r, body).Code; got != want {
			t.Fatalf("login %s: got %d, want %d", body, got, want)
		}
	}

	external := mux.NewRouter()
	registerTestRoutes(external, setupTestHandler(t))
	if got := login(external, `{"username":"admin","password":"admin"}`).Code; got != http.StatusNotFound {
		t.Fatalf("external mode must not offer console login, got %d", got)
	}
}
