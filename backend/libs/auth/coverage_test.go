package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/logger"
	"github.com/go-jose/go-jose/v4"
)

func TestNewJWTVerifierVerifiesSignedToken(t *testing.T) {
	key := testRSAKey(t)
	issuerServer := oidcIssuerServer(t, key, "")
	defer issuerServer.Close()

	cfg := Config{
		InternalIssuerURL: issuerServer.URL,
		ExternalIssuerURL: issuerServer.URL,
		ClientID:          "workflow-backend",
		HTTPTimeout:       time.Second,
		AllowedAlgorithms: []string{"RS256"},
		EnforceAudience:   true,
		ClaimSubjectPath:  "sub",
		ClaimRolesPath:    "roles",
		ClaimTenantPath:   "tenant_id",
		ClaimEmailPath:    "email",
		ClaimNamePath:     "name",
	}
	verifier, err := newJWTVerifier(context.Background(), cfg, logger.New("auth-test"))
	if err != nil {
		t.Fatal(err)
	}
	principal, err := verifier.Verify(context.Background(), signTestToken(t, key, issuerServer.URL, "workflow-backend"))
	if err != nil {
		t.Fatal(err)
	}
	if principal.Subject != "user-1" || principal.TenantID != "tenant-a" || principal.Issuer != issuerServer.URL {
		t.Fatalf("principal = %+v", principal)
	}
	if _, err := verifier.Verify(context.Background(), "not-a-token"); err == nil {
		t.Fatal("expected invalid token to be rejected")
	}
}

func TestNewJWTVerifierRewritesInsecureIssuer(t *testing.T) {
	key := testRSAKey(t)
	const externalIssuer = "http://login.example/realms/af"
	internal := oidcIssuerServer(t, key, externalIssuer)
	defer internal.Close()

	cfg := Config{
		InternalIssuerURL:   internal.URL + "/realms/af",
		ExternalIssuerURL:   externalIssuer,
		ClientID:            "workflow-backend",
		AllowInsecureIssuer: true,
		EnforceAudience:     false,
		ClaimRolesPath:      "roles",
		ClaimTenantPath:     "tenant_id",
	}
	verifier, err := newJWTVerifier(context.Background(), cfg, logger.New("auth-test"))
	if err != nil {
		t.Fatal(err)
	}
	principal, err := verifier.Verify(context.Background(), signTestToken(t, key, cfg.ExternalIssuerURL, "workflow-backend"))
	if err != nil {
		t.Fatal(err)
	}
	if principal.Issuer != cfg.ExternalIssuerURL {
		t.Fatalf("issuer = %q", principal.Issuer)
	}
}

func TestNewJWTVerifierRejectsMissingAndInvalidIssuers(t *testing.T) {
	log := logger.New("auth-test")
	if _, err := newJWTVerifier(context.Background(), Config{}, log); err == nil {
		t.Fatal("expected missing internal issuer to fail")
	}
	cfg := Config{
		InternalIssuerURL:   "not a url",
		ExternalIssuerURL:   "http://login.example",
		AllowInsecureIssuer: true,
		ClientID:            "workflow-backend",
	}
	if _, err := newJWTVerifier(context.Background(), cfg, log); err == nil {
		t.Fatal("expected invalid internal issuer to fail before discovery")
	}
}

