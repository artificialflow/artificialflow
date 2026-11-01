package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	queryrepository "github.com/artificialflow/artificialflow/backend/services/workflow-query/internal/domain/repository"
)

type searchCall struct {
	index string
	body  json.RawMessage
}

type fakeSearchBackend struct {
	responses map[string]json.RawMessage
	errors    map[string]error
	calls     []searchCall
}

func (f *fakeSearchBackend) Search(_ context.Context, index string, body json.RawMessage) (json.RawMessage, error) {
	f.calls = append(f.calls, searchCall{index: index, body: append(json.RawMessage(nil), body...)})
	if err := f.errors[index]; err != nil {
		return nil, err
	}
	return f.responses[index], nil
}

func (f *fakeSearchBackend) Upsert(context.Context, string, string, any) error {
	return nil
}

func (f *fakeSearchBackend) Delete(context.Context, string, string) error {
	return nil
}

func TestNewESRepositoryDefaultsToCanonicalPrefix(t *testing.T) {
	repo := NewESRepository(&fakeSearchBackend{}, "")

	if repo.instanceIndex != "artificialflow-process_instance" || repo.processIndex != "artificialflow-process" {
		t.Fatalf("expected canonical indexes, got instance=%q process=%q", repo.instanceIndex, repo.processIndex)
	}
	if repo.legacyInstanceIndex != "" || repo.legacyProcessIndex != "" {
		t.Fatalf("expected no legacy indexes, got instance=%q process=%q", repo.legacyInstanceIndex, repo.legacyProcessIndex)
	}
}

func TestTenantFilterRequiresExactTenantAndBoundsLegacyDocuments(t *testing.T) {
	tenantCtx := auth.WithPrincipal(context.Background(), auth.Principal{Subject: "reader", TenantID: "tenant-a"})
	exact := tenantFilter(tenantCtx)
	if !reflect.DeepEqual(exact, map[string]any{"term": map[string]any{"tenant_id": "tenant-a"}}) {
		t.Fatalf("exact tenant filter = %#v", exact)
	}

	compat := tenantFilter(context.Background())
	boolFilter, ok := compat["bool"].(map[string]any)
	if !ok || boolFilter["minimum_should_match"] != 1 {
		t.Fatalf("default compatibility filter = %#v", compat)
	}
	clauses, ok := boolFilter["should"].([]any)
	if !ok || len(clauses) != 2 {
		t.Fatalf("default compatibility clauses = %#v", boolFilter["should"])
	}
}

func TestESRepositorySearchInstancesCanonicalOnly(t *testing.T) {
	backend := &fakeSearchBackend{
		responses: map[string]json.RawMessage{
			"artificialflow-process_instance": searchResponse(
				map[string]any{"key": 2, "state": "ACTIVE", "created_at": "2026-01-02T00:00:00Z"},
				map[string]any{"key": 1, "state": "COMPLETED", "created_at": "2026-01-01T00:00:00Z"},
			),
		},
	}
	repo := NewESRepository(backend, "")

	result, err := repo.SearchInstances(context.Background(), queryrepository.InstanceFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("canonical search failed: %v", err)
	}
	if result.Total != 2 || keysOfInstances(result.Instances) != "2,1" {
		t.Fatalf("unexpected canonical result: %#v", result)
	}
	var body map[string]any
	if err := json.Unmarshal(backend.calls[0].body, &body); err != nil {
		t.Fatal(err)
	}
	if body["from"] != float64(0) || body["size"] != float64(10) {
		t.Fatalf("canonical query must request only the API page, got from=%v size=%v", body["from"], body["size"])
	}
	assertSearchOrder(t, backend.calls, "artificialflow-process_instance")
}

func TestESRepositorySearchWorkflowsCanonicalOnly(t *testing.T) {
	backend := &fakeSearchBackend{
		responses: map[string]json.RawMessage{
			"artificialflow-process": searchResponse(
				map[string]any{"key": 2, "bpmn_process_id": "canonical", "created_at": "2026-01-02T00:00:00Z"},
				map[string]any{"key": 1, "bpmn_process_id": "older", "created_at": "2026-01-01T00:00:00Z"},
			),
		},
	}
	repo := NewESRepository(backend, "")

	result, err := repo.SearchWorkflows(context.Background(), queryrepository.WorkflowFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("canonical workflow search failed: %v", err)
	}
	if result.Total != 2 || len(result.Workflows) != 2 || result.Workflows[0].BpmnProcessID != "canonical" {
		t.Fatalf("unexpected workflow result: %#v", result)
	}
	assertSearchOrder(t, backend.calls, "artificialflow-process")
}

