package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/httpproblem"
	"github.com/artificialflow/artificialflow/backend/libs/iam"
	"github.com/artificialflow/artificialflow/backend/libs/logger"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/interfaces/http/dto"
	"github.com/gorilla/mux"
)

func TestVersionedRouteAliasesMatchLegacyRoutes(t *testing.T) {
	router := mux.NewRouter()
	handler := NewHandler(nil, iam.DeploymentConfig{})
	handler.RegisterRoutes(router)
	handler.RegisterRoutes(router.PathPrefix("/v1").Subrouter())

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/decisions"},
		{http.MethodPost, "/decisions/invoice/evaluate"},
		{http.MethodGet, "/incidents"},
		{http.MethodPost, "/incidents/123/resolve"},
		{http.MethodGet, "/instances/123/jobs"},
		{http.MethodPost, "/jobs/456/retry"},
		{http.MethodGet, "/jobs/capabilities"},
	}
	for _, route := range routes {
		for _, prefix := range []string{"", "/v1"} {
			request, err := http.NewRequest(route.method, prefix+route.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			var match mux.RouteMatch
			if !router.Match(request, &match) {
				t.Errorf("%s %s did not match: %v", route.method, prefix+route.path, match.MatchErr)
			}
		}
	}
}

func TestLegacyAndVersionedCommandErrors(t *testing.T) {
	router := mux.NewRouter()
	handler := NewHandler(nil, iam.DeploymentConfig{})
	handler.RegisterRoutes(router)
	handler.RegisterRoutes(router.PathPrefix("/v1").Subrouter())
	server := logger.NewHTTPMiddleware("test").Middleware(httpproblem.Middleware(router))

	for _, test := range []struct {
		path        string
		contentType string
		versioned   bool
	}{
		{path: "/incidents?processInstanceKey=bad", contentType: "text/plain; charset=utf-8"},
		{path: "/v1/incidents?processInstanceKey=bad", contentType: httpproblem.ContentType, versioned: true},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{
			Subject: "admin", Roles: []string{auth.RoleAdmin},
		}))
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || response.Header().Get("Content-Type") != test.contentType {
			t.Fatalf("%s status=%d content-type=%q", test.path, response.Code, response.Header().Get("Content-Type"))
		}
		if !test.versioned {
			if response.Body.String() != "invalid processInstanceKey\n" {
				t.Fatalf("legacy body = %q", response.Body.String())
			}
			continue
		}
		var problem httpproblem.Problem
		if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
			t.Fatal(err)
		}
		if problem.Detail != "invalid processInstanceKey" || problem.Instance != "/v1/incidents" || strings.TrimSpace(problem.CorrelationID) == "" {
			t.Fatalf("versioned problem = %#v", problem)
		}
	}
}

func TestIncidentResponseWrapperAndResolveParity(t *testing.T) {
	handler, repository := setupTestHandlerWithRepository(t, iam.DeploymentConfig{})
	incident := &model.Incident{
		Key: 101, ID: "incident-101", ProcessInstanceKey: 202, ElementInstanceKey: 303,
		JobKey: 404, ErrorType: "JOB_FAILED", ErrorMessage: "failed", State: "CREATED",
	}
	if err := repository.CreateIncident(t.Context(), incident); err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	handler.RegisterRoutes(router)
	handler.RegisterRoutes(router.PathPrefix("/v1").Subrouter())

	requestAsAdmin := func(method, path string) *http.Request {
		request := httptest.NewRequest(method, path, nil)
		return request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{
			Subject: "admin", Roles: []string{auth.RoleAdmin},
		}))
	}
	for _, path := range []string{"/incidents", "/v1/incidents"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, requestAsAdmin(http.MethodGet, path))
		var payload struct {
			Incidents []dto.IncidentResponse `json:"incidents"`
		}
		if response.Code != http.StatusOK || json.NewDecoder(response.Body).Decode(&payload) != nil || len(payload.Incidents) != 1 {
			t.Fatalf("%s response=%d body=%q", path, response.Code, response.Body.String())
		}
	}

	response := httptest.NewRecorder()
	router.ServeHTTP(response, requestAsAdmin(http.MethodPost, "/v1/incidents/101/resolve"))
	var resolved dto.IncidentResponse
	if response.Code != http.StatusOK || json.NewDecoder(response.Body).Decode(&resolved) != nil || resolved.State != "RESOLVED" {
		t.Fatalf("resolve response=%d body=%q", response.Code, response.Body.String())
	}
}