func TestIssuerRewriteHTTPClient(t *testing.T) {
	if _, err := newIssuerRewriteHTTPClient("://bad", "http://internal"); err == nil {
		t.Fatal("expected external URL parse error")
	}
	if _, err := newIssuerRewriteHTTPClient("http://external", "://bad"); err == nil {
		t.Fatal("expected internal URL parse error")
	}
	if _, err := newIssuerRewriteHTTPClient("external", "http://internal"); err == nil {
		t.Fatal("expected external URL without scheme to fail")
	}
	if _, err := newIssuerRewriteHTTPClient("http://external", "internal"); err == nil {
		t.Fatal("expected internal URL without scheme to fail")
	}

	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "login.example" {
			t.Errorf("host = %q", r.Host)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer internal.Close()

	client, err := newIssuerRewriteHTTPClient("http://login.example/realms/af", internal.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get("http://login.example/realms/af/keys")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", response.StatusCode)
	}

	passthrough := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer passthrough.Close()
	response, err = client.Get(passthrough.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	unreachable, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&issuerRewriteTransport{}).RoundTrip(unreachable); err == nil {
		t.Fatal("expected default transport to reject a closed port")
	}
}

func TestDynamicMiddlewareAndHandlerBranches(t *testing.T) {
	if _, err := NewDynamicMiddleware(context.Background(), nil, nil); err == nil {
		t.Fatal("expected nil provider to fail")
	}
	if _, err := NewDynamicMiddleware(context.Background(), errConfigProvider{err: errors.New("down")}, nil); err == nil {
		t.Fatal("expected provider error to fail")
	}
	if _, err := NewMiddleware(context.Background(), Config{Production: true}); err == nil {
		t.Fatal("expected production without auth to fail validation")
	}

	introspection := Config{
		ClientID:            "workflow-backend",
		TokenValidationMode: TokenModeIntrospection,
		IntrospectionURL:    "http://user:pass@issuer.example/introspect",
	}
	if _, err := NewDynamicMiddleware(context.Background(), StaticConfigProvider{Config: introspection}, []string{"", "/metrics"}); err == nil {
		t.Fatal("expected introspection URL with userinfo to fail verifier construction")
	}

	disabled := &Middleware{
		provider:    StaticConfigProvider{Config: Config{}},
		logger:      logger.New("auth-test"),
		publicPaths: map[string]struct{}{"/health": {}},
	}
	if status := serveAuth(disabled, httptest.NewRequest(http.MethodGet, "/v1/health", nil)); status != http.StatusOK {
		t.Fatalf("public status = %d", status)
	}
	if status := serveAuth(disabled, httptest.NewRequest(http.MethodGet, "/workflows", nil)); status != http.StatusOK {
		t.Fatalf("disabled auth status = %d", status)
	}

	unavailable := &Middleware{
		provider: errConfigProvider{err: errors.New("down")},
		logger:   logger.New("auth-test"),
	}
	if status := serveAuth(unavailable, httptest.NewRequest(http.MethodGet, "/workflows", nil)); status != http.StatusServiceUnavailable {
		t.Fatalf("provider error status = %d", status)
	}

	invalid := &Middleware{
		provider: StaticConfigProvider{Config: Config{Production: true}},
		logger:   logger.New("auth-test"),
	}
	if status := serveAuth(invalid, httptest.NewRequest(http.MethodGet, "/workflows", nil)); status != http.StatusServiceUnavailable {
		t.Fatalf("invalid config status = %d", status)
	}

	broken := &Middleware{
		provider: StaticConfigProvider{Config: introspection},
		logger:   logger.New("auth-test"),
	}
	if status := serveAuth(broken, httptest.NewRequest(http.MethodGet, "/workflows", nil)); status != http.StatusServiceUnavailable {
		t.Fatalf("verifier init status = %d", status)
	}

	cfg := Config{InternalIssuerURL: "http://issuer.example", ClientID: "workflow-backend"}
	middleware := &Middleware{
		provider:       StaticConfigProvider{Config: cfg},
		logger:         logger.New("auth-test"),
		verifier:       fakeTokenVerifier{err: errors.New("bad token")},
		verifierConfig: configFingerprint(cfg),
	}
	missing := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	if status := serveAuth(middleware, missing); status != http.StatusUnauthorized {
		t.Fatalf("missing bearer status = %d", status)
	}
	rejected := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	rejected.Header.Set("Authorization", "Bearer token")
	if status := serveAuth(middleware, rejected); status != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d", status)
	}

	t.Setenv("TENANT_MODE", "strict")
	middleware.verifier = fakeTokenVerifier{principal: Principal{Subject: "user-1"}}
	unscoped := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	unscoped.Header.Set("Authorization", "Bearer token")
	if status := serveAuth(middleware, unscoped); status != http.StatusUnauthorized {
		t.Fatalf("missing tenant status = %d", status)
	}

	middleware.verifier = fakeTokenVerifier{principal: Principal{Subject: "user-1", TenantID: "tenant-a", Roles: []string{RoleClient}}}
	conflict := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	conflict.Header.Set("Authorization", "Bearer token")
	conflict.Header.Set("X-Tenant-ID", "tenant-b")
	if status := serveAuth(middleware, conflict); status != http.StatusBadRequest {
		t.Fatalf("header conflict status = %d", status)
	}

	queryConflict := httptest.NewRequest(http.MethodPost, "/workflows?tenant_id=tenant-b", strings.NewReader(`{"name":"ok"}`))
	queryConflict.Header.Set("Authorization", "Bearer token")
	queryConflict.Header.Set("Content-Type", "application/json")
	if status := serveAuth(middleware, queryConflict); status != http.StatusBadRequest {
		t.Fatalf("query conflict status = %d", status)
	}

	accepted := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(`{"tenant_id":"tenant-a"}`))
	accepted.Header.Set("Authorization", "Bearer token")
	accepted.Header.Set("Content-Type", "application/json")
	accepted.Header.Set("X-Tenant-ID", "tenant-a")
	recorder := httptest.NewRecorder()
	var got string
	middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = TenantIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, accepted)
	if recorder.Code != http.StatusNoContent || got != "tenant-a" {
		t.Fatalf("status=%d tenant=%q", recorder.Code, got)
	}

	changed := Config{
		ClientID:            "workflow-backend",
		TokenValidationMode: TokenModeIntrospection,
		IntrospectionURL:    "http://user:pass@issuer.example/introspect",
	}
	middleware.provider = StaticConfigProvider{Config: changed}
	middleware.verifier = nil
	if status := serveAuth(middleware, accepted); status != http.StatusServiceUnavailable {
		t.Fatalf("rebuilt verifier status = %d", status)
	}
}

