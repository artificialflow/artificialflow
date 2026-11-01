package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/iam"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/gorilla/mux"
)

const (
	tenantBoundaryA = "tenant-a"
	tenantBoundaryB = "tenant-b"
)

func tenantBoundaryPrincipal(tenant string) auth.Principal {
	return auth.Principal{
		Subject: tenant + "-admin", Email: tenant + "@example.test", TenantID: tenant,
		Roles: []string{auth.RoleAdmin},
	}
}

func tenantBoundaryRequest(method, path, body string, principal auth.Principal) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request.WithContext(auth.WithPrincipal(request.Context(), principal))
}

func TestCommandHTTPRejectsCrossTenantResourcesLikeMissingResources(t *testing.T) {
	handler, repo := setupTestHandlerWithRepository(t, iam.DeploymentConfig{IAMEnabled: true})
	router := mux.NewRouter()
	registerTestRoutes(router, handler)
	tenantA := auth.WithPrincipal(context.Background(), tenantBoundaryPrincipal(tenantBoundaryA))
	now := time.Now().UTC()

	if err := repo.CreateProcess(tenantA, &model.Process{
		Key: 100, BpmnProcessID: "tenant-a-process", Version: 1, ResourceName: "tenant-a.bpmn",
		Resource: []byte(`[]`), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateProcess(tenantA, &model.Process{
		Key: 101, BpmnProcessID: "tenant-a-target", Version: 1, ResourceName: "target.bpmn",
		Resource: []byte(`[]`), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateProcessInstance(tenantA, &model.ProcessInstance{
		Key: 200, ID: "tenant-a-instance", ProcessDefinitionKey: 100, Version: 1,
		State: model.ProcessInstanceStateActive, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateElementInstance(tenantA, &model.ElementInstance{
		Key: 300, ID: "tenant-a-element", ProcessInstanceKey: 200, ProcessDefinitionKey: 100,
		ElementID: "approve", BpmnElementType: "USER_TASK", State: model.ElementInstanceStateActivated,
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	lockExpiry := now.Add(time.Minute)
	if err := repo.CreateJob(tenantA, &model.Job{
		Key: 400, ID: "tenant-a-job", Type: "artificialflow:userTask", ProcessInstanceKey: 200,
		ElementInstanceKey: 300, ProcessDefinitionKey: 100, ElementID: "approve",
		Worker: "worker-a", Retries: 3, State: model.JobStateActivated,
		LockExpirationTime: &lockExpiry, CandidateGroups: "accountant", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateVariable(tenantA, &model.Variable{
		ScopeKey: 200, ProcessInstanceKey: 200, Name: "context",
		Value: []byte(`{"secret":"tenant-a"}`), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateIncident(tenantA, &model.Incident{
		Key: 500, ID: "tenant-a-incident", ProcessInstanceKey: 200, ElementInstanceKey: 300,
		JobKey: 400, ErrorType: "JOB_FAILED", ErrorMessage: "tenant-a-only",
		State: model.IncidentStateCreated, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateMessageSubscription(tenantA, &model.MessageSubscription{
		Key: 600, ID: "tenant-a-subscription", ProcessInstanceKey: 200, ElementInstanceKey: 300,
		ElementID: "message-wait", MessageName: "payment", CorrelationKey: "order-a",
		State: model.MessageSubscriptionStateOpen, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertDecision(tenantA, &model.DecisionDefinition{
		Key: 700, DecisionID: "tenant-a-decision", Name: "Tenant A",
		Resource: []byte(`{"id":"tenant-a-decision","rules":[]}`),
	}); err != nil {
		t.Fatal(err)
	}

	tenantBAdmin := tenantBoundaryPrincipal(tenantBoundaryB)
	tests := []struct {
		name, method, foreignPath, missingPath, body string
		allowSuccess                                 bool
	}{
		{"workflow get", http.MethodGet, "/workflows/100", "/workflows/999", "", false},
		{"workflow delete", http.MethodDelete, "/workflows/100", "/workflows/999", "", true},
		{"instance get", http.MethodGet, "/instances/200", "/instances/999", "", false},
		{"instance delete", http.MethodDelete, "/instances/200", "/instances/999", "", true},
		{"instance variables", http.MethodPost, "/instances/200/variables", "/instances/999/variables", `{"variables":{"changed":true}}`, false},
		{"instance complete", http.MethodPost, "/instances/200/complete", "/instances/999/complete", `{}`, false},
		{"instance jobs", http.MethodGet, "/instances/200/jobs", "/instances/999/jobs", "", true},
		{"user tasks list", http.MethodGet, "/instances/200/tasks", "/instances/999/tasks", "", true},
		{"user task claim", http.MethodPost, "/instances/200/tasks/300/claim", "/instances/999/tasks/999/claim", `{}`, false},
		{"user task complete", http.MethodPost, "/instances/200/tasks/300/complete", "/instances/999/tasks/999/complete", `{}`, false},
		{"incident resolve", http.MethodPost, "/incidents/500/resolve", "/incidents/999/resolve", `{}`, false},
		{"job retry", http.MethodPost, "/jobs/400/retry", "/jobs/999/retry", `{"retries":5}`, false},
		{"job complete", http.MethodPost, "/jobs/400/complete", "/jobs/999/complete", `{"worker":"worker-a"}`, false},
		{"job fail", http.MethodPost, "/jobs/400/fail", "/jobs/999/fail", `{"worker":"worker-a","errorMessage":"failed"}`, false},
		{"job extend", http.MethodPost, "/jobs/400/extend-lock", "/jobs/999/extend-lock", `{"worker":"worker-a","lockDurationMs":1000}`, false},
		{"decision evaluate", http.MethodPost, "/decisions/tenant-a-decision/evaluate", "/decisions/missing/evaluate", `{"inputs":{}}`, false},
		{"migration dry run", http.MethodPost, "/instances/200/migration/dry-run", "/instances/999/migration/dry-run", `{"sourceDefinitionKey":"100","targetDefinitionKey":"101","elementMapping":{}}`, false},
		{"migration apply", http.MethodPost, "/instances/200/migration", "/instances/999/migration", `{"sourceDefinitionKey":"100","targetDefinitionKey":"101","elementMapping":{}}`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			foreign := httptest.NewRecorder()
			router.ServeHTTP(foreign, tenantBoundaryRequest(test.method, test.foreignPath, test.body, tenantBAdmin))
			missing := httptest.NewRecorder()
			router.ServeHTTP(missing, tenantBoundaryRequest(test.method, test.missingPath, test.body, tenantBAdmin))
			if foreign.Code != missing.Code {
				t.Fatalf("foreign status=%d body=%q; missing status=%d body=%q",
					foreign.Code, foreign.Body.String(), missing.Code, missing.Body.String())
			}
			if !test.allowSuccess && foreign.Code >= http.StatusOK && foreign.Code < http.StatusMultipleChoices {
				t.Fatalf("foreign resource unexpectedly succeeded: status=%d body=%q", foreign.Code, foreign.Body.String())
			}
		})
	}

	listTests := []struct {
		name, path string
		markers    []string
	}{
		{"workflows", "/workflows", []string{"tenant-a-process", `"100"`}},
		{"instances", "/instances", []string{"tenant-a-instance", `"200"`}},
		{"completed instances", "/instances/history/completed", []string{"tenant-a-instance", `"200"`}},
		{"incidents", "/incidents", []string{"tenant-a-incident", "tenant-a-only"}},
		{"decisions", "/decisions", []string{"tenant-a-decision", "Tenant A"}},
		{"user task search", "/tasks?candidateGroup=accountant&state=all", []string{`"400"`, `"200"`}},
	}
	for _, test := range listTests {
		t.Run("list "+test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, tenantBoundaryRequest(http.MethodGet, test.path, "", tenantBAdmin))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			for _, marker := range test.markers {
				if strings.Contains(response.Body.String(), marker) {
					t.Fatalf("foreign marker %q leaked in %s: %s", marker, test.path, response.Body.String())
				}
			}
		})
	}

	t.Run("job activation and instance job list", func(t *testing.T) {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, tenantBoundaryRequest(
			http.MethodPost, "/jobs/activate",
			`{"type":"artificialflow:userTask","worker":"worker-b","maxJobs":10,"lockDurationMs":1000}`,
			tenantBAdmin,
		))
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"400"`) {
			t.Fatalf("foreign job activated: status=%d body=%q", response.Code, response.Body.String())
		}
	})

	t.Run("message correlation is tenant scoped", func(t *testing.T) {
		for _, correlationKey := range []string{"order-a", "missing"} {
			response := httptest.NewRecorder()
			body := fmt.Sprintf(`{"message_name":"payment","correlation_key":%q,"payload":{"changed":true}}`, correlationKey)
			router.ServeHTTP(response, tenantBoundaryRequest(http.MethodPost, "/messages", body, tenantBAdmin))
			if response.Code != http.StatusOK {
				t.Fatalf("correlation %q status=%d body=%q", correlationKey, response.Code, response.Body.String())
			}
		}
		var subscription model.MessageSubscription
		if err := repo.DB.WithContext(tenantA).First(&subscription, "key = ?", 600).Error; err != nil {
			t.Fatal(err)
		}
		if subscription.State != model.MessageSubscriptionStateOpen {
			t.Fatalf("foreign message mutated subscription state to %q", subscription.State)
		}
	})

	var job model.Job
	if err := repo.DB.WithContext(tenantA).First(&job, "key = ?", 400).Error; err != nil {
		t.Fatal(err)
	}
	if job.State != model.JobStateActivated || job.Worker != "worker-a" || job.Retries != 3 {
		t.Fatalf("foreign worker routes mutated job: %#v", job)
	}
	var variable model.Variable
	if err := repo.DB.WithContext(tenantA).First(&variable, "scope_key = ?", 200).Error; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(variable.Value, []byte(`{"secret":"tenant-a"}`)) {
		t.Fatalf("foreign variable mutation changed value to %s", variable.Value)
	}
	if _, err := repo.GetProcess(tenantA, 100); err != nil {
		t.Fatalf("foreign workflow delete removed tenant A process: %v", err)
	}
	if _, err := repo.GetProcessInstance(tenantA, 200); err != nil {
		t.Fatalf("foreign instance routes removed tenant A instance: %v", err)
	}
	incident, err := repo.GetIncident(tenantA, 500)
	if err != nil || incident.State != model.IncidentStateCreated {
		t.Fatalf("foreign incident resolve mutated tenant A incident: incident=%#v err=%v", incident, err)
	}
	decision, err := repo.GetDecisionByDecisionID(tenantA, "tenant-a-decision")
	if err != nil || decision.Name != "Tenant A" {
		t.Fatalf("foreign decision route changed tenant A decision: decision=%#v err=%v", decision, err)
	}
}

func TestCommandTenantRouteMatrixIsAuthoritative(t *testing.T) {
	handler := NewHandler(nil, iam.DeploymentConfig{IAMEnabled: true})
	router := mux.NewRouter()
	handler.RegisterRoutes(router)

	covered := map[string]string{
		"GET /tasks":      "task-search",
		"POST /workflows": "context-derived-create", "GET /workflows": "workflow-list",
		"GET /workflows/{id}": "workflow-resource", "DELETE /workflows/{id}": "workflow-resource",
		"GET /decisions": "decision-list", "POST /decisions": "context-derived-create",
		"POST /decisions/{id}/evaluate": "decision-resource",
		"POST /instances":               "context-derived-create", "GET /instances": "instance-list",
		"GET /instances/history/completed": "instance-list",
		"POST /instances/{id}/variables":   "instance-resource", "POST /instances/{id}/complete": "instance-resource",
		"GET /instances/{id}/tasks":                         "instance-resource",
		"POST /instances/{id}/tasks/{executionId}/claim":    "instance-resource",
		"POST /instances/{id}/tasks/{executionId}/complete": "instance-resource",
		"GET /instances/{id}/jobs":                          "instance-resource", "GET /instances/{id}": "instance-resource",
		"DELETE /instances/{id}":                 "instance-resource",
		"POST /instances/{id}/migration/dry-run": "migration-resource",
		"POST /instances/{id}/migration":         "migration-resource",
		"GET /incidents":                         "incident-list", "POST /incidents/{key}/resolve": "incident-resource",
		"POST /jobs/{key}/retry": "job-resource", "POST /jobs/activate": "job-activation",
		"POST /jobs/{key}/complete": "job-resource", "POST /jobs/{key}/fail": "job-resource",
		"POST /jobs/{key}/extend-lock": "job-resource",
		"POST /signals":                "tenant-scoped-fanout", "POST /messages": "message-correlation",
		"POST /escalations": "tenant-scoped-fanout",
	}
	excluded := map[string]string{
		"GET /identity/config":   "deployment metadata is not tenant-owned",
		"POST /identity/login":   "console credential check is not tenant-owned",
		"GET /jobs/capabilities": "static protocol metadata has no tenant data",
		"GET /internal/metrics":  "aggregate operational telemetry is authorization-scoped, not row-tenant-owned",
	}

	err := router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		path, pathErr := route.GetPathTemplate()
		if pathErr != nil || strings.HasPrefix(path, "/swagger/") {
			return nil
		}
		methods, methodErr := route.GetMethods()
		if methodErr != nil {
			return nil
		}
		for _, method := range methods {
			key := method + " " + path
			if _, ok := covered[key]; ok {
				continue
			}
			if reason, ok := excluded[key]; ok && reason != "" {
				continue
			}
			t.Errorf("public route %s has no tenant-boundary fixture or explicit exclusion", key)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTenantBoundaryFixtureBodiesAreValidJSON(t *testing.T) {
	for _, body := range []string{
		`{"variables":{"changed":true}}`,
		`{"inputs":{}}`,
		`{"sourceDefinitionKey":"100","targetDefinitionKey":"101","elementMapping":{}}`,
	} {
		var value any
		if err := json.Unmarshal([]byte(body), &value); err != nil {
			t.Fatal(err)
		}
	}
}
