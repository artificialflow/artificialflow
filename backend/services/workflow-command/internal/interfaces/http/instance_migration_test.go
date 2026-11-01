package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/gorilla/mux"
)

func TestInstanceMigrationRouteDoesNotDiscloseInvalidIdentifier(t *testing.T) {
	handler := setupTestHandler(t)
	request := httptest.NewRequest(http.MethodPost, "/instances/not-a-key/migration/dry-run", strings.NewReader(`{}`))
	request = mux.SetURLVars(request, map[string]string{"id": "not-a-key"})
	request = request.WithContext(auth.WithPrincipal(request.Context(), auth.Principal{
		Subject: "admin", TenantID: "tenant-a", Roles: []string{auth.RoleAdmin},
	}))
	response := httptest.NewRecorder()
	handler.dryRunInstanceMigration(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "Not Found") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestInstanceMigrationRouteRejectsUnknownFields(t *testing.T) {
	handler := setupTestHandler(t)
	request := httptest.NewRequest(http.MethodPost, "/instances/1/migration/dry-run", strings.NewReader(`{"tenantId":"other"}`))
	request = mux.SetURLVars(request, map[string]string{"id": "1"})
	response := httptest.NewRecorder()
	handler.dryRunInstanceMigration(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}