func TestRequestTenantConflictEdgeCases(t *testing.T) {
	plain := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(`{"tenant_id":"other"}`))
	if requestTenantConflict(plain, "tenant-a") {
		t.Fatal("non-json body was treated as a tenant selector")
	}

	broken := httptest.NewRequest(http.MethodPost, "/workflows", nil)
	broken.Body = io.NopCloser(errReadCloser{})
	broken.Header.Set("Content-Type", "application/json")
	if requestTenantConflict(broken, "tenant-a") {
		t.Fatal("unreadable body was treated as a conflict")
	}

	oversized := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(strings.Repeat("a", int(20<<20)+1)))
	oversized.Header.Set("Content-Type", "application/json")
	if requestTenantConflict(oversized, "tenant-a") {
		t.Fatal("oversized body was treated as a conflict")
	}

	invalid := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(`not-json`))
	invalid.Header.Set("Content-Type", "application/json")
	if requestTenantConflict(invalid, "tenant-a") {
		t.Fatal("invalid json was treated as a conflict")
	}

	number := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(`{"tenantId":1}`))
	number.Header.Set("Content-Type", "application/json")
	if requestTenantConflict(number, "tenant-a") {
		t.Fatal("non-string tenant field was treated as a conflict")
	}
}

func TestAuthHelpersCoverRemainingBranches(t *testing.T) {
	if TenantIDFromContext(context.Background()) != DefaultTenantID {
		t.Fatal("missing principal should use the default tenant")
	}
	if TenantIDFromContext(WithPrincipal(context.Background(), Principal{})) != DefaultTenantID {
		t.Fatal("blank tenant should use the default tenant")
	}
	if !IsStandardRole(" ArtificialFlowAdmin ") || IsStandardRole("accountant") {
		t.Fatal("standard role classification failed")
	}

	handler := RequireAuthenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", anonymous.Code)
	}
	authed := httptest.NewRecorder()
	handler.ServeHTTP(authed, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(
		WithPrincipal(context.Background(), Principal{Subject: "user-1"}),
	))
	if authed.Code != http.StatusNoContent {
		t.Fatalf("authenticated status = %d", authed.Code)
	}

	if normalizeTokenMode(" opaque ") != TokenModeIntrospection || normalizeTokenMode("unknown") != TokenModeJWT {
		t.Fatal("token mode normalization failed")
	}
	t.Setenv("AUTH_ENFORCE_AUDIENCE", "yes")
	t.Setenv("AUTH_HTTP_TIMEOUT_SECONDS", "0")
	if !envBoolOrDefault("AUTH_ENFORCE_AUDIENCE", false) || envDurationSeconds("AUTH_HTTP_TIMEOUT_SECONDS") != 0 {
		t.Fatal("env helpers did not honor explicit values")
	}
	t.Setenv("AUTH_HTTP_TIMEOUT_SECONDS", "nope")
	if envDurationOrDefault("AUTH_HTTP_TIMEOUT_SECONDS", 3*time.Second) != 3*time.Second {
		t.Fatal("invalid duration should fall back")
	}
	if !isClusterLocalHost("postgres") || !isClusterLocalHost("keycloak.svc") || isClusterLocalHost("issuer.example") || isClusterLocalHost("") {
		t.Fatal("cluster-local host classification failed")
	}

	production := Config{Production: true, TokenValidationMode: TokenModeJWT, ClientID: "workflow-backend", InternalIssuerURL: "http://issuer.example", ExternalIssuerURL: "http://issuer.example", HTTPTimeout: time.Second, AllowedAlgorithms: []string{"RS256"}, EnforceAudience: true}
	if err := production.Validate(); err == nil {
		t.Fatal("production JWT issuer must use https")
	}
	if strict := configFingerprint(production); !strings.Contains(strict, "production") || !strings.Contains(strict, "issuer:strict") {
		t.Fatalf("fingerprint = %s", strict)
	}
	insecure := production
	insecure.AllowInsecureIssuer = true
	insecure.EnforceAudience = false
	if fingerprint := configFingerprint(insecure); !strings.Contains(fingerprint, "issuer:insecure") || !strings.Contains(fingerprint, "audience:off") {
		t.Fatalf("fingerprint = %s", fingerprint)
	}

	principal := principalFromClaims(map[string]any{
		"sub":   stringerValue("user-1"),
		"roles": "artificialflowadmin, artificialflowadmin",
		"aud":   []any{"workflow-backend", "workflow-backend", ""},
		"email": " user@example.com ",
	}, "", Config{ClaimSubjectPath: "sub", ClaimRolesPath: "roles", ClaimEmailPath: "email"}, TokenModeJWT)
	if principal.Subject != "user-1" || principal.Email != "user@example.com" || len(principal.Audience) != 1 {
		t.Fatalf("principal = %+v", principal)
	}
	if valueAsStringSlice(123) != nil || valueAsString(123) != "" {
		t.Fatal("non-string claim values should be ignored")
	}
	if !valueAsBool(" YES ") || !valueAsBool(float64(2)) || valueAsBool("no") || valueAsBool(1) {
		t.Fatal("bool claim parsing failed")
	}
	if unix, ok := numericDate(json.Number("1700000000")); !ok || unix != 1700000000 {
		t.Fatal("numeric date failed")
	}
	if _, ok := numericDate(json.Number("not-a-number")); ok {
		t.Fatal("invalid numeric date was accepted")
	}
	if _, ok := numericDate("2026-11-01T00:00:00Z"); !ok {
		t.Fatal("RFC3339 date was rejected")
	}
	if _, ok := numericDate("yesterday"); ok {
		t.Fatal("invalid date string was accepted")
	}
}

