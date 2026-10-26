package auth

import (
	"context"
	"strings"
)

const DefaultTenantID = "default"

type Principal struct {
	Subject   string         `json:"subject"`
	Issuer    string         `json:"issuer,omitempty"`
	Audience  []string       `json:"audience,omitempty"`
	Email     string         `json:"email,omitempty"`
	Name      string         `json:"name,omitempty"`
	TenantID  string         `json:"tenant_id,omitempty"`
	Roles     []string       `json:"roles,omitempty"`
	TokenMode string         `json:"token_mode,omitempty"`
	Claims    map[string]any `json:"claims,omitempty"`
}

type principalContextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	value := ctx.Value(principalContextKey{})
	if value == nil {
		return Principal{}, false
	}
	principal, ok := value.(Principal)
	if !ok {
		return Principal{}, false
	}
	return principal, true
}

// TenantIDFromContext returns the trusted tenant resolved by authentication.
// Compatibility mode intentionally maps missing 1.x claims to the reserved
// default tenant; request payloads and headers are never used as authority.
func TenantIDFromContext(ctx context.Context) string {
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		return DefaultTenantID
	}
	tenant := strings.TrimSpace(principal.TenantID)
	if tenant == "" {
		return DefaultTenantID
	}
	return tenant
}
