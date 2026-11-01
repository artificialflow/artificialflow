package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/logger"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/domain/repository"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/infrastructure/migration"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	postgresConnectWait  = 2 * time.Minute
	postgresConnectRetry = 2 * time.Second
)

type GormRepository struct {
	DB *gorm.DB

	tenantDiscoveryMu    sync.Mutex
	tenantDiscovery      []string
	tenantDiscoveryUntil time.Time
}

var runtimeTenantDiscoveryTTL = 10 * time.Minute

const outboxProcessingLease = 5 * time.Minute

const (
	canonicalUserTaskJobType = "artificialflow:userTask"
)

// Ensure GormRepository implements repository.Repository
var _ repository.Repository = &GormRepository{}

func NewGormRepository(db *gorm.DB) *GormRepository {
	installTenantCallbacks(db)
	return &GormRepository{DB: db}
}

func installTenantCallbacks(db *gorm.DB) {
	if db == nil {
		return
	}
	scope := func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Schema == nil || tx.Statement.Schema.LookUpField("TenantID") == nil {
			return
		}
		tenant := auth.TenantIDFromContext(tx.Statement.Context)
		tx.Statement.AddClause(clause.Where{Exprs: []clause.Expression{
			clause.Eq{Column: clause.Column{Table: clause.CurrentTable, Name: "tenant_id"}, Value: tenant},
		}})
	}
	stamp := func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Schema == nil || tx.Statement.Schema.LookUpField("TenantID") == nil {
			return
		}
		tx.Statement.SetColumn("TenantID", auth.TenantIDFromContext(tx.Statement.Context))
	}
	if db.Callback().Create().Get("artificialflow:tenant_stamp") == nil {
		_ = db.Callback().Create().Before("gorm:create").Register("artificialflow:tenant_stamp", stamp)
	}
	if db.Callback().Query().Get("artificialflow:tenant_scope") == nil {
		_ = db.Callback().Query().Before("gorm:query").Register("artificialflow:tenant_scope", scope)
	}
	if db.Callback().Update().Get("artificialflow:tenant_scope") == nil {
		_ = db.Callback().Update().Before("gorm:update").Register("artificialflow:tenant_scope", scope)
	}
	if db.Callback().Delete().Get("artificialflow:tenant_scope") == nil {
		_ = db.Callback().Delete().Before("gorm:delete").Register("artificialflow:tenant_scope", scope)
	}
}

func NewPostgresRepository(dsn string) (*GormRepository, error) {
	// Use custom GORM logger that integrates with our structured logging
	// and suppresses noisy "record not found" messages
	gormLogger := logger.NewGormLogger("gorm")

	// Postgres may still be starting when the services come up in the same
	// install; wait instead of crash-looping.
	var db *gorm.DB
	var err error
	deadline := time.Now().Add(postgresConnectWait)
	for {
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormLogger})
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(postgresConnectRetry)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres within %s: %v", postgresConnectWait, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get postgres connection pool: %w", err)
	}
	// Keep the five database-using pods in the reference topology below
	// PostgreSQL's default connection limit. Unbounded database/sql pools let
	// load spikes exhaust the server and turn queueing into cluster-wide 500s.
	sqlDB.SetMaxOpenConns(12)
	sqlDB.SetMaxIdleConns(6)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	// Apply is mandatory and idempotent. The advisory lock inside Apply lets
	// command and runtime start together without a separate migration process.
	if err := migration.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if err := migration.Validate(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}

	return NewGormRepository(db), nil
}

// Ready verifies both connectivity and the exact embedded migration contract.
// It is intentionally bounded by the caller's context.
func (s *GormRepository) Ready(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return errors.New("postgres repository is not initialized")
	}
	sqlDB, err := s.DB.DB()
	if err != nil {
		return fmt.Errorf("get postgres connection pool: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	return migration.Validate(ctx, sqlDB)
}

func (s *GormRepository) WithTx(ctx context.Context, fn func(txRepo repository.Repository) error) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := &GormRepository{DB: tx}
		return fn(txRepo)
	})
}

const runtimeSchedulerLeaseID = "runtime-scheduler"

