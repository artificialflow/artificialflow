package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/iam"
	"github.com/artificialflow/artificialflow/backend/libs/logger"
	sharedmetrics "github.com/artificialflow/artificialflow/backend/libs/metrics"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	workerproto "github.com/artificialflow/artificialflow/backend/libs/worker"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/application"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/domain/repository"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/interfaces/http/dto"

	"github.com/gorilla/mux"
)

type Handler struct {
	engine         *application.Engine
	identityConfig iam.DeploymentConfig
	log            *logger.Logger
}

const idempotencyKeyHeader = "Idempotency-Key"

const maxIdempotencyKeyLength = 128
const maxBPMNDeployBodyBytes int64 = 20 << 20

func NewHandler(e *application.Engine, identityConfig iam.DeploymentConfig) *Handler {
	return &Handler{
		engine:         e,
		identityConfig: identityConfig,
		log:            logger.New("command-handler"),
	}
}

func (h *Handler) dryRunInstanceMigration(w http.ResponseWriter, r *http.Request) {
	h.handleInstanceMigration(w, r, true)
}

func (h *Handler) applyInstanceMigration(w http.ResponseWriter, r *http.Request) {
	h.handleInstanceMigration(w, r, false)
}

func (h *Handler) handleInstanceMigration(w http.ResponseWriter, r *http.Request, dryRun bool) {
	instanceKey, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil || instanceKey <= 0 {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	var body dto.InstanceMigrationRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "invalid migration request", http.StatusBadRequest)
		return
	}
	sourceKey, sourceErr := strconv.ParseInt(body.SourceDefinitionKey, 10, 64)
	targetKey, targetErr := strconv.ParseInt(body.TargetDefinitionKey, 10, 64)
	if sourceErr != nil || targetErr != nil {
		http.Error(w, "invalid migration request", http.StatusBadRequest)
		return
	}
	principal, _ := auth.PrincipalFromContext(r.Context())
	result, err := h.engine.MigrateProcessInstance(r.Context(), model.InstanceMigrationRequest{
		InstanceKey: instanceKey, SourceDefinitionKey: sourceKey, TargetDefinitionKey: targetKey,
		ElementMapping: body.ElementMapping, DryRun: dryRun, OperationID: body.OperationID, Actor: principal.Subject,
	})
	if err != nil {
		sharedmetrics.RecordInstanceMigration(dryRun, "failed")
		h.log.Error(r.Context(), "instance migration failed", map[string]any{"error": err.Error()})
		http.Error(w, "instance migration failed", http.StatusInternalServerError)
		return
	}
	if result.ReasonCode == "INSTANCE_NOT_FOUND" {
		sharedmetrics.RecordInstanceMigration(dryRun, "not-found")
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	status := http.StatusOK
	if result.Status == "REJECTED" {
		status = http.StatusUnprocessableEntity
	}
	outcome := strings.ToLower(string(result.Status))
	sharedmetrics.RecordInstanceMigration(dryRun, outcome)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

// @Summary Deploy a workflow
// @Description Deploy a new workflow definition using BPMN 2.0 XML
// @Tags workflows
// @Accept xml
// @Produce json
// @Param file body string true "BPMN XML File"
// @Success 200 {object} dto.WorkflowDefinitionResponse
// @Failure 400 {string} string "Bad Request"
// @Failure 413 {string} string "Request Entity Too Large"
// @Failure 500 {string} string "Internal Server Error"
// @Router /workflows [post]
func (h *Handler) deployWorkflow(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	start := time.Now()

	h.log.Info(ctx, "deploying workflow", nil)

	r.Body = http.MaxBytesReader(w, r.Body, maxBPMNDeployBodyBytes)
	body, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(readErr, &maxBytesErr) {
			h.log.Error(ctx, "BPMN payload exceeds maximum size", map[string]any{
				"error":     readErr.Error(),
				"max_bytes": maxBPMNDeployBodyBytes,
			})
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		h.log.Error(ctx, "failed to read request body", map[string]any{"error": readErr.Error()})
		http.Error(w, readErr.Error(), http.StatusBadRequest)
		return
	}

	h.log.Debug(ctx, "received BPMN payload", map[string]any{"size": len(body)})

	wf, err := h.engine.DeployWorkflowFromBPMN(ctx, body)
	if err != nil {
		h.log.Error(ctx, "workflow deployment failed", map[string]any{"error": err.Error()})
		if errors.Is(err, application.ErrBPMNValidation) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.log.Info(ctx, "workflow deployed", map[string]any{
		"workflow_id":   wf.ID,
		"workflow_name": wf.Name,
		"version":       wf.Version,
		"duration_ms":   time.Since(start).Milliseconds(),
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dto.ToWorkflowResponse(wf))
}

// @Summary List workflows
// @Description Get a list of all deployed workflow definitions
// @Tags workflows
// @Produce json
// @Success 200 {array} dto.WorkflowDefinitionResponse
// @Failure 500 {string} string "Internal Server Error"
// @Router /workflows [get]
func (h *Handler) listWorkflows(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)

	wfs, err := h.engine.ListWorkflows(ctx)
	if err != nil {
		h.log.Error(ctx, "failed to list workflows", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.log.Debug(ctx, "listed workflows", map[string]any{"count": len(wfs)})

	responseWfs := make([]dto.WorkflowDefinitionResponse, len(wfs))
	for i, wf := range wfs {
		responseWfs[i] = dto.ToWorkflowResponse(wf)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responseWfs)
}

// @Summary Get workflow definition
// @Description Get a specific workflow definition by ID or BPMN Process ID
// @Tags workflows
// @Produce json
// @Param id path string true "Workflow ID"
// @Success 200 {object} dto.WorkflowDefinitionResponse
// @Failure 404 {string} string "Not Found"
// @Router /workflows/{id} [get]
func (h *Handler) getWorkflow(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	vars := mux.Vars(r)
	id := vars["id"]

	h.log.Debug(ctx, "getting workflow", map[string]any{"workflow_id": id})

	wf, err := h.engine.GetWorkflow(ctx, id)
	if err != nil {
		h.log.Warn(ctx, "workflow not found", map[string]any{"workflow_id": id, "error": err.Error()})
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dto.ToWorkflowResponse(wf))
}

// @Summary Delete workflow
// @Description Delete a workflow definition
// @Tags workflows
// @Param id path string true "Workflow ID"
// @Success 204 {string} string "No Content"
// @Failure 404 {string} string "Not Found"
// @Failure 500 {string} string "Internal Server Error"
// @Router /workflows/{id} [delete]
func (h *Handler) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	vars := mux.Vars(r)
	id := vars["id"]

	h.log.Info(ctx, "deleting workflow", map[string]any{"workflow_id": id})

	if err := h.engine.DeleteWorkflow(ctx, id); err != nil {
		h.log.Error(ctx, "failed to delete workflow", map[string]any{"workflow_id": id, "error": err.Error()})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.log.Info(ctx, "workflow deleted", map[string]any{"workflow_id": id})
	w.WriteHeader(http.StatusNoContent)
}

// @Summary Start a workflow instance
// @Description Start a new instance of a workflow
// @Tags instances
// @Accept json
// @Produce json
// @Param request body dto.StartInstanceRequest true "Start Instance Request"
// @Success 200 {object} dto.WorkflowInstanceResponse
// @Failure 400 {string} string "Bad Request"
// @Failure 500 {string} string "Internal Server Error"
// @Router /instances [post]
func (h *Handler) startInstance(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	start := time.Now()

	var req dto.StartInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.Error(ctx, "failed to decode start instance request", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.log.Debug(ctx, "starting workflow instance", map[string]any{
		"workflow_id":  req.WorkflowID,
		"context_keys": len(req.Context),
	})

	instance, err := h.engine.StartInstanceVersion(ctx, req.WorkflowID, req.Context, req.Version)
	if err != nil {
		h.log.Error(ctx, "failed to start instance", map[string]any{
			"workflow_id": req.WorkflowID,
			"error":       err.Error(),
		})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.log.Debug(ctx, "workflow instance started", map[string]any{
		"instance_id": instance.ID,
		"workflow_id": req.WorkflowID,
		"status":      instance.Status,
		"duration_ms": time.Since(start).Milliseconds(),
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dto.ToWorkflowInstanceResponse(instance))
}

func (h *Handler) listUserTasks(w http.ResponseWriter, r *http.Request) {
	instanceID := mux.Vars(r)["id"]
	includeCompleted, _ := strconv.ParseBool(r.URL.Query().Get("includeCompleted"))
	tasks, err := h.userTaskResponses(r.Context(), instanceID, includeCompleted)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dto.ListUserTasksResponse{Tasks: tasks})
}

func (h *Handler) listDecisions(w http.ResponseWriter, r *http.Request) {
	decisions, err := h.engine.ListDecisions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type decisionSummary struct {
		Key        string `json:"key"`
		DecisionID string `json:"decision_id"`
		Name       string `json:"name"`
		Version    int    `json:"version"`
		CreatedAt  string `json:"created_at"`
		UpdatedAt  string `json:"updated_at"`
	}
	out := make([]decisionSummary, 0, len(decisions))
	for _, d := range decisions {
		out = append(out, decisionSummary{
			Key:        strconv.FormatInt(d.Key, 10),
			DecisionID: d.DecisionID,
			Name:       d.Name,
			Version:    d.Version,
			CreatedAt:  d.CreatedAt.Format(time.RFC3339),
			UpdatedAt:  d.UpdatedAt.Format(time.RFC3339),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"decisions": out})
}

func (h *Handler) deployDecision(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DecisionID string `json:"decision_id"`
		Name       string `json:"name"`
		Resource   string `json:"resource"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	def, err := h.engine.DeployDecision(r.Context(), req.DecisionID, req.Name, []byte(req.Resource))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(def)
}

func (h *Handler) evaluateDecision(w http.ResponseWriter, r *http.Request) {
	decisionID := mux.Vars(r)["id"]
	var req struct {
		Inputs map[string]any `json:"inputs"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	out, err := h.engine.EvaluateDecision(r.Context(), decisionID, req.Inputs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"outputs": out})
}

func (h *Handler) listInstanceJobs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	instanceID := vars["id"]
	jobs, err := h.engine.ListInstanceJobs(r.Context(), instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jobs": jobs})
}

// @Summary List incidents
// @Description List engine incidents (including SLA due-date breaches)
// @Tags incidents
// @Produce json
// @Param processInstanceKey query string false "Filter by process instance key"
// @Param limit query int false "Max rows (default 100)"
// @Success 200 {object} map[string]any
// @Router /incidents [get]
func (h *Handler) listIncidents(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	var processInstanceKey int64
	if raw := strings.TrimSpace(r.URL.Query().Get("processInstanceKey")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			http.Error(w, "invalid processInstanceKey", http.StatusBadRequest)
			return
		}
		processInstanceKey = parsed
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	incidents, err := h.engine.ListIncidents(ctx, processInstanceKey, limit)
	if err != nil {
		h.log.Error(ctx, "failed to list incidents", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp := make([]dto.IncidentResponse, 0, len(incidents))
	for _, inc := range incidents {
		resp = append(resp, incidentResponse(inc))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"incidents": resp})
}

func (h *Handler) resolveIncident(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseInt(mux.Vars(r)["key"], 10, 64)
	if err != nil {
		http.Error(w, "invalid incident key", http.StatusBadRequest)
		return
	}
	incident, err := h.engine.ResolveIncident(r.Context(), key)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(incidentResponse(*incident))
}

func incidentResponse(inc model.Incident) dto.IncidentResponse {
	item := dto.IncidentResponse{
		Key:                strconv.FormatInt(inc.Key, 10),
		ID:                 inc.ID,
		ProcessInstanceKey: strconv.FormatInt(inc.ProcessInstanceKey, 10),
		ElementInstanceKey: strconv.FormatInt(inc.ElementInstanceKey, 10),
		ErrorType:          string(inc.ErrorType),
		ErrorMessage:       inc.ErrorMessage,
		State:              string(inc.State),
		CreatedAt:          inc.CreatedAt,
	}
	if inc.JobKey != 0 {
		item.JobKey = strconv.FormatInt(inc.JobKey, 10)
	}
	if !inc.ResolvedAt.IsZero() {
		resolved := inc.ResolvedAt
		item.ResolvedAt = &resolved
	}
	return item
}

func (h *Handler) retryJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	jobKey, err := strconv.ParseInt(vars["key"], 10, 64)
	if err != nil {
		http.Error(w, "invalid job key", http.StatusBadRequest)
		return
	}
	retries := 3
	var body struct {
		Retries *int `json:"retries"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Retries != nil && *body.Retries > 0 {
		retries = *body.Retries
	}
	if err := h.engine.RetryJob(r.Context(), jobKey, retries); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Job retried"))
}

// @Summary Search user tasks
// @Description Find user tasks for a client application's own end user. The caller authenticates its users; assignee, candidateUser and candidateGroup are OR-combined.
// @Tags tasks
// @Produce json
// @Param assignee query string false "Tasks assigned to or claimed by this user"
// @Param candidateUser query string false "Tasks this user may act on (assignee, claim owner or candidate user)"
// @Param candidateGroup query []string false "Tasks offered to any of these groups (repeat or comma-separate)"
// @Param includeUnassigned query bool false "Also return tasks without assignee, candidates or claim"
// @Param processInstanceKey query string false "Restrict to one process instance"
// @Param state query string false "active (default), completed or all"
// @Param limit query int false "Max rows (default 100, max 500)"
// @Success 200 {object} dto.ListUserTasksResponse
// @Failure 400 {string} string "Bad Request"
// @Router /tasks [get]
func (h *Handler) searchUserTasks(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := repository.UserTaskFilter{
		Assignee:        strings.TrimSpace(query.Get("assignee")),
		CandidateUser:   strings.TrimSpace(query.Get("candidateUser")),
		CandidateGroups: splitQueryList(query["candidateGroup"]),
		Limit:           100,
	}
	switch state := strings.ToLower(strings.TrimSpace(query.Get("state"))); state {
	case "", repository.UserTaskStateActive:
		filter.State = repository.UserTaskStateActive
	case repository.UserTaskStateCompleted, repository.UserTaskStateAll:
		filter.State = state
	default:
		http.Error(w, "invalid state: use active, completed or all", http.StatusBadRequest)
		return
	}
	if raw := strings.TrimSpace(query.Get("processInstanceKey")); raw != "" {
		key, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || key <= 0 {
			http.Error(w, "invalid processInstanceKey", http.StatusBadRequest)
			return
		}
		filter.ProcessInstanceKey = key
	}
	if raw := strings.TrimSpace(query.Get("includeUnassigned")); raw != "" {
		include, err := strconv.ParseBool(raw)
		if err != nil {
			http.Error(w, "invalid includeUnassigned", http.StatusBadRequest)
			return
		}
		filter.IncludeUnassigned = include
	}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 500 {
			http.Error(w, "invalid limit: must be between 1 and 500", http.StatusBadRequest)
			return
		}
		filter.Limit = limit
	}

	jobs, err := h.engine.SearchUserTasks(r.Context(), filter)
	if err != nil {
		h.log.Error(r.Context(), "failed to search user tasks", map[string]any{"error": err.Error()})
		http.Error(w, "failed to search user tasks", http.StatusInternalServerError)
		return
	}
	tasks := make([]dto.UserTaskResponse, 0, len(jobs))
	for _, job := range jobs {
		tasks = append(tasks, dto.ToUserTaskResponse(job))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dto.ListUserTasksResponse{Tasks: tasks})
}

func (h *Handler) claimUserTask(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	assignee, err := userTaskAssignee(r, principal)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	vars := mux.Vars(r)
	claimed, err := h.engine.ClaimUserTask(r.Context(), vars["id"], vars["executionId"], assignee, principal.HasRole(auth.RoleAdmin))
	if err != nil {
		http.Error(w, err.Error(), userTaskErrorStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dto.ToUserTaskResponse(*claimed))
}

func (h *Handler) completeUserTask(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	assignee, err := userTaskAssignee(r, principal)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	vars := mux.Vars(r)
	if err := h.engine.CompleteUserTask(r.Context(), vars["id"], vars["executionId"], assignee, principal.HasRole(auth.RoleAdmin)); err != nil {
		http.Error(w, err.Error(), userTaskErrorStatus(err))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Task completed"))
}

type userTaskActionRequest struct {
	Assignee string `json:"assignee"`
}

// userTaskAssignee resolves the end user acting on a task. Client
// applications authenticate their own users and must name the assignee;
// admins default to themselves.
func userTaskAssignee(r *http.Request, principal auth.Principal) (string, error) {
	var body userTaskActionRequest
	if r.Body != nil {
		decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("invalid request body")
		}
	}
	assignee := strings.TrimSpace(body.Assignee)
	if assignee == "" && principal.HasRole(auth.RoleAdmin) {
		assignee = taskOwner(principal)
	}
	if assignee == "" {
		return "", fmt.Errorf("assignee is required")
	}
	return assignee, nil
}

func userTaskErrorStatus(err error) int {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "invalid"):
		return http.StatusBadRequest
	case strings.Contains(message, "no user task"), strings.Contains(message, "not found"):
		return http.StatusNotFound
	default:
		return http.StatusConflict
	}
}

func (h *Handler) userTaskResponses(ctx context.Context, instanceID string, includeCompleted bool) ([]dto.UserTaskResponse, error) {
	jobs, err := h.engine.ListUserTaskJobs(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	responses := make([]dto.UserTaskResponse, 0, len(jobs))
	for _, job := range jobs {
		if job.State == model.JobStateCompleted && !includeCompleted {
			continue
		}
		responses = append(responses, dto.ToUserTaskResponse(job))
	}
	return responses, nil
}

func taskOwner(principal auth.Principal) string {
	for _, candidate := range []string{principal.Email, principal.Subject, principal.Name} {
		trimmed := strings.TrimSpace(candidate)
		if trimmed != "" {
			return trimmed
		}
	}
	return "unknown-user"
}

func splitQueryList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}

// @Summary List active workflow instances
// @Description Get a list of all currently active workflow instances
// @Tags instances
// @Produce json
// @Success 200 {array} dto.WorkflowInstanceResponse
// @Failure 500 {string} string "Internal Server Error"
// @Router /instances [get]
func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	instances, err := h.engine.ListActiveInstances(ctx)
	if err != nil {
		h.log.Error(ctx, "failed to list instances", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.log.Debug(ctx, "listed instances", map[string]any{"count": len(instances)})

	responseInstances := make([]dto.WorkflowInstanceResponse, len(instances))
	for i, instance := range instances {
		responseInstances[i] = dto.ToWorkflowInstanceResponse(instance)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responseInstances)
}

func (h *Handler) listCompletedInstanceHistory(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	limit := 100
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		if parsed < limit {
			limit = parsed
		}
	}

	instances, err := h.engine.ListCompletedInstances(ctx, limit)
	if err != nil {
		h.log.Error(ctx, "failed to list completed instance history", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	responseInstances := make([]dto.WorkflowInstanceResponse, len(instances))
	for i, instance := range instances {
		responseInstances[i] = dto.ToWorkflowInstanceResponse(instance)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responseInstances)
}

// @Summary Update instance variables
// @Description Update variables (parameters) for a running workflow instance
// @Tags instances
// @Accept json
// @Produce json
// @Param id path string true "Instance ID"
// @Param request body dto.UpdateVariablesRequest true "Variables to update"
// @Success 200 {string} string "Variables updated"
// @Failure 400 {string} string "Bad Request"
// @Failure 500 {string} string "Internal Server Error"
// @Router /instances/{id}/variables [post]
func (h *Handler) updateVariables(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	vars := mux.Vars(r)
	id := vars["id"]

	var req dto.UpdateVariablesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.Error(ctx, "failed to decode update variables request", map[string]any{
			"instance_id": id,
			"error":       err.Error(),
		})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Extract variable names for logging (not values for security)
	varNames := make([]string, 0, len(req.Variables))
	for k := range req.Variables {
		varNames = append(varNames, k)
	}

	h.log.Info(ctx, "updating instance variables", map[string]any{
		"instance_id":    id,
		"variable_names": varNames,
	})

	if err := h.engine.UpdateInstanceVariables(ctx, id, req.Variables); err != nil {
		h.log.Error(ctx, "failed to update variables", map[string]any{
			"instance_id": id,
			"error":       err.Error(),
		})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.log.Info(ctx, "variables updated", map[string]any{
		"instance_id":    id,
		"variable_count": len(req.Variables),
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Variables updated"))
}

// @Summary Complete a task
// @Description Complete the current task for a workflow instance. Optionally specify step_id for parallel tasks.
// @Tags instances
// @Param id path string true "Instance ID"
// @Param request body dto.CompleteTaskRequest false "Complete Task Request"
// @Success 200 {string} string "Task completed"
// @Failure 404 {string} string "Not Found"
// @Failure 500 {string} string "Internal Server Error"
// @Router /instances/{id}/complete [post]
func (h *Handler) completeTask(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	start := time.Now()
	vars := mux.Vars(r)
	id := vars["id"]

	var req dto.CompleteTaskRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	h.log.Info(ctx, "completing task", map[string]any{
		"instance_id": id,
		"step_id":     req.StepID,
	})

	if err := h.engine.CompleteTask(ctx, id, req.StepID); err != nil {
		h.log.Error(ctx, "failed to complete task", map[string]any{
			"instance_id": id,
			"step_id":     req.StepID,
			"error":       err.Error(),
		})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.log.Info(ctx, "task completed", map[string]any{
		"instance_id": id,
		"step_id":     req.StepID,
		"duration_ms": time.Since(start).Milliseconds(),
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Task completed"))
}

// @Summary Activate jobs
// @Description Activate available jobs for an external worker. Supports long-poll timeout and lock duration.
// @Tags jobs
// @Accept json
// @Produce json
// @Param request body dto.ActivateJobsRequest false "Activate Jobs Request"
// @Success 200 {object} dto.ActivateJobsResponse
// @Failure 400 {string} string "Bad Request"
// @Failure 500 {string} string "Internal Server Error"
// @Router /jobs/activate [post]
func (h *Handler) activateJobs(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	if err := validateWorkerProtocolVersion(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	setWorkerProtocolHeaders(w)

	var req dto.ActivateJobsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		h.log.Error(ctx, "failed to decode activate jobs request", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	requestTimeout := time.Duration(req.TimeoutMs) * time.Millisecond
	lockDuration := time.Duration(req.LockDurationMs) * time.Millisecond

	jobs, err := h.engine.ActivateJobs(ctx, req.Type, req.Worker, req.MaxJobs, requestTimeout, lockDuration)
	if err != nil {
		h.log.Error(ctx, "failed to activate jobs", map[string]any{
			"job_type": req.Type,
			"worker":   req.Worker,
			"error":    err.Error(),
		})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	responseJobs := dto.ToJobResponses(jobs)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dto.ActivateJobsResponse{Jobs: responseJobs})
}

// @Summary Get worker API capabilities
// @Description Returns protocol version and supported worker capabilities.
// @Tags jobs
// @Produce json
// @Success 200 {object} dto.WorkerCapabilitiesResponse
// @Router /jobs/capabilities [get]
func (h *Handler) jobsCapabilities(w http.ResponseWriter, r *http.Request) {
	setWorkerProtocolHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dto.WorkerCapabilitiesResponse{
		ProtocolVersion: workerproto.WorkerProtocolVersion,
		Capabilities: []string{
			"activate",
			"complete",
			"fail",
			"extend-lock",
		},
	})
}

// @Summary Complete a job
// @Description Complete an activated external worker job and optionally persist result variables.
// @Tags jobs
// @Accept json
// @Produce plain
// @Param key path string true "Job Key"
// @Param request body dto.CompleteJobRequest false "Complete Job Request"
// @Success 200 {string} string "Job completed"
// @Failure 400 {string} string "Bad Request"
// @Failure 500 {string} string "Internal Server Error"
// @Router /jobs/{key}/complete [post]
func (h *Handler) completeJob(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	if err := validateWorkerProtocolVersion(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	setWorkerProtocolHeaders(w)

	vars := mux.Vars(r)
	jobKey, err := strconv.ParseInt(vars["key"], 10, 64)
	if err != nil {
		http.Error(w, "invalid job key", http.StatusBadRequest)
		return
	}
	idempotencyKey, err := readIdempotencyKey(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idempotencyOperation := fmt.Sprintf("jobs.complete:%d", jobKey)
	if idempotencyKey != "" {
		alreadyProcessed, err := h.engine.HasProcessedIdempotencyKey(ctx, idempotencyKey, idempotencyOperation)
		if err != nil {
			h.log.Error(ctx, "failed to check idempotency key", map[string]any{"error": err.Error(), "operation": idempotencyOperation})
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if alreadyProcessed {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Job completed"))
			return
		}
	}

	var req dto.CompleteJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		h.log.Error(ctx, "failed to decode complete job request", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.engine.CompleteJob(ctx, jobKey, req.Worker, req.Variables); err != nil {
		h.log.Error(ctx, "failed to complete job", map[string]any{"job_key": jobKey, "error": err.Error()})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if idempotencyKey != "" {
		if err := h.engine.RecordIdempotencyKey(ctx, idempotencyKey, idempotencyOperation); err != nil {
			h.log.Warn(ctx, "failed to store idempotency key", map[string]any{"error": err.Error(), "operation": idempotencyOperation})
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Job completed"))
}

// @Summary Fail a job
// @Description Report external worker job failure and optionally override remaining retries.
// @Tags jobs
// @Accept json
// @Produce plain
// @Param key path string true "Job Key"
// @Param request body dto.FailJobRequest false "Fail Job Request"
// @Success 200 {string} string "Job failed"
// @Failure 400 {string} string "Bad Request"
// @Failure 500 {string} string "Internal Server Error"
// @Router /jobs/{key}/fail [post]
func (h *Handler) failJob(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	if err := validateWorkerProtocolVersion(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	setWorkerProtocolHeaders(w)

	vars := mux.Vars(r)
	jobKey, err := strconv.ParseInt(vars["key"], 10, 64)
	if err != nil {
		http.Error(w, "invalid job key", http.StatusBadRequest)
		return
	}
	idempotencyKey, err := readIdempotencyKey(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idempotencyOperation := fmt.Sprintf("jobs.fail:%d", jobKey)
	if idempotencyKey != "" {
		alreadyProcessed, err := h.engine.HasProcessedIdempotencyKey(ctx, idempotencyKey, idempotencyOperation)
		if err != nil {
			h.log.Error(ctx, "failed to check idempotency key", map[string]any{"error": err.Error(), "operation": idempotencyOperation})
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if alreadyProcessed {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Job failed"))
			return
		}
	}

	var req dto.FailJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		h.log.Error(ctx, "failed to decode fail job request", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.engine.FailJob(ctx, jobKey, req.Worker, req.ErrorMessage, req.Retries); err != nil {
		h.log.Error(ctx, "failed to fail job", map[string]any{"job_key": jobKey, "error": err.Error()})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if idempotencyKey != "" {
		if err := h.engine.RecordIdempotencyKey(ctx, idempotencyKey, idempotencyOperation); err != nil {
			h.log.Warn(ctx, "failed to store idempotency key", map[string]any{"error": err.Error(), "operation": idempotencyOperation})
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Job failed"))
}

// @Summary Extend job lock
// @Description Extend the lock for an activated job owned by the requesting worker.
// @Tags jobs
// @Accept json
// @Produce plain
// @Param key path string true "Job Key"
// @Param request body dto.ExtendJobLockRequest false "Extend Job Lock Request"
// @Success 200 {string} string "Job lock extended"
// @Failure 400 {string} string "Bad Request"
// @Failure 500 {string} string "Internal Server Error"
// @Router /jobs/{key}/extend-lock [post]
func (h *Handler) extendJobLock(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	if err := validateWorkerProtocolVersion(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	setWorkerProtocolHeaders(w)

	vars := mux.Vars(r)
	jobKey, err := strconv.ParseInt(vars["key"], 10, 64)
	if err != nil {
		http.Error(w, "invalid job key", http.StatusBadRequest)
		return
	}
	idempotencyKey, err := readIdempotencyKey(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	idempotencyOperation := fmt.Sprintf("jobs.extend-lock:%d", jobKey)
	if idempotencyKey != "" {
		alreadyProcessed, err := h.engine.HasProcessedIdempotencyKey(ctx, idempotencyKey, idempotencyOperation)
		if err != nil {
			h.log.Error(ctx, "failed to check idempotency key", map[string]any{"error": err.Error(), "operation": idempotencyOperation})
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if alreadyProcessed {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Job lock extended"))
			return
		}
	}

	var req dto.ExtendJobLockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		h.log.Error(ctx, "failed to decode extend lock request", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	lockDuration := time.Duration(req.LockDurationMs) * time.Millisecond
	if err := h.engine.ExtendJobLock(ctx, jobKey, req.Worker, lockDuration); err != nil {
		h.log.Error(ctx, "failed to extend job lock", map[string]any{"job_key": jobKey, "error": err.Error()})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if idempotencyKey != "" {
		if err := h.engine.RecordIdempotencyKey(ctx, idempotencyKey, idempotencyOperation); err != nil {
			h.log.Warn(ctx, "failed to store idempotency key", map[string]any{"error": err.Error(), "operation": idempotencyOperation})
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Job lock extended"))
}

func setWorkerProtocolHeaders(w http.ResponseWriter) {
	w.Header().Set(workerproto.HeaderEngineProtocolVersion, workerproto.WorkerProtocolVersion)
}

func validateWorkerProtocolVersion(r *http.Request) error {
	version := strings.TrimSpace(r.Header.Get(workerproto.HeaderWorkerProtocolVersion))
	if version == "" || version == workerproto.WorkerProtocolVersion {
		return nil
	}
	return fmt.Errorf("unsupported worker protocol version %q (supported: %s)", version, workerproto.WorkerProtocolVersion)
}

func readIdempotencyKey(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get(idempotencyKeyHeader))
	if key == "" {
		return "", nil
	}
	if len(key) > maxIdempotencyKeyLength {
		return "", fmt.Errorf("%s exceeds %d characters", idempotencyKeyHeader, maxIdempotencyKeyLength)
	}
	return key, nil
}

// Simple helper to expose reading state for verification
// @Summary Get workflow instance
// @Description Get the details of a workflow instance
// @Tags instances
// @Param id path string true "Instance ID"
// @Success 200 {object} dto.WorkflowInstanceResponse
// @Failure 404 {string} string "Not Found"
// @Router /instances/{id} [get]
func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	id := mux.Vars(r)["id"]

	h.log.Debug(ctx, "getting instance", map[string]any{"instance_id": id})

	instance, err := h.engine.GetInstance(ctx, id)
	if err != nil {
		h.log.Warn(ctx, "instance not found", map[string]any{"instance_id": id, "error": err.Error()})
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	taskResponses, err := h.userTaskResponses(ctx, id, false)
	if err != nil {
		h.log.Warn(ctx, "failed to load user task metadata", map[string]any{"instance_id": id, "error": err.Error()})
		taskResponses = nil
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dto.ToWorkflowInstanceResponseWithTasks(instance, taskResponses))
}

func (h *Handler) engineMetrics(w http.ResponseWriter, r *http.Request) {
	snapshot := h.engine.MetricsSnapshot()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dto.EngineMetricsResponse{
		OutboxPending:        snapshot.OutboxPending,
		OutboxPublishSuccess: snapshot.OutboxPublishSuccess,
		OutboxPublishFailure: snapshot.OutboxPublishFailure,
		OutboxPublishLagSec:  snapshot.OutboxPublishLagSec,
		OutboxMaxAttempts:    h.engine.OutboxMaxAttempts(),
		IdempotencyHit:       snapshot.IdempotencyHit,
		IdempotencyMiss:      snapshot.IdempotencyMiss,
	})
}

// @Summary Delete workflow instance
// @Description Delete a workflow instance
// @Tags instances
// @Param id path string true "Instance ID"
// @Success 204 {string} string "No Content"
// @Failure 404 {string} string "Not Found"
// @Failure 500 {string} string "Internal Server Error"
// @Router /instances/{id} [delete]
func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)
	start := time.Now()
	vars := mux.Vars(r)
	id := vars["id"]

	h.log.Info(ctx, "deleting instance", map[string]any{"instance_id": id})

	if err := h.engine.DeleteInstance(ctx, id); err != nil {
		h.log.Error(ctx, "failed to delete instance", map[string]any{
			"instance_id": id,
			"error":       err.Error(),
		})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.log.Info(ctx, "instance deleted", map[string]any{
		"instance_id": id,
		"duration_ms": time.Since(start).Milliseconds(),
	})

	w.WriteHeader(http.StatusNoContent)
}

// @Description Publish a signal to trigger intermediate catch events
// @Tags signals
// @Accept json
// @Produce json
// @Param request body dto.PublishSignalRequest true "Publish Signal Request"
// @Success 200 {string} string "Signal published"
// @Failure 400 {string} string "Bad Request"
// @Failure 500 {string} string "Internal Server Error"
// @Router /signals [post]
func (h *Handler) publishSignal(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)

	var req dto.PublishSignalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.Error(ctx, "failed to decode signal request", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.log.Info(ctx, "publishing signal", map[string]any{
		"signal_name": req.SignalName,
	})

	if err := h.engine.PublishSignal(ctx, req.SignalName, req.Payload); err != nil {
		h.log.Error(ctx, "failed to publish signal", map[string]any{
			"signal_name": req.SignalName,
			"error":       err.Error(),
		})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.log.Info(ctx, "signal published", map[string]any{"signal_name": req.SignalName})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Signal published"))
}

// @Summary Publish a message
// @Description Publish a message to trigger intermediate catch events
// @Tags messages
// @Accept json
// @Produce json
// @Param request body dto.PublishMessageRequest true "Publish Message Request"
// @Success 200 {string} string "Message published"
// @Failure 400 {string} string "Bad Request"
// @Failure 500 {string} string "Internal Server Error"
// @Router /messages [post]
func (h *Handler) publishMessage(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)

	var req dto.PublishMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.Error(ctx, "failed to decode message request", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.log.Info(ctx, "publishing message", map[string]any{
		"message_name":    req.MessageName,
		"correlation_key": req.CorrelationKey,
	})

	if err := h.engine.PublishMessage(ctx, req.MessageName, req.CorrelationKey, req.Payload); err != nil {
		h.log.Error(ctx, "failed to publish message", map[string]any{
			"message_name":    req.MessageName,
			"correlation_key": req.CorrelationKey,
			"error":           err.Error(),
		})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.log.Info(ctx, "message published", map[string]any{
		"message_name":    req.MessageName,
		"correlation_key": req.CorrelationKey,
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Message published"))
}

// @Summary Publish an escalation
// @Description Publish an escalation to trigger catch/boundary events and escalation start / event sub-processes
// @Tags escalations
// @Accept json
// @Produce json
// @Param request body dto.PublishEscalationRequest true "Publish Escalation Request"
// @Success 200 {string} string "Escalation published"
// @Failure 400 {string} string "Bad Request"
// @Failure 500 {string} string "Internal Server Error"
// @Router /escalations [post]
func (h *Handler) publishEscalation(w http.ResponseWriter, r *http.Request) {
	ctx := logger.ContextFromRequest(r)

	var req dto.PublishEscalationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.Error(ctx, "failed to decode escalation request", map[string]any{"error": err.Error()})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.EscalationCode) == "" {
		http.Error(w, "escalation_code is required", http.StatusBadRequest)
		return
	}

	h.log.Info(ctx, "publishing escalation", map[string]any{
		"escalation_code": req.EscalationCode,
	})

	if err := h.engine.PublishEscalation(ctx, req.EscalationCode, req.Payload); err != nil {
		h.log.Error(ctx, "failed to publish escalation", map[string]any{
			"escalation_code": req.EscalationCode,
			"error":           err.Error(),
		})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.log.Info(ctx, "escalation published", map[string]any{"escalation_code": req.EscalationCode})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Escalation published"))
}