type errConfigProvider struct {
	err error
}

func (p errConfigProvider) GetConfig(context.Context) (Config, error) {
	return Config{}, p.err
}

type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, errors.New("read failed") }

type stringerValue string

func (s stringerValue) String() string { return string(s) }

func serveAuth(middleware *Middleware, request *http.Request) int {
	recorder := httptest.NewRecorder()
	middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(recorder, request)
	return recorder.Code
}

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func oidcIssuerServer(t *testing.T, key *rsa.PrivateKey, publishedIssuer string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		issuer := publishedIssuer
		if issuer == "" {
			issuer = server.URL
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                issuer,
				"authorization_endpoint":                issuer + "/auth",
				"token_endpoint":                        issuer + "/token",
				"jwks_uri":                              issuer + "/keys",
				"response_types_supported":              []string{"id_token"},
				"subject_types_supported":               []string{"public"},
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case strings.HasSuffix(r.URL.Path, "/keys"):
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
				Key:       &key.PublicKey,
				KeyID:     "test",
				Algorithm: string(jose.RS256),
				Use:       "sig",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}

func signTestToken(t *testing.T, key *rsa.PrivateKey, issuer, audience string) string {
	t.Helper()
	options := &jose.SignerOptions{}
	options.WithType("JWT")
	options.WithHeader("kid", "test")
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, options)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	payload, err := json.Marshal(map[string]any{
		"iss":       issuer,
		"sub":       "user-1",
		"aud":       audience,
		"exp":       now.Add(time.Hour).Unix(),
		"iat":       now.Unix(),
		"tenant_id": "tenant-a",
		"roles":     []string{RoleClient},
		"email":     "user@example.com",
		"name":      "Ada",
	})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}
