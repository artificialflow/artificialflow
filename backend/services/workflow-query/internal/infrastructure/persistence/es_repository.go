package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/libs/search"
	"github.com/artificialflow/artificialflow/backend/services/workflow-query/internal/domain/repository"
)

type ESRepository struct {
	client              search.Backend
	instanceIndex       string
	processIndex        string
	legacyInstanceIndex string
	legacyProcessIndex  string
}

const (
	canonicalIndexPrefix = "artificialflow"

	// transitionMergeMaxDocuments matches Elasticsearch's default max_result_window.
	// Compatibility list reads load both result sets so they can de-duplicate and
	// globally sort them without allowing a new canonical index to hide legacy data.
	transitionMergeMaxDocuments = 10_000
)

func tenantFilter(ctx context.Context) map[string]any {
	tenant := auth.TenantIDFromContext(ctx)
	if tenant != auth.DefaultTenantID {
		return map[string]any{"term": map[string]any{"tenant_id": tenant}}
	}
	// Existing 1.x projection documents may predate tenant_id. They belong only
	// to the reserved default tenant during the compatibility window.
	return map[string]any{"bool": map[string]any{
		"minimum_should_match": 1,
		"should": []any{
			map[string]any{"term": map[string]any{"tenant_id": auth.DefaultTenantID}},
			map[string]any{"bool": map[string]any{"must_not": map[string]any{"exists": map[string]any{"field": "tenant_id"}}}},
		},
	}}
}

// Ensure implementation
var _ repository.QueryRepository = &ESRepository{}

func NewESRepository(client search.Backend, indexPrefix string) *ESRepository {
	indexPrefix = strings.TrimSpace(indexPrefix)
	if indexPrefix == "" {
		indexPrefix = canonicalIndexPrefix
	}

	r := &ESRepository{
		client:        client,
		instanceIndex: indexPrefix + "-process_instance",
		processIndex:  indexPrefix + "-process",
	}
	return r
}

func (r *ESRepository) GetInstance(ctx context.Context, id string) (*model.ProcessInstance, error) {
	instanceKey, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid instance id: %w", err)
	}

	// Search by exact key match
	queryMap := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{"filter": []any{
				map[string]any{"term": map[string]any{"key": instanceKey}},
				tenantFilter(ctx),
			}},
		},
	}

	bodyBytes, err := json.Marshal(queryMap)
	if err != nil {
		return nil, err
	}

	results, err := r.searchCompatible(
		ctx,
		r.instanceIndex,
		r.legacyInstanceIndex,
		bodyBytes,
		func(result json.RawMessage) (bool, error) {
			hasHits, err := searchResultHasHits(result)
			return !hasHits, err
		},
	)
	if err != nil {
		return nil, err
	}

	result := results.canonical
	if len(result) > 0 {
		hasHits, err := searchResultHasHits(result)
		if err != nil {
			return nil, err
		}
		if !hasHits {
			result = results.legacy
		}
	} else {
		result = results.legacy
	}
	if len(result) == 0 {
		return nil, repository.ErrInstanceNotFound
	}

	esResp := esSearchResponse[esProcessInstance]{}
	if err := json.Unmarshal(result, &esResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ES response: %v", err)
	}
	if len(esResp.Hits.Hits) == 0 {
		return nil, repository.ErrInstanceNotFound
	}

	src := esResp.Hits.Hits[0].Source
	return &model.ProcessInstance{
		Key:                      src.Key,
		ProcessDefinitionKey:     src.ProcessDefinitionKey,
		Version:                  src.Version,
		ParentProcessInstanceKey: src.ParentProcessInstanceKey,
		ParentElementInstanceKey: src.ParentElementInstanceKey,
		State:                    model.ProcessInstanceState(src.State),
		TenantID:                 src.TenantID,
		CreatedAt:                src.CreatedAt,
		EndTime:                  src.EndTime,
		Context:                  src.Context,
	}, nil
}

