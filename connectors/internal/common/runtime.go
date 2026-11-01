package common

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/libs/tracing"
	"github.com/artificialflow/artificialflow/backend/libs/worker"
)

const maxRuntimeAttempts = 3

type VariableLoader interface {
	Load(context.Context, int64) (map[string]any, error)
}

type SecretResolver interface {
	ResolveSecret(context.Context, string) (string, error)
}

type MetricsHook interface {
	Activation(string)
	Attempt(string)
	Success(string, time.Duration)
	Failure(string, time.Duration)
	IdempotencyHit(string)
}

type Adapter func(context.Context, map[string]any) (map[string]any, error)

type RuntimeConfig struct {
	BaseURL      string
	Token        string
	JobType      string
	WorkerName   string
	MaxJobs      int
	LockDuration time.Duration
	MaxAttempts  int
	RetryBackoff time.Duration
	Loader       VariableLoader
	Secrets      SecretResolver
	Metrics      MetricsHook
	Idempotency  IdempotencyStore
	Execute      Adapter
}

type Runtime struct {
	config         RuntimeConfig
	worker         *worker.Worker
	shutdownTracer func(context.Context) error
}

type HTTPVariableLoader struct {
	BaseURL string
	Token   string
}

func (l HTTPVariableLoader) Load(ctx context.Context, processInstanceKey int64) (map[string]any, error) {
	return LoadInstanceVars(ctx, l.BaseURL, l.Token, processInstanceKey)
}

type EnvironmentSecretResolver struct{}

func (EnvironmentSecretResolver) ResolveSecret(_ context.Context, name string) (string, error) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return "", fmt.Errorf("secret %q is not configured", name)
	}
	return value, nil
}

type IdempotencyStore interface {
	Load(string) (map[string]any, bool)
	Store(string, map[string]any)
}

type MemoryIdempotencyStore struct {
	mu      sync.Mutex
	maxSize int
	order   []string
	values  map[string]map[string]any
}

func NewMemoryIdempotencyStore(maxSize int) *MemoryIdempotencyStore {
	if maxSize <= 0 {
		maxSize = 10000
	}
	return &MemoryIdempotencyStore{maxSize: maxSize, values: map[string]map[string]any{}}
}

func (s *MemoryIdempotencyStore) Load(key string) (map[string]any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	return cloneMap(value), ok
}

func (s *MemoryIdempotencyStore) Store(key string, value map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.values[key]; !exists {
		s.order = append(s.order, key)
	}
	s.values[key] = cloneMap(value)
	for len(s.order) > s.maxSize {
		delete(s.values, s.order[0])
		s.order = s.order[1:]
	}
}

type RuntimeMetrics struct {
	Activations     atomic.Uint64
	Attempts        atomic.Uint64
	Successes       atomic.Uint64
	Failures        atomic.Uint64
	IdempotencyHits atomic.Uint64
}

func (m *RuntimeMetrics) Activation(string)             { m.Activations.Add(1) }
func (m *RuntimeMetrics) Attempt(string)                { m.Attempts.Add(1) }
func (m *RuntimeMetrics) Success(string, time.Duration) { m.Successes.Add(1) }
func (m *RuntimeMetrics) Failure(string, time.Duration) { m.Failures.Add(1) }
func (m *RuntimeMetrics) IdempotencyHit(string)         { m.IdempotencyHits.Add(1) }

func NewRuntime(config RuntimeConfig) (*Runtime, error) {
	if config.Execute == nil {
		return nil, fmt.Errorf("connector adapter is required")
	}
	if _, ok := DescriptorByJobType(config.JobType); !ok {
		return nil, fmt.Errorf("unknown connector job type %q", config.JobType)
	}
	if config.BaseURL == "" {
		config.BaseURL = EnvOr("ARTIFICIALFLOW_BASE_URL", "http://localhost:9100/api")
	}
	if config.WorkerName == "" {
		config.WorkerName = config.JobType
	}
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = envIntBounded("CONNECTOR_MAX_ATTEMPTS", 1, 1, maxRuntimeAttempts)
	}
	if config.MaxAttempts > maxRuntimeAttempts {
		config.MaxAttempts = maxRuntimeAttempts
	}
	if config.RetryBackoff <= 0 {
		config.RetryBackoff = 100 * time.Millisecond
	}
	if config.Loader == nil {
		config.Loader = HTTPVariableLoader{BaseURL: config.BaseURL, Token: config.Token}
	}
	if config.Secrets == nil {
		config.Secrets = EnvironmentSecretResolver{}
	}
	if config.Metrics == nil {
		config.Metrics = &RuntimeMetrics{}
	}
	if config.Idempotency == nil {
		config.Idempotency = NewMemoryIdempotencyStore(10000)
	}
	client, err := worker.NewClient(worker.ClientConfig{BaseURL: config.BaseURL, BearerToken: config.Token})
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{config: config}
	if provider, traceErr := tracing.InitTracer(context.Background(), config.WorkerName); traceErr == nil {
		runtime.shutdownTracer = provider.Shutdown
	}
	runtime.worker, err = worker.NewWorker(client, worker.WorkerConfig{
		JobType:         config.JobType,
		WorkerName:      config.WorkerName,
		MaxJobs:         config.MaxJobs,
		ActivateTimeout: 5 * time.Second,
		LockDuration:    config.LockDuration,
		Handler:         runtime.Handle,
	})
	if err != nil {
		return nil, err
	}
	return runtime, nil
}

