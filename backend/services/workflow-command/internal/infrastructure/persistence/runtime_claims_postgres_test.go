package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/domain/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostgresThreeOwnerRuntimeClaimsAndDelayedFence(t *testing.T) {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN is required for PostgreSQL runtime claim evidence")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewGormRepository(db)
	const (
		firstKey    int64 = 8_800_000
		timerCount        = 30
		delayedKey  int64 = firstKey + timerCount
		cancelKey   int64 = delayedKey + 1
		staleOutbox       = "runtime-delayed-stale-effect"
		liveOutbox        = "runtime-takeover-live-effect"
	)
	t.Cleanup(func() {
		_ = db.Exec("DELETE FROM timer WHERE key BETWEEN ? AND ?", firstKey, cancelKey).Error
		_ = db.Exec("DELETE FROM outbox_message WHERE id IN (?, ?)", staleOutbox, liveOutbox).Error
	})

	due := time.Now().UTC().Add(-time.Minute)
	for index := 0; index < timerCount; index++ {
		tenant := "runtime-tenant-" + string(rune('a'+index%3))
		ctx := auth.WithPrincipal(context.Background(), auth.Principal{Subject: "test", TenantID: tenant})
		if err := repo.CreateTimer(ctx, &model.Timer{
			Key: firstKey + int64(index), ID: "runtime-claim-evidence",
			State: "CREATED", DueDate: due, CreatedAt: due,
		}); err != nil {
			t.Fatal(err)
		}
	}

	type claimedBatch struct {
		owner  string
		timers []model.Timer
		err    error
	}
	results := make(chan claimedBatch, 3)
	var owners sync.WaitGroup
	for _, owner := range []string{"runtime-a", "runtime-b", "runtime-c"} {
		owners.Add(1)
		go func(owner string) {
			defer owners.Done()
			timers, claimErr := repo.ClaimDueTimers(context.Background(), owner, time.Minute, 10)
			results <- claimedBatch{owner: owner, timers: timers, err: claimErr}
		}(owner)
	}
	owners.Wait()
	close(results)

	seen := make(map[int64]string, timerCount)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		for index := range result.timers {
			timer := &result.timers[index]
			if previous := seen[timer.Key]; previous != "" {
				t.Fatalf("timer %d claimed by both %s and %s", timer.Key, previous, result.owner)
			}
			seen[timer.Key] = result.owner
			timer.State = "TRIGGERED"
			itemCtx := auth.WithPrincipal(context.Background(), auth.Principal{
				Subject: "test", TenantID: timer.TenantID,
			})
			if err := repo.UpdateClaimedTimer(itemCtx, timer, result.owner, timer.ClaimGeneration); err != nil {
				t.Fatalf("complete timer %d: %v", timer.Key, err)
			}
		}
	}
	if len(seen) != timerCount {
		t.Fatalf("claimed %d timers, want %d", len(seen), timerCount)
	}
	var triggered int64
	if err := db.Raw(
		"SELECT count(*) FROM timer WHERE key >= ? AND key < ? AND state = ?",
		firstKey, delayedKey, "TRIGGERED",
	).Scan(&triggered).Error; err != nil {
		t.Fatal(err)
	}
	if triggered != timerCount {
		t.Fatalf("committed %d timer effects, want %d", triggered, timerCount)
	}

	defaultCtx := auth.WithPrincipal(context.Background(), auth.Principal{
		Subject: "test", TenantID: auth.DefaultTenantID,
	})
	if err := repo.CreateTimer(defaultCtx, &model.Timer{
		Key: delayedKey, ID: "delayed-owner", State: "CREATED", DueDate: due, CreatedAt: due,
	}); err != nil {
		t.Fatal(err)
	}
	first, err := repo.ClaimDueTimers(context.Background(), "runtime-delayed", time.Minute, 1)
	if err != nil || len(first) != 1 || first[0].Key != delayedKey {
		t.Fatalf("first delayed claim = %#v, err=%v", first, err)
	}
	stale := first[0]
	takeoverCompleted := make(chan struct{})
	formerOwnerResult := make(chan error, 1)
	go func() {
		<-takeoverCompleted
		formerOwnerResult <- repo.WithTx(defaultCtx, func(txRepo repository.Repository) error {
			if err := txRepo.LockClaimedTimer(defaultCtx, stale.Key, "runtime-delayed", stale.ClaimGeneration); err != nil {
				return err
			}
			return txRepo.CreateOutboxMessage(defaultCtx, &model.OutboxMessage{
				ID: staleOutbox, EventType: "TimerFired", Status: "PENDING", CreatedAt: time.Now().UTC(),
			})
		})
	}()

	if err := db.Exec(
		"UPDATE timer SET claim_expires_at = transaction_timestamp() - interval '1 second' WHERE key = ?",
		delayedKey,
	).Error; err != nil {
		t.Fatal(err)
	}
	second, err := repo.ClaimDueTimers(context.Background(), "runtime-takeover", time.Minute, 1)
	if err != nil || len(second) != 1 || second[0].Key != delayedKey {
		t.Fatalf("takeover claim = %#v, err=%v", second, err)
	}
	current := second[0]
	current.State = "TRIGGERED"
	if err := repo.WithTx(defaultCtx, func(txRepo repository.Repository) error {
		if err := txRepo.LockClaimedTimer(defaultCtx, current.Key, "runtime-takeover", current.ClaimGeneration); err != nil {
			return err
		}
		if err := txRepo.CreateOutboxMessage(defaultCtx, &model.OutboxMessage{
			ID: liveOutbox, EventType: "TimerFired", Status: "PENDING", CreatedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		return txRepo.UpdateClaimedTimer(defaultCtx, &current, "runtime-takeover", current.ClaimGeneration)
	}); err != nil {
		t.Fatalf("takeover completion: %v", err)
	}
	close(takeoverCompleted)
	if err := <-formerOwnerResult; !errors.Is(err, repository.ErrLeaseLost) {
		t.Fatalf("delayed former-owner transaction = %v, want ErrLeaseLost", err)
	}
	var durableEffects int64
	if err := db.Raw(
		"SELECT count(*) FROM outbox_message WHERE id IN (?, ?)", staleOutbox, liveOutbox,
	).Scan(&durableEffects).Error; err != nil {
		t.Fatal(err)
	}
	if durableEffects != 1 {
		t.Fatalf("durable timer side effects = %d, want exactly one", durableEffects)
	}

	if err := repo.CreateTimer(defaultCtx, &model.Timer{
		Key: cancelKey, ID: "cancelled-claim", State: "CREATED", DueDate: due, CreatedAt: due,
	}); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repo.ClaimDueTimers(cancelled, "runtime-disconnected", time.Minute, 1); err == nil {
		t.Fatal("cancelled claim unexpectedly succeeded")
	}
	afterCancel, err := repo.ClaimDueTimers(context.Background(), "runtime-reconnected", time.Minute, 1)
	if err != nil || len(afterCancel) != 1 || afterCancel[0].Key != cancelKey {
		t.Fatalf("reconnected claim = %#v, err=%v", afterCancel, err)
	}
}

func TestPostgresOverdueJobClaimsIgnoreUnsetDueDate(t *testing.T) {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN is required for PostgreSQL runtime claim evidence")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewGormRepository(db)
	const (
		unsetDueKey int64 = 8_800_100
		overdueKey  int64 = 8_800_101
	)
	t.Cleanup(func() {
		_ = db.Exec("DELETE FROM job WHERE key IN (?, ?)", unsetDueKey, overdueKey).Error
	})

	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		Subject: "test", TenantID: auth.DefaultTenantID,
	})
	now := time.Now().UTC()
	for _, job := range []model.Job{
		{Key: unsetDueKey, Type: "test", State: "CREATED", CreatedAt: now},
		{Key: overdueKey, Type: "test", State: "CREATED", DueDate: now.Add(-time.Minute), CreatedAt: now},
	} {
		job := job
		if err := repo.CreateJob(ctx, &job); err != nil {
			t.Fatal(err)
		}
	}

	claimed, err := repo.ClaimOverdueJobs(context.Background(), "runtime-sla", time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].Key != overdueKey {
		t.Fatalf("claimed overdue jobs = %#v, want only key %d", claimed, overdueKey)
	}
}

