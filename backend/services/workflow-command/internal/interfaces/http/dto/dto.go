package dto

import (
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"time"
)

// --- Workflow Definitions ---

type WorkflowDefinitionResponse struct {
	ID                  string    `json:"id"` // Converted to string for JSON API consistency (even if int64 internally)
	ProcessDefinitionID string    `json:"process_definition_id"`
	Name                string    `json:"name"`
	Version             int       `json:"version"`
	ResourceName        string    `json:"resource_name"`
	DeploymentID        string    `json:"deployment_id"`
	TenantID            string    `json:"tenant_id"`
	ResourceChecksum    string    `json:"resource_checksum"`
	BPMNXML             string    `json:"bpmn_xml,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	// Steps are usually not returned in full detail in list/get summaries unless requested,
	// but to mirror model exactly for now:
	Steps []model.StepDefinition `json:"steps"`
}

// --- Workflow Instances ---

type WorkflowInstanceResponse struct {
	ID                string         `json:"id"`
	WorkflowID        string         `json:"workflow_id"`
	Status            string         `json:"status"` // string representation of WorkflowStatus
	ParentInstanceID  string         `json:"parent_instance_id,omitempty"`
	ParentExecutionID string         `json:"parent_execution_id,omitempty"`
	Context           map[string]any `json:"context"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	// Executions might be detailed or distinct DTOs too, but let's decouple top level first.
	// For deep structures, we might still use model types if we don't want to duplicate everything yet.
	// ADR goal is boundaries.
	Executions []ExecutionResponse `json:"executions"`
}

// --- Requests (Moved from handler.go) ---

type StartInstanceRequest struct {
	WorkflowID string         `json:"workflow_id"`
	Version    *int           `json:"version,omitempty"` // when workflow_id is a BPMN process id, pin a version; ignored for definition keys
	Context    map[string]any `json:"context"`
}

type UpdateVariablesRequest struct {
	Variables map[string]any `json:"variables"`
}

type CompleteTaskRequest struct {
	StepID string `json:"step_id"`
}

type InstanceMigrationRequest struct {
	SourceDefinitionKey string            `json:"sourceDefinitionKey"`
	TargetDefinitionKey string            `json:"targetDefinitionKey"`
	ElementMapping      map[string]string `json:"elementMapping"`
	OperationID         string            `json:"operationId,omitempty"`
}

type ExecutionResponse struct {
	ID                 string            `json:"id"`
	StepID             string            `json:"step_id"`
	Status             string            `json:"status"`
	ParentID           string            `json:"parent_id,omitempty"`
	StartTime          time.Time         `json:"start_time"`
	ElementInstanceKey string            `json:"element_instance_key,omitempty"`
	Task               *UserTaskResponse `json:"task,omitempty"`
}

type UserTaskResponse struct {
	Key                  string    `json:"key"`
	ProcessInstanceKey   string    `json:"processInstanceKey"`
	ProcessDefinitionKey string    `json:"processDefinitionKey"`
	ElementID            string    `json:"elementId"`
	ExecutionID          string    `json:"executionId"`
	State                string    `json:"state"`
	Assignee             string    `json:"assignee,omitempty"`
	CandidateUsers       []string  `json:"candidateUsers,omitempty"`
	CandidateGroups      []string  `json:"candidateGroups,omitempty"`
	ClaimedBy            string    `json:"claimedBy,omitempty"`
	CanClaim             bool      `json:"canClaim"`
	CanComplete          bool      `json:"canComplete"`
	DueDate              time.Time `json:"dueDate,omitempty"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type ListUserTasksResponse struct {
	Tasks []UserTaskResponse `json:"tasks"`
}

type ActivateJobsRequest struct {
	Type           string `json:"type"`
	Worker         string `json:"worker"`
	MaxJobs        int    `json:"maxJobs"`
	TimeoutMs      int    `json:"timeoutMs"`
	LockDurationMs int    `json:"lockDurationMs"`
}

type ActivateJobsResponse struct {
	Jobs []JobResponse `json:"jobs"`
}

type WorkerCapabilitiesResponse struct {
	ProtocolVersion string   `json:"protocolVersion"`
	Capabilities    []string `json:"capabilities"`
}

type EngineMetricsResponse struct {
	OutboxPending        int64 `json:"outboxPending"`
	OutboxPublishSuccess int64 `json:"outboxPublishSuccess"`
	OutboxPublishFailure int64 `json:"outboxPublishFailure"`
	OutboxPublishLagSec  int64 `json:"outboxPublishLagSec"`
	OutboxMaxAttempts    int   `json:"outboxMaxAttempts"`
	IdempotencyHit       int64 `json:"idempotencyHit"`
	IdempotencyMiss      int64 `json:"idempotencyMiss"`
}

type CompleteJobRequest struct {
	Worker    string         `json:"worker"`
	Variables map[string]any `json:"variables"`
}

type FailJobRequest struct {
	Worker       string `json:"worker"`
	ErrorMessage string `json:"errorMessage"`
	Retries      *int   `json:"retries"`
}

type ExtendJobLockRequest struct {
	Worker         string `json:"worker"`
	LockDurationMs int    `json:"lockDurationMs"`
}

type PublishSignalRequest struct {
	SignalName string         `json:"signal_name"`
	Payload    map[string]any `json:"payload"`
}

type PublishMessageRequest struct {
	MessageName    string         `json:"message_name"`
	CorrelationKey string         `json:"correlation_key"`
	Payload        map[string]any `json:"payload"`
}

type PublishEscalationRequest struct {
	EscalationCode string         `json:"escalation_code"`
	Payload        map[string]any `json:"payload"`
}

// --- Job Response ---

type JobResponse struct {
	Key                  string     `json:"key"` // String for API
	Type                 string     `json:"type"`
	ProcessInstanceKey   string     `json:"processInstanceKey"`
	ElementInstanceKey   string     `json:"elementInstanceKey"`
	ProcessDefinitionKey string     `json:"processDefinitionKey"`
	ElementID            string     `json:"elementId"`
	Worker               string     `json:"worker"`
	Retries              int        `json:"retries"`
	State                string     `json:"state"`
	Assignee             string     `json:"assignee,omitempty"`
	CandidateUsers       string     `json:"candidateUsers,omitempty"`
	CandidateGroups      string     `json:"candidateGroups,omitempty"`
	LockExpirationTime   *time.Time `json:"lockExpirationTime,omitempty"`
	DueDate              time.Time  `json:"dueDate,omitempty"`
	BreachedAt           *time.Time `json:"breachedAt,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

type IncidentResponse struct {
	Key                string     `json:"key"`
	ID                 string     `json:"id"`
	ProcessInstanceKey string     `json:"processInstanceKey"`
	ElementInstanceKey string     `json:"elementInstanceKey"`
	JobKey             string     `json:"jobKey,omitempty"`
	ErrorType          string     `json:"errorType"`
	ErrorMessage       string     `json:"errorMessage"`
	State              string     `json:"state"`
	CreatedAt          time.Time  `json:"createdAt"`
	ResolvedAt         *time.Time `json:"resolvedAt,omitempty"`
}