func (r *ESRepository) SearchInstances(ctx context.Context, filter repository.InstanceFilter) (*repository.InstanceSearchResult, error) {
	from, to, err := transitionPageBounds(filter.Page, filter.PageSize)
	if err != nil {
		return nil, err
	}

	filterClauses := []map[string]any{tenantFilter(ctx)}
	if filter.WorkflowID != "" {
		workflowKey, err := strconv.ParseInt(filter.WorkflowID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid workflow id filter %q: %w", filter.WorkflowID, err)
		}
		filterClauses = append(filterClauses, map[string]any{
			"term": map[string]any{
				"process_definition_key": workflowKey,
			},
		})
	}
	if filter.State != "" {
		filterClauses = append(filterClauses, map[string]any{
			"term": map[string]any{
				"state": filter.State,
			},
		})
	}

	queryMap := map[string]any{
		"bool": map[string]any{
			"filter": filterClauses,
		},
	}

	searchBody := map[string]any{
		"query":            queryMap,
		"from":             0,
		"size":             transitionMergeMaxDocuments,
		"track_total_hits": true,
		"sort": []map[string]any{
			{"created_at": "desc"},
			{"key": "desc"},
		},
	}
	if r.legacyInstanceIndex == "" {
		searchBody["from"] = from
		searchBody["size"] = to - from
	}

	bodyBytes, err := json.Marshal(searchBody)
	if err != nil {
		return nil, err
	}

	results, err := r.searchCompatible(
		ctx,
		r.instanceIndex,
		r.legacyInstanceIndex,
		bodyBytes,
		func(json.RawMessage) (bool, error) { return true, nil },
	)
	if err != nil {
		return nil, err
	}
	if r.legacyInstanceIndex == "" {
		page, total, err := decodePageSearch[esProcessInstance](results.canonical)
		if err != nil {
			return nil, err
		}
		instances := make([]model.ProcessInstance, 0, len(page))
		for _, source := range page {
			instances = append(instances, mapProcessInstance(source))
		}
		return &repository.InstanceSearchResult{Instances: instances, Total: total}, nil
	}

	canonical, err := decodeCompleteSearch[esProcessInstance](results.canonical, r.instanceIndex)
	if err != nil {
		return nil, err
	}
	legacy, err := decodeCompleteSearch[esProcessInstance](results.legacy, r.legacyInstanceIndex)
	if err != nil {
		return nil, err
	}

	byKey := make(map[int64]model.ProcessInstance, len(canonical)+len(legacy))
	for _, source := range canonical {
		byKey[source.Key] = mapProcessInstance(source)
	}
	for _, source := range legacy {
		if _, canonicalWins := byKey[source.Key]; !canonicalWins {
			byKey[source.Key] = mapProcessInstance(source)
		}
	}

	instances := make([]model.ProcessInstance, 0, len(byKey))
	for _, instance := range byKey {
		instances = append(instances, instance)
	}
	sort.Slice(instances, func(i, j int) bool {
		if instances[i].CreatedAt.Equal(instances[j].CreatedAt) {
			return instances[i].Key > instances[j].Key
		}
		return instances[i].CreatedAt.After(instances[j].CreatedAt)
	})

	return &repository.InstanceSearchResult{
		Instances: pageSlice(instances, from, to),
		Total:     int64(len(instances)),
	}, nil
}