func (s *GormRepository) UpsertDecision(ctx context.Context, decision *model.DecisionDefinition) error {
	if decision == nil || strings.TrimSpace(decision.DecisionID) == "" {
		return fmt.Errorf("decision id is required")
	}
	now := time.Now().UTC()
	var existing model.DecisionDefinition
	err := s.DB.WithContext(ctx).Where("decision_id = ?", decision.DecisionID).First(&existing).Error
	if err == nil {
		decision.Key = existing.Key
		decision.Version = existing.Version + 1
		decision.CreatedAt = existing.CreatedAt
		decision.UpdatedAt = now
		return s.DB.WithContext(ctx).Model(&existing).Updates(map[string]any{
			"name":       decision.Name,
			"version":    decision.Version,
			"resource":   decision.Resource,
			"updated_at": decision.UpdatedAt,
		}).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if decision.Key == 0 {
		decision.Key = time.Now().UnixNano()
	}
	if decision.Version == 0 {
		decision.Version = 1
	}
	decision.CreatedAt = now
	decision.UpdatedAt = now
	return s.DB.WithContext(ctx).Create(decision).Error
}

func (s *GormRepository) GetDecisionByDecisionID(ctx context.Context, decisionID string) (*model.DecisionDefinition, error) {
	var decision model.DecisionDefinition
	if err := s.DB.WithContext(ctx).Where("decision_id = ?", decisionID).First(&decision).Error; err != nil {
		return nil, err
	}
	return &decision, nil
}

func (s *GormRepository) ListDecisions(ctx context.Context) ([]model.DecisionDefinition, error) {
	var decisions []model.DecisionDefinition
	if err := s.DB.WithContext(ctx).Order("decision_id asc").Find(&decisions).Error; err != nil {
		return nil, err
	}
	return decisions, nil
}

// TryAcquireRuntimeSchedulerLease attempts to become the single-active runtime scheduler.
// A holder may renew its own lease; another replica may steal only after expiry.
func (s *GormRepository) TryAcquireRuntimeSchedulerLease(ctx context.Context, holder string, ttl time.Duration) (bool, error) {
	if strings.TrimSpace(holder) == "" {
		return false, fmt.Errorf("runtime scheduler lease holder is required")
	}
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	if s.DB.Dialector.Name() == "sqlite" {
		now := time.Now().UTC()
		result := s.DB.WithContext(ctx).Exec(`
			INSERT INTO runtime_scheduler_lease (id, holder, expires_at, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET holder = excluded.holder,
			  expires_at = excluded.expires_at, updated_at = excluded.updated_at
			WHERE runtime_scheduler_lease.expires_at <= ? OR runtime_scheduler_lease.holder = ?
		`, runtimeSchedulerLeaseID, holder, now.Add(ttl), now, now, holder)
		return result.RowsAffected > 0, result.Error
	}
	result := s.DB.WithContext(ctx).Exec(`
		INSERT INTO runtime_scheduler_lease (id, holder, expires_at, updated_at)
		VALUES (?, ?, transaction_timestamp() + (? * interval '1 millisecond'), transaction_timestamp())
		ON CONFLICT (id) DO UPDATE
		SET holder = EXCLUDED.holder,
		    expires_at = EXCLUDED.expires_at,
		    updated_at = EXCLUDED.updated_at
		WHERE runtime_scheduler_lease.expires_at <= transaction_timestamp()
		   OR runtime_scheduler_lease.holder = ?
	`, runtimeSchedulerLeaseID, holder, ttl.Milliseconds(), holder)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// --- ENGINE OPERATIONS ---

func (s *GormRepository) CreateProcess(ctx context.Context, process *model.Process) error {
	if err := s.DB.WithContext(ctx).Create(process).Error; err != nil {
		return err
	}
	return s.ensureRuntimeWorkTenant(ctx, auth.TenantIDFromContext(ctx))
}

func (s *GormRepository) ensureRuntimeWorkTenant(ctx context.Context, tenant string) error {
	if !s.DB.Migrator().HasTable(&model.RuntimeWorkItem{}) {
		return nil
	}
	now := time.Now().UTC()
	items := make([]model.RuntimeWorkItem, 0, 3)
	for _, workType := range []model.RuntimeWorkType{
		model.RuntimeWorkConditionalScan,
		model.RuntimeWorkTimerStartScan,
		model.RuntimeWorkIdempotencyMaintenance,
	} {
		items = append(items, model.RuntimeWorkItem{
			TenantID: tenant, WorkType: workType, WorkKey: "0",
			AvailableAt: now, Payload: []byte("{}"), UpdatedAt: now,
		})
	}
	return s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&items).Error
}

// EnsureRuntimeWorkTenants discovers every tenant represented by durable
// command-side data and seeds the scheduler work that must run per tenant.
// It also returns the tenants for the compat scheduler's explicit scan loop.
func (s *GormRepository) EnsureRuntimeWorkTenants(ctx context.Context) ([]string, error) {
	s.tenantDiscoveryMu.Lock()
	if runtimeTenantDiscoveryTTL > 0 && time.Now().Before(s.tenantDiscoveryUntil) && len(s.tenantDiscovery) > 0 {
		cached := append([]string(nil), s.tenantDiscovery...)
		s.tenantDiscoveryMu.Unlock()
		return cached, nil
	}
	s.tenantDiscoveryMu.Unlock()

	tenants := map[string]struct{}{auth.DefaultTenantID: {}}
	tables := []string{
		"runtime_work_item", "process", "decision_definition", "job", "process_instance",
		"element_instance", "variable", "incident", "timer", "message_subscription",
		"idempotency_record", "outbox_message",
	}
	parts := make([]string, 0, len(tables))
	for _, table := range tables {
		if !s.DB.Migrator().HasTable(table) {
			continue
		}
		parts = append(parts, "SELECT tenant_id FROM "+table+" WHERE tenant_id IS NOT NULL AND tenant_id <> '' GROUP BY tenant_id")
	}
	if len(parts) > 0 {
		var values []string
		if err := s.DB.WithContext(ctx).
			Raw(strings.Join(parts, " UNION ")).
			Scan(&values).Error; err != nil {
			return nil, fmt.Errorf("discover runtime tenants: %w", err)
		}
		for _, tenant := range values {
			if tenant = strings.TrimSpace(tenant); tenant != "" {
				tenants[tenant] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(tenants))
	for tenant := range tenants {
		tenantCtx := auth.WithPrincipal(ctx, auth.Principal{Subject: "workflow-runtime", TenantID: tenant})
		if err := s.ensureRuntimeWorkTenant(tenantCtx, tenant); err != nil {
			return nil, fmt.Errorf("seed runtime work for tenant %q: %w", tenant, err)
		}
		result = append(result, tenant)
	}
	sort.Strings(result)
	s.tenantDiscoveryMu.Lock()
	s.tenantDiscovery = append([]string(nil), result...)
	if runtimeTenantDiscoveryTTL > 0 {
		s.tenantDiscoveryUntil = time.Now().Add(runtimeTenantDiscoveryTTL)
	}
	s.tenantDiscoveryMu.Unlock()
	return result, nil
}

func (s *GormRepository) GetProcess(ctx context.Context, key int64) (*model.Process, error) {
	var p model.Process
	if err := s.DB.WithContext(ctx).First(&p, "key = ?", key).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *GormRepository) DeleteProcess(ctx context.Context, key int64) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Find all instances
		var instances []model.ProcessInstance
		if err := tx.Where("process_definition_key = ?", key).Find(&instances).Error; err != nil {
			return err
		}

		// 2. Delete all instances and their related data
		for _, instance := range instances {
			if err := tx.Delete(&model.Variable{}, "process_instance_key = ?", instance.Key).Error; err != nil {
				return err
			}
			if err := tx.Delete(&model.ElementInstance{}, "process_instance_key = ?", instance.Key).Error; err != nil {
				return err
			}
			if err := tx.Delete(&model.Job{}, "process_instance_key = ?", instance.Key).Error; err != nil {
				return err
			}
			if err := tx.Delete(&model.Incident{}, "process_instance_key = ?", instance.Key).Error; err != nil {
				return err
			}
			if err := tx.Delete(&model.Timer{}, "process_instance_key = ?", instance.Key).Error; err != nil {
				return err
			}
			if err := tx.Delete(&model.MessageSubscription{}, "process_instance_key = ?", instance.Key).Error; err != nil {
				return err
			}
			if err := tx.Delete(&model.ProcessInstance{}, "key = ?", instance.Key).Error; err != nil {
				return err
			}
		}

		// 3. Delete the process definition
		if err := tx.Delete(&model.Process{}, "key = ?", key).Error; err != nil {
			return err
		}

		return nil
	})
}

