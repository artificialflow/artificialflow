package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "artificialflow_http_requests_total",
		Help: "Total HTTP requests by service, method, route and status code.",
	}, []string{"service", "method", "route", "status"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "artificialflow_http_request_duration_seconds",
		Help:    "HTTP request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"service", "method", "route"})

	dependencyReady = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "artificialflow_dependency_ready",
		Help: "Whether a required bounded dependency is currently ready (1 ready, 0 unavailable).",
	}, []string{"service", "dependency"})

	schemaReady = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "artificialflow_schema_ready",
		Help: "Whether the service database schema is compatible with this binary.",
	}, []string{"service"})

	runtimeFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "artificialflow_runtime_failures_total",
		Help: "Runtime claim, fencing, repository, and scheduler failures by bounded reason.",
	}, []string{"reason"})

	runtimeClaimOutcomes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "artificialflow_runtime_claim_outcomes_total",
		Help: "Runtime ownership claims by bounded work kind and outcome.",
	}, []string{"kind", "outcome"})

	workLag = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "artificialflow_work_lag_seconds",
		Help: "Observed lag for timer, job, and projection work.",
	}, []string{"service", "kind"})

	instanceMigrationOutcomes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "artificialflow_instance_migration_outcomes_total",
		Help: "Process-instance migration attempts by dry-run/apply mode and bounded outcome.",
	}, []string{"mode", "outcome"})
)

// Handler returns the Prometheus HTTP metrics handler.
func Handler() http.Handler {
	return promhttp.Handler()
}

// SetDependencyReady records readiness without placing addresses or errors in labels.
func SetDependencyReady(service, dependency string, ready bool) {
	value := 0.0
	if ready {
		value = 1
	}
	dependencyReady.WithLabelValues(service, dependency).Set(value)
}

// SetSchemaReady records whether the current schema is compatible.
func SetSchemaReady(service string, ready bool) {
	value := 0.0
	if ready {
		value = 1
	}
	schemaReady.WithLabelValues(service).Set(value)
}

// IncRuntimeFailure increments a bounded runtime failure reason.
func IncRuntimeFailure(reason string) {
	switch reason {
	case "claim", "fence", "repository", "timer", "job":
	default:
		reason = "other"
	}
	runtimeFailures.WithLabelValues(reason).Inc()
}

// RecordRuntimeClaim records successful acquisition and stale-fence rejection.
func RecordRuntimeClaim(kind, outcome string) {
	switch kind {
	case "timer", "job", "work", "outbox":
	default:
		kind = "other"
	}
	switch outcome {
	case "acquired", "stale":
	default:
		outcome = "other"
	}
	runtimeClaimOutcomes.WithLabelValues(kind, outcome).Inc()
}

// SetWorkLag records non-negative work lag for a bounded kind.
func SetWorkLag(service, kind string, seconds float64) {
	switch kind {
	case "timer", "job", "projection", "broker":
	default:
		kind = "other"
	}
	if seconds < 0 {
		seconds = 0
	}
	workLag.WithLabelValues(service, kind).Set(seconds)
}

// RecordInstanceMigration records a bounded migration outcome.
func RecordInstanceMigration(dryRun bool, outcome string) {
	mode := "apply"
	if dryRun {
		mode = "dry-run"
	}
	switch outcome {
	case "validated", "applied", "rejected", "failed", "not-found":
	default:
		outcome = "failed"
	}
	instanceMigrationOutcomes.WithLabelValues(mode, outcome).Inc()
}

// MuxMiddleware returns a gorilla/mux compatible middleware that records
// http_requests_total and http_request_duration_seconds for the given service.
func MuxMiddleware(service string) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			duration := time.Since(start).Seconds()
			status := strconv.Itoa(rw.status)
			route := routeLabel(r)
			httpRequestsTotal.WithLabelValues(service, r.Method, route, status).Inc()
			httpRequestDuration.WithLabelValues(service, r.Method, route).Observe(duration)
		})
	}
}

func routeLabel(r *http.Request) string {
	if route := mux.CurrentRoute(r); route != nil {
		if tpl, err := route.GetPathTemplate(); err == nil {
			return tpl
		}
	}
	if strings.TrimSpace(r.URL.Path) == "" {
		return "/"
	}
	return "_unmatched"
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
