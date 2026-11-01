package model

import (
	"time"
)

// Engine Schema Models

// Process represents a deployed workflow definition (BPMN process)
type Process struct {
	Key              int64     `json:"key,string" gorm:"primaryKey"`                                                                   // Unique key (PK)
	BpmnProcessID    string    `json:"bpmnProcessId" gorm:"uniqueIndex:uq_process_tenant_bpmn_version,priority:2"`                     // ID from BPMN XML (e.g. "order-process")
	Version          int       `json:"version" gorm:"uniqueIndex:uq_process_tenant_bpmn_version,priority:3"`                           // Version of the process
	ResourceName     string    `json:"resourceName"`                                                                                   // Name of the BPMN file
	DeploymentKey    int64     `json:"deploymentKey,string"`                                                                           // Key of the deployment
	Resource         []byte    `json:"resource"`                                                                                       // BPMN XML content
	ResourceChecksum string    `json:"resourceChecksum"`                                                                               // Checksum to detect duplicates
	TenantID         string    `json:"tenantId" gorm:"not null;default:default;uniqueIndex:uq_process_tenant_bpmn_version,priority:1"` // Multi-tenancy support
	CreatedAt        time.Time `json:"createdAt"`
}

func (Process) TableName() string {
	return "process"
}

// ProcessInstance represents a running instance of a process
type ProcessInstance struct {
	Key                      int64                `json:"key,string" gorm:"primaryKey"`    // Unique key (PK)
	ID                       string               `json:"id" gorm:"index"`                 // Unique ID (UUIDv7)
	ProcessDefinitionKey     int64                `json:"processDefinitionKey,string"`     // FK to Process.Key
	Version                  int                  `json:"version"`                         // Cached
	ParentProcessInstanceKey int64                `json:"parentProcessInstanceKey,string"` // For Call Activity
	ParentElementInstanceKey int64                `json:"parentElementInstanceKey,string"` // For Call Activity
	State                    ProcessInstanceState `json:"state"`                           // ACTIVE, COMPLETED, CANCELED
	TenantID                 string               `json:"tenantId" gorm:"not null;default:default"`
	CreatedAt                time.Time            `json:"createdAt"`
	EndTime                  time.Time            `json:"endTime"`
	Context                  map[string]any       `json:"context,omitempty" gorm:"-"` // Denormalized Context (ES only)
}

func (ProcessInstance) TableName() string {
	return "process_instance"
}

// ElementInstance represents a token/step execution state
type ElementInstance struct {
	Key                  int64                `json:"key,string" gorm:"primaryKey"`
	ID                   string               `json:"id" gorm:"index"` // Unique ID (UUIDv7)
	ProcessInstanceKey   int64                `json:"processInstanceKey,string"`
	ProcessDefinitionKey int64                `json:"processDefinitionKey,string"`
	ElementID            string               `json:"elementId"`           // ID of the element in BPMN
	BpmnElementType      string               `json:"bpmnElementType"`     // SERVICE_TASK, START_EVENT, etc.
	FlowScopeKey         int64                `json:"flowScopeKey,string"` // Key of parent scope (e.g. SubProcess)
	State                ElementInstanceState `json:"state"`               // ACTIVATING, ACTIVATED, COMPLETING, COMPLETED, TERMINATED
	TenantID             string               `json:"tenantId" gorm:"not null;default:default"`
	CreatedAt            time.Time            `json:"createdAt"`
	EndTime              time.Time            `json:"endTime"`
}

func (ElementInstance) TableName() string {
	return "element_instance"
}