func (s *GormRepository) CreateProcessInstance(ctx context.Context, instance *model.ProcessInstance) error {
	if err := s.DB.WithContext(ctx).Create(instance).Error; err != nil {
		return err
	}
	return s.ensureRuntimeWorkTenant(ctx, auth.TenantIDFromContext(ctx))
}

func (s *GormRepository) GetProcessInstance(ctx context.Context, key int64) (*model.ProcessInstance, error) {
	var pi model.ProcessInstance
	if err := s.DB.WithContext(ctx).First(&pi, "key = ?", key).Error; err != nil {
		return nil, err
	}
	return &pi, nil
}

func (s *GormRepository) UpdateProcessInstance(ctx context.Context, instance *model.ProcessInstance) error {
	return s.DB.WithContext(ctx).Model(instance).Updates(map[string]interface{}{
		"state":    instance.State,
		"end_time": instance.EndTime,
	}).Error
}

func (s *GormRepository) DeleteProcessInstance(ctx context.Context, key int64) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.Variable{}, "process_instance_key = ?", key).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.ElementInstance{}, "process_instance_key = ?", key).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.Job{}, "process_instance_key = ?", key).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.Incident{}, "process_instance_key = ?", key).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.Timer{}, "process_instance_key = ?", key).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.MessageSubscription{}, "process_instance_key = ?", key).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.ProcessInstance{}, "key = ?", key).Error; err != nil {
			return err
		}
		return nil
	})
}

func (s *GormRepository) CreateElementInstance(ctx context.Context, element *model.ElementInstance) error {
	return s.DB.WithContext(ctx).Create(element).Error
}

func (s *GormRepository) UpdateElementInstance(ctx context.Context, element *model.ElementInstance) error {
	return s.DB.WithContext(ctx).Model(element).Updates(map[string]interface{}{
		"state":    element.State,
		"end_time": element.EndTime,
	}).Error
}

func (s *GormRepository) CreateVariable(ctx context.Context, variable *model.Variable) error {
	return s.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "scope_key"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"process_instance_key": variable.ProcessInstanceKey,
			"name":                 variable.Name,
			"value":                variable.Value,
			"updated_at":           variable.UpdatedAt,
		}),
	}).Create(variable).Error
}

func (s *GormRepository) UpdateVariable(ctx context.Context, variable *model.Variable) error {
	return s.DB.WithContext(ctx).Model(variable).Updates(map[string]interface{}{
		"value":      variable.Value,
		"updated_at": variable.UpdatedAt,
	}).Error
}

func (s *GormRepository) CreateJob(ctx context.Context, job *model.Job) error {
	if err := s.DB.WithContext(ctx).Create(job).Error; err != nil {
		return err
	}
	return s.ensureRuntimeWorkTenant(ctx, auth.TenantIDFromContext(ctx))
}

func (s *GormRepository) GetJob(ctx context.Context, key int64) (*model.Job, error) {
	var job model.Job
	if err := s.DB.WithContext(ctx).First(&job, "key = ?", key).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *GormRepository) UpdateJob(ctx context.Context, job *model.Job) error {
	if job.ClaimOwner != "" && job.ClaimGeneration > 0 {
		return s.UpdateClaimedJob(ctx, job, job.ClaimOwner, job.ClaimGeneration)
	}
	return s.DB.WithContext(ctx).Model(job).Updates(map[string]interface{}{
		"worker":               job.Worker,
		"lock_expiration_time": job.LockExpirationTime,
		"retries":              job.Retries,
		"state":                job.State,
		"assignee":             job.Assignee,
		"candidate_users":      job.CandidateUsers,
		"candidate_groups":     job.CandidateGroups,
		"breached_at":          job.BreachedAt,
		"updated_at":           job.UpdatedAt,
	}).Error
}

func (s *GormRepository) ListActivatableJobs(ctx context.Context, jobType string, maxJobs int) ([]model.Job, error) {
	if maxJobs <= 0 {
		maxJobs = 1
	}
	if maxJobs > 100 {
		maxJobs = 100
	}

	now := time.Now()
	query := s.DB.WithContext(ctx).
		Where("(state = ? OR (state = ? AND lock_expiration_time IS NOT NULL AND lock_expiration_time <= ?)) AND retries > 0", "CREATED", "ACTIVATED", now)

	if jobType != "" {
		query = query.Where("type IN ?", compatibleJobTypes(jobType))
	}

	if s.DB.Dialector.Name() != "sqlite" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
	}

	var jobs []model.Job
	if err := query.Order("created_at asc").Limit(maxJobs).Find(&jobs).Error; err != nil {
		return nil, err
	}

	return jobs, nil
}

func (s *GormRepository) ListJobsByProcessInstanceAndType(ctx context.Context, processInstanceKey int64, jobType string) ([]model.Job, error) {
	query := s.DB.WithContext(ctx).
		Where("process_instance_key = ?", processInstanceKey)
	if jobType != "" {
		query = query.Where("type IN ?", compatibleJobTypes(jobType))
	}

	var jobs []model.Job
	if err := query.Order("created_at asc").Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *GormRepository) CreateIncident(ctx context.Context, incident *model.Incident) error {
	return s.DB.WithContext(ctx).Create(incident).Error
}