func TestESRepositoryGetInstanceUsesCanonicalIndex(t *testing.T) {
	backend := &fakeSearchBackend{
		responses: map[string]json.RawMessage{
			"artificialflow-process_instance": searchResponse(
				map[string]any{"key": 42, "process_definition_key": 7, "state": "ACTIVE"},
			),
		},
	}
	repo := NewESRepository(backend, "")

	instance, err := repo.GetInstance(context.Background(), "42")
	if err != nil {
		t.Fatalf("canonical get failed: %v", err)
	}
	if instance.State != "ACTIVE" || len(backend.calls) != 1 {
		t.Fatalf("unexpected get result: instance=%#v calls=%#v", instance, backend.calls)
	}
}

func TestESRepositoryTreatsMissingIndicesAsEmpty(t *testing.T) {
	missing := errors.New(`search failed: status=404 type=index_not_found_exception`)
	backend := &fakeSearchBackend{
		responses: map[string]json.RawMessage{},
		errors: map[string]error{
			"artificialflow-process_instance": missing,
			"artificialflow-process":          missing,
		},
	}
	repo := NewESRepository(backend, "")

	instances, err := repo.SearchInstances(context.Background(), queryrepository.InstanceFilter{Page: 1, PageSize: 10})
	if err != nil || instances.Total != 0 || len(instances.Instances) != 0 {
		t.Fatalf("missing instance indices should be empty, got result=%#v err=%v", instances, err)
	}
	workflows, err := repo.SearchWorkflows(context.Background(), queryrepository.WorkflowFilter{Page: 1, PageSize: 10})
	if err != nil || workflows.Total != 0 || len(workflows.Workflows) != 0 {
		t.Fatalf("missing workflow indices should be empty, got result=%#v err=%v", workflows, err)
	}
	if _, err := repo.GetInstance(context.Background(), "42"); !errors.Is(err, queryrepository.ErrInstanceNotFound) {
		t.Fatalf("missing instance indices should return not found, got %v", err)
	}
}

func TestESRepositoryPropagatesCanonicalErrors(t *testing.T) {
	backend := &fakeSearchBackend{
		errors: map[string]error{
			"artificialflow-process_instance": errors.New("status=503 cluster unavailable"),
		},
	}
	repo := NewESRepository(backend, "")

	if _, err := repo.SearchInstances(context.Background(), queryrepository.InstanceFilter{Page: 1, PageSize: 10}); err == nil {
		t.Fatal("expected canonical search error")
	}
	assertSearchOrder(t, backend.calls, "artificialflow-process_instance")
}

func TestESRepositoryValidatesPaginationWindow(t *testing.T) {
	repo := NewESRepository(&fakeSearchBackend{}, "")
	if _, err := repo.SearchInstances(context.Background(), queryrepository.InstanceFilter{
		Page:     2,
		PageSize: transitionMergeMaxDocuments,
	}); err == nil || !strings.Contains(err.Error(), "first 10000") {
		t.Fatalf("expected page-window error, got %v", err)
	}
}

func searchResponse(docs ...map[string]any) json.RawMessage {
	hits := make([]map[string]any, 0, len(docs))
	for _, doc := range docs {
		hits = append(hits, map[string]any{"_source": doc})
	}
	payload, err := json.Marshal(map[string]any{
		"hits": map[string]any{
			"total": map[string]any{"value": len(docs)},
			"hits":  hits,
		},
	})
	if err != nil {
		panic(err)
	}
	return payload
}

func keysOfInstances(instances []model.ProcessInstance) string {
	parts := make([]string, 0, len(instances))
	for _, instance := range instances {
		parts = append(parts, strconv.FormatInt(instance.Key, 10))
	}
	return strings.Join(parts, ",")
}

func assertSearchOrder(t *testing.T, calls []searchCall, want ...string) {
	t.Helper()
	got := make([]string, 0, len(calls))
	for _, call := range calls {
		got = append(got, call.index)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected search order: got %#v want %#v", got, want)
	}
}
