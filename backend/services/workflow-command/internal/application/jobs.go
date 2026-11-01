package application

import (
	"context"
	"encoding/json"
	"fmt"
	pb "github.com/artificialflow/artificialflow/backend/api/v1/go"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultJobLockDuration = 30 * time.Second
	activationPollInterval = 100 * time.Millisecond
	UserTaskJobType        = "artificialflow:userTask"
)

func (e *Engine) ActivateJobs(ctx context.Context, jobType, worker string, maxJobs int, requestTimeout, lockDuration time.Duration) ([]model.Job, error) {
	jobs, err := e.activateJobsOnce(ctx, jobType, worker, maxJobs, lockDuration)
	if err != nil {
		return nil, err
	}
	if len(jobs) > 0 || requestTimeout <= 0 {
		return jobs, nil
	}

	timeout := time.NewTimer(requestTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(activationPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout.C:
			return []model.Job{}, nil
		case <-ticker.C:
			jobs, err := e.activateJobsOnce(ctx, jobType, worker, maxJobs, lockDuration)
			if err != nil {
				return nil, err
			}
			if len(jobs) > 0 {
				return jobs, nil
			}
		}
	}
}

func (e *Engine) activateJobsOnce(ctx context.Context, jobType, worker string, maxJobs int, lockDuration time.Duration) ([]model.Job, error) {
	if worker == "" {
		return nil, fmt.Errorf("worker is required")
	}
	if lockDuration <= 0 {
		lockDuration = defaultJobLockDuration
	}

	var activated []model.Job
	err := e.withTx(ctx, func(txEngine *Engine) error {
		jobs, err := txEngine.repo.ListActivatableJobs(ctx, jobType, maxJobs)
		if err != nil {
			return err
		}

		now := time.Now()
		activated = make([]model.Job, 0, len(jobs))
		for _, job := range jobs {
			lockUntil := now.Add(lockDuration)
			job.State = model.JobStateActivated
			job.Worker = worker
			job.LockExpirationTime = &lockUntil
			job.UpdatedAt = now
			if err := txEngine.repo.UpdateJob(ctx, &job); err != nil {
				return err
			}
			activated = append(activated, job)

			// Publish JobActivated
			if err := txEngine.eventPublisher.Publish(ctx, &pb.JobActivated{
				Key:                job.Key,
				Worker:             worker,
				LockExpirationTime: timestamppb.New(*job.LockExpirationTime),
			}, "JobActivated"); err != nil {
				fmt.Printf("failed to publish JobActivated: %v\n", err)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if activated == nil {
		activated = []model.Job{}
	}

	return activated, nil
}

func (e *Engine) CompleteJob(ctx context.Context, jobKey int64, worker string, variables map[string]any) error {
	return e.withTx(ctx, func(txEngine *Engine) error {
		job, err := txEngine.repo.GetJob(ctx, jobKey)
		if err != nil {
			return err
		}

		if job.State == model.JobStateCompleted {
			return nil
		}
		if job.State != model.JobStateActivated {
			return fmt.Errorf("job %d is not activated", job.Key)
		}

		now := time.Now()
		if err := ensureActiveLockOwnership(job, worker, now); err != nil {
			return err
		}

		instanceID := strconv.FormatInt(job.ProcessInstanceKey, 10)
		if len(variables) > 0 {
			instance, err := txEngine.GetInstance(ctx, instanceID)
			if err != nil {
				return err
			}
			if instance.Context == nil {
				instance.Context = make(map[string]any)
			}
			for k, v := range variables {
				instance.Context[k] = v
			}
			if err := txEngine.persistVariables(ctx, instanceID, job.ProcessInstanceKey, variables); err != nil {
				return err
			}
		}

		job.State = model.JobStateCompleted
		job.LockExpirationTime = nil
		job.UpdatedAt = now
		if err := txEngine.repo.UpdateJob(ctx, job); err != nil {
			return err
		}

		serializedVariables, err := serializeJobVariables(variables)
		if err != nil {
			return err
		}

		// Publish JobCompleted
		if err := txEngine.eventPublisher.Publish(ctx, &pb.JobCompleted{
			Key:       job.Key,
			Worker:    worker,
			Variables: serializedVariables,
			UpdatedAt: timestamppb.New(now),
		}, "JobCompleted"); err != nil {
			fmt.Printf("failed to publish JobCompleted: %v\n", err)
		}

		executionID := strconv.FormatInt(job.ElementInstanceKey, 10)
		return txEngine.completeExecution(ctx, instanceID, executionID)
	})
}

func serializeJobVariables(variables map[string]any) (string, error) {
	if len(variables) == 0 {
		return "{}", nil
	}
	data, err := json.Marshal(variables)
	if err != nil {
		return "", fmt.Errorf("serialize completed job variables: %w", err)
	}
	return string(data), nil
}

func (e *Engine) FailJob(ctx context.Context, jobKey int64, worker, errorMessage string, retries *int) error {
	return e.withTx(ctx, func(txEngine *Engine) error {
		job, err := txEngine.repo.GetJob(ctx, jobKey)
		if err != nil {
			return err
		}

		if job.State == model.JobStateCompleted {
			return fmt.Errorf("job %d already completed", job.Key)
		}
		if job.State == model.JobStateFailed {
			return nil
		}

		now := time.Now()
		if job.State == model.JobStateActivated {
			if err := ensureActiveLockOwnership(job, worker, now); err != nil {
				return err
			}
		}

		remainingRetries := job.Retries - 1
		if retries != nil {
			remainingRetries = *retries
		}
		if remainingRetries < 0 {
			remainingRetries = 0
		}

		job.Retries = remainingRetries
		job.Worker = ""
		job.LockExpirationTime = nil
		job.UpdatedAt = now

		if remainingRetries > 0 {
			job.State = model.JobStateCreated
			return txEngine.repo.UpdateJob(ctx, job)
		}

		job.State = model.JobStateFailed
		if err := txEngine.repo.UpdateJob(ctx, job); err != nil {
			return err
		}

		// Publish JobFailed
		if err := txEngine.eventPublisher.Publish(ctx, &pb.JobFailed{
			Key:          job.Key,
			Worker:       worker,
			Retries:      safeInt32(job.Retries),
			ErrorMessage: errorMessage,
			UpdatedAt:    timestamppb.New(now),
		}, "JobFailed"); err != nil {
			fmt.Printf("failed to publish JobFailed: %v\n", err)
		}

		if errorMessage == "" {
			errorMessage = "external worker failure"
		}
		incident := &model.Incident{
			Key:                generateKey(fmt.Sprintf("%d_incident", job.Key)),
			ProcessInstanceKey: job.ProcessInstanceKey,
			ElementInstanceKey: job.ElementInstanceKey,
			JobKey:             job.Key,
			ErrorType:          model.IncidentErrorJobNoRetries,
			ErrorMessage:       errorMessage,
			State:              model.IncidentStateCreated,
			CreatedAt:          time.Now(),
		}
		return txEngine.repo.CreateIncident(ctx, incident)
	})
}

func (e *Engine) ExtendJobLock(ctx context.Context, jobKey int64, worker string, lockDuration time.Duration) error {
	if lockDuration <= 0 {
		return fmt.Errorf("lock duration must be greater than zero")
	}

	return e.withTx(ctx, func(txEngine *Engine) error {
		job, err := txEngine.repo.GetJob(ctx, jobKey)
		if err != nil {
			return err
		}
		if job.State != model.JobStateActivated {
			return fmt.Errorf("job %d is not activated", job.Key)
		}

		now := time.Now()
		if err := ensureActiveLockOwnership(job, worker, now); err != nil {
			return err
		}

		lockUntil := now.Add(lockDuration)
		job.LockExpirationTime = &lockUntil
		job.UpdatedAt = now
		return txEngine.repo.UpdateJob(ctx, job)
	})
}

func (e *Engine) ListInstanceJobs(ctx context.Context, instanceID string) ([]model.Job, error) {
	instanceKey, err := strconv.ParseInt(instanceID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid instance id: %w", err)
	}
	return e.repo.ListJobsByProcessInstanceAndType(ctx, instanceKey, "")
}

// ListIncidents returns engine incidents, optionally filtered by process instance key.
func (e *Engine) ListIncidents(ctx context.Context, processInstanceKey int64, limit int) ([]model.Incident, error) {
	return e.repo.ListIncidents(ctx, processInstanceKey, limit)
}

// ResolveIncident marks an incident resolved. Repeated resolution is idempotent.
func (e *Engine) ResolveIncident(ctx context.Context, incidentKey int64) (*model.Incident, error) {
	var resolved *model.Incident
	err := e.withTx(ctx, func(txEngine *Engine) error {
		var incident *model.Incident
		var err error
		if getter, ok := txEngine.repo.(interface {
			GetIncident(context.Context, int64) (*model.Incident, error)
		}); ok {
			incident, err = getter.GetIncident(ctx, incidentKey)
		} else {
			var incidents []model.Incident
			incidents, err = txEngine.repo.ListIncidents(ctx, 0, 100000)
			for index := range incidents {
				if incidents[index].Key == incidentKey {
					copy := incidents[index]
					incident = &copy
					break
				}
			}
		}
		if err != nil {
			return err
		}
		if incident == nil {
			return fmt.Errorf("incident %d not found", incidentKey)
		}
		if incident.State != model.IncidentStateResolved {
			incident.State = model.IncidentStateResolved
			incident.ResolvedAt = time.Now()
			if err := txEngine.repo.UpdateIncident(ctx, incident); err != nil {
				return err
			}
		}
		resolved = incident
		return nil
	})
	return resolved, err
}

// RetryJob resets a failed or unlocked job so workers can pick it up again (ops/admin).
func (e *Engine) RetryJob(ctx context.Context, jobKey int64, retries int) error {
	if retries <= 0 {
		retries = 3
	}
	return e.withTx(ctx, func(txEngine *Engine) error {
		job, err := txEngine.repo.GetJob(ctx, jobKey)
		if err != nil {
			return err
		}
		if job.State == model.JobStateCompleted {
			return fmt.Errorf("job %d already completed", job.Key)
		}
		now := time.Now()
		if job.State == model.JobStateActivated && job.LockExpirationTime != nil && job.LockExpirationTime.After(now) {
			return fmt.Errorf("job %d is locked by worker %s until %s", job.Key, job.Worker, job.LockExpirationTime.Format(time.RFC3339))
		}
		job.State = model.JobStateCreated
		job.Retries = retries
		job.Worker = ""
		job.LockExpirationTime = nil
		job.UpdatedAt = now
		return txEngine.repo.UpdateJob(ctx, job)
	})
}

func (e *Engine) ListUserTaskJobs(ctx context.Context, instanceID string) ([]model.Job, error) {
	instanceKey, err := strconv.ParseInt(instanceID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid instance id: %v", err)
	}
	return e.repo.ListJobsByProcessInstanceAndType(ctx, instanceKey, UserTaskJobType)
}

func (e *Engine) ClaimUserTask(ctx context.Context, instanceID, executionID, owner string, force bool) (*model.Job, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil, fmt.Errorf("task owner is required")
	}

	var claimed *model.Job
	err := e.withTx(ctx, func(txEngine *Engine) error {
		job, err := txEngine.getUserTaskJob(ctx, instanceID, executionID)
		if err != nil {
			return err
		}
		if job.State == model.JobStateCompleted {
			return fmt.Errorf("user task %s is already completed", executionID)
		}
		if job.Worker != "" && job.Worker != owner && !force {
			return fmt.Errorf("user task %s is already claimed by %s", executionID, job.Worker)
		}

		now := time.Now()
		job.Worker = owner
		job.State = model.JobStateActivated
		job.UpdatedAt = now
		if err := txEngine.repo.UpdateJob(ctx, job); err != nil {
			return err
		}
		copy := *job
		claimed = &copy
		return nil
	})
	return claimed, err
}

func (e *Engine) CompleteUserTask(ctx context.Context, instanceID, executionID, owner string, force bool) error {
	owner = strings.TrimSpace(owner)
	if owner == "" && !force {
		return fmt.Errorf("task owner is required")
	}

	return e.withTx(ctx, func(txEngine *Engine) error {
		job, err := txEngine.getUserTaskJob(ctx, instanceID, executionID)
		if err != nil {
			return err
		}
		if job.State == model.JobStateCompleted {
			return nil
		}
		if !force {
			if job.Worker == "" {
				return fmt.Errorf("user task %s must be claimed before completion", executionID)
			}
			if job.Worker != owner {
				return fmt.Errorf("user task %s is claimed by %s", executionID, job.Worker)
			}
		}

		now := time.Now()
		job.State = model.JobStateCompleted
		job.LockExpirationTime = nil
		job.UpdatedAt = now
		if err := txEngine.repo.UpdateJob(ctx, job); err != nil {
			return err
		}
		return txEngine.completeExecution(ctx, instanceID, executionID)
	})
}

func (e *Engine) getUserTaskJob(ctx context.Context, instanceID, executionID string) (*model.Job, error) {
	instanceKey, err := strconv.ParseInt(instanceID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid instance id: %v", err)
	}
	executionKey, err := strconv.ParseInt(executionID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid execution id: %v", err)
	}

	jobs, err := e.repo.ListJobsByProcessInstanceAndType(ctx, instanceKey, UserTaskJobType)
	if err != nil {
		return nil, err
	}
	for idx := range jobs {
		if jobs[idx].ElementInstanceKey == executionKey {
			return &jobs[idx], nil
		}
	}
	return nil, fmt.Errorf("no user task job found for execution %s", executionID)
}

func ensureActiveLockOwnership(job *model.Job, worker string, now time.Time) error {
	if worker == "" {
		return fmt.Errorf("worker is required")
	}
	if job.Worker != worker {
		return fmt.Errorf("job %d locked by worker %s", job.Key, job.Worker)
	}
	if job.LockExpirationTime == nil {
		return fmt.Errorf("job %d has no active lock", job.Key)
	}
	if now.After(*job.LockExpirationTime) {
		return fmt.Errorf("job %d lock expired", job.Key)
	}

	return nil
}

func (e *Engine) HasProcessedIdempotencyKey(ctx context.Context, key, operation string) (bool, error) {
	key = strings.TrimSpace(key)
	operation = strings.TrimSpace(operation)
	if key == "" || operation == "" {
		return false, nil
	}

	record, err := e.repo.GetIdempotencyRecord(ctx, key, operation)
	if err != nil {
		return false, err
	}
	if e.metrics != nil {
		if record != nil {
			e.metrics.incIdempotencyHit()
		} else {
			e.metrics.incIdempotencyMiss()
		}
	}

	return record != nil, nil
}

func (e *Engine) RecordIdempotencyKey(ctx context.Context, key, operation string) error {
	key = strings.TrimSpace(key)
	operation = strings.TrimSpace(operation)
	if key == "" || operation == "" {
		return nil
	}

	return e.repo.CreateIdempotencyRecord(ctx, &model.IdempotencyRecord{
		Key:       key,
		Operation: operation,
		CreatedAt: time.Now(),
	})
}