func (s *GormRepository) GetIncident(ctx context.Context, key int64) (*model.Incident, error) {
	var incident model.Incident
	if err := s.DB.WithContext(ctx).First(&incident, "key = ?", key).Error; err != nil {
		return nil, err
	}
	return &incident, nil
}

func (s *GormRepository) UpdateIncident(ctx context.Context, incident *model.Incident) error {
	return s.DB.WithContext(ctx).Model(incident).Updates(map[string]interface{}{
		"state":       incident.State,
		"resolved_at": incident.ResolvedAt,
	}).Error
}

func (s *GormRepository) CreateTimer(ctx context.Context, timer *model.Timer) error {
	if err := s.DB.WithContext(ctx).Create(timer).Error; err != nil {
		return err
	}
	return s.ensureRuntimeWorkTenant(ctx, auth.TenantIDFromContext(ctx))
}

func (s *GormRepository) GetTimer(ctx context.Context, key int64) (*model.Timer, error) {
	var timer model.Timer
	if err := s.DB.WithContext(ctx).First(&timer, "key = ?", key).Error; err != nil {
		return nil, err
	}
	return &timer, nil
}

func (s *GormRepository) UpdateTimer(ctx context.Context, timer *model.Timer) error {
	if timer.ClaimOwner != "" && timer.ClaimGeneration > 0 {
		return s.UpdateClaimedTimer(ctx, timer, timer.ClaimOwner, timer.ClaimGeneration)
	}
	return s.DB.WithContext(ctx).Model(timer).Updates(map[string]interface{}{
		"state":        timer.State,
		"due_date":     timer.DueDate,
		"repeat_count": timer.RepeatCount,
	}).Error
}

func (s *GormRepository) ListCreatedTimersByElementInstanceKey(ctx context.Context, elementInstanceKey int64) ([]model.Timer, error) {
	var timers []model.Timer
	if err := s.DB.WithContext(ctx).Where("state = ? AND element_instance_key = ?", "CREATED", elementInstanceKey).Find(&timers).Error; err != nil {
		return nil, err
	}
	return timers, nil
}

func (s *GormRepository) ListIncidents(ctx context.Context, processInstanceKey int64, limit int) ([]model.Incident, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	query := s.DB.WithContext(ctx).Order("created_at desc").Limit(limit)
	if processInstanceKey != 0 {
		query = query.Where("process_instance_key = ?", processInstanceKey)
	}
	var incidents []model.Incident
	if err := query.Find(&incidents).Error; err != nil {
		return nil, err
	}
	return incidents, nil
}

func (s *GormRepository) CreateMessageSubscription(ctx context.Context, subscription *model.MessageSubscription) error {
	if err := s.DB.WithContext(ctx).Create(subscription).Error; err != nil {
		return err
	}
	return s.ensureRuntimeWorkTenant(ctx, auth.TenantIDFromContext(ctx))
}

func (s *GormRepository) GetMessageSubscription(ctx context.Context, key int64) (*model.MessageSubscription, error) {
	var sub model.MessageSubscription
	if err := s.DB.WithContext(ctx).First(&sub, "key = ?", key).Error; err != nil {
		return nil, err
	}
	return &sub, nil
}

func (s *GormRepository) UpdateMessageSubscription(ctx context.Context, subscription *model.MessageSubscription) error {
	return s.DB.WithContext(ctx).Model(subscription).Updates(map[string]interface{}{
		"state": subscription.State,
	}).Error
}

func (s *GormRepository) GetIdempotencyRecord(ctx context.Context, key, operation string) (*model.IdempotencyRecord, error) {
	var record model.IdempotencyRecord
	err := s.DB.WithContext(ctx).
		Where("key = ? AND operation = ?", key, operation).
		First(&record).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &record, nil
}

func (s *GormRepository) CreateIdempotencyRecord(ctx context.Context, record *model.IdempotencyRecord) error {
	if err := s.DB.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(record).Error; err != nil {
		return err
	}
	return s.ensureRuntimeWorkTenant(ctx, auth.TenantIDFromContext(ctx))
}

func (s *GormRepository) DeleteIdempotencyRecordsBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	query := s.DB.WithContext(ctx).
		Where("created_at < ?", cutoff).
		Order("created_at asc")
	if limit > 0 {
		query = query.Limit(limit)
	}

	var records []model.IdempotencyRecord
	if err := query.Find(&records).Error; err != nil {
		return 0, err
	}
	if len(records) == 0 {
		return 0, nil
	}

	var deleted int64
	for _, rec := range records {
		result := s.DB.WithContext(ctx).
			Where("key = ? AND operation = ?", rec.Key, rec.Operation).
			Delete(&model.IdempotencyRecord{})
		if result.Error != nil {
			return deleted, result.Error
		}
		deleted += result.RowsAffected
	}

	return deleted, nil
}

func (s *GormRepository) CreateOutboxMessage(ctx context.Context, message *model.OutboxMessage) error {
	if message.SchemaVersion == 0 {
		message.SchemaVersion = 1
	}
	if message.OccurredAt.IsZero() {
		message.OccurredAt = message.CreatedAt
		if message.OccurredAt.IsZero() {
			message.OccurredAt = time.Now().UTC()
		}
	}
	if message.AggregateType == "" {
		message.AggregateType = "legacy"
	}
	if message.AggregateID == "" {
		message.AggregateID = message.ID
	}
	if len(message.TraceContext) == 0 {
		message.TraceContext = []byte("{}")
	}
	return s.DB.WithContext(ctx).Create(message).Error
}