func (r *ESRepository) SearchWorkflows(ctx context.Context, filter repository.WorkflowFilter) (*repository.WorkflowSearchResult, error) {
	from, to, err := transitionPageBounds(filter.Page, filter.PageSize)
	if err != nil {
		return nil, err
	}

	searchBody := map[string]any{
		"query":            map[string]any{"bool": map[string]any{"filter": []any{tenantFilter(ctx)}}},
		"from":             0,
		"size":             transitionMergeMaxDocuments,
		"track_total_hits": true,
		"sort": []map[string]any{
			{"created_at": "desc"},
			{"key": "desc"},
		},
	}
	if r.legacyProcessIndex == "" {
		searchBody["from"] = from
		searchBody["size"] = to - from
	}

	bodyBytes, err := json.Marshal(searchBody)
	if err != nil {
		return nil, err
	}

	results, err := r.searchCompatible(
		ctx,
		r.processIndex,
		r.legacyProcessIndex,
		bodyBytes,
		func(json.RawMessage) (bool, error) { return true, nil },
	)
	if err != nil {
		return nil, err
	}
	if r.legacyProcessIndex == "" {
		page, total, err := decodePageSearch[esProcess](results.canonical)
		if err != nil {
			return nil, err
		}
		workflows := make([]model.Process, 0, len(page))
		for _, source := range page {
			workflows = append(workflows, mapProcess(source))
		}
		return &repository.WorkflowSearchResult{Workflows: workflows, Total: total}, nil
	}

	canonical, err := decodeCompleteSearch[esProcess](results.canonical, r.processIndex)
	if err != nil {
		return nil, err
	}
	legacy, err := decodeCompleteSearch[esProcess](results.legacy, r.legacyProcessIndex)
	if err != nil {
		return nil, err
	}

	byKey := make(map[int64]model.Process, len(canonical)+len(legacy))
	for _, source := range canonical {
		byKey[source.Key] = mapProcess(source)
	}
	for _, source := range legacy {
		if _, canonicalWins := byKey[source.Key]; !canonicalWins {
			byKey[source.Key] = mapProcess(source)
		}
	}

	workflows := make([]model.Process, 0, len(byKey))
	for _, workflow := range byKey {
		workflows = append(workflows, workflow)
	}
	sort.Slice(workflows, func(i, j int) bool {
		if workflows[i].CreatedAt.Equal(workflows[j].CreatedAt) {
			return workflows[i].Key > workflows[j].Key
		}
		return workflows[i].CreatedAt.After(workflows[j].CreatedAt)
	})

	return &repository.WorkflowSearchResult{
		Workflows: pageSlice(workflows, from, to),
		Total:     int64(len(workflows)),
	}, nil
}

type compatibleSearchResults struct {
	canonical json.RawMessage
	legacy    json.RawMessage
}

func (r *ESRepository) searchCompatible(
	ctx context.Context,
	canonicalIndex string,
	legacyIndex string,
	body json.RawMessage,
	shouldSearchLegacy func(json.RawMessage) (bool, error),
) (compatibleSearchResults, error) {
	results := compatibleSearchResults{}
	canonical, err := r.client.Search(ctx, canonicalIndex, body)
	if err != nil {
		if !isIndexNotFoundError(err) {
			return results, err
		}
	} else {
		results.canonical = canonical
	}

	if legacyIndex == "" {
		return results, nil
	}

	searchLegacy := len(results.canonical) == 0
	if !searchLegacy {
		searchLegacy, err = shouldSearchLegacy(results.canonical)
		if err != nil {
			return compatibleSearchResults{}, err
		}
	}
	if !searchLegacy {
		return results, nil
	}

	legacy, err := r.client.Search(ctx, legacyIndex, body)
	if err != nil {
		if isIndexNotFoundError(err) {
			return results, nil
		}
		return compatibleSearchResults{}, err
	}
	results.legacy = legacy
	return results, nil
}

