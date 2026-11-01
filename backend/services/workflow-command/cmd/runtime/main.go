package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/logger"
	"github.com/artificialflow/artificialflow/backend/libs/metrics"
	"github.com/artificialflow/artificialflow/backend/libs/tracing"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/application"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/domain/repository"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/infrastructure/messaging"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/infrastructure/persistence"
)

func main() {
	log := logger.New("workflow-runtime")
	ctx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()

	runtimeEnabled := envBool("RUNTIME_ENABLED", true)
	if !runtimeEnabled {
		log.Info(ctx, "runtime scheduler disabled by config", nil)
		return
	}

	// Initialize tracing for runtime worker.
	tp, err := tracing.InitTracer(ctx, "workflow-runtime")
	if err != nil {
		log.Error(ctx, "failed to init tracer", map[string]any{"error": err.Error()})
	} else {
		defer func() {
			if err := tp.Shutdown(context.Background()); err != nil {
				log.Error(ctx, "failed to shutdown tracer", map[string]any{"error": err.Error()})
			}
		}()
	}

	pgDSN := os.Getenv("PG_DSN")
	if pgDSN == "" {
		pgDSN = "host=localhost user=artificialflow password=password dbname=workflow_db port=5433 sslmode=disable TimeZone=UTC"
	}

	tickInterval := parseDurationEnv("RUNTIME_TICK_INTERVAL", time.Second)
	taskTimeout := parseDurationEnv("RUNTIME_TASK_TIMEOUT", 5*time.Second)
	schedulerMode := strings.ToLower(envOrDefault("RUNTIME_SCHEDULER_MODE", "compat"))
	leaseHolder := envOrDefault("RUNTIME_LEASE_HOLDER", fmt.Sprintf("%s-%d", hostnameOrUnknown(), os.Getpid()))
	leaseTTL := parseDurationEnv("RUNTIME_LEASE_TTL", 15*time.Second)
	if leaseTTL < 2*tickInterval {
		leaseTTL = 2 * tickInterval
	}
	runtimeClaimBatch := envInt("RUNTIME_CLAIM_BATCH_SIZE", 100)

	repo, err := persistence.NewPostgresRepository(pgDSN)
	if err != nil {
		log.Error(ctx, "failed to initialize storage", map[string]any{"error": err.Error()})
		os.Exit(1)
	}

	kafkaBrokers := envOrDefault("KAFKA_BROKERS", "localhost:9092")
	eventTopic := envOrDefault("KAFKA_TOPIC_EVENTS", "workflow.events.v1")
	var publisher messaging.EventPublisher
	if kafkaBrokers != "" {
		publisher = messaging.NewKafkaPublisher(splitAndTrim(kafkaBrokers), eventTopic)
		log.Info(ctx, "kafka publisher initialized", map[string]any{
			"brokers": kafkaBrokers,
			"topic":   eventTopic,
		})
	} else {
		publisher = &messaging.NoOpPublisher{}
		log.Warn(ctx, "kafka publisher disabled (using no-op)", nil)
	}
	defer func() {
		if publisher != nil {
			_ = publisher.Close()
		}
	}()

	outboxRelayBatchSize := envInt("OUTBOX_RELAY_BATCH_SIZE", 200)
	if outboxRelayBatchSize <= 0 {
		outboxRelayBatchSize = 200
	}
	outboxRelayInterval := parseDurationEnv("OUTBOX_RELAY_INTERVAL", tickInterval)
	if outboxRelayInterval <= 0 {
		outboxRelayInterval = tickInterval
	}
	outboxRelayMaxAttempts := envInt("OUTBOX_RELAY_MAX_ATTEMPTS", 10)
	outboxPublishedRetention := parseDurationEnv("OUTBOX_PUBLISHED_RETENTION", 30*time.Minute)
	outboxCleanupInterval := parseDurationEnv("OUTBOX_CLEANUP_INTERVAL", 10*time.Second)
	if outboxCleanupInterval <= 0 {
		outboxCleanupInterval = 10 * time.Second
	}
	outboxCleanupBatchSize := envInt("OUTBOX_CLEANUP_BATCH_SIZE", 5000)
	if outboxCleanupBatchSize <= 0 {
		outboxCleanupBatchSize = 5000
	}
	idempotencyCleanupEnabled := envBool("IDEMPOTENCY_CLEANUP_ENABLED", true)
	idempotencyRetention := parseDurationEnv("IDEMPOTENCY_RETENTION", 168*time.Hour)
	idempotencyCleanupInterval := parseDurationEnv("IDEMPOTENCY_CLEANUP_INTERVAL", time.Hour)
	idempotencyCleanupTimeout := parseDurationEnv("IDEMPOTENCY_CLEANUP_TIMEOUT", 10*time.Second)
	idempotencyCleanupBatchSize := envInt("IDEMPOTENCY_CLEANUP_BATCH_SIZE", 500)
	if idempotencyCleanupBatchSize <= 0 {
		idempotencyCleanupBatchSize = 500
	}

	eng := application.NewEngine(repo, publisher)
	eng.SetFlushOutboxAfterCommit(false)
	eng.SetOutboxMaxAttempts(outboxRelayMaxAttempts)
	metrics.NewOutboxCollector(func() metrics.OutboxSnapshot {
		s := eng.MetricsSnapshot()
		return metrics.OutboxSnapshot{
			Pending: s.OutboxPending, PublishSuccess: s.OutboxPublishSuccess,
			PublishFailure: s.OutboxPublishFailure, Retries: s.OutboxRetries,
			DeadLetters: s.OutboxDeadLetters, PublishLagSec: s.OutboxPublishLagSec,
		}
	})

	if schedulerMode == "multi-active" {
		go func() {
			ticker := time.NewTicker(outboxRelayInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					relayCtx, cancel := context.WithTimeout(ctx, taskTimeout)
					if _, err := eng.RunOutboxRelayCycle(relayCtx, outboxRelayBatchSize); err != nil {
						metrics.IncRuntimeFailure("repository")
						log.Error(relayCtx, "outbox relay cycle failed", map[string]any{"error": err.Error()})
					}
					cancel()
				}
			}
		}()
	}

	if schedulerMode == "multi-active" && outboxPublishedRetention > 0 {
		go func() {
			ticker := time.NewTicker(outboxCleanupInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					cleanupCtx, cancel := context.WithTimeout(ctx, taskTimeout)
					result, cleanupErr := eng.RunOutboxCleanup(
						cleanupCtx, outboxPublishedRetention, outboxCleanupBatchSize,
					)
					cancel()
					if cleanupErr != nil {
						log.Warn(ctx, "outbox cleanup failed", map[string]any{"error": cleanupErr.Error()})
						continue
					}
					if result.Deleted > 0 {
						log.Info(ctx, "outbox cleanup completed", map[string]any{
							"deleted": result.Deleted, "cutoff": result.Cutoff.Format(time.RFC3339),
						})
					}
				}
			}
		}()
	}

	if schedulerMode != "multi-active" && idempotencyCleanupEnabled && idempotencyRetention > 0 {
		go func() {
			ticker := time.NewTicker(idempotencyCleanupInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					cleanupCtx, cancel := context.WithTimeout(context.Background(), idempotencyCleanupTimeout)
					tenants, err := repo.EnsureRuntimeWorkTenants(cleanupCtx)
					if err != nil {
						cancel()
						log.Error(ctx, "idempotency cleanup failed", map[string]any{"error": err.Error()})
						continue
					}
					for _, tenant := range tenants {
						tenantBaseCtx, tenantCancel := context.WithTimeout(context.Background(), idempotencyCleanupTimeout)
						tenantCtx := auth.WithPrincipal(tenantBaseCtx, auth.Principal{
							Subject: "workflow-runtime", TenantID: tenant,
						})
						result, cleanupErr := eng.RunIdempotencyCleanup(
							tenantCtx, idempotencyRetention, idempotencyCleanupBatchSize,
						)
						if cleanupErr != nil {
							log.Error(ctx, "idempotency cleanup failed", map[string]any{
								"tenant_id": tenant, "error": cleanupErr.Error(),
							})
							tenantCancel()
							continue
						}
						if result.Deleted > 0 {
							log.Info(ctx, "idempotency cleanup completed", map[string]any{
								"tenant_id": tenant, "deleted": result.Deleted,
								"cutoff": result.Cutoff.Format(time.RFC3339),
							})
						}
						tenantCancel()
					}
					cancel()
				}
			}
		}()
	}

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	metricsAddr := envOrDefault("METRICS_ADDR", ":9091")
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", metrics.Handler())
		mux.HandleFunc("/livez", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		})
		mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
			checkCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			err := repo.Ready(checkCtx)
			repositoryReady := err == nil
			brokerReady := true
			if checker, ok := publisher.(interface{ Ready(context.Context) error }); ok {
				brokerReady = checker.Ready(checkCtx) == nil
			}
			ready := repositoryReady && brokerReady
			metrics.SetDependencyReady("workflow-runtime", "postgres", repositoryReady)
			metrics.SetDependencyReady("workflow-runtime", "broker", brokerReady)
			metrics.SetSchemaReady("workflow-runtime", repositoryReady)
			w.Header().Set("Content-Type", "application/json")
			if !ready {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"not-ready","dependency":"postgres-or-broker"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"ready"}`))
		})
		srv := &http.Server{Addr: metricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		}()
		log.Info(ctx, "runtime metrics server started", map[string]any{"addr": metricsAddr})
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error(ctx, "runtime metrics server failed", map[string]any{"error": err.Error()})
		}
	}()

	log.Info(ctx, "runtime scheduler started", map[string]any{
		"tick_interval":               tickInterval.String(),
		"task_timeout":                taskTimeout.String(),
		"outbox_relay_interval":       outboxRelayInterval.String(),
		"outbox_relay_batch_size":     outboxRelayBatchSize,
		"outbox_relay_max_attempts":   outboxRelayMaxAttempts,
		"outbox_published_retention":  outboxPublishedRetention.String(),
		"outbox_cleanup_interval":     outboxCleanupInterval.String(),
		"outbox_cleanup_batch_size":   outboxCleanupBatchSize,
		"outbox_cleanup_enabled":      schedulerMode == "multi-active" && outboxPublishedRetention > 0,
		"idempotency_cleanup_enabled": idempotencyCleanupEnabled && idempotencyRetention > 0,
		"lease_holder":                leaseHolder,
		"lease_ttl":                   leaseTTL.String(),
		"scheduler_mode":              schedulerMode,
	})

	for {
		select {
		case sig := <-sigCh:
			log.Info(ctx, "received shutdown signal", map[string]any{"signal": sig.String()})
			cancelRoot()
			return
		case <-ticker.C:
			runCtx, cancel := context.WithTimeout(context.Background(), taskTimeout)
			if schedulerMode == "multi-active" {
				claimCtx, claimCancel := context.WithTimeout(context.Background(), taskTimeout)
				if err := eng.CheckTimersClaimed(claimCtx, leaseHolder, leaseTTL, runtimeClaimBatch); err != nil {
					metrics.IncRuntimeFailure("timer")
					log.Error(claimCtx, "claimed timer cycle failed", map[string]any{"error": err.Error()})
				}
				if err := eng.CheckSLAsClaimed(claimCtx, leaseHolder, leaseTTL, runtimeClaimBatch); err != nil {
					metrics.IncRuntimeFailure("job")
					log.Error(claimCtx, "claimed SLA cycle failed", map[string]any{"error": err.Error()})
				}
				if idempotencyCleanupEnabled && idempotencyRetention > 0 {
					claims, err := repo.ClaimRuntimeWork(claimCtx, "IDEMPOTENCY_MAINTENANCE", leaseHolder, leaseTTL, 1)
					if err != nil {
						metrics.IncRuntimeFailure("claim")
						log.Error(claimCtx, "maintenance claim failed", map[string]any{"error": err.Error()})
					} else if len(claims) > 0 {
						maintenanceCtx := auth.WithPrincipal(claimCtx, auth.Principal{
							Subject: "workflow-runtime", TenantID: claims[0].TenantID,
						})
						metrics.RecordRuntimeClaim("work", "acquired")
						_, cleanupErr := eng.RunClaimedIdempotencyCleanup(
							maintenanceCtx, claims[0], idempotencyRetention,
							idempotencyCleanupBatchSize, idempotencyCleanupInterval,
						)
						if errors.Is(cleanupErr, repository.ErrLeaseLost) {
							metrics.RecordRuntimeClaim("work", "stale")
							metrics.IncRuntimeFailure("fence")
							log.Error(claimCtx, "maintenance fence lost", map[string]any{"error": cleanupErr.Error()})
						} else if cleanupErr != nil {
							log.Error(claimCtx, "idempotency cleanup failed", map[string]any{"error": cleanupErr.Error()})
						}
					}
				}
				claimCancel()
				cancel()
				continue
			}
			tenants, err := repo.EnsureRuntimeWorkTenants(runCtx)
			if err != nil {
				metrics.IncRuntimeFailure("repository")
				log.Error(runCtx, "runtime tenant discovery failed", map[string]any{"error": err.Error()})
				cancel()
				continue
			}
			acquired, err := repo.TryAcquireRuntimeSchedulerLease(runCtx, leaseHolder, leaseTTL)
			if err != nil {
				metrics.IncRuntimeFailure("claim")
				log.Error(runCtx, "runtime scheduler lease acquire failed", map[string]any{"error": err.Error()})
				cancel()
				continue
			}
			if !acquired {
				cancel()
				continue
			}
			if _, err := eng.RunOutboxRelayCycle(runCtx, outboxRelayBatchSize); err != nil {
				log.Error(runCtx, "outbox relay cycle failed", map[string]any{"error": err.Error()})
			}
			for _, tenant := range tenants {
				tenantBaseCtx, tenantCancel := context.WithTimeout(context.Background(), taskTimeout)
				tenantCtx := auth.WithPrincipal(tenantBaseCtx, auth.Principal{
					Subject: "workflow-runtime", TenantID: tenant,
				})
				if err := eng.CheckTimers(tenantCtx); err != nil {
					log.Error(runCtx, "check timers failed", map[string]any{
						"tenant_id": tenant, "error": err.Error(),
					})
				}
				if err := eng.CheckSLAs(tenantCtx); err != nil {
					log.Error(runCtx, "check slas failed", map[string]any{
						"tenant_id": tenant, "error": err.Error(),
					})
				}
				tenantCancel()
			}
			cancel()
		}
	}
}

func hostnameOrUnknown() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "unknown"
	}
	return host
}

func envOrDefault(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

func envInt(key string, defaultValue int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultValue
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return defaultValue
	}
	return v
}

func splitAndTrim(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func parseDurationEnv(key string, defaultValue time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultValue
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return defaultValue
	}
	return d
}

func envBool(key string, defaultValue bool) bool {
	val := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch val {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}