func (s *GormRepository) ListPendingOutboxMessages(ctx context.Context, now time.Time, limit int) ([]model.OutboxMessage, error) {
	if limit <= 0 {
		limit = 100
	}

	var messages []model.OutboxMessage
	if s.DB.Dialector.Name() != "sqlite" {
		err := s.DB.WithContext(ctx).Raw(`
			SELECT * FROM (
			  (SELECT * FROM outbox_message
			   WHERE status = 'PENDING'
			     AND (next_attempt IS NULL OR next_attempt <= transaction_timestamp())
			   ORDER BY created_at LIMIT ?)
			  UNION ALL
			  (SELECT * FROM outbox_message
			   WHERE status = 'PROCESSING'
			     AND (claim_expires_at <= transaction_timestamp()
			          OR (claim_expires_at IS NULL AND processing_started_at <= transaction_timestamp() - interval '5 minutes'))
			   ORDER BY created_at LIMIT ?)
			) claimable
			ORDER BY created_at LIMIT ?
		`, limit, limit, limit).Scan(&messages).Error
		return messages, err
	}
	if err := s.DB.WithContext(ctx).
		Scopes(claimableOutboxMessages(now)).
		Order("created_at asc").
		Limit(limit).
		Find(&messages).Error; err != nil {
		return nil, err
	}

	return messages, nil
}

func (s *GormRepository) ClaimOutboxMessages(ctx context.Context, owner string, ttl time.Duration, limit int) ([]model.OutboxMessage, error) {
	if owner == "" {
		return nil, fmt.Errorf("outbox claim owner is required")
	}
	if ttl <= 0 {
		ttl = outboxProcessingLease
	}
	if limit <= 0 {
		limit = 100
	}
	if s.DB.Dialector.Name() != "sqlite" {
		var claimed []model.OutboxMessage
		err := s.DB.WithContext(ctx).Raw(`
			WITH claimable AS (
			  (SELECT id, tenant_id, created_at
			   FROM outbox_message
			   WHERE status = 'PENDING' AND next_attempt IS NULL
			   ORDER BY created_at, id, tenant_id
			   LIMIT ?)
			  UNION ALL
			  (SELECT id, tenant_id, created_at
			   FROM outbox_message
			   WHERE status = 'PENDING'
			     AND next_attempt IS NOT NULL
			     AND next_attempt <= transaction_timestamp()
			   ORDER BY created_at, id, tenant_id
			   LIMIT ?)
			  UNION ALL
			  (SELECT id, tenant_id, created_at
			   FROM outbox_message
			   WHERE status = 'PROCESSING'
			     AND claim_expires_at IS NOT NULL
			     AND claim_expires_at <= transaction_timestamp()
			   ORDER BY created_at, id, tenant_id
			   LIMIT ?)
			  UNION ALL
			  (SELECT id, tenant_id, created_at
			   FROM outbox_message
			   WHERE status = 'PROCESSING'
			     AND claim_expires_at IS NULL
			     AND processing_started_at <= transaction_timestamp() - interval '5 minutes'
			   ORDER BY created_at, id, tenant_id
			   LIMIT ?)
			),
			bounded AS (
			  SELECT id, tenant_id, created_at
			  FROM claimable
			  ORDER BY created_at, id, tenant_id
			  LIMIT ?
			),
			candidates AS (
			  SELECT message.id, message.tenant_id
			  FROM outbox_message AS message
			  JOIN bounded
			    ON bounded.id = message.id AND bounded.tenant_id = message.tenant_id
			  ORDER BY message.created_at, message.id, message.tenant_id
			  FOR UPDATE OF message SKIP LOCKED
			)
			UPDATE outbox_message AS message
			SET status = 'PROCESSING', attempts = message.attempts + 1, last_error = '',
			    next_attempt = NULL, processing_started_at = transaction_timestamp(),
			    claim_owner = ?,
			    claim_expires_at = transaction_timestamp() + (? * interval '1 millisecond'),
			    claim_generation = message.claim_generation + 1
			FROM candidates
			WHERE message.id = candidates.id AND message.tenant_id = candidates.tenant_id
			RETURNING message.*
		`, limit, limit, limit, limit, limit, owner, ttl.Milliseconds()).Scan(&claimed).Error
		return claimed, err
	}

	messages, err := s.ListPendingOutboxMessages(ctx, time.Now().UTC(), limit)
	if err != nil {
		return nil, err
	}
	claimed := make([]model.OutboxMessage, 0, len(messages))
	for _, message := range messages {
		messageCtx := auth.WithPrincipal(ctx, auth.Principal{TenantID: message.TenantID})
		item, claimErr := s.ClaimOutboxMessage(messageCtx, message.ID, owner, ttl)
		if claimErr != nil {
			return nil, claimErr
		}
		if item != nil {
			claimed = append(claimed, *item)
		}
	}
	return claimed, nil
}

func (s *GormRepository) ClaimOutboxMessage(ctx context.Context, id, owner string, ttl time.Duration) (*model.OutboxMessage, error) {
	if owner == "" {
		return nil, fmt.Errorf("outbox claim owner is required")
	}
	if ttl <= 0 {
		ttl = outboxProcessingLease
	}
	if s.DB.Dialector.Name() != "sqlite" {
		var claimed model.OutboxMessage
		result := s.DB.WithContext(ctx).Raw(`
			UPDATE outbox_message
			SET status = 'PROCESSING', attempts = attempts + 1, last_error = '',
			    next_attempt = NULL, processing_started_at = transaction_timestamp(),
			    claim_owner = ?,
			    claim_expires_at = transaction_timestamp() + (? * interval '1 millisecond'),
			    claim_generation = claim_generation + 1
			WHERE tenant_id = ? AND id = ? AND (
			  (status = 'PENDING' AND (next_attempt IS NULL OR next_attempt <= transaction_timestamp()))
			  OR (status = 'PROCESSING' AND (
			    claim_expires_at <= transaction_timestamp()
			    OR (claim_expires_at IS NULL AND processing_started_at <= transaction_timestamp() - interval '5 minutes')
			  ))
			)
			RETURNING *
		`, owner, ttl.Milliseconds(), auth.TenantIDFromContext(ctx), id).Scan(&claimed)
		if result.Error != nil {
			return nil, result.Error
		}
		if claimed.ID == "" {
			return nil, nil
		}
		return &claimed, nil
	}
	claimedAt := time.Now().UTC()
	var candidate model.OutboxMessage
	if err := s.DB.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", auth.TenantIDFromContext(ctx), id).
		First(&candidate).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	pendingDue := candidate.Status == "PENDING" &&
		(candidate.NextAttempt == nil || !candidate.NextAttempt.After(claimedAt))
	staleProcessing := candidate.Status == "PROCESSING" &&
		((candidate.ClaimExpiresAt != nil && !candidate.ClaimExpiresAt.After(claimedAt)) ||
			(candidate.ClaimExpiresAt == nil && candidate.ProcessingStartedAt != nil &&
				!candidate.ProcessingStartedAt.After(claimedAt.Add(-outboxProcessingLease))))
	if !pendingDue && !staleProcessing {
		return nil, nil
	}
	updates := map[string]any{
		"status":                "PROCESSING",
		"attempts":              gorm.Expr("attempts + ?", 1),
		"last_error":            "",
		"next_attempt":          nil,
		"processing_started_at": claimedAt,
		"claim_owner":           owner,
		"claim_expires_at":      claimedAt.Add(ttl),
		"claim_generation":      gorm.Expr("claim_generation + ?", 1),
	}

	result := s.DB.WithContext(ctx).
		Model(&model.OutboxMessage{}).
		Where("tenant_id = ? AND id = ? AND claim_generation = ?",
			auth.TenantIDFromContext(ctx), id, candidate.ClaimGeneration).
		Updates(updates)

	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	var claimed model.OutboxMessage
	if err := s.DB.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", auth.TenantIDFromContext(ctx), id).
		First(&claimed).Error; err != nil {
		return nil, err
	}
	return &claimed, nil
}

