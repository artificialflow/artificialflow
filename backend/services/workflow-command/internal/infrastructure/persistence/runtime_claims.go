package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/domain/repository"
	"gorm.io/gorm"
)

const defaultClaimBatch = 100

func normalizedClaimArgs(owner string, ttl time.Duration, limit int) (string, time.Duration, int, error) {
	if owner == "" {
		return "", 0, 0, fmt.Errorf("claim owner is required")
	}
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	if limit <= 0 {
		limit = defaultClaimBatch
	}
	if limit > 1000 {
		limit = 1000
	}
	return owner, ttl, limit, nil
}

func (s *GormRepository) ClaimDueTimers(ctx context.Context, owner string, ttl time.Duration, limit int) ([]model.Timer, error) {
	owner, ttl, limit, err := normalizedClaimArgs(owner, ttl, limit)
	if err != nil {
		return nil, err
	}
	var timers []model.Timer
	err = s.DB.WithContext(ctx).Raw(`
		WITH selected AS (
			SELECT key FROM timer
			WHERE state = 'CREATED'
			  AND due_date <= transaction_timestamp()
			  AND (claim_expires_at IS NULL OR claim_expires_at <= transaction_timestamp())
			ORDER BY due_date, key
			FOR UPDATE SKIP LOCKED
			LIMIT ?
		)
		UPDATE timer t
		SET claim_owner = ?, claim_expires_at = transaction_timestamp() + (? * interval '1 millisecond'),
		    claim_generation = t.claim_generation + 1
		FROM selected
		WHERE t.key = selected.key
		RETURNING t.*
	`, limit, owner, ttl.Milliseconds()).Scan(&timers).Error
	return timers, err
}

// LockClaimedTimer validates a live ownership fence and locks the timer row for
// the remainder of the caller's transaction. Call this before applying any
// durable effects caused by a claimed timer.
func (s *GormRepository) LockClaimedTimer(ctx context.Context, key int64, owner string, generation int64) error {
	var locked int
	query := `
		SELECT 1 FROM timer
		WHERE tenant_id = ? AND key = ? AND claim_owner = ? AND claim_generation = ?`
	args := []any{auth.TenantIDFromContext(ctx), key, owner, generation}
	if s.DB.Dialector.Name() == "sqlite" {
		query += " AND claim_expires_at > ?"
		args = append(args, time.Now().UTC())
	} else {
		query += " AND claim_expires_at > transaction_timestamp() FOR UPDATE"
	}
	err := s.DB.WithContext(ctx).Raw(query, args...).Scan(&locked).Error
	if err != nil {
		return err
	}
	if locked != 1 {
		return repository.ErrLeaseLost
	}
	return nil
}