func TestPostgresConcurrentJobAndRuntimeWorkClaims(t *testing.T) {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN is required for PostgreSQL runtime claim evidence")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewGormRepository(db)
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		Subject: "test", TenantID: auth.DefaultTenantID,
	})
	const (
		firstJobKey  int64 = 8_800_200
		itemCount          = 18
		workType           = "POSTGRES_CLAIM_TEST"
		staleWorkKey       = "work-stale"
	)
	t.Cleanup(func() {
		_ = db.Exec("DELETE FROM job WHERE key >= ? AND key < ?", firstJobKey, firstJobKey+itemCount).Error
		_ = db.Exec("DELETE FROM runtime_work_item WHERE work_type = ?", workType).Error
	})

	due := time.Now().UTC().Add(-time.Minute)
	for index := 0; index < itemCount; index++ {
		if err := repo.CreateJob(ctx, &model.Job{
			Key: firstJobKey + int64(index), Type: "claim-test", State: "CREATED",
			DueDate: due, CreatedAt: due,
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.WithContext(ctx).Create(&model.RuntimeWorkItem{
			WorkType: model.RuntimeWorkType(workType), WorkKey: fmt.Sprintf("work-%02d", index),
			AvailableAt: due, Payload: []byte(`{}`), UpdatedAt: due,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	type jobBatch struct {
		owner string
		jobs  []model.Job
		err   error
	}
	jobResults := make(chan jobBatch, 3)
	var owners sync.WaitGroup
	for _, owner := range []string{"runtime-a", "runtime-b", "runtime-c"} {
		owners.Add(1)
		go func(owner string) {
			defer owners.Done()
			jobs, claimErr := repo.ClaimOverdueJobs(context.Background(), owner, time.Minute, itemCount/3)
			jobResults <- jobBatch{owner: owner, jobs: jobs, err: claimErr}
		}(owner)
	}
	owners.Wait()
	close(jobResults)

	seenJobs := make(map[int64]string, itemCount)
	for result := range jobResults {
		if result.err != nil {
			t.Fatal(result.err)
		}
		for index := range result.jobs {
			job := &result.jobs[index]
			if previous := seenJobs[job.Key]; previous != "" {
				t.Fatalf("job %d claimed by both %s and %s", job.Key, previous, result.owner)
			}
			seenJobs[job.Key] = result.owner
			breachedAt := time.Now().UTC()
			job.BreachedAt = &breachedAt
			if err := repo.UpdateClaimedJob(ctx, job, result.owner, job.ClaimGeneration); err != nil {
				t.Fatalf("complete job %d: %v", job.Key, err)
			}
		}
	}
	if len(seenJobs) != itemCount {
		t.Fatalf("claimed %d jobs, want %d", len(seenJobs), itemCount)
	}

	type workBatch struct {
		claims []model.RuntimeClaim
		err    error
	}
	workResults := make(chan workBatch, 3)
	for _, owner := range []string{"runtime-a", "runtime-b", "runtime-c"} {
		owners.Add(1)
		go func(owner string) {
			defer owners.Done()
			claims, claimErr := repo.ClaimRuntimeWork(
				context.Background(), workType, owner, time.Minute, itemCount/3,
			)
			workResults <- workBatch{claims: claims, err: claimErr}
		}(owner)
	}
	owners.Wait()
	close(workResults)

	seenWork := make(map[string]string, itemCount)
	for result := range workResults {
		if result.err != nil {
			t.Fatal(result.err)
		}
		for _, claim := range result.claims {
			if previous := seenWork[claim.Key]; previous != "" {
				t.Fatalf("work %s claimed by both %s and %s", claim.Key, previous, claim.Owner)
			}
			seenWork[claim.Key] = claim.Owner
			if err := repo.CompleteRuntimeWork(ctx, claim, time.Minute); err != nil {
				t.Fatalf("complete work %s: %v", claim.Key, err)
			}
		}
	}
	if len(seenWork) != itemCount {
		t.Fatalf("claimed %d work items, want %d", len(seenWork), itemCount)
	}

	if err := db.WithContext(ctx).Create(&model.RuntimeWorkItem{
		WorkType: model.RuntimeWorkType(workType), WorkKey: staleWorkKey,
		AvailableAt: due, Payload: []byte(`{}`), UpdatedAt: due,
	}).Error; err != nil {
		t.Fatal(err)
	}
	first, err := repo.ClaimRuntimeWork(context.Background(), workType, "former-owner", time.Minute, 1)
	if err != nil || len(first) != 1 || first[0].Key != staleWorkKey {
		t.Fatalf("former work claim = %#v, err=%v", first, err)
	}
	if err := db.Exec(`
		UPDATE runtime_work_item
		SET claim_expires_at = transaction_timestamp() - interval '1 second'
		WHERE work_type = ? AND work_key = ?
	`, workType, staleWorkKey).Error; err != nil {
		t.Fatal(err)
	}
	second, err := repo.ClaimRuntimeWork(context.Background(), workType, "takeover-owner", time.Minute, 1)
	if err != nil || len(second) != 1 || second[0].Key != staleWorkKey {
		t.Fatalf("takeover work claim = %#v, err=%v", second, err)
	}
	if err := repo.CompleteRuntimeWork(ctx, first[0], time.Second); !errors.Is(err, repository.ErrLeaseLost) {
		t.Fatalf("stale work completion = %v, want ErrLeaseLost", err)
	}
	if err := repo.CompleteRuntimeWork(ctx, second[0], 2*time.Second); err != nil {
		t.Fatalf("takeover work completion: %v", err)
	}
	var secondsUntilAvailable float64
	if err := db.Raw(`
		SELECT extract(epoch FROM (available_at - transaction_timestamp()))
		FROM runtime_work_item WHERE work_type = ? AND work_key = ?
	`, workType, staleWorkKey).Scan(&secondsUntilAvailable).Error; err != nil {
		t.Fatal(err)
	}
	if secondsUntilAvailable < 1 || secondsUntilAvailable > 3 {
		t.Fatalf("database-time reschedule delta = %f seconds, want approximately 2", secondsUntilAvailable)
	}
}

func TestPostgresSearchUserTaskJobs(t *testing.T) {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN is required for PostgreSQL user task search evidence")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	const base int64 = 8_900_000
	t.Cleanup(func() {
		_ = db.Exec("DELETE FROM job WHERE key BETWEEN ? AND ?", base+201, base+208).Error
	})
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Subject: "test", TenantID: "user-task-search-evidence"})
	runSearchUserTaskJobsCases(t, NewGormRepository(db), ctx, base)
}