func (s *GormRepository) MarkOutboxMessagePublishFailed(
	ctx context.Context, id, owner string, generation int64, lastError string, retryDelay time.Duration,
) error {
	updates := map[string]any{
		"status": "PENDING", "last_error": lastError, "processing_started_at": nil,
		"claim_owner": nil, "claim_expires_at": nil,
	}
	query := s.DB.WithContext(ctx).Model(&model.OutboxMessage{}).
		Where("tenant_id = ? AND id = ? AND claim_owner = ? AND claim_generation = ?",
			auth.TenantIDFromContext(ctx), id, owner, generation)
	if s.DB.Dialector.Name() == "sqlite" {
		now := time.Now().UTC()
		query = query.Where("claim_expires_at > ?", now)
		updates["next_attempt"] = now.Add(retryDelay)
	} else {
		query = query.Where("claim_expires_at > transaction_timestamp()")
		updates["next_attempt"] = gorm.Expr("transaction_timestamp() + (? * interval '1 millisecond')", retryDelay.Milliseconds())
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return repository.ErrLeaseLost
	}
	return nil
}

func (s *GormRepository) MarkOutboxMessageTerminalFailed(
	ctx context.Context, id, owner string, generation int64, lastError string,
) error {
	query := s.DB.WithContext(ctx).
		Model(&model.OutboxMessage{}).
		Where("tenant_id = ? AND id = ? AND claim_owner = ? AND claim_generation = ?",
			auth.TenantIDFromContext(ctx), id, owner, generation)
	updates := map[string]any{
		"status": "FAILED", "last_error": lastError, "next_attempt": nil,
		"processing_started_at": nil, "claim_owner": nil, "claim_expires_at": nil,
	}
	if s.DB.Dialector.Name() == "sqlite" {
		now := time.Now().UTC()
		query = query.Where("claim_expires_at > ?", now)
		updates["published_at"] = now
	} else {
		query = query.Where("claim_expires_at > transaction_timestamp()")
		updates["published_at"] = gorm.Expr("transaction_timestamp()")
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return repository.ErrLeaseLost
	}
	return nil
}

func (s *GormRepository) MarkOutboxMessagePublished(ctx context.Context, id, owner string, generation int64) error {
	query := s.DB.WithContext(ctx).
		Model(&model.OutboxMessage{}).
		Where("tenant_id = ? AND id = ? AND claim_owner = ? AND claim_generation = ?",
			auth.TenantIDFromContext(ctx), id, owner, generation)
	updates := map[string]any{
		"status": "PUBLISHED", "last_error": "", "next_attempt": nil,
		"processing_started_at": nil, "claim_owner": nil, "claim_expires_at": nil,
	}
	if s.DB.Dialector.Name() == "sqlite" {
		now := time.Now().UTC()
		query = query.Where("claim_expires_at > ?", now)
		updates["published_at"] = now
	} else {
		query = query.Where("claim_expires_at > transaction_timestamp()")
		updates["published_at"] = gorm.Expr("transaction_timestamp()")
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return repository.ErrLeaseLost
	}
	return nil
}

func (s *GormRepository) MarkOutboxMessagesPublished(ctx context.Context, owner string, messages []model.OutboxMessage) error {
	if len(messages) == 0 {
		return nil
	}
	if s.DB.Dialector.Name() == "sqlite" {
		for _, message := range messages {
			result := s.DB.WithContext(ctx).
				Where("tenant_id = ? AND id = ? AND claim_owner = ? AND claim_generation = ? AND claim_expires_at > ?",
					message.TenantID, message.ID, owner, message.ClaimGeneration, time.Now().UTC()).
				Delete(&model.OutboxMessage{})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return repository.ErrLeaseLost
			}
		}
		return nil
	}

	type claim struct {
		ID              string `json:"id"`
		TenantID        string `json:"tenant_id"`
		ClaimGeneration int64  `json:"claim_generation"`
	}
	claims := make([]claim, 0, len(messages))
	for _, message := range messages {
		claims = append(claims, claim{
			ID: message.ID, TenantID: message.TenantID, ClaimGeneration: message.ClaimGeneration,
		})
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return fmt.Errorf("marshal outbox publish claims: %w", err)
	}
	var completedIDs []string
	result := s.DB.WithContext(ctx).Raw(`
		WITH claims AS (
		  SELECT *
		  FROM jsonb_to_recordset(?::jsonb)
		       AS claim(id text, tenant_id text, claim_generation bigint)
		)
		DELETE FROM outbox_message AS message
		USING claims
		WHERE message.id = claims.id
		  AND message.tenant_id = claims.tenant_id
		  AND message.claim_owner = ?
		  AND message.claim_generation = claims.claim_generation
		  AND message.claim_expires_at > transaction_timestamp()
		RETURNING message.id
	`, string(payload), owner).Scan(&completedIDs)
	if result.Error != nil {
		return result.Error
	}
	if len(completedIDs) != len(messages) {
		return repository.ErrLeaseLost
	}
	return nil
}

