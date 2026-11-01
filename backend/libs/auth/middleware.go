package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/artificialflow/artificialflow/backend/libs/logger"
)

type Middleware struct {
	provider       ConfigProvider
	logger         *logger.Logger
	publicPaths    map[string]struct{}
	mu             sync.RWMutex
	verifier       TokenVerifier
	verifierConfig string
}

func NewMiddleware(ctx context.Context, cfg Config) (*Middleware, error) {
	return NewDynamicMiddleware(ctx, StaticConfigProvider{Config: cfg}, nil)
}

func NewDynamicMiddleware(ctx context.Context, provider ConfigProvider, publicPaths []string) (*Middleware, error) {
	log := logger.New("auth-middleware")
	if provider == nil {
		return nil, fmt.Errorf("auth middleware requires a config provider")
	}
	paths := map[string]struct{}{
		"/health": {},
		"/livez":  {},
		"/readyz": {},
	}
	for _, path := range publicPaths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		paths[trimmed] = struct{}{}
	}
	m := &Middleware{
		provider:    provider,
		logger:      log,
		publicPaths: paths,
	}
	initialConfig, err := provider.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if err := initialConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid authentication configuration: %w", err)
	}
	if initialConfig.Enabled() {
		verifier, err := m.buildVerifier(ctx, initialConfig)
		if err != nil {
			return nil, err
		}
		m.verifier = verifier
		m.verifierConfig = configFingerprint(initialConfig)
	}
	return m, nil
}

// Handler verifies the Bearer token and adds claims to the context
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		publicPath := r.URL.Path
		if strings.HasPrefix(publicPath, "/v1/") {
			publicPath = strings.TrimPrefix(publicPath, "/v1")
		}
		if _, ok := m.publicPaths[publicPath]; ok {
			next.ServeHTTP(w, r)
			return
		}
		cfg, err := m.provider.GetConfig(r.Context())
		if err != nil {
			m.logger.Error(r.Context(), "failed to resolve auth configuration", map[string]any{"error": err.Error()})
			http.Error(w, "Authentication configuration unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := cfg.Validate(); err != nil {
			m.logger.Error(r.Context(), "invalid auth configuration", map[string]any{"error": err.Error()})
			http.Error(w, "Authentication configuration unavailable", http.StatusServiceUnavailable)
			return
		}
		if !cfg.Enabled() {
			principal := disabledAuthPrincipal()
			principal.TenantID = DefaultTenantID
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
			return
		}
		verifier, err := m.getVerifier(r.Context(), cfg)
		if err != nil {
			m.logger.Error(r.Context(), "failed to initialize token verifier", map[string]any{"error": err.Error()})
			http.Error(w, "Authentication configuration unavailable", http.StatusServiceUnavailable)
			return
		}

		rawAccessToken := extractBearerToken(r.Header.Get("Authorization"))

		if rawAccessToken == "" {
			http.Error(w, "Authorization header required", http.StatusUnauthorized)
			return
		}

		principal, err := verifier.Verify(r.Context(), rawAccessToken)
		if err != nil {
			m.logger.Error(r.Context(), "token verification failed", map[string]any{"error": err.Error()})
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}
		if err := resolvePrincipalTenant(principal); err != nil {
			http.Error(w, "Tenant claim required", http.StatusUnauthorized)
			return
		}
		// X-Tenant-ID may be sent by older gateways as a consistency assertion,
		// but it can never select a tenant.
		if requested := strings.TrimSpace(r.Header.Get("X-Tenant-ID")); requested != "" && requested != principal.TenantID {
			http.Error(w, "Tenant conflict", http.StatusBadRequest)
			return
		}
		if requestTenantConflict(r, principal.TenantID) {
			http.Error(w, "Tenant conflict", http.StatusBadRequest)
			return
		}

		// Add claims to context
		ctx := WithPrincipal(r.Context(), *principal)
		ctx = context.WithValue(ctx, "user_claims", principal.Claims)
		// Also standard sub/user_id
		ctx = context.WithValue(ctx, "user_id", principal.Subject)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestTenantConflict(r *http.Request, trustedTenant string) bool {
	for _, key := range []string{"tenant_id", "tenantId"} {
		if requested := strings.TrimSpace(r.URL.Query().Get(key)); requested != "" && requested != trustedTenant {
			return true
		}
	}
	if r.Body == nil || !strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return false
	}
	const inspectLimit = int64(20 << 20)
	body, err := io.ReadAll(io.LimitReader(r.Body, inspectLimit+1))
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		return false
	}
	if int64(len(body)) > inspectLimit {
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return false
	}
	for _, key := range []string{"tenant_id", "tenantId"} {
		raw, ok := object[key]
		if !ok {
			continue
		}
		var requested string
		if json.Unmarshal(raw, &requested) == nil {
			requested = strings.TrimSpace(requested)
			if requested != "" && requested != trustedTenant {
				return true
			}
		}
	}
	return false
}

