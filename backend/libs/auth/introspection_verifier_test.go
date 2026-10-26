package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestIntrospectionVerifierAcceptsBrowserJWT(t *testing.T) {
	verifier, requests := newTestIntrospectionVerifier(t, func(w http.ResponseWriter, _ *http.Request) {
		writeIntrospectionResponse(t, w, map[string]any{
			"active":       true,
			"sub":          "user-1",
			"exp":          time.Now().Add(time.Hour).Unix(),
			"realm_access": map[string]any{"roles": []any{RoleAdmin}},
		})
	})

	principal, err := verifier.Verify(context.Background(), "header.payload.signature")
	if err != nil {
		t.Fatalf("expected browser JWT to be accepted by introspection: %v", err)
	}
	if principal.Subject != "user-1" || !principal.HasRole(RoleAdmin) {
		t.Fatalf("unexpected principal: %#v", principal)
	}
	if requests() != 1 {
		t.Fatalf("expected one introspection request, got %d", requests())
	}
}

func TestIntrospectionVerifierAcceptsOpaqueSDKClientTokenWithRole(t *testing.T) {
	const opaqueToken = "opaque-sdk-client-token"
	verifier, _ := newTestIntrospectionVerifier(t, func(w http.ResponseWriter, r *http.Request) {
		if username, password, ok := r.BasicAuth(); !ok || username != "artificialflow-api" || password != "introspection-secret" {
			t.Fatalf("unexpected introspection credentials: username=%q ok=%v", username, ok)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("token") != opaqueToken {
			t.Fatalf("unexpected token submitted for introspection")
		}
		writeIntrospectionResponse(t, w, map[string]any{
			"active":       true,
			"sub":          "sdk-client-1",
			"username":     "orders-worker",
			"exp":          time.Now().Add(time.Hour).Unix(),
			"realm_access": map[string]any{"roles": []any{RoleClient}},
		})
	})

	principal, err := verifier.Verify(context.Background(), opaqueToken)
	if err != nil {
		t.Fatalf("expected opaque SDK token to be accepted: %v", err)
	}
	if principal.Subject != "sdk-client-1" || !principal.HasRole(RoleClient) {
		t.Fatalf("expected artificialflowclient principal, got %#v", principal)
	}
	if principal.TokenMode != TokenModeIntrospection {
		t.Fatalf("expected introspection token mode, got %q", principal.TokenMode)
	}
}

func TestIntrospectionVerifierRejectsInvalidLifecycleStates(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
	}{
		{name: "malformed", payload: map[string]any{"active": true}},
		{name: "expired", payload: map[string]any{"active": true, "sub": "client-1", "exp": time.Now().Add(-time.Minute).Unix()}},
		{name: "not yet active", payload: map[string]any{"active": true, "sub": "client-1", "nbf": time.Now().Add(time.Minute).Unix()}},
		{name: "revoked", payload: map[string]any{"active": false, "reason": "revoked"}},
		{name: "unknown", payload: map[string]any{"active": false}},
		{name: "disabled client", payload: map[string]any{"active": false, "reason": "user inactive"}},
		{name: "deleted client", payload: map[string]any{"active": false, "reason": "user not found"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier, _ := newTestIntrospectionVerifier(t, func(w http.ResponseWriter, _ *http.Request) {
				writeIntrospectionResponse(t, w, test.payload)
			})
			if principal, err := verifier.Verify(context.Background(), "untrusted-token"); err == nil || principal != nil {
				t.Fatalf("expected token rejection, got principal=%#v err=%v", principal, err)
			}
		})
	}
}

func TestIntrospectionVerifierDoesNotExposeProviderResponseBody(t *testing.T) {
	verifier, _ := newTestIntrospectionVerifier(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `provider rejected secret token "do-not-log"`, http.StatusUnauthorized)
	})
	_, err := verifier.Verify(context.Background(), "untrusted-token")
	if err == nil {
		t.Fatal("expected introspection failure")
	}
	if strings.Contains(err.Error(), "do-not-log") {
		t.Fatalf("provider response body leaked through error: %v", err)
	}
}

func newTestIntrospectionVerifier(t *testing.T, handler http.HandlerFunc) (TokenVerifier, func() int) {
	t.Helper()
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	verifier, err := newIntrospectionVerifier(Config{
		TokenValidationMode:       TokenModeIntrospection,
		IntrospectionURL:          server.URL,
		IntrospectionClientID:     "artificialflow-api",
		IntrospectionClientSecret: "introspection-secret",
		IntrospectionAuthMethod:   "basic",
		ClaimRolesPath:            "roles,realm_access.roles,groups",
		ClaimSubjectPath:          "sub",
		EnforceAudience:           false,
	})
	if err != nil {
		t.Fatal(err)
	}
	return verifier, func() int { return requestCount }
}

func writeIntrospectionResponse(t *testing.T, w http.ResponseWriter, payload map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatal(err)
	}
}

func TestIntrospectionVerifierPostAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := url.Values{}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		for key, values := range r.Form {
			body[key] = values
		}
		if body.Get("client_id") != "artificialflow-api" || body.Get("client_secret") != "introspection-secret" {
			t.Fatalf("missing client_secret_post credentials")
		}
		writeIntrospectionResponse(t, w, map[string]any{"active": true, "sub": "client-1"})
	}))
	defer server.Close()
	verifier, err := newIntrospectionVerifier(Config{
		IntrospectionURL:          server.URL,
		IntrospectionClientID:     "artificialflow-api",
		IntrospectionClientSecret: "introspection-secret",
		IntrospectionAuthMethod:   "post",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), "token"); err != nil {
		t.Fatal(err)
	}
}

func TestIntrospectionVerifierUsesPublicIssuerHostForInternalProxy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "localhost:9181" {
			t.Fatalf("expected public issuer host, got %q", r.Host)
		}
		writeIntrospectionResponse(t, w, map[string]any{"active": true, "sub": "client-1"})
	}))
	defer server.Close()
	verifier, err := newIntrospectionVerifier(Config{
		IntrospectionURL:          server.URL,
		IntrospectionClientID:     "artificialflow-api",
		IntrospectionClientSecret: "introspection-secret",
		ExternalIssuerURL:         "http://localhost:9181",
		AllowInsecureIssuer:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), "token"); err != nil {
		t.Fatal(err)
	}
}