func (s *GormRepository) CountPendingOutboxMessages(ctx context.Context, now time.Time) (int64, error) {
	var count int64
	if s.DB.Dialector.Name() != "sqlite" {
		err := s.DB.WithContext(ctx).Raw(`
			SELECT
			  (SELECT count(*) FROM outbox_message
			   WHERE status = 'PENDING'
			     AND (next_attempt IS NULL OR next_attempt <= transaction_timestamp()))
			  +
			  (SELECT count(*) FROM outbox_message
			   WHERE status = 'PROCESSING'
			     AND (claim_expires_at <= transaction_timestamp()
			          OR (claim_expires_at IS NULL AND processing_started_at <= transaction_timestamp() - interval '5 minutes')))
		`).Scan(&count).Error
		return count, err
	}
	err := s.DB.WithContext(ctx).
		Model(&model.OutboxMessage{}).
		Scopes(claimableOutboxMessages(now)).
		Count(&count).Error
	if err != nil {
		return 0, err
	}

	return count, nil
}

func (s *GormRepository) DeletePublishedOutboxMessagesBefore(
	ctx context.Context, cutoff time.Time, limit int,
) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	if s.DB.Dialector.Name() != "sqlite" {
		type deletedKey struct {
			ID       string
			TenantID string
		}
		var deleted []deletedKey
		err := s.DB.WithContext(ctx).Raw(`
			WITH candidates AS (
			  SELECT message.id, message.tenant_id
			  FROM outbox_message AS message
			  WHERE message.status = 'PUBLISHED'
			    AND message.published_at < ?
			  ORDER BY message.published_at, message.id, message.tenant_id
			  FOR UPDATE OF message SKIP LOCKED
			  LIMIT ?
			)
			DELETE FROM outbox_message AS message
			USING candidates
			WHERE message.id = candidates.id
			  AND message.tenant_id = candidates.tenant_id
			  AND message.status = 'PUBLISHED'
			  AND message.published_at < ?
			RETURNING message.id, message.tenant_id
		`, cutoff, limit, cutoff).Scan(&deleted).Error
		return int64(len(deleted)), err
	}

	var messages []model.OutboxMessage
	if err := s.DB.WithContext(ctx).
		Where("status = ? AND published_at < ?", "PUBLISHED", cutoff).
		Order("published_at asc, id asc, tenant_id asc").
		Limit(limit).
		Find(&messages).Error; err != nil {
		return 0, err
	}
	var deleted int64
	for _, message := range messages {
		result := s.DB.WithContext(ctx).
			Where("id = ? AND tenant_id = ? AND status = ? AND published_at < ?",
				message.ID, message.TenantID, "PUBLISHED", cutoff).
			Delete(&model.OutboxMessage{})
		if result.Error != nil {
			return deleted, result.Error
		}
		deleted += result.RowsAffected
	}
	return deleted, nil
}

func claimableOutboxMessages(now time.Time) func(*gorm.DB) *gorm.DB {
	legacyStaleBefore := now.Add(-outboxProcessingLease)
	return func(db *gorm.DB) *gorm.DB {
		return db.Where(
			"(status = ? AND (next_attempt IS NULL OR next_attempt <= ?)) OR "+
				"(status = ? AND (claim_expires_at <= ? OR "+
				"(claim_expires_at IS NULL AND processing_started_at <= ?)))",
			"PENDING", now,
			"PROCESSING", now, legacyStaleBefore,
		)
	}
}

func (s *GormRepository) ListDueTimers(ctx context.Context, now time.Time) ([]model.Timer, error) {
	var timers []model.Timer
	if err := s.DB.WithContext(ctx).Where("state = ? AND due_date <= ?", "CREATED", now).Find(&timers).Error; err != nil {
		return nil, err
	}
	return timers, nil
}

func (s *GormRepository) ListOverdueJobs(ctx context.Context, now time.Time) ([]model.Job, error) {
	var jobs []model.Job
	// Find Active jobs with a real DueDate set (exclude zero-value), past, and NOT yet breached.
	zeroDue := time.Time{}
	if err := s.DB.WithContext(ctx).Where(
		"state IN ? AND due_date > ? AND due_date <= ? AND breached_at IS NULL",
		[]string{"CREATED", "ACTIVATED"}, zeroDue, now,
	).Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *GormRepository) ListMessageSubscriptions(ctx context.Context, messageName, correlationKey string) ([]model.MessageSubscription, error) {
	query := s.DB.WithContext(ctx).Where("state = ? AND message_name = ?", "OPEN", messageName)
	if correlationKey != "" {
		query = query.Where("correlation_key = ?", correlationKey)
	}

	var subs []model.MessageSubscription
	if err := query.Find(&subs).Error; err != nil {
		return nil, err
	}
	return subs, nil
}

// --- QUERY HELPERS ---

func (s *GormRepository) GetProcessByBpmnProcessID(ctx context.Context, bpmnProcessID string) (*model.Process, error) {
	var p model.Process
	if err := s.DB.WithContext(ctx).Where("bpmn_process_id = ?", bpmnProcessID).Order("version desc").First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *GormRepository) GetProcessByBpmnProcessIDAndVersion(ctx context.Context, bpmnProcessID string, version int) (*model.Process, error) {
	var p model.Process
	if err := s.DB.WithContext(ctx).Where("bpmn_process_id = ? AND version = ?", bpmnProcessID, version).First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *GormRepository) GetProcessInstanceWithState(ctx context.Context, key int64) (*model.ProcessInstance, []model.ElementInstance, []model.Variable, error) {
	var pi model.ProcessInstance
	if err := s.DB.WithContext(ctx).First(&pi, "key = ?", key).Error; err != nil {
		return nil, nil, nil, err
	}

	var elements []model.ElementInstance
	if err := s.DB.WithContext(ctx).Where("process_instance_key = ?", key).Find(&elements).Error; err != nil {
		return nil, nil, nil, err
	}

	var variables []model.Variable
	if err := s.DB.WithContext(ctx).Where("process_instance_key = ?", key).Find(&variables).Error; err != nil {
		return nil, nil, nil, err
	}

	return &pi, elements, variables, nil
}