func (r *Runtime) Run(ctx context.Context) error {
	if r.shutdownTracer != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = r.shutdownTracer(shutdownCtx)
		}()
	}
	return r.worker.Run(ctx)
}

func (r *Runtime) Handle(ctx context.Context, job model.Job) (map[string]any, error) {
	started := time.Now()
	r.config.Metrics.Activation(r.config.JobType)
	idempotencyKey := r.config.JobType + ":" + strconv.FormatInt(job.Key, 10)
	if output, ok := r.config.Idempotency.Load(idempotencyKey); ok {
		r.config.Metrics.IdempotencyHit(r.config.JobType)
		return output, nil
	}
	variables, err := r.config.Loader.Load(ctx, job.ProcessInstanceKey)
	if err != nil {
		r.config.Metrics.Failure(r.config.JobType, time.Since(started))
		return nil, err
	}
	inputs, err := BindConnectorInputs(ctx, r.config.JobType, variables, r.config.Secrets)
	if err != nil {
		r.config.Metrics.Failure(r.config.JobType, time.Since(started))
		return nil, err
	}
	inputs["artificialflowOperationId"] = idempotencyKey
	var output map[string]any
	for attempt := 1; attempt <= r.config.MaxAttempts; attempt++ {
		r.config.Metrics.Attempt(r.config.JobType)
		output, err = r.config.Execute(ctx, inputs)
		if err == nil {
			r.config.Idempotency.Store(idempotencyKey, output)
			r.config.Metrics.Success(r.config.JobType, time.Since(started))
			return output, nil
		}
		if attempt < r.config.MaxAttempts {
			timer := time.NewTimer(r.config.RetryBackoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				err = ctx.Err()
				attempt = r.config.MaxAttempts
			case <-timer.C:
			}
		}
	}
	r.config.Metrics.Failure(r.config.JobType, time.Since(started))
	return output, err
}

func BindConnectorInputs(ctx context.Context, jobType string, variables map[string]any, secrets SecretResolver) (map[string]any, error) {
	descriptor, ok := DescriptorByJobType(jobType)
	if !ok {
		return nil, fmt.Errorf("unknown connector job type %q", jobType)
	}
	bound := cloneMap(variables)
	applyLegacyBindings(jobType, bound)
	for _, field := range descriptor.Fields {
		raw, exists := bound[field.Key]
		if (!exists || isEmpty(raw)) && field.Default != "" {
			raw, exists = field.Default, true
		}
		if !exists || isEmpty(raw) {
			if field.Required {
				return nil, fmt.Errorf("connector input %q is required", field.Key)
			}
			continue
		}
		resolved, err := resolveSecretReference(ctx, raw, secrets)
		if err != nil {
			return nil, fmt.Errorf("connector input %q: %w", field.Key, err)
		}
		coerced, err := coerceConnectorKind(field.Kind, resolved)
		if err != nil {
			return nil, fmt.Errorf("connector input %q: %w", field.Key, err)
		}
		bound[field.Key] = coerced
	}
	return bound, nil
}

func applyLegacyBindings(jobType string, inputs map[string]any) {
	aliases := map[string]map[string]string{
		"io.artificialflow.connector.webhook": {"webhookUrl": "url", "payload": "body"},
		"io.artificialflow.connector.email":   {"emailTo": "to", "emailSubject": "subject", "emailBody": "body"},
		"io.artificialflow.connector.kafka":   {"kafkaTopic": "topic", "kafkaValue": "payload"},
		"io.artificialflow.connector.s3":      {"s3Body": "body"},
	}
	for canonical, legacy := range aliases[jobType] {
		if isEmpty(inputs[canonical]) && !isEmpty(inputs[legacy]) {
			inputs[canonical] = inputs[legacy]
		}
	}
	if jobType == "io.artificialflow.connector.s3" && isEmpty(inputs["s3Bucket"]) {
		if bucket := strings.TrimSpace(os.Getenv("S3_BUCKET")); bucket != "" {
			inputs["s3Bucket"] = bucket
		}
	}
}

func resolveSecretReference(ctx context.Context, raw any, resolver SecretResolver) (any, error) {
	value, ok := raw.(string)
	if !ok || !strings.HasPrefix(strings.TrimSpace(value), "secret://") {
		return raw, nil
	}
	name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "secret://"))
	if name == "" {
		return nil, fmt.Errorf("secret reference name is required")
	}
	if resolver == nil {
		return nil, fmt.Errorf("secret resolver is not configured")
	}
	return resolver.ResolveSecret(ctx, name)
}

func coerceConnectorKind(kind string, value any) (any, error) {
	switch kind {
	case "string":
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("must be a string")
		}
		return text, nil
	case "number":
		switch typed := value.(type) {
		case float64, float32, int, int64:
			return typed, nil
		case string:
			number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
			if err != nil {
				return nil, fmt.Errorf("must be a number")
			}
			return number, nil
		default:
			return nil, fmt.Errorf("must be a number")
		}
	case "boolean":
		switch value.(type) {
		case bool:
			return value, nil
		case string:
			normalized := strings.ToLower(strings.TrimSpace(value.(string)))
			if normalized == "true" || normalized == "false" {
				return normalized == "true", nil
			}
		}
		return nil, fmt.Errorf("must be a boolean")
	case "json":
		return CoerceJSONValue(value)
	default:
		return nil, fmt.Errorf("has unsupported descriptor kind %q", kind)
	}
}

func isEmpty(value any) bool {
	text, ok := value.(string)
	return value == nil || (ok && strings.TrimSpace(text) == "")
}

func cloneMap(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func envIntBounded(key string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
