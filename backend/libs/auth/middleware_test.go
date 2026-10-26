package auth

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		expected      string
	}{
		{name: "valid token", authorization: "Bearer abc123", expected: "abc123"},
		{name: "case-insensitive scheme", authorization: "bearer xyz", expected: "xyz"},
		{name: "missing token", authorization: "Bearer ", expected: ""},
		{name: "invalid scheme", authorization: "Basic x", expected: ""},
		{name: "empty", authorization: "", expected: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extractBearerToken(test.authorization); got != test.expected {
				t.Fatalf("expected %q, got %q", test.expected, got)
			}
		})
	}
}

func TestRequestTenantConflictPreservesBody(t *testing.T) {
	request := httptest.NewRequest("POST", "/instances?tenant_id=tenant-a", io.NopCloser(
		strings.NewReader(`{"tenantId":"tenant-b","context":{"tenant_id":"business-value"}}`),
	))
	request.Header.Set("Content-Type", "application/json")
	if !requestTenantConflict(request, "tenant-a") {
		t.Fatal("expected conflicting top-level tenant to be rejected")
	}
	body, err := io.ReadAll(request.Body)
	if err != nil || string(body) == "" {
		t.Fatalf("request body was not preserved: %q err=%v", body, err)
	}
}

func TestResolvePrincipalTenantCompatibilityAndStrictModes(t *testing.T) {
	t.Run("compat assigns reserved default", func(t *testing.T) {
		t.Setenv("TENANT_MODE", "compat")
		principal := &Principal{Subject: "legacy-client"}
		if err := resolvePrincipalTenant(principal); err != nil {
			t.Fatal(err)
		}
		if principal.TenantID != DefaultTenantID {
			t.Fatalf("tenant = %q", principal.TenantID)
		}
	})
	t.Run("strict rejects missing claim", func(t *testing.T) {
		t.Setenv("TENANT_MODE", "strict")
		principal := &Principal{Subject: "unscoped-client"}
		if err := resolvePrincipalTenant(principal); err == nil {
			t.Fatal("strict mode accepted a principal without a tenant claim")
		}
	})
}

func TestRequestTenantConflictChecksTrustedSelectorsOnly(t *testing.T) {
	request := httptest.NewRequest("POST", "/instances?tenantId=tenant-b", strings.NewReader(
		`{"tenant_id":"tenant-a","context":{"tenant_id":"business-value"}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	if !requestTenantConflict(request, "tenant-a") {
		t.Fatal("query selector from another tenant was accepted")
	}

	request = httptest.NewRequest("POST", "/instances", strings.NewReader(
		`{"tenantId":"tenant-a","context":{"tenant_id":"business-value"}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	if requestTenantConflict(request, "tenant-a") {
		t.Fatal("matching top-level tenant or nested business variable was rejected")
	}
}
