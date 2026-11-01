package httpproblem

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/artificialflow/artificialflow/backend/libs/logger"
)

func TestMiddlewareConvertsHTTPError(t *testing.T) {
	handler := logger.NewHTTPMiddleware("test").Middleware(Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "token required", http.StatusUnauthorized)
	})))
	request := httptest.NewRequest(http.MethodGet, "/v1/private", nil)
	request.Header.Set("X-Correlation-ID", "test-correlation")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != ContentType {
		t.Fatalf("content type = %q", got)
	}
	var problem Problem
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Detail != "token required" || problem.CorrelationID != "test-correlation" || problem.Instance != "/v1/private" {
		t.Fatalf("unexpected problem: %#v", problem)
	}
}

func TestMiddlewarePreservesUnversionedHTTPError(t *testing.T) {
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "legacy error", http.StatusBadRequest)
	}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/instances", nil))

	if got := response.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	if got := response.Body.String(); got != "legacy error\n" {
		t.Fatalf("body = %q", got)
	}
}

func TestMiddlewarePreservesJSONResponse(t *testing.T) {
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"structured"}`))
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := response.Body.String(); got != `{"error":"structured"}` {
		t.Fatalf("body = %q", got)
	}
}

func TestMiddlewareConvertsBadRequestAndUsesResponseCorrelationID(t *testing.T) {
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Correlation-ID", "response-correlation")
		http.Error(w, "invalid request", http.StatusBadRequest)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/instances", nil))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
	var problem Problem
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.CorrelationID != "response-correlation" || problem.Detail != "invalid request" {
		t.Fatalf("unexpected problem: %#v", problem)
	}
}