func resolvePrincipalTenant(principal *Principal) error {
	if principal == nil {
		return fmt.Errorf("principal is required")
	}
	principal.TenantID = strings.TrimSpace(principal.TenantID)
	if principal.TenantID == "" {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("TENANT_MODE")), "strict") {
			return fmt.Errorf("tenant claim required")
		}
		principal.TenantID = DefaultTenantID
	}
	return nil
}

func disabledAuthPrincipal() Principal {
	return Principal{
		Subject:   "local-disabled-auth",
		Roles:     []string{RoleAdmin},
		TokenMode: "disabled",
		TenantID:  DefaultTenantID,
	}
}

func (m *Middleware) getVerifier(ctx context.Context, cfg Config) (TokenVerifier, error) {
	fingerprint := configFingerprint(cfg)
	m.mu.RLock()
	if m.verifier != nil && m.verifierConfig == fingerprint {
		verifier := m.verifier
		m.mu.RUnlock()
		return verifier, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.verifier != nil && m.verifierConfig == fingerprint {
		return m.verifier, nil
	}
	verifier, err := m.buildVerifier(ctx, cfg)
	if err != nil {
		return nil, err
	}
	m.verifier = verifier
	m.verifierConfig = fingerprint
	return verifier, nil
}

func (m *Middleware) buildVerifier(ctx context.Context, cfg Config) (TokenVerifier, error) {
	switch cfg.TokenValidationMode {
	case TokenModeIntrospection:
		return newIntrospectionVerifier(cfg)
	default:
		return newJWTVerifier(ctx, cfg, m.logger)
	}
}

func configFingerprint(cfg Config) string {
	parts := []string{
		strings.TrimSpace(cfg.InternalIssuerURL),
		strings.TrimSpace(cfg.ExternalIssuerURL),
		strings.TrimSpace(cfg.ClientID),
		strings.TrimSpace(cfg.TokenValidationMode),
		strings.TrimSpace(cfg.IntrospectionURL),
		strings.TrimSpace(cfg.IntrospectionClientID),
		cfg.IntrospectionClientSecret,
		strings.TrimSpace(cfg.IntrospectionAuthMethod),
		strings.TrimSpace(cfg.ClaimSubjectPath),
		strings.TrimSpace(cfg.ClaimRolesPath),
		strings.TrimSpace(cfg.ClaimTenantPath),
		strings.TrimSpace(cfg.ClaimEmailPath),
		strings.TrimSpace(cfg.ClaimNamePath),
		strings.Join(cfg.AllowedAlgorithms, ","),
		cfg.HTTPTimeout.String(),
	}
	if cfg.EnforceAudience {
		parts = append(parts, "audience:on")
	} else {
		parts = append(parts, "audience:off")
	}
	if cfg.AllowInsecureIssuer {
		parts = append(parts, "issuer:insecure")
	} else {
		parts = append(parts, "issuer:strict")
	}
	if cfg.Production {
		parts = append(parts, "production")
	}
	return strings.Join(parts, "|")
}

func extractBearerToken(rawAuthorization string) string {
	authorization := strings.TrimSpace(rawAuthorization)
	if authorization == "" {
		return ""
	}
	parts := strings.SplitN(authorization, " ", 2)
	if len(parts) != 2 {
		return ""
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
