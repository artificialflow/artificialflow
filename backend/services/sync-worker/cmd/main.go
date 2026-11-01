package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	esadapter "github.com/artificialflow/artificialflow/backend/libs/elasticsearch"
	"github.com/artificialflow/artificialflow/backend/libs/logger"
	"github.com/artificialflow/artificialflow/backend/libs/metrics"
	"github.com/artificialflow/artificialflow/backend/libs/tracing"
	"github.com/artificialflow/artificialflow/backend/services/sync-worker/internal/application"
	"github.com/artificialflow/artificialflow/backend/services/sync-worker/internal/infrastructure/messaging"
	"github.com/artificialflow/artificialflow/backend/services/sync-worker/internal/infrastructure/persistence"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	log := logger.New("sync-worker")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tp, err := tracing.InitTracer(ctx, "sync-worker")
	if err != nil {
		log.Error(ctx, "failed to initialize tracing", map[string]any{"error": err.Error()})
	} else {
		defer func() { _ = tp.Shutdown(context.Background()) }()
	}

	kafkaBrokers := envOrDefault("KAFKA_BROKERS", "localhost:9092")
	groupID := envOrDefault("KAFKA_GROUP_ID", "artificialflow-sync-worker")
	eventTopic := strings.TrimSpace(envOrDefault("KAFKA_TOPIC_EVENTS", "workflow.events.v1"))
	projectionContract := normalizeProjectionContract(envOrDefault("SYNC_PROJECTION_CONTRACT", "hybrid"))
	kafkaTopics := parseTopicsEnv(os.Getenv("KAFKA_TOPICS"))
	if len(kafkaTopics) == 0 {
		if eventTopic != "" {
			kafkaTopics = []string{eventTopic}
			log.Warn(ctx, "KAFKA_TOPICS is empty; using legacy KAFKA_TOPIC_EVENTS fallback", map[string]any{
				"topic": eventTopic,
			})
		}
	}
	if err := validateProjectionContract(projectionContract, kafkaTopics, eventTopic); err != nil {
		log.Error(ctx, "invalid sync projection contract configuration", map[string]any{
			"error":               err.Error(),
			"projection_contract": projectionContract,
			"topics":              kafkaTopics,
			"event_topic":         eventTopic,
		})
		os.Exit(1)
	}
	if err := validateDebeziumIdentifierTransition(kafkaTopics, envBool("SYNC_ALLOW_MIXED_DEBEZIUM_PREFIXES", false)); err != nil {
		log.Error(ctx, "unsafe Debezium identifier transition configuration", map[string]any{
			"error":  err.Error(),
			"topics": kafkaTopics,
		})
		os.Exit(1)
	}
	dlqTopic := strings.TrimSpace(envOrDefault("KAFKA_DLQ_TOPIC", "workflow.sync.dlq"))
	topicPartitions := envInt("KAFKA_TOPIC_PARTITIONS", 0)
	maxProcessRetries := envInt("SYNC_MAX_PROCESS_RETRIES", 3)
	retryBackoff := parseDurationEnv("SYNC_RETRY_BACKOFF", 500*time.Millisecond)

	esAddr := envOrDefault("ES_ADDR", "http://localhost:9200")
	indexPrefix := envOrDefault("ES_INDEX_PREFIX", "artificialflow")
	logLevel := envOrDefault("LOG_LEVEL", "INFO")
	connectBootstrapEnabled := envBool("CONNECT_BOOTSTRAP_ENABLED", true)
	connectURL := strings.TrimSpace(envOrDefault("CONNECT_URL", "http://connect:8083"))
	connectorName := strings.TrimSpace(envOrDefault("CONNECTOR_NAME", "artificialflow-postgres-connector"))
	connectorFile := strings.TrimSpace(os.Getenv("CONNECTOR_FILE"))
	connectorJSON := strings.TrimSpace(os.Getenv("CONNECTOR_JSON"))
	connectorTopicPrefix := strings.TrimSpace(os.Getenv("CONNECTOR_TOPIC_PREFIX"))
	connectorSlotName := strings.TrimSpace(os.Getenv("CONNECTOR_SLOT_NAME"))
	connectorDatabaseUser := strings.TrimSpace(os.Getenv("CONNECTOR_DATABASE_USER"))
	allowParallelConnectors := envBool("CONNECT_ALLOW_PARALLEL_IDENTIFIERS", false)
	connectWaitTimeoutSec := envInt("CONNECT_WAIT_TIMEOUT_SEC", 180)
	if connectWaitTimeoutSec < 1 {
		connectWaitTimeoutSec = 180
	}
	connectWaitTimeout := time.Duration(connectWaitTimeoutSec) * time.Second
	metricsAddr := strings.TrimSpace(envOrDefault("SYNC_METRICS_ADDR", ":8092"))
	freshnessSLOSec := int64(envInt("SYNC_FRESHNESS_SLO_SEC", 120))
	failOnStale := envBool("SYNC_HEALTH_FAIL_ON_STALE", false)
	tenantMode := strings.ToLower(strings.TrimSpace(envOrDefault("TENANT_MODE", "compat")))

	log.Info(ctx, "initializing sync-worker", map[string]any{
		"kafka_brokers":     kafkaBrokers,
		"kafka_group":       groupID,
		"kafka_topics":      kafkaTopics,
		"event_topic":       eventTopic,
		"contract":          projectionContract,
		"kafka_dlq":         dlqTopic,
		"topic_partitions":  topicPartitions,
		"retry_max":         maxProcessRetries,
		"retry_backoff":     retryBackoff.String(),
		"es_addr":           esAddr,
		"index_prefix":      indexPrefix,
		"connect_bootstrap": connectBootstrapEnabled,
		"connector_name":    connectorName,
		"metrics_addr":      metricsAddr,
		"freshness_slo_sec": freshnessSLOSec,
		"fail_on_stale":     failOnStale,
		"log_level":         logLevel,
	})

	esClient, err := esadapter.NewClient(esadapter.Config{Addresses: splitAndTrim(esAddr)})
	if err != nil {
		log.Error(ctx, "failed to init elasticsearch client", map[string]any{
			"error": err.Error(),
		})
		os.Exit(1)
	}
	esClientRepo := esadapter.NewRepository(esClient)

	// DDD Assembly
	repo := persistence.NewESRepository(esClientRepo)
	service := application.NewSyncService(repo, indexPrefix)
	log.Info(ctx, "initializing kafka consumer", map[string]any{
		"brokers": kafkaBrokers,
		"group":   groupID,
		"topics":  kafkaTopics,
	})
	var consumer messaging.EventConsumer = messaging.NewKafkaConsumer(messaging.Config{
		Brokers:           splitAndTrim(kafkaBrokers),
		GroupID:           groupID,
		Topics:            kafkaTopics,
		DLQTopic:          dlqTopic,
		MaxProcessRetries: maxProcessRetries,
		RetryBackoff:      retryBackoff,
		TenantMode:        tenantMode,
	}, service)

	var statsProvider messaging.ConsumerStatsProvider
	if provider, ok := consumer.(messaging.ConsumerStatsProvider); ok {
		statsProvider = provider
	}
	if statsProvider != nil {
		prometheus.MustRegister(metrics.NewSyncCollector(func() metrics.SyncSnapshot {
			s := statsProvider.Snapshot()
			return metrics.SyncSnapshot{
				Processed: s.Processed, Succeeded: s.Succeeded, Failed: s.Failed,
				Retried: s.Retried, DeadLetters: s.DLQPublished, LastProcessedAt: s.LastProcessedAt,
			}
		}))
	}
	var bootstrapped atomic.Bool
	startSyncHealthServer(ctx, log, metricsAddr, statsProvider, freshnessSLOSec, failOnStale, func(ctx context.Context) error {
		if !bootstrapped.Load() {
			return fmt.Errorf("consumer bootstrap has not completed")
		}
		searchReady := esClient.Ready(ctx) == nil
		brokerReady := true
		if checker, ok := consumer.(interface{ Ready(context.Context) error }); ok {
			brokerReady = checker.Ready(ctx) == nil
		}
		metrics.SetDependencyReady("sync-worker", "search", searchReady)
		metrics.SetDependencyReady("sync-worker", "broker", brokerReady)
		if !searchReady || !brokerReady {
			return fmt.Errorf("search or broker dependency is unavailable")
		}
		return nil
	})

	// Pre-create subscription topics before the consumer joins the group.
	// Debezium otherwise creates them asynchronously after connector RUNNING,
	// and kafka-go can get a permanent empty assignment until restart.
	topicsToEnsure := append([]string{}, kafkaTopics...)
	if dlqTopic != "" {
		topicsToEnsure = append(topicsToEnsure, dlqTopic)
	}
	log.Info(ctx, "ensuring kafka topics exist before consumer start", map[string]any{
		"topics": topicsToEnsure,
	})
	if err := messaging.EnsureTopicsReady(ctx, splitAndTrim(kafkaBrokers), topicsToEnsure, topicPartitions, connectWaitTimeout); err != nil {
		log.Error(ctx, "failed to ensure kafka topics", map[string]any{"error": err.Error()})
		os.Exit(1)
	}

	if err := ensureConnectorBootstrap(ctx, log, connectorBootstrapOptions{
		Enabled:                  connectBootstrapEnabled,
		ConnectURL:               connectURL,
		ConnectorName:            connectorName,
		ConnectorFile:            connectorFile,
		ConnectorJSON:            connectorJSON,
		ConnectorTopicPrefix:     connectorTopicPrefix,
		ConnectorSlotName:        connectorSlotName,
		ConnectorDatabaseUser:    connectorDatabaseUser,
		ConnectorDatabaseHost:    strings.TrimSpace(os.Getenv("CONNECTOR_DATABASE_HOSTNAME")),
		ConnectorDatabasePort:    strings.TrimSpace(os.Getenv("CONNECTOR_DATABASE_PORT")),
		ConnectorDatabaseName:    strings.TrimSpace(os.Getenv("CONNECTOR_DATABASE_NAME")),
		ConnectorDatabasePass:    strings.TrimSpace(os.Getenv("CONNECTOR_DATABASE_PASSWORD")),
		WaitTimeout:              connectWaitTimeout,
		PollInterval:             3 * time.Second,
		BlockedConnectorNames:    conflictingConnectorNames(connectorName),
		AllowParallelIdentifiers: allowParallelConnectors,
	}); err != nil {
		log.Error(ctx, "failed to bootstrap connector", map[string]any{"error": err.Error()})
		os.Exit(1)
	}

	log.Info(ctx, "re-checking kafka topic readiness after connector bootstrap", map[string]any{
		"topics": topicsToEnsure,
	})
	if err := messaging.EnsureTopicsReady(ctx, splitAndTrim(kafkaBrokers), topicsToEnsure, topicPartitions, connectWaitTimeout); err != nil {
		log.Error(ctx, "failed to ensure kafka topics after connector bootstrap", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
	bootstrapped.Store(true)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Info(ctx, "received shutdown signal", map[string]any{
			"signal": sig.String(),
		})
		cancel()
	}()

	log.Info(ctx, "sync-worker starting", nil)
	if err := consumer.Start(ctx); err != nil && err != context.Canceled {
		log.Error(ctx, "sync-worker stopped with error", map[string]any{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	log.Info(ctx, "sync-worker stopped gracefully", nil)
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

func parseTopicsEnv(raw string) []string {
	topics := splitAndTrim(raw)
	if len(topics) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(topics))
	unique := make([]string, 0, len(topics))
	for _, topic := range topics {
		if _, ok := seen[topic]; ok {
			continue
		}
		seen[topic] = struct{}{}
		unique = append(unique, topic)
	}
	return unique
}

func normalizeProjectionContract(raw string) string {
	contract := strings.ToLower(strings.TrimSpace(raw))
	switch contract {
	case "", "hybrid":
		return "hybrid"
	case "event-first", "event_first", "eventfirst", "events":
		return "event-first"
	case "debezium":
		return "debezium"
	default:
		return contract
	}
}

func validateProjectionContract(contract string, topics []string, eventTopic string) error {
	if len(topics) == 0 {
		return fmt.Errorf("kafka topics are required for projection contract validation")
	}

	contract = normalizeProjectionContract(contract)
	eventTopic = strings.TrimSpace(eventTopic)
	if eventTopic == "" {
		eventTopic = "workflow.events.v1"
	}

	hasEventTopic := containsTopic(topics, eventTopic)
	hasDebeziumTopics := containsDebeziumTopics(topics)

	switch contract {
	case "hybrid":
		if !hasEventTopic {
			return fmt.Errorf("hybrid contract requires event topic %q in KAFKA_TOPICS", eventTopic)
		}
		if !hasDebeziumTopics {
			return fmt.Errorf("hybrid contract requires at least one Debezium table topic in KAFKA_TOPICS (expected *.public.<table>)")
		}
		return nil
	case "event-first":
		if !hasEventTopic {
			return fmt.Errorf("event-first contract requires event topic %q in KAFKA_TOPICS", eventTopic)
		}
		if hasDebeziumTopics {
			return fmt.Errorf("event-first contract does not allow Debezium table topics in KAFKA_TOPICS")
		}
		return nil
	case "debezium":
		if !hasDebeziumTopics {
			return fmt.Errorf("debezium contract requires at least one Debezium table topic in KAFKA_TOPICS")
		}
		return nil
	default:
		return fmt.Errorf("unsupported SYNC_PROJECTION_CONTRACT=%q (expected hybrid|event-first|debezium)", contract)
	}
}

func containsTopic(topics []string, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	for _, topic := range topics {
		if strings.TrimSpace(topic) == expected {
			return true
		}
	}
	return false
}

func containsDebeziumTopics(topics []string) bool {
	for _, topic := range topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			continue
		}
		parts := strings.Split(topic, ".")
		if len(parts) >= 3 && strings.EqualFold(parts[len(parts)-2], "public") {
			return true
		}
	}
	return false
}

func validateDebeziumIdentifierTransition(topics []string, allowMixed bool) error {
	_ = allowMixed
	for _, topic := range topics {
		parts := strings.Split(strings.TrimSpace(topic), ".")
		if len(parts) < 3 || !strings.EqualFold(parts[len(parts)-2], "public") {
			continue
		}
		prefix := strings.Join(parts[:len(parts)-2], ".")
		if prefix != "" && prefix != "artificialflow" {
			return fmt.Errorf("KAFKA_TOPICS uses unsupported Debezium prefix %q; expected artificialflow", prefix)
		}
	}
	return nil
}

func conflictingConnectorNames(connectorName string) []string {
	_ = connectorName
	return nil
}

func startSyncHealthServer(ctx context.Context, log *logger.Logger, addr string, provider messaging.ConsumerStatsProvider, sloSec int64, failOnStale bool, dependencyReady func(context.Context) error) {
	if strings.TrimSpace(addr) == "" {
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/livez", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})
	readyHandler := func(w http.ResponseWriter, r *http.Request) {
		snapshot := messaging.ConsumerStatsSnapshot{}
		if provider != nil {
			snapshot = provider.Snapshot()
		}
		status, lagSec, stale, httpStatus := evaluateSyncHealth(snapshot, sloSec, failOnStale)
		dependencyOK := dependencyReady != nil
		if dependencyReady != nil {
			checkCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			dependencyOK = dependencyReady(checkCtx) == nil
			cancel()
		}
		if !dependencyOK {
			status = "not-ready"
			httpStatus = http.StatusServiceUnavailable
		}
		metrics.SetWorkLag("sync-worker", "projection", float64(lagSec))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":             status,
			"lagSeconds":         lagSec,
			"stale":              stale,
			"freshnessSloSec":    sloSec,
			"processed":          snapshot.Processed,
			"succeeded":          snapshot.Succeeded,
			"failed":             snapshot.Failed,
			"retried":            snapshot.Retried,
			"dlqPublished":       snapshot.DLQPublished,
			"lastProcessedAt":    snapshot.LastProcessedAt,
			"topics":             snapshot.Topics,
			"failOnStaleEnabled": failOnStale,
		})
	}
	mux.HandleFunc("/readyz", readyHandler)
	mux.HandleFunc("/health", readyHandler)
	mux.Handle("/metrics", metrics.Handler())

	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() {
		log.Info(ctx, "sync-worker health server starting", map[string]any{"addr": addr})
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error(ctx, "sync-worker health server stopped with error", map[string]any{"error": err.Error()})
		}
	}()
}

func evaluateSyncHealth(snapshot messaging.ConsumerStatsSnapshot, sloSec int64, failOnStale bool) (string, int64, bool, int) {
	lagSec := int64(-1)
	if !snapshot.LastProcessedAt.IsZero() {
		lagSec = int64(time.Since(snapshot.LastProcessedAt).Seconds())
		if lagSec < 0 {
			lagSec = 0
		}
	}

	if sloSec <= 0 {
		return "ok", lagSec, false, 200
	}
	if snapshot.LastProcessedAt.IsZero() {
		return "starting", lagSec, false, 200
	}
	if lagSec <= sloSec {
		return "ok", lagSec, false, 200
	}
	if failOnStale {
		return "unhealthy", lagSec, true, 503
	}
	return "degraded", lagSec, true, 200
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

func parseDurationEnv(key string, defaultValue time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultValue
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return defaultValue
	}
	return v
}

func envBool(key string, defaultValue bool) bool {
	val := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch val {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}