func (s *GormRepository) GetElementInstance(ctx context.Context, key int64) (*model.ElementInstance, error) {
	var el model.ElementInstance
	if err := s.DB.WithContext(ctx).First(&el, "key = ?", key).Error; err != nil {
		return nil, err
	}
	return &el, nil
}

func (s *GormRepository) ListActiveElementInstances(ctx context.Context, processInstanceKey int64) ([]model.ElementInstance, error) {
	return s.listActiveElementInstances(ctx, processInstanceKey, nil)
}

func (s *GormRepository) ListActiveElementInstancesByTypes(ctx context.Context, processInstanceKey int64, elementTypes []string) ([]model.ElementInstance, error) {
	return s.listActiveElementInstances(ctx, processInstanceKey, elementTypes)
}

func (s *GormRepository) listActiveElementInstances(ctx context.Context, processInstanceKey int64, elementTypes []string) ([]model.ElementInstance, error) {
	var elements []model.ElementInstance
	// Keep this list identical to the idx_element_active_scan predicate
	// (migration 0006) or cross-instance scans fall back to full table scans.
	query := s.DB.WithContext(ctx).Where("state IN ?", []string{"ACTIVATED", "ACTIVATING", "COMPLETING"})
	if processInstanceKey != 0 {
		query = query.Where("process_instance_key = ?", processInstanceKey)
	}
	if len(elementTypes) > 0 {
		query = query.Where("bpmn_element_type IN ?", elementTypes)
	}
	if err := query.Find(&elements).Error; err != nil {
		return nil, err
	}
	return elements, nil
}

func (s *GormRepository) ListActiveProcessInstances(ctx context.Context) ([]*model.ProcessInstance, error) {
	var instances []*model.ProcessInstance
	if err := s.DB.WithContext(ctx).Where("state = ?", "ACTIVE").Find(&instances).Error; err != nil {
		return nil, err
	}
	return instances, nil
}

func (s *GormRepository) ListProcessInstancesByState(ctx context.Context, state string, limit int) ([]*model.ProcessInstance, error) {
	var instances []*model.ProcessInstance
	query := s.DB.WithContext(ctx)
	if state != "" {
		query = query.Where("state = ?", state)
	}
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Order("end_time desc, created_at desc").Find(&instances).Error; err != nil {
		return nil, err
	}
	return instances, nil
}

func (s *GormRepository) SearchUserTaskJobs(ctx context.Context, jobType string, filter repository.UserTaskFilter) ([]model.Job, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	query := s.DB.WithContext(ctx).Where("type IN ?", compatibleJobTypes(jobType))
	switch filter.State {
	case repository.UserTaskStateCompleted:
		query = query.Where("state = ?", model.JobStateCompleted)
	case repository.UserTaskStateAll:
	default:
		query = query.Where("state <> ?", model.JobStateCompleted)
	}
	if filter.ProcessInstanceKey != 0 {
		query = query.Where("process_instance_key = ?", filter.ProcessInstanceKey)
	}

	var clauses []string
	var args []any
	matchOwner := func(value string) {
		clauses = append(clauses, "LOWER(TRIM(assignee)) = ?", "LOWER(TRIM(worker)) = ?")
		args = append(args, value, value)
	}
	if assignee := strings.ToLower(strings.TrimSpace(filter.Assignee)); assignee != "" {
		matchOwner(assignee)
	}
	if user := strings.ToLower(strings.TrimSpace(filter.CandidateUser)); user != "" {
		matchOwner(user)
		clauses = append(clauses, assignmentListContains("candidate_users"))
		args = append(args, assignmentListPattern(user))
	}
	for _, group := range normalizeQueryValues(filter.CandidateGroups) {
		clauses = append(clauses, assignmentListContains("candidate_groups"))
		args = append(args, assignmentListPattern(group))
	}
	if len(clauses) > 0 {
		if filter.IncludeUnassigned {
			clauses = append(clauses, "(COALESCE(TRIM(assignee), '') = '' AND COALESCE(TRIM(candidate_users), '') = '' AND COALESCE(TRIM(candidate_groups), '') = '' AND COALESCE(TRIM(worker), '') = '')")
		}
		query = query.Where("("+strings.Join(clauses, " OR ")+")", args...)
	}

	var jobs []model.Job
	if err := query.Order("created_at asc").Limit(limit).Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

// Candidate lists are stored comma-separated; spaces around commas are
// normalized so "a, b" and "a,b" match the same entries.
func assignmentListContains(column string) string {
	return "(',' || LOWER(REPLACE(REPLACE(TRIM(COALESCE(" + column + ", '')), ', ', ','), ' ,', ',')) || ',') LIKE ? ESCAPE '\\'"
}

func assignmentListPattern(value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
	return "%," + escaped + ",%"
}

func compatibleJobTypes(jobType string) []string {
	switch strings.TrimSpace(jobType) {
	default:
		return []string{jobType}
	}
}

func normalizeQueryValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func (s *GormRepository) ListProcesses(ctx context.Context) ([]*model.Process, error) {
	var processes []*model.Process
	if err := s.DB.WithContext(ctx).Order("created_at desc").Find(&processes).Error; err != nil {
		return nil, err
	}
	return processes, nil
}

// ListProcessesForEventStarts supports message/signal/escalation start fan-out.
func (s *GormRepository) ListProcessesForEventStarts(ctx context.Context) ([]*model.Process, error) {
	return s.ListProcesses(ctx)
}