// Variable represents process variables/context
type Variable struct {
	ScopeKey           int64     `json:"scopeKey,string" gorm:"primaryKey"` // ProcessInstanceKey or ElementInstanceKey
	ProcessInstanceKey int64     `json:"processInstanceKey,string"`
	Name               string    `json:"name"`                    // Kept for backward compatibility or index
	Value              []byte    `json:"value" gorm:"type:jsonb"` // Full JSON context document
	TenantID           string    `json:"tenantId" gorm:"primaryKey;not null;default:default"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

func (Variable) TableName() string {
	return "variable"
}

// Job represents a task for an external worker (Service Task)
type Job struct {
	Key                  int64      `json:"key,string" gorm:"primaryKey"`
	ID                   string     `json:"id" gorm:"index"` // Unique ID (UUIDv7)
	Type                 string     `json:"type"`            // Task type (e.g. "payment-service")
	ProcessInstanceKey   int64      `json:"processInstanceKey,string"`
	ElementInstanceKey   int64      `json:"elementInstanceKey,string"`
	ProcessDefinitionKey int64      `json:"processDefinitionKey,string"`
	ElementID            string     `json:"elementId"`
	Worker               string     `json:"worker"`  // ID of the worker that locked the job
	Retries              int        `json:"retries"` // Remaining retries
	State                JobState   `json:"state"`   // CREATED, ACTIVATED, COMPLETED, FAILED
	Assignee             string     `json:"assignee"`
	CandidateUsers       string     `json:"candidateUsers"`
	CandidateGroups      string     `json:"candidateGroups"`
	LockExpirationTime   *time.Time `json:"lockExpirationTime,omitempty"`
	DueDate              time.Time  `json:"dueDate"` // SLA Target
	BreachedAt           *time.Time `json:"breachedAt,omitempty"`
	TenantID             string     `json:"tenantId" gorm:"not null;default:default"`
	ClaimOwner           string     `json:"claimOwner,omitempty"`
	ClaimExpiresAt       *time.Time `json:"claimExpiresAt,omitempty"`
	ClaimGeneration      int64      `json:"claimGeneration"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

func (Job) TableName() string {
	return "job"
}

// Timer represents a timer event (intermediate catch or boundary)
type Timer struct {
	Key                int64      `json:"key,string" gorm:"primaryKey"`
	ID                 string     `json:"id" gorm:"index"` // Unique ID (UUIDv7)
	ElementInstanceKey int64      `json:"elementInstanceKey,string"`
	ProcessInstanceKey int64      `json:"processInstanceKey,string"`
	ElementID          string     `json:"elementId"` // ID of the timer element (step ID or boundary event ID)
	DueDate            time.Time  `json:"dueDate"`
	State              TimerState `json:"state"` // CREATED, TRIGGERED, CANCELED
	RepeatCount        int        `json:"repeatCount"`
	TenantID           string     `json:"tenantId" gorm:"not null;default:default"`
	ClaimOwner         string     `json:"claimOwner,omitempty"`
	ClaimExpiresAt     *time.Time `json:"claimExpiresAt,omitempty"`
	ClaimGeneration    int64      `json:"claimGeneration"`
	CreatedAt          time.Time  `json:"createdAt"`
}

func (Timer) TableName() string {
	return "timer"
}

// MessageSubscription represents a subscription for message correlation
type MessageSubscription struct {
	Key                int64                    `json:"key,string" gorm:"primaryKey"`
	ID                 string                   `json:"id" gorm:"index"` // Unique ID (UUIDv7)
	ElementInstanceKey int64                    `json:"elementInstanceKey,string"`
	ProcessInstanceKey int64                    `json:"processInstanceKey,string"`
	ElementID          string                   `json:"elementId"` // ID of the message element (catch event or boundary event)
	MessageName        string                   `json:"messageName"`
	CorrelationKey     string                   `json:"correlationKey"`
	State              MessageSubscriptionState `json:"state"` // OPEN, CORRELATED, CLOSED
	TenantID           string                   `json:"tenantId" gorm:"not null;default:default"`
	CreatedAt          time.Time                `json:"createdAt"`
}

func (MessageSubscription) TableName() string {
	return "message_subscription"
}

// Incident represents an error that halted execution
type Incident struct {
	Key                int64             `json:"key,string" gorm:"primaryKey"`
	ID                 string            `json:"id" gorm:"index"` // Unique ID (UUIDv7)
	ProcessInstanceKey int64             `json:"processInstanceKey,string"`
	ElementInstanceKey int64             `json:"elementInstanceKey,string"`
	JobKey             int64             `json:"jobKey,string"` // Optional
	ErrorType          IncidentErrorType `json:"errorType"`
	ErrorMessage       string            `json:"errorMessage"`
	State              IncidentState     `json:"state"` // CREATED, RESOLVED
	TenantID           string            `json:"tenantId" gorm:"not null;default:default"`
	CreatedAt          time.Time         `json:"createdAt"`
	ResolvedAt         time.Time         `json:"resolvedAt"`
}

func (Incident) TableName() string {
	return "incident"
}

// IdempotencyRecord stores completed externally-triggered operations.
// It enables safe retries from clients without reapplying state transitions.
type IdempotencyRecord struct {
	Key       string    `json:"key" gorm:"primaryKey;size:128"`
	Operation string    `json:"operation" gorm:"primaryKey;size:64"`
	TenantID  string    `json:"tenantId" gorm:"primaryKey;not null;default:default"`
	CreatedAt time.Time `json:"createdAt"`
}

func (IdempotencyRecord) TableName() string {
	return "idempotency_record"
}

// OutboxMessage stores domain events produced inside transactions.
// Messages are inserted during commit and marked published after dispatch.
type OutboxMessage struct {
	ID                  string       `json:"id" gorm:"primaryKey;size:64"`
	EventType           string       `json:"eventType" gorm:"index;size:128"`
	TenantID            string       `json:"tenantId" gorm:"not null;default:default"`
	AggregateType       string       `json:"aggregateType" gorm:"size:64;not null"`
	AggregateID         string       `json:"aggregateId" gorm:"size:256;not null"`
	SchemaVersion       int          `json:"schemaVersion" gorm:"not null"`
	OccurredAt          time.Time    `json:"occurredAt" gorm:"not null"`
	TraceContext        []byte       `json:"traceContext" gorm:"type:jsonb;not null"`
	Payload             []byte       `json:"payload" gorm:"type:bytea"`
	Status              OutboxStatus `json:"status" gorm:"index;size:16"`
	Attempts            int          `json:"attempts"`
	LastError           string       `json:"lastError" gorm:"type:text"`
	NextAttempt         *time.Time   `json:"nextAttempt,omitempty" gorm:"index"`
	ProcessingStartedAt *time.Time   `json:"processingStartedAt,omitempty" gorm:"index"`
	CreatedAt           time.Time    `json:"createdAt"`
	PublishedAt         *time.Time   `json:"publishedAt,omitempty"`
	ClaimOwner          string       `json:"claimOwner,omitempty"`
	ClaimExpiresAt      *time.Time   `json:"claimExpiresAt,omitempty"`
	ClaimGeneration     int64        `json:"claimGeneration"`
}

func (OutboxMessage) TableName() string {
	return "outbox_message"
}

// RuntimeSchedulerLease coordinates a single-active runtime scheduler across replicas.
type RuntimeSchedulerLease struct {
	ID        string    `json:"id" gorm:"primaryKey;size:64"`
	Holder    string    `json:"holder" gorm:"size:256;not null"`
	ExpiresAt time.Time `json:"expiresAt" gorm:"index;not null"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (RuntimeSchedulerLease) TableName() string {
	return "runtime_scheduler_lease"
}

// DecisionDefinition stores a JSON decision table referenced by business-rule tasks.
type DecisionDefinition struct {
	Key        int64     `json:"key,string" gorm:"primaryKey"`
	DecisionID string    `json:"decisionId" gorm:"uniqueIndex:uq_decision_tenant_id,priority:2;size:128"`
	Name       string    `json:"name" gorm:"size:256"`
	Version    int       `json:"version"`
	TenantID   string    `json:"tenantId" gorm:"not null;default:default;uniqueIndex:uq_decision_tenant_id,priority:1"`
	Resource   []byte    `json:"resource" gorm:"type:bytea"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// RuntimeClaim is an ownership capability. Generation is a monotonic fence:
// callers must present the same owner and generation for completion.
type RuntimeClaim struct {
	TenantID   string          `json:"tenantId"`
	WorkType   RuntimeWorkType `json:"workType"`
	Key        string          `json:"key"`
	Owner      string          `json:"owner"`
	ExpiresAt  time.Time       `json:"expiresAt"`
	Generation int64           `json:"generation"`
}

type RuntimeWorkItem struct {
	TenantID        string          `json:"tenantId" gorm:"primaryKey;not null;default:default"`
	WorkType        RuntimeWorkType `json:"workType" gorm:"primaryKey;size:64"`
	WorkKey         string          `json:"workKey" gorm:"primaryKey;size:256"`
	AvailableAt     time.Time       `json:"availableAt"`
	ClaimOwner      string          `json:"claimOwner,omitempty"`
	ClaimedAt       *time.Time      `json:"claimedAt,omitempty"`
	ClaimExpiresAt  *time.Time      `json:"claimExpiresAt,omitempty"`
	ClaimGeneration int64           `json:"claimGeneration"`
	Payload         []byte          `json:"payload" gorm:"type:jsonb"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

func (RuntimeWorkItem) TableName() string { return "runtime_work_item" }

type ProcessInstanceMigration struct {
	TenantID            string              `json:"tenantId" gorm:"primaryKey;not null;default:default"`
	OperationID         string              `json:"operationId" gorm:"primaryKey;size:64"`
	InstanceKey         int64               `json:"instanceKey,string"`
	SourceDefinitionKey int64               `json:"sourceDefinitionKey,string"`
	TargetDefinitionKey int64               `json:"targetDefinitionKey,string"`
	SourceVersion       int                 `json:"sourceVersion"`
	TargetVersion       int                 `json:"targetVersion"`
	DryRun              bool                `json:"dryRun"`
	Status              MigrationStatus     `json:"status"`
	ReasonCode          MigrationReasonCode `json:"reasonCode,omitempty"`
	Mapping             []byte              `json:"mapping" gorm:"type:jsonb"`
	Actor               string              `json:"actor"`
	CreatedAt           time.Time           `json:"createdAt"`
	CompletedAt         *time.Time          `json:"completedAt,omitempty"`
}

func (ProcessInstanceMigration) TableName() string { return "process_instance_migration" }

type InstanceMigrationRequest struct {
	InstanceKey         int64             `json:"instanceKey,string"`
	SourceDefinitionKey int64             `json:"sourceDefinitionKey,string"`
	TargetDefinitionKey int64             `json:"targetDefinitionKey,string"`
	ElementMapping      map[string]string `json:"elementMapping"`
	DryRun              bool              `json:"dryRun"`
	OperationID         string            `json:"operationId,omitempty"`
	Actor               string            `json:"-"`
}

type InstanceMigrationResult struct {
	OperationID   string              `json:"operationId"`
	InstanceKey   string              `json:"instanceKey"`
	DryRun        bool                `json:"dryRun"`
	Status        MigrationStatus     `json:"status"`
	ReasonCode    MigrationReasonCode `json:"reasonCode,omitempty"`
	Message       string              `json:"message,omitempty"`
	SourceVersion int                 `json:"sourceVersion,omitempty"`
	TargetVersion int                 `json:"targetVersion,omitempty"`
}

func (DecisionDefinition) TableName() string {
	return "decision_definition"
}