func (s *GormRepository) UpdateClaimedTimer(ctx context.Context, timer *model.Timer, owner string, generation int64) error {
	query := s.DB.WithContext(ctx).Model(&model.Timer{}).
		Where("key = ? AND claim_owner = ? AND claim_generation = ?", timer.Key, owner, generation)
	if s.DB.Dialector.Name() == "sqlite" {
		query = query.Where("claim_expires_at > ?", time.Now().UTC())
	} else {
		query = query.Where("claim_expires_at > transaction_timestamp()")
	}
	result := query.
		Updates(map[string]any{
			"state": timer.State, "due_date": timer.DueDate, "repeat_count": timer.RepeatCount,
			"claim_owner": nil, "claim_expires_at": nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return repository.ErrLeaseLost
	}
	return nil
}

func (s *GormRepository) ReleaseTimerClaim(ctx context.Context, key int64, owner string, generation int64) error {
	return s.releaseClaim(ctx, "timer", key, owner, generation)
}

func (s *GormRepository) CancelUnclaimedTimersForElement(ctx context.Context, elementInstanceKey, exceptTimerKey int64) error {
	query := s.DB.WithContext(ctx).Model(&model.Timer{}).
		Where("tenant_id = ? AND element_instance_key = ? AND key <> ? AND state = ?",
			auth.TenantIDFromContext(ctx), elementInstanceKey, exceptTimerKey, "CREATED")
	if s.DB.Dialector.Name() == "sqlite" {
		query = query.Where("claim_expires_at IS NULL OR claim_expires_at <= ?", time.Now().UTC())
	} else {
		query = query.Where("claim_expires_at IS NULL OR claim_expires_at <= transaction_timestamp()")
	}
	return query.Update("state", "CANCELED").Error
}

func (s *GormRepository) ClaimOverdueJobs(ctx context.Context, owner string, ttl time.Duration, limit int) ([]model.Job, error) {
	owner, ttl, limit, err := normalizedClaimArgs(owner, ttl, limit)
	if err != nil {
		return nil, err
	}
	var jobs []model.Job
	err = s.DB.WithContext(ctx).Raw(`
		WITH selected AS (
			SELECT key FROM job
			WHERE state IN ('CREATED', 'ACTIVATED')
			  -- GORM persists an unset time.Time as year 1 instead of NULL.
			  -- Treat that sentinel as "no SLA due date"; otherwise every job is
			  -- reclaimed once per lease and emits an unbounded breach event.
			  AND due_date > '0001-01-01 00:00:00+00'::timestamptz
			  AND due_date <= transaction_timestamp() AND breached_at IS NULL
			  AND (claim_expires_at IS NULL OR claim_expires_at <= transaction_timestamp())
			ORDER BY due_date, key
			FOR UPDATE SKIP LOCKED
			LIMIT ?
		)
		UPDATE job j
		SET claim_owner = ?, claim_expires_at = transaction_timestamp() + (? * interval '1 millisecond'),
		    claim_generation = j.claim_generation + 1
		FROM selected
		WHERE j.key = selected.key
		RETURNING j.*
	`, limit, owner, ttl.Milliseconds()).Scan(&jobs).Error
	return jobs, err
}

func (s *GormRepository) UpdateClaimedJob(ctx context.Context, job *model.Job, owner string, generation int64) error {
	query := s.DB.WithContext(ctx).Model(&model.Job{}).
		Where("key = ? AND claim_owner = ? AND claim_generation = ?", job.Key, owner, generation)
	if s.DB.Dialector.Name() == "sqlite" {
		query = query.Where("claim_expires_at > ?", time.Now().UTC())
	} else {
		query = query.Where("claim_expires_at > transaction_timestamp()")
	}
	result := query.
		Updates(map[string]any{
			"worker": job.Worker, "lock_expiration_time": job.LockExpirationTime,
			"retries": job.Retries, "state": job.State, "assignee": job.Assignee,
			"candidate_users": job.CandidateUsers, "candidate_groups": job.CandidateGroups,
			"breached_at": job.BreachedAt, "updated_at": job.UpdatedAt,
			"claim_owner": nil, "claim_expires_at": nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return repository.ErrLeaseLost
	}
	return nil
}

func (s *GormRepository) ReleaseJobClaim(ctx context.Context, key int64, owner string, generation int64) error {
	return s.releaseClaim(ctx, "job", key, owner, generation)
}

func (s *GormRepository) releaseClaim(ctx context.Context, table string, key int64, owner string, generation int64) error {
	if table != "timer" && table != "job" {
		return fmt.Errorf("unsupported claim table")
	}
	result := s.DB.WithContext(ctx).Exec(
		"UPDATE "+table+" SET claim_owner = NULL, claim_expires_at = NULL WHERE tenant_id = ? AND key = ? AND claim_owner = ? AND claim_generation = ?",
		auth.TenantIDFromContext(ctx), key, owner, generation,
	)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return repository.ErrLeaseLost
	}
	return nil
}

func (s *GormRepository) ClaimRuntimeWork(ctx context.Context, workType, owner string, ttl time.Duration, limit int) ([]model.RuntimeClaim, error) {
	owner, ttl, limit, err := normalizedClaimArgs(owner, ttl, limit)
	if err != nil {
		return nil, err
	}
	var rows []model.RuntimeWorkItem
	err = s.DB.WithContext(ctx).Raw(`
		WITH selected AS (
			SELECT tenant_id, work_type, work_key FROM runtime_work_item
			WHERE work_type = ? AND available_at <= transaction_timestamp()
			  AND (claim_expires_at IS NULL OR claim_expires_at <= transaction_timestamp())
			ORDER BY available_at, work_key FOR UPDATE SKIP LOCKED LIMIT ?
		)
		UPDATE runtime_work_item w
		SET claim_owner = ?, claimed_at = transaction_timestamp(),
		    claim_expires_at = transaction_timestamp() + (? * interval '1 millisecond'),
		    claim_generation = w.claim_generation + 1, updated_at = transaction_timestamp()
		FROM selected s
		WHERE w.tenant_id = s.tenant_id AND w.work_type = s.work_type AND w.work_key = s.work_key
		RETURNING w.*
	`, workType, limit, owner, ttl.Milliseconds()).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	claims := make([]model.RuntimeClaim, 0, len(rows))
	for _, row := range rows {
		claims = append(claims, model.RuntimeClaim{
			TenantID: row.TenantID, WorkType: row.WorkType, Key: row.WorkKey,
			Owner: owner, ExpiresAt: *row.ClaimExpiresAt, Generation: row.ClaimGeneration,
		})
	}
	return claims, nil
}

func (s *GormRepository) LockRuntimeWork(ctx context.Context, claim model.RuntimeClaim) error {
	var locked int
	query := `
		SELECT 1 FROM runtime_work_item
		WHERE tenant_id = ? AND work_type = ? AND work_key = ?
		  AND claim_owner = ? AND claim_generation = ?`
	args := []any{claim.TenantID, claim.WorkType, claim.Key, claim.Owner, claim.Generation}
	if s.DB.Dialector.Name() == "sqlite" {
		query += " AND claim_expires_at > ?"
		args = append(args, time.Now().UTC())
	} else {
		query += " AND claim_expires_at > transaction_timestamp() FOR UPDATE"
	}
	if err := s.DB.WithContext(ctx).Raw(query, args...).Scan(&locked).Error; err != nil {
		return err
	}
	if locked != 1 {
		return repository.ErrLeaseLost
	}
	return nil
}

func (s *GormRepository) CompleteRuntimeWork(ctx context.Context, claim model.RuntimeClaim, delay time.Duration) error {
	query := s.DB.WithContext(ctx)
	var update *gorm.DB
	if s.DB.Dialector.Name() == "sqlite" {
		update = query.Exec(`
			UPDATE runtime_work_item
			SET claim_owner = NULL, claimed_at = NULL, claim_expires_at = NULL,
			    available_at = ?, updated_at = ?
			WHERE tenant_id = ? AND work_type = ? AND work_key = ?
			  AND claim_owner = ? AND claim_generation = ? AND claim_expires_at > ?
		`, time.Now().UTC().Add(delay), time.Now().UTC(), claim.TenantID, claim.WorkType, claim.Key,
			claim.Owner, claim.Generation, time.Now().UTC())
	} else {
		update = query.Exec(`
			UPDATE runtime_work_item
			SET claim_owner = NULL, claimed_at = NULL, claim_expires_at = NULL,
			    available_at = transaction_timestamp() + (? * interval '1 millisecond'),
			    updated_at = transaction_timestamp()
			WHERE tenant_id = ? AND work_type = ? AND work_key = ?
			  AND claim_owner = ? AND claim_generation = ? AND claim_expires_at > transaction_timestamp()
		`, delay.Milliseconds(), claim.TenantID, claim.WorkType, claim.Key, claim.Owner, claim.Generation)
	}
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return repository.ErrLeaseLost
	}
	return nil
}
