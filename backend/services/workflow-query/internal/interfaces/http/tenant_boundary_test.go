package http

import (
	"context"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/services/workflow-query/internal/application"
	"github.com/artificialflow/artificialflow/backend/services/workflow-query/internal/domain/repository"
	"github.com/gorilla/mux"
)

type tenantBoundaryQueryRepository struct {
	instances []model.ProcessInstance
	workflows []model.Process
}

func (r *tenantBoundaryQueryRepository) SearchInstances(ctx context.Context, _ repository.InstanceFilter) (*repository.InstanceSearchResult, error) {
	tenant := auth.TenantIDFromContext(ctx)
	items := make([]model.ProcessInstance, 0)
	for _, instance := range r.instances {
		if instance.TenantID == tenant {
			items = append(items, instance)
		}
	}
	return &repository.InstanceSearchResult{Instances: items, Total: int64(len(items))}, nil
}

func (r *tenantBoundaryQueryRepository) SearchWorkflows(ctx context.Context, _ repository.WorkflowFilter) (*repository.WorkflowSearchResult, error) {
	tenant := auth.TenantIDFromContext(ctx)
	items := make([]model.Process, 0)
	for _, workflow := range r.workflows {
		if workflow.TenantID == tenant {
			items = append(items, workflow)
		}
	}
	return &repository.WorkflowSearchResult{Workflows: items, Total: int64(len(items))}, nil
}

func (r *tenantBoundaryQueryRepository) GetInstance(ctx context.Context, id string) (*model.ProcessInstance, error) {
	tenant := auth.TenantIDFromContext(ctx)
	for _, instance := range r.instances {
		if fmt.Sprintf("%d", instance.Key) == id && instance.TenantID == tenant {
			copy := instance
			return &copy, nil
		}
	}
	return nil, repository.ErrInstanceNotFound
}

func TestQueryHTTPTenantBoundaryGetAndLists(t *testing.T) {
	now := time.Now().UTC()
	repo := &tenantBoundaryQueryRepository{
		instances: []model.ProcessInstance{{
			Key: 123, ID: "tenant-a-instance", ProcessDefinitionKey: 456,
			State: model.ProcessInstanceStateActive, TenantID: "tenant-a", CreatedAt: now,
		}},
		workflows: []model.Process{{
			Key: 456, BpmnProcessID: "tenant-a-process", Version: 1,
			TenantID: "tenant-a", CreatedAt: now,
		}},
	}
	handler := NewHandler(application.NewQueryService(repo))
	router := mux.NewRouter()
	registerTestRoutes(router, handler)
	tenantB := auth.Principal{
		Subject: "tenant-b-reader", TenantID: "tenant-b",
		Roles: []string{auth.RoleAdmin},
	}
	request := func(path string) *nethttp.Request {
		req := httptest.NewRequest(nethttp.MethodGet, path, nil)
		return req.WithContext(auth.WithPrincipal(req.Context(), tenantB))
	}

	foreign := httptest.NewRecorder()
	router.ServeHTTP(foreign, request("/instances/123"))
	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, request("/instances/999"))
	if foreign.Code != nethttp.StatusNotFound || foreign.Code != missing.Code ||
		strings.TrimSpace(foreign.Body.String()) != strings.TrimSpace(missing.Body.String()) {
		t.Fatalf("foreign=%d %q missing=%d %q",
			foreign.Code, foreign.Body.String(), missing.Code, missing.Body.String())
	}

	for _, fixture := range []struct {
		path    string
		markers []string
	}{
		{path: "/instances", markers: []string{"tenant-a-instance", `"123"`}},
		{path: "/workflows", markers: []string{"tenant-a-process", `"456"`}},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request(fixture.path))
		if response.Code != nethttp.StatusOK {
			t.Fatalf("%s status=%d body=%q", fixture.path, response.Code, response.Body.String())
		}
		for _, marker := range fixture.markers {
			if strings.Contains(response.Body.String(), marker) {
				t.Fatalf("%s leaked marker %q: %s", fixture.path, marker, response.Body.String())
			}
		}
	}
}

func TestQueryTenantRouteMatrixIsAuthoritative(t *testing.T) {
	handler := NewHandler(nil)
	router := mux.NewRouter()
	handler.RegisterRoutes(router)
	covered := map[string]struct{}{
		"GET /instances":      {},
		"GET /instances/{id}": {},
		"GET /workflows":      {},
	}
	err := router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		path, pathErr := route.GetPathTemplate()
		methods, methodErr := route.GetMethods()
		if pathErr != nil || methodErr != nil {
			return nil
		}
		for _, method := range methods {
			if _, ok := covered[method+" "+path]; !ok {
				t.Errorf("query route %s %s has no tenant-boundary fixture", method, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
