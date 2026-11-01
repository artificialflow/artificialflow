package http

import (
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/httpproblem"
	"github.com/artificialflow/artificialflow/backend/libs/logger"
	"github.com/gorilla/mux"
)

func TestVersionedRouteAliasesMatchLegacyRoutes(t *testing.T) {
	router := mux.NewRouter()
	handler := NewHandler(nil)
	handler.RegisterRoutes(router)
	handler.RegisterRoutes(router.PathPrefix("/v1").Subrouter())

	for _, path := range []string{"/instances", "/instances/123", "/workflows"} {
		for _, prefix := range []string{"", "/v1"} {
			request, err := nethttp.NewRequest(nethttp.MethodGet, prefix+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			var match mux.RouteMatch
			if !router.Match(request, &match) {
				t.Errorf("GET %s did not match: %v", prefix+path, match.MatchErr)
			}
		}
	}
}

func TestLegacyAndVersionedQueryErrors(t *testing.T) {
	router := mux.NewRouter()
	handler := NewHandler(nil)
	handler.RegisterRoutes(router)
	handler.RegisterRoutes(router.PathPrefix("/v1").Subrouter())
	server := logger.NewHTTPMiddleware("test").Middleware(httpproblem.Middleware(router))

	for _, test := range []struct {
		path        string
		contentType string
		versioned   bool
	}{
		{path: "/instances?workflowId=bad", contentType: "text/plain; charset=utf-8"},
		{path: "/v1/instances?workflowId=bad", contentType: httpproblem.ContentType, versioned: true},
	} {
		request := httptest.NewRequest(nethttp.MethodGet, test.path, nil)
		request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{
			Subject: "admin", Roles: []string{auth.RoleAdmin},
		}))
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != nethttp.StatusBadRequest || response.Header().Get("Content-Type") != test.contentType {
			t.Fatalf("%s status=%d content-type=%q", test.path, response.Code, response.Header().Get("Content-Type"))
		}
		if !test.versioned {
			if response.Body.String() != "invalid workflowId: must be numeric\n" {
				t.Fatalf("legacy body = %q", response.Body.String())
			}
			continue
		}
		var problem httpproblem.Problem
		if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
			t.Fatal(err)
		}
		if problem.Detail != "invalid workflowId: must be numeric" || problem.Instance != "/v1/instances" || strings.TrimSpace(problem.CorrelationID) == "" {
			t.Fatalf("versioned problem = %#v", problem)
		}
	}
}
