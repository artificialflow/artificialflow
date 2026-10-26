package model

// Persistent engine states are named string types so database and JSON wire
// values remain backward compatible while invalid cross-domain assignments are
// caught at compile time.
type ProcessInstanceState string
type ElementInstanceState string
type JobState string
type TimerState string
type MessageSubscriptionState string
type IncidentState string
type OutboxStatus string
type MigrationStatus string
type MigrationReasonCode string
type RuntimeWorkType string
type IncidentErrorType string

const (
	ProcessInstanceStateActive    ProcessInstanceState = "ACTIVE"
	ProcessInstanceStateCompleted ProcessInstanceState = "COMPLETED"
	ProcessInstanceStateCanceled  ProcessInstanceState = "CANCELED"
)

const (
	ElementInstanceStateActivating ElementInstanceState = "ACTIVATING"
	ElementInstanceStateActivated  ElementInstanceState = "ACTIVATED"
	ElementInstanceStateCompleting ElementInstanceState = "COMPLETING"
	ElementInstanceStateCompleted  ElementInstanceState = "COMPLETED"
	ElementInstanceStateTerminated ElementInstanceState = "TERMINATED"
)

const (
	JobStateCreated   JobState = "CREATED"
	JobStateActivated JobState = "ACTIVATED"
	JobStateCompleted JobState = "COMPLETED"
	JobStateFailed    JobState = "FAILED"
)

const (
	TimerStateCreated   TimerState = "CREATED"
	TimerStateTriggered TimerState = "TRIGGERED"
	TimerStateCanceled  TimerState = "CANCELED"
)

const (
	MessageSubscriptionStateOpen       MessageSubscriptionState = "OPEN"
	MessageSubscriptionStateCorrelated MessageSubscriptionState = "CORRELATED"
	MessageSubscriptionStateClosed     MessageSubscriptionState = "CLOSED"
)

const (
	IncidentStateCreated  IncidentState = "CREATED"
	IncidentStateResolved IncidentState = "RESOLVED"
)

const (
	IncidentErrorJobNoRetries IncidentErrorType = "JOB_NO_RETRIES"
)

const (
	OutboxStatusPending        OutboxStatus = "PENDING"
	OutboxStatusProcessing     OutboxStatus = "PROCESSING"
	OutboxStatusPublished      OutboxStatus = "PUBLISHED"
	OutboxStatusPublishFailed  OutboxStatus = "PUBLISH_FAILED"
	OutboxStatusTerminalFailed OutboxStatus = "TERMINAL_FAILED"
)

const (
	MigrationStatusValidated MigrationStatus = "VALIDATED"
	MigrationStatusApplied   MigrationStatus = "APPLIED"
	MigrationStatusRejected  MigrationStatus = "REJECTED"
)

const (
	MigrationReasonInstanceNotFound MigrationReasonCode = "INSTANCE_NOT_FOUND"
)

const (
	RuntimeWorkConditionalScan        RuntimeWorkType = "CONDITIONAL_SCAN"
	RuntimeWorkTimerStartScan         RuntimeWorkType = "TIMER_START_SCAN"
	RuntimeWorkIdempotencyMaintenance RuntimeWorkType = "IDEMPOTENCY_MAINTENANCE"
)