type esSearchResponse[T any] struct {
	Hits struct {
		Total struct {
			Value    int64  `json:"value"`
			Relation string `json:"relation"`
		} `json:"total"`
		Hits []struct {
			Source T `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

func searchResultHasHits(result json.RawMessage) (bool, error) {
	response := esSearchResponse[json.RawMessage]{}
	if err := json.Unmarshal(result, &response); err != nil {
		return false, fmt.Errorf("failed to unmarshal ES response: %w", err)
	}
	return len(response.Hits.Hits) > 0, nil
}

func decodePageSearch[T any](result json.RawMessage) ([]T, int64, error) {
	if len(result) == 0 {
		return []T{}, 0, nil
	}
	response := esSearchResponse[T]{}
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, 0, fmt.Errorf("failed to unmarshal ES response: %w", err)
	}
	sources := make([]T, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		sources = append(sources, hit.Source)
	}
	return sources, response.Hits.Total.Value, nil
}

func decodeCompleteSearch[T any](result json.RawMessage, index string) ([]T, error) {
	if len(result) == 0 {
		return nil, nil
	}

	response := esSearchResponse[T]{}
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ES response from %q: %w", index, err)
	}
	if relation := strings.ToLower(strings.TrimSpace(response.Hits.Total.Relation)); relation != "" && relation != "eq" {
		return nil, fmt.Errorf(
			`transition compatibility search for %q requires an exact total, got relation %q`,
			index,
			response.Hits.Total.Relation,
		)
	}
	if response.Hits.Total.Value > transitionMergeMaxDocuments {
		return nil, fmt.Errorf(
			"transition compatibility search for %q has %d documents; maximum is %d per index",
			index,
			response.Hits.Total.Value,
			transitionMergeMaxDocuments,
		)
	}
	if response.Hits.Total.Value != int64(len(response.Hits.Hits)) {
		return nil, fmt.Errorf(
			"transition compatibility search for %q returned %d of %d documents",
			index,
			len(response.Hits.Hits),
			response.Hits.Total.Value,
		)
	}

	sources := make([]T, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		sources = append(sources, hit.Source)
	}
	return sources, nil
}

func transitionPageBounds(page, pageSize int) (int, int, error) {
	if page < 1 || pageSize < 1 {
		return 0, 0, fmt.Errorf("page and page size must be positive")
	}
	from := int64(page-1) * int64(pageSize)
	to := from + int64(pageSize)
	if from < 0 || to < from || to > transitionMergeMaxDocuments {
		return 0, 0, fmt.Errorf(
			"transition compatibility pagination supports only the first %d merged documents",
			transitionMergeMaxDocuments,
		)
	}
	return int(from), int(to), nil
}

func pageSlice[T any](values []T, from, to int) []T {
	if from >= len(values) {
		return make([]T, 0)
	}
	if to > len(values) {
		to = len(values)
	}
	return values[from:to]
}

func mapProcessInstance(source esProcessInstance) model.ProcessInstance {
	return model.ProcessInstance{
		Key:                      source.Key,
		ProcessDefinitionKey:     source.ProcessDefinitionKey,
		Version:                  source.Version,
		ParentProcessInstanceKey: source.ParentProcessInstanceKey,
		ParentElementInstanceKey: source.ParentElementInstanceKey,
		State:                    model.ProcessInstanceState(source.State),
		CreatedAt:                source.CreatedAt,
		EndTime:                  source.EndTime,
		Context:                  source.Context,
	}
}

func mapProcess(source esProcess) model.Process {
	return model.Process{
		Key:              source.Key,
		BpmnProcessID:    source.BpmnProcessID,
		Version:          source.Version,
		ResourceName:     source.ResourceName,
		DeploymentKey:    source.DeploymentKey,
		Resource:         source.Resource,
		ResourceChecksum: source.ResourceChecksum,
		TenantID:         source.TenantID,
		CreatedAt:        source.CreatedAt,
	}
}

func isIndexNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "status=404") ||
		strings.Contains(message, "index_not_found_exception") ||
		strings.Contains(message, "no such index")
}

// esProcessInstance matches the Elasticsearch document structure (snake_case)
type esProcessInstance struct {
	Key                      int64          `json:"key"`
	ProcessDefinitionKey     int64          `json:"process_definition_key"`
	Version                  int            `json:"version"`
	ParentProcessInstanceKey int64          `json:"parent_process_instance_key"`
	ParentElementInstanceKey int64          `json:"parent_element_instance_key"`
	State                    string         `json:"state"`
	TenantID                 string         `json:"tenant_id"`
	CreatedAt                time.Time      `json:"created_at"`
	EndTime                  time.Time      `json:"end_time"`
	Context                  map[string]any `json:"context"`
}

// esProcess matches the Elasticsearch document structure for processes (snake_case)
type esProcess struct {
	Key              int64     `json:"key"`
	BpmnProcessID    string    `json:"bpmn_process_id"`
	Version          int       `json:"version"`
	ResourceName     string    `json:"resource_name"`
	DeploymentKey    int64     `json:"deployment_key"`
	Resource         []byte    `json:"resource"`
	ResourceChecksum string    `json:"resource_checksum"`
	TenantID         string    `json:"tenant_id"`
	CreatedAt        time.Time `json:"created_at"`
}
