package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/logger"
	"github.com/artificialflow/artificialflow/backend/services/workflow-query/internal/application"
	"github.com/artificialflow/artificialflow/backend/services/workflow-query/internal/domain/repository"

	"github.com/gorilla/mux"
)

type Handler struct {
	service *application.QueryService
	log     *logger.Logger
}

func NewHandler(service *application.QueryService) *Handler {
	return &Handler{
		service: service,
		log:     logger.New("query-handler"),
	}
}

func (h *Handler) RegisterRoutes(r *mux.Router) {
	instanceRead := auth.RequireAnyRole(auth.RoleAdmin, auth.RoleClient)
	workflowRead := auth.RequireAnyRole(auth.RoleAdmin, auth.RoleBusiness, auth.RoleClient)

	r.Handle("/instances", instanceRead(http.HandlerFunc(h.searchInstances))).Methods("GET")
	r.Handle("/instances/{id}", instanceRead(http.HandlerFunc(h.getInstance))).Methods("GET")
	r.Handle("/workflows", workflowRead(http.HandlerFunc(h.searchWorkflows))).Methods("GET")
}

func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	if !canReadProjectedInstances(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	vars := mux.Vars(r)
	id := vars["id"]
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		h.log.Warn(ctx, "invalid instance id", map[string]any{"instance_id": id})
		http.Error(w, "invalid instance id", http.StatusBadRequest)
		return
	}

	h.log.Debug(ctx, "getting instance", map[string]any{"instance_id": id})

	instance, err := h.service.GetInstance(ctx, id)
	if err != nil {
		h.log.Warn(ctx, "instance not found", map[string]any{"instance_id": id, "error": err.Error()})
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	response := mapInstanceResponse(*instance)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.Error(ctx, "failed to encode response", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) searchInstances(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	if !canReadProjectedInstances(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	start := time.Now()

	// Parse Query Params
	workflowID := strings.TrimSpace(r.URL.Query().Get("workflowId"))
	if workflowID != "" {
		if _, err := strconv.ParseInt(workflowID, 10, 64); err != nil {
			h.log.Warn(ctx, "invalid workflow id filter", map[string]any{"workflow_id": workflowID})
			http.Error(w, "invalid workflowId: must be numeric", http.StatusBadRequest)
			return
		}
	}
	state := normalizeInstanceStateFilter(r.URL.Query().Get("state"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if pageSize < 1 {
		pageSize = 20
	}

	h.log.Debug(ctx, "searching instances", map[string]any{
		"workflow_id": workflowID,
		"state":       state,
		"page":        page,
		"page_size":   pageSize,
	})

	filter := repository.InstanceFilter{
		WorkflowID: workflowID,
		State:      state,
		Page:       page,
		PageSize:   pageSize,
	}

	result, err := h.service.SearchInstances(ctx, filter)
	if err != nil {
		h.log.Error(ctx, "failed to search instances", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.log.Debug(ctx, "instances search completed", map[string]any{
		"total":       result.Total,
		"count":       len(result.Instances),
		"duration_ms": time.Since(start).Milliseconds(),
	})

	response := InstanceSearchResponse{
		Instances: make([]InstanceResponse, 0, len(result.Instances)),
		Total:     result.Total,
	}
	for _, instance := range result.Instances {
		response.Instances = append(response.Instances, mapInstanceResponse(instance))
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.Error(ctx, "failed to encode response", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func canReadProjectedInstances(r *http.Request) bool {
	principal, ok := auth.PrincipalFromContext(r.Context())
	return ok && principal.HasAnyRole(auth.RoleAdmin, auth.RoleClient)
}

func normalizeInstanceStateFilter(state string) string {
	normalized := strings.ToUpper(strings.TrimSpace(state))
	switch normalized {
	case "":
		return ""
	case "RUNNING":
		return "ACTIVE"
	default:
		return normalized
	}
}

func (h *Handler) searchWorkflows(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	start := time.Now()

	// Parse Query Params
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if pageSize < 1 {
		pageSize = 20
	}

	h.log.Debug(ctx, "searching workflows", map[string]any{
		"page":      page,
		"page_size": pageSize,
	})

	filter := repository.WorkflowFilter{
		Page:     page,
		PageSize: pageSize,
	}

	result, err := h.service.SearchWorkflows(ctx, filter)
	if err != nil {
		h.log.Error(ctx, "failed to search workflows", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.log.Debug(ctx, "workflows search completed", map[string]any{
		"total":       result.Total,
		"count":       len(result.Workflows),
		"duration_ms": time.Since(start).Milliseconds(),
	})

	response := WorkflowSearchResponse{
		Workflows: make([]WorkflowResponse, 0, len(result.Workflows)),
		Total:     result.Total,
	}
	for _, workflow := range result.Workflows {
		response.Workflows = append(response.Workflows, mapWorkflowResponse(workflow))
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.Error(ctx, "failed to encode response", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
