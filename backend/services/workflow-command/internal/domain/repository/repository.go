package repository

import (
	"context"
	"errors"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"time"
)

var ErrLeaseLost = errors.New("runtime claim lease lost")

type Repository interface {
	WithTx(ctx context.Context, fn func(txRepo Repository) error) error

	// Engine-like Operations (Incremental Adoption)
	CreateProcess(ctx context.Context, process *model.Process) error
	GetProcess(ctx context.Context, key int64) (*model.Process, error)
	DeleteProcess(ctx context.Context, key int64) error // New
	CreateProcessInstance(ctx context.Context, instance *model.ProcessInstance) error
	GetProcessInstance(ctx context.Context, key int64) (*model.ProcessInstance, error)
	UpdateProcessInstance(ctx context.Context, instance *model.ProcessInstance) error
	DeleteProcessInstance(ctx context.Context, key int64) error // New
	CreateElementInstance(ctx context.Context, element *model.ElementInstance) error
	UpdateElementInstance(ctx context.Context, element *model.ElementInstance) error
	CreateVariable(ctx context.Context, variable *model.Variable) error
	UpdateVariable(ctx context.Context, variable *model.Variable) error
	CreateJob(ctx context.Context, job *model.Job) error
	GetJob(ctx context.Context, key int64) (*model.Job, error)
	UpdateJob(ctx context.Context, job *model.Job) error
	ListActivatableJobs(ctx context.Context, jobType string, maxJobs int) ([]model.Job, error)
	ListJobsByProcessInstanceAndType(ctx context.Context, processInstanceKey int64, jobType string) ([]model.Job, error)
	CreateIncident(ctx context.Context, incident *model.Incident) error
	UpdateIncident(ctx context.Context, incident *model.Incident) error
	ListIncidents(ctx context.Context, processInstanceKey int64, limit int) ([]model.Incident, error)
	CreateTimer(ctx context.Context, timer *model.Timer) error
	GetTimer(ctx context.Context, key int64) (*model.Timer, error)
	UpdateTimer(ctx context.Context, timer *model.Timer) error
	ListCreatedTimersByElementInstanceKey(ctx context.Context, elementInstanceKey int64) ([]model.Timer, error)
	CreateMessageSubscription(ctx context.Context, subscription *model.MessageSubscription) error
	GetMessageSubscription(ctx context.Context, key int64) (*model.MessageSubscription, error)
	UpdateMessageSubscription(ctx context.Context, subscription *model.MessageSubscription) error
	GetIdempotencyRecord(ctx context.Context, key, operation string) (*model.IdempotencyRecord, error)
	CreateIdempotencyRecord(ctx context.Context, record *model.IdempotencyRecord) error
	CreateOutboxMessage(ctx context.Context, message *model.OutboxMessage) error
	ListPendingOutboxMessages(ctx context.Context, now time.Time, limit int) ([]model.OutboxMessage, error)
	ClaimOutboxMessages(ctx context.Context, owner string, ttl time.Duration, limit int) ([]model.OutboxMessage, error)
	ClaimOutboxMessage(ctx context.Context, id, owner string, ttl time.Duration) (*model.OutboxMessage, error)
	MarkOutboxMessagePublishFailed(ctx context.Context, id, owner string, generation int64, lastError string, retryDelay time.Duration) error
	MarkOutboxMessageTerminalFailed(ctx context.Context, id, owner string, generation int64, lastError string) error
	MarkOutboxMessagePublished(ctx context.Context, id, owner string, generation int64) error
	MarkOutboxMessagesPublished(ctx context.Context, owner string, messages []model.OutboxMessage) error
	CountPendingOutboxMessages(ctx context.Context, now time.Time) (int64, error)
	DeletePublishedOutboxMessagesBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
	DeleteIdempotencyRecordsBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
	ListDueTimers(ctx context.Context, now time.Time) ([]model.Timer, error)
	ListOverdueJobs(ctx context.Context, now time.Time) ([]model.Job, error) // New for SLA
	ListMessageSubscriptions(ctx context.Context, messageName, correlationKey string) ([]model.MessageSubscription, error)
	ClaimDueTimers(ctx context.Context, owner string, ttl time.Duration, limit int) ([]model.Timer, error)
	LockClaimedTimer(ctx context.Context, key int64, owner string, generation int64) error
	UpdateClaimedTimer(ctx context.Context, timer *model.Timer, owner string, generation int64) error
	ReleaseTimerClaim(ctx context.Context, key int64, owner string, generation int64) error
	CancelUnclaimedTimersForElement(ctx context.Context, elementInstanceKey, exceptTimerKey int64) error
	ClaimOverdueJobs(ctx context.Context, owner string, ttl time.Duration, limit int) ([]model.Job, error)
	UpdateClaimedJob(ctx context.Context, job *model.Job, owner string, generation int64) error
	ReleaseJobClaim(ctx context.Context, key int64, owner string, generation int64) error
	ClaimRuntimeWork(ctx context.Context, workType, owner string, ttl time.Duration, limit int) ([]model.RuntimeClaim, error)
	LockRuntimeWork(ctx context.Context, claim model.RuntimeClaim) error
	CompleteRuntimeWork(ctx context.Context, claim model.RuntimeClaim, delay time.Duration) error

	// Engine Query Helpers
	GetProcessByBpmnProcessID(ctx context.Context, bpmnProcessID string) (*model.Process, error)                                           // Get latest version
	GetProcessInstanceWithState(ctx context.Context, key int64) (*model.ProcessInstance, []model.ElementInstance, []model.Variable, error) // For re-hydrating state
	GetElementInstance(ctx context.Context, key int64) (*model.ElementInstance, error)
	ListActiveElementInstances(ctx context.Context, processInstanceKey int64) ([]model.ElementInstance, error)
	ListActiveElementInstancesByTypes(ctx context.Context, processInstanceKey int64, elementTypes []string) ([]model.ElementInstance, error)
	ListActiveProcessInstances(ctx context.Context) ([]*model.ProcessInstance, error)
	ListProcessInstancesByState(ctx context.Context, state string, limit int) ([]*model.ProcessInstance, error)
	SearchUserTaskJobs(ctx context.Context, jobType string, filter UserTaskFilter) ([]model.Job, error)
	ListProcesses(ctx context.Context) ([]*model.Process, error)
}

const (
	UserTaskStateActive    = "active"
	UserTaskStateCompleted = "completed"
	UserTaskStateAll       = "all"
)

// UserTaskFilter selects user tasks on behalf of a client application that
// has already authenticated its own end user. Assignee, CandidateUser and
// CandidateGroups are OR-combined; when all are empty every task matches.
type UserTaskFilter struct {
	ProcessInstanceKey int64
	Assignee           string
	CandidateUser      string
	CandidateGroups    []string
	// IncludeUnassigned adds tasks without assignee, candidates or claim when
	// identity filters are present.
	IncludeUnassigned bool
	State             string
	Limit             int
}
