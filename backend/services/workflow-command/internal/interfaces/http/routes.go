package api

import (
	"net/http"
	"strings"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/gorilla/mux"
	httpSwagger "github.com/swaggo/http-swagger/v2"
)

type auditResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *auditResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *auditResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

// auditSensitive emits structured, secret-free audit records for security
// operations, including authorization and handler failures.
func (h *Handler) auditSensitive(resource, action string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &auditResponseWriter{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		principal, _ := auth.PrincipalFromContext(r.Context())
		outcome := "success"
		if status >= http.StatusBadRequest {
			outcome = "failure"
		}
		resourceID := mux.Vars(r)["id"]
		if resourceID == "" {
			resourceID = mux.Vars(r)["key"]
		}
		h.log.Info(r.Context(), "security audit event", map[string]any{
			"audit":          true,
			"actor":          principal.Subject,
			"tenant":         auth.TenantIDFromContext(r.Context()),
			"resource":       resource,
			"resource_id":    resourceID,
			"action":         action,
			"outcome":        outcome,
			"status":         status,
			"correlation_id": strings.TrimSpace(r.Header.Get("X-Correlation-ID")),
		})
	})
}

// RegisterRoutes applies the role matrix. Admins can do everything; business
// users manage models only; clients are SDK/API service accounts that run
// workflows and act on tasks for their own end users; analytics has no API
// access yet.
func (h *Handler) RegisterRoutes(r *mux.Router) {
	admin := func(fn http.HandlerFunc) http.Handler {
		return auth.RequireAnyRole(auth.RoleAdmin)(fn)
	}
	runtime := func(fn http.HandlerFunc) http.Handler {
		return auth.RequireAnyRole(auth.RoleAdmin, auth.RoleClient)(fn)
	}
	modeling := func(fn http.HandlerFunc) http.Handler {
		return auth.RequireAnyRole(auth.RoleAdmin, auth.RoleBusiness, auth.RoleClient)(fn)
	}

	r.HandleFunc("/identity/config", h.getIdentityConfig).Methods("GET")
	r.HandleFunc("/identity/login", h.consoleLogin).Methods("POST")
	r.Handle("/workflows", modeling(h.deployWorkflow)).Methods("POST")
	r.Handle("/workflows", modeling(h.listWorkflows)).Methods("GET")
	r.Handle("/workflows/{id}", modeling(h.getWorkflow)).Methods("GET")
	r.Handle("/workflows/{id}", admin(h.deleteWorkflow)).Methods("DELETE")
	r.Handle("/decisions", modeling(h.listDecisions)).Methods("GET")
	r.Handle("/decisions", modeling(h.deployDecision)).Methods("POST")
	r.Handle("/decisions/{id}/evaluate", runtime(h.evaluateDecision)).Methods("POST")
	r.Handle("/tasks", runtime(h.searchUserTasks)).Methods("GET")
	r.Handle("/instances", runtime(h.startInstance)).Methods("POST")
	r.Handle("/instances", runtime(h.listInstances)).Methods("GET")
	r.Handle("/instances/history/completed", runtime(h.listCompletedInstanceHistory)).Methods("GET")
	r.Handle("/instances/{id}/variables", runtime(h.updateVariables)).Methods("POST")
	r.Handle("/instances/{id}/complete", runtime(h.completeTask)).Methods("POST")
	r.Handle("/instances/{id}/tasks", runtime(h.listUserTasks)).Methods("GET")
	r.Handle("/instances/{id}/tasks/{executionId}/claim", runtime(h.claimUserTask)).Methods("POST")
	r.Handle("/instances/{id}/tasks/{executionId}/complete", runtime(h.completeUserTask)).Methods("POST")
	r.Handle("/instances/{id}/jobs", admin(h.listInstanceJobs)).Methods("GET")
	r.Handle("/incidents", admin(h.listIncidents)).Methods("GET")
	r.Handle("/incidents/{key}/resolve", h.auditSensitive("incidents", "update", admin(h.resolveIncident))).Methods("POST")
	r.Handle("/jobs/{key}/retry", h.auditSensitive("incidents", "update", admin(h.retryJob))).Methods("POST")
	r.Handle("/instances/{id}", runtime(h.getInstance)).Methods("GET")
	r.Handle("/instances/{id}", admin(h.deleteInstance)).Methods("DELETE")
	r.Handle("/instances/{id}/migration/dry-run", h.auditSensitive("migrations", "read", admin(h.dryRunInstanceMigration))).Methods("POST")
	r.Handle("/instances/{id}/migration", h.auditSensitive("migrations", "execute", admin(h.applyInstanceMigration))).Methods("POST")
	r.Handle("/signals", runtime(h.publishSignal)).Methods("POST")
	r.Handle("/messages", runtime(h.publishMessage)).Methods("POST")
	r.Handle("/escalations", runtime(h.publishEscalation)).Methods("POST")
	r.Handle("/jobs/activate", runtime(h.activateJobs)).Methods("POST")
	r.Handle("/jobs/capabilities", runtime(h.jobsCapabilities)).Methods("GET")
	r.Handle("/jobs/{key}/complete", runtime(h.completeJob)).Methods("POST")
	r.Handle("/jobs/{key}/fail", runtime(h.failJob)).Methods("POST")
	r.Handle("/jobs/{key}/extend-lock", runtime(h.extendJobLock)).Methods("POST")
	r.Handle("/internal/metrics", admin(h.engineMetrics)).Methods("GET")
	r.PathPrefix("/swagger/").Handler(httpSwagger.WrapHandler)
}
