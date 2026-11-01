package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/httpproblem"
	"github.com/artificialflow/artificialflow/backend/libs/iam"
	"github.com/artificialflow/artificialflow/backend/libs/id"
	"github.com/artificialflow/artificialflow/backend/libs/logger"
	"github.com/artificialflow/artificialflow/backend/libs/metrics"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/libs/tracing"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/application"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/infrastructure/persistence"
	api "github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/interfaces/http"

	pb "github.com/artificialflow/artificialflow/backend/api/v1/go"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/infrastructure/messaging"
	grpcImpl "github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/interfaces/grpc"

	"github.com/gorilla/mux"
	"github.com/rs/cors"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc"
)

func main() {
	log := logger.New("workflow-command")
	nodeID, err := id.ResolveSnowflakeNodeID(os.Getenv("SNOWFLAKE_NODE_ID"), os.Getenv("POD_NAME"))
	if err != nil {
		log.Error(context.Background(), "invalid snowflake node identity", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
	id.InitDefaultNode(nodeID)
	// Use explicit context for main to allow cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tp, err := tracing.InitTracer(ctx, "workflow-command")
	if err != nil {
		log.Error(ctx, "failed to initialize tracing", map[string]any{"error": err.Error()})
	} else {
		defer func() { _ = tp.Shutdown(context.Background()) }()
	}

	pgDSN := os.Getenv("PG_DSN")
	if pgDSN == "" {
		pgDSN = "host=localhost user=artificialflow password=password dbname=workflow_db port=5433 sslmode=disable TimeZone=UTC"
	}

	serverAddr := os.Getenv("SERVER_ADDR")
	if serverAddr == "" {
		serverAddr = "0.0.0.0:8080"
	}

	// Auth Configuration
	deploymentConfig, err := iam.ResolveDeploymentConfigFromEnv()
	if err != nil {
		log.Error(ctx, "invalid IAM deployment configuration", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
	authConfig := deploymentConfig.AuthConfig
	internalIssuer := authConfig.InternalIssuerURL
	externalIssuer := authConfig.ExternalIssuerURL
	clientID := authConfig.ClientID

	log.Info(ctx, "initializing workflow-command service", map[string]any{
		"server_addr":     serverAddr,
		"internal_issuer": internalIssuer,
		"external_issuer": externalIssuer,
		"auth_client_id":  clientID,
		"auth_mode":       authConfig.TokenValidationMode,
		"auth_enabled":    authConfig.Enabled(),
		"snowflake_node":  nodeID,
	})
	if authConfig.TokenValidationMode == auth.TokenModeIntrospection && strings.TrimSpace(authConfig.IntrospectionURL) == "" {
		log.Error(ctx, "introspection auth mode requires introspection URL", map[string]any{
			"env": "AUTH_INTROSPECTION_URL",
		})
		os.Exit(1)
	}

	// Initialize Auth Middleware
	var authMiddleware *auth.Middleware

	log.Info(ctx, "connecting to storage", nil)
	repo, err := persistence.NewPostgresRepository(pgDSN)
	if err != nil {
		log.Error(ctx, "failed to initialize storage", map[string]any{
			"error": err.Error(),
		})
		os.Exit(1)
	}
	log.Info(ctx, "storage connected", nil)
	authMiddleware, err = auth.NewMiddleware(ctx, authConfig)
	if err != nil {
		log.Error(ctx, "failed to initialize auth middleware", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
	log.Info(ctx, "auth middleware initialized", nil)

	// ...

	log.Info(ctx, "storage connected", nil)

	// Messaging Configuration
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

	eng := application.NewEngine(repo, publisher)
	eng.SetFlushOutboxAfterCommit(false)
	metrics.NewOutboxCollector(func() metrics.OutboxSnapshot {
		snapshot := eng.MetricsSnapshot()
		return metrics.OutboxSnapshot{
			Pending: snapshot.OutboxPending, PublishSuccess: snapshot.OutboxPublishSuccess,
			PublishFailure: snapshot.OutboxPublishFailure, Retries: snapshot.OutboxRetries,
			DeadLetters: snapshot.OutboxDeadLetters, PublishLagSec: snapshot.OutboxPublishLagSec,
		}
	})
	outboxRelayMaxAttempts := envInt("OUTBOX_RELAY_MAX_ATTEMPTS", 10)
	eng.SetOutboxMaxAttempts(outboxRelayMaxAttempts)
	defer func() {
		if publisher != nil {
			_ = publisher.Close()
		}
	}()

	outboxRelayEnabled := envBool("OUTBOX_RELAY_ENABLED", true)
	outboxRelayInterval := parseDurationEnv("OUTBOX_RELAY_INTERVAL", time.Second)
	outboxRelayTimeout := parseDurationEnv("OUTBOX_RELAY_TIMEOUT", 5*time.Second)
	outboxRelayBatchSize := envInt("OUTBOX_RELAY_BATCH_SIZE", 200)
	if outboxRelayBatchSize <= 0 {
		outboxRelayBatchSize = 200
	}
	idempotencyCleanupEnabled := envBool("IDEMPOTENCY_CLEANUP_ENABLED", true)
	idempotencyRetention := parseDurationEnv("IDEMPOTENCY_RETENTION", 168*time.Hour)
	idempotencyCleanupInterval := parseDurationEnv("IDEMPOTENCY_CLEANUP_INTERVAL", time.Hour)
	idempotencyCleanupTimeout := parseDurationEnv("IDEMPOTENCY_CLEANUP_TIMEOUT", 10*time.Second)
	idempotencyCleanupBatchSize := envInt("IDEMPOTENCY_CLEANUP_BATCH_SIZE", 500)
	if idempotencyCleanupBatchSize <= 0 {
		idempotencyCleanupBatchSize = 500
	}

	if outboxRelayEnabled {
		go func() {
			ticker := time.NewTicker(outboxRelayInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					relayCtx, cancel := context.WithTimeout(context.Background(), outboxRelayTimeout)
					result, relayErr := eng.RunOutboxRelayCycle(relayCtx, outboxRelayBatchSize)
					if relayErr != nil {
						log.Error(ctx, "outbox relay cycle failed", map[string]any{"error": relayErr.Error()})
					} else if result.Pending > 0 || result.Published > 0 || result.Failed > 0 {
						log.Debug(ctx, "outbox relay cycle", map[string]any{
							"pending": result.Pending, "claimed": result.Claimed,
							"published": result.Published, "failed": result.Failed,
						})
					}
					cancel()
				}
			}
		}()
		log.Info(ctx, "outbox relay enabled", map[string]any{
			"interval":     outboxRelayInterval.String(),
			"timeout":      outboxRelayTimeout.String(),
			"batch_size":   outboxRelayBatchSize,
			"max_attempts": outboxRelayMaxAttempts,
		})
	} else {
		log.Info(ctx, "outbox relay disabled", nil)
	}

	if idempotencyCleanupEnabled && idempotencyRetention > 0 {
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
							Subject: "workflow-command", TenantID: tenant,
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
		log.Info(ctx, "idempotency cleanup enabled", map[string]any{
			"retention":  idempotencyRetention.String(),
			"interval":   idempotencyCleanupInterval.String(),
			"timeout":    idempotencyCleanupTimeout.String(),
			"batch_size": idempotencyCleanupBatchSize,
		})
	} else {
		log.Info(ctx, "idempotency cleanup disabled", nil)
	}

	// Register service handlers with logging
	serviceLog := logger.New("service-handler")
	eng.RegisterHandler("logService", func(ctx context.Context, instance *model.WorkflowInstance, step *model.StepDefinition) error {
		serviceLog.Info(ctx, "executing log service", map[string]any{
			"instance_id": instance.ID,
			"step_id":     step.ID,
			"step_name":   step.Name,
		})
		return nil
	})

	eng.RegisterHandler("paymentService", func(ctx context.Context, instance *model.WorkflowInstance, step *model.StepDefinition) error {
		serviceLog.Info(ctx, "processing payment", map[string]any{
			"instance_id": instance.ID,
			"step_id":     step.ID,
		})
		instance.Context["payment_status"] = "success"
		serviceLog.Info(ctx, "payment completed", map[string]any{
			"instance_id":    instance.ID,
			"payment_status": "success",
		})
		return nil
	})

	runtimeEnabled := envBool("RUNTIME_ENABLED", true)
	if runtimeEnabled {
		leaseHolder := envOrDefault("RUNTIME_LEASE_HOLDER", fmt.Sprintf("%s-%d", hostnameOrUnknown(), os.Getpid()))
		leaseTTL := parseDurationEnv("RUNTIME_LEASE_TTL", 15*time.Second)
		go func() {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					tenants, err := repo.EnsureRuntimeWorkTenants(bgCtx)
					if err != nil {
						log.Error(bgCtx, "runtime tenant discovery failed", map[string]any{"error": err.Error()})
						cancel()
						continue
					}
					acquired, err := repo.TryAcquireRuntimeSchedulerLease(bgCtx, leaseHolder, leaseTTL)
					if err != nil {
						log.Error(bgCtx, "runtime scheduler lease acquire failed", map[string]any{"error": err.Error()})
						cancel()
						continue
					}
					if !acquired {
						cancel()
						continue
					}
					for _, tenant := range tenants {
						tenantBaseCtx, tenantCancel := context.WithTimeout(context.Background(), 5*time.Second)
						tenantCtx := auth.WithPrincipal(tenantBaseCtx, auth.Principal{
							Subject: "workflow-command", TenantID: tenant,
						})
						if err := eng.CheckTimers(tenantCtx); err != nil {
							log.Error(bgCtx, "check timers failed", map[string]any{
								"tenant_id": tenant, "error": err.Error(),
							})
						}
						if err := eng.CheckSLAs(tenantCtx); err != nil {
							log.Error(bgCtx, "check SLAs failed", map[string]any{
								"tenant_id": tenant, "error": err.Error(),
							})
						}
						tenantCancel()
					}
					cancel()
				}
			}
		}()
		log.Info(ctx, "in-process runtime scheduler enabled", map[string]any{
			"lease_holder": leaseHolder,
			"lease_ttl":    leaseTTL.String(),
		})
	} else {
		log.Info(ctx, "in-process runtime scheduler disabled", nil)
	}

	// Setup HTTP Handler
	handler := api.NewHandler(eng, deploymentConfig)
	r := mux.NewRouter()

	// Apply Auth Middleware if enabled
	if authMiddleware != nil {
		r.Use(authMiddleware.Handler)
		log.Info(ctx, "auth middleware enabled on router", nil)
	}

	livenessHandler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}
	readinessHandler := func(w http.ResponseWriter, r *http.Request) {
		checkCtx, checkCancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer checkCancel()
		err := repo.Ready(checkCtx)
		ready := err == nil
		brokerReady := true
		if checker, ok := publisher.(interface{ Ready(context.Context) error }); ok {
			brokerReady = checker.Ready(checkCtx) == nil
		}
		metrics.SetDependencyReady("workflow-command", "postgres", ready)
		metrics.SetDependencyReady("workflow-command", "broker", brokerReady)
		metrics.SetSchemaReady("workflow-command", ready)
		w.Header().Set("Content-Type", "application/json")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "not-ready", "dependency": "postgres"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ready"})
	}
	identityHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": false})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authenticated": true,
			"principal":     principal,
		})
	}
	r.HandleFunc("/livez", livenessHandler).Methods("GET")
	r.HandleFunc("/readyz", readinessHandler).Methods("GET")
	r.HandleFunc("/health", readinessHandler).Methods("GET")
	r.HandleFunc("/v1/health", readinessHandler).Methods("GET")
	r.HandleFunc("/identity/me", identityHandler).Methods("GET")
	r.HandleFunc("/v1/identity/me", identityHandler).Methods("GET")

	handler.RegisterRoutes(r)
	handler.RegisterRoutes(r.PathPrefix("/v1").Subrouter())
	r.Use(metrics.MuxMiddleware("workflow-command"))

	// Add logging middleware
	httpMiddleware := logger.NewHTTPMiddleware("http")
	allowedOrigins := parseAllowedOrigins(os.Getenv("ALLOWED_ORIGINS"))
	if len(allowedOrigins) == 0 {
		allowedOrigins = []string{"http://localhost:9100", "http://localhost:5173"}
	}
	allowCredentials := envBool("CORS_ALLOW_CREDENTIALS", false)
	if len(allowedOrigins) == 1 && allowedOrigins[0] == "*" && allowCredentials {
		log.Warn(ctx, "cors credentials disabled for wildcard origin", nil)
		allowCredentials = false
	}

	c := cors.New(cors.Options{
		AllowedOrigins: allowedOrigins,
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{
			"Authorization", "Content-Type", "X-Correlation-ID", "Idempotency-Key",
			"X-Workflow-Worker-Protocol-Version",
		},
		AllowCredentials: allowCredentials,
	})

	srv := &http.Server{
		Handler:      otelhttp.NewHandler(httpMiddleware.Middleware(httpproblem.Middleware(c.Handler(r))), "workflow-command.http"),
		Addr:         serverAddr,
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}

	metricsAddr := envOrDefault("METRICS_ADDR", ":9090")
	metricsServer := &http.Server{Addr: metricsAddr, Handler: metrics.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error(ctx, "metrics server failed", map[string]any{"error": err.Error()})
			cancel()
		}
	}()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = metricsServer.Shutdown(shutdownCtx)
	}()

	// Start HTTP Server
	go func() {
		log.Info(ctx, "starting http server", map[string]any{"addr": serverAddr})
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error(ctx, "http server failed", map[string]any{"error": err.Error()})
			cancel()
		}
	}()

	// Start gRPC Server
	grpcAddr := envOrDefault("GRPC_ADDR", ":50051")
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(authMiddleware.UnaryServerInterceptor(auth.RoleAdmin, auth.RoleClient)))
	jobWorkerServer := grpcImpl.NewServer(eng)
	pb.RegisterJobWorkerServiceServer(grpcServer, jobWorkerServer)

	go func() {
		log.Info(ctx, "starting grpc server", map[string]any{"addr": grpcAddr})
		lis, err := net.Listen("tcp", grpcAddr)
		if err != nil {
			log.Error(ctx, "failed to listen grpc", map[string]any{"error": err.Error()})
			cancel()
			return
		}

		log.Info(ctx, "grpc server started", nil)
		if err := grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			log.Error(ctx, "grpc server failed", map[string]any{"error": err.Error()})
			cancel()
		}
	}()

	log.Info(ctx, "server starting", map[string]any{
		"http_addr": serverAddr,
		"grpc_addr": grpcAddr,
	})

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Info(ctx, "received shutdown signal", map[string]any{
		"signal": sig.String(),
	})

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error(ctx, "http server shutdown error", map[string]any{"error": err.Error()})
	}
	grpcServer.GracefulStop()

	log.Info(ctx, "server stopped gracefully", nil)
}

func parseAllowedOrigins(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v == "" {
			continue
		}
		origins = append(origins, v)
	}
	return origins
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

func envOrDefault(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
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

func hostnameOrUnknown() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "unknown"
	}
	return host
}
