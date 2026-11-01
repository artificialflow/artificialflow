package auth

import (
	"testing"
	"time"
)

func TestResolveConfigFromEnv_PrefersAuthContract(t *testing.T) {
	t.Setenv("AUTH_ISSUER_INTERNAL_URL", "https://auth-internal.example.com")
	t.Setenv("AUTH_ISSUER_PUBLIC_URL", "https://auth-public.example.com")
	t.Setenv("AUTH_CLIENT_ID", "auth-client")
	t.Setenv("AUTH_TOKEN_MODE", "jwt")
	t.Setenv("OIDC_ISSUER_INTERNAL_URL", "https://oidc-internal.example.com")
	t.Setenv("OIDC_ISSUER_PUBLIC_URL", "https://oidc-public.example.com")
	t.Setenv("OIDC_CLIENT_ID", "oidc-client")

	cfg := ResolveConfigFromEnv()
	if cfg.InternalIssuerURL != "https://auth-internal.example.com" {
		t.Fatalf("expected AUTH_ISSUER_INTERNAL_URL precedence, got %s", cfg.InternalIssuerURL)
	}
	if cfg.ExternalIssuerURL != "https://auth-public.example.com" {
		t.Fatalf("expected AUTH_ISSUER_PUBLIC_URL precedence, got %s", cfg.ExternalIssuerURL)
	}
	if cfg.ClientID != "auth-client" {
		t.Fatalf("expected AUTH_CLIENT_ID precedence, got %s", cfg.ClientID)
	}
	if cfg.TokenValidationMode != TokenModeJWT {
		t.Fatalf("expected jwt mode, got %s", cfg.TokenValidationMode)
	}
}

func TestConfigValidateProductionFailsClosed(t *testing.T) {
	base := Config{
		InternalIssuerURL:   "https://login.example.com",
		ExternalIssuerURL:   "https://login.example.com",
		ClientID:            "workflow-backend",
		TokenValidationMode: TokenModeJWT,
		EnforceAudience:     true,
		Production:          true,
		AllowedAlgorithms:   []string{"RS256"},
		HTTPTimeout:         10 * time.Second,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"http issuer", func(c *Config) {
			c.InternalIssuerURL, c.ExternalIssuerURL = "http://login.example.com", "http://login.example.com"
		}},
		{"audience disabled", func(c *Config) { c.EnforceAudience = false }},
		{"insecure rewrite", func(c *Config) { c.AllowInsecureIssuer = true }},
		{"algorithm allowlist empty", func(c *Config) { c.AllowedAlgorithms = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			tt.edit(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected invalid production config to fail")
			}
		})
	}
}

func TestResolveConfigFromEnv_IgnoresOIDCAliasIssuers(t *testing.T) {
	t.Setenv("OIDC_ISSUER_INTERNAL_URL", "https://oidc-internal.example.com")
	t.Setenv("OIDC_ISSUER_PUBLIC_URL", "https://oidc-public.example.com")
	cfg := ResolveConfigFromEnv()
	if cfg.InternalIssuerURL != "" {
		t.Fatalf("expected empty internal issuer without AUTH_ISSUER_INTERNAL_URL, got %s", cfg.InternalIssuerURL)
	}
	if cfg.ExternalIssuerURL != "" {
		t.Fatalf("expected empty external issuer without AUTH_ISSUER_PUBLIC_URL, got %s", cfg.ExternalIssuerURL)
	}
}

func TestResolveConfigFromEnv_IntrospectionAuthMethod(t *testing.T) {
	t.Setenv("AUTH_INTROSPECTION_AUTH_METHOD", "post")
	cfg := ResolveConfigFromEnv()
	if cfg.IntrospectionAuthMethod != "post" {
		t.Fatalf("expected AUTH_INTROSPECTION_AUTH_METHOD value, got %s", cfg.IntrospectionAuthMethod)
	}
}

func TestResolveConfigFromEnv_IntrospectionClientIDDefaultsToClientID(t *testing.T) {
	t.Setenv("AUTH_CLIENT_ID", "workflow-backend")
	t.Setenv("AUTH_INTROSPECTION_CLIENT_ID", "")
	cfg := ResolveConfigFromEnv()
	if cfg.IntrospectionClientID != "workflow-backend" {
		t.Fatalf("expected AUTH_CLIENT_ID fallback for introspection client id, got %s", cfg.IntrospectionClientID)
	}
}

func TestConfigEnabled_IntrospectionRequiresURL(t *testing.T) {
	cfg := Config{TokenValidationMode: TokenModeIntrospection}
	if cfg.Enabled() {
		t.Fatal("expected introspection mode without URL to be disabled")
	}
	cfg.IntrospectionURL = "https://auth.example.com/oauth2/introspect"
	if !cfg.Enabled() {
		t.Fatal("expected introspection mode with URL to be enabled")
	}
}

func TestResolveConfigFromEnv_ClientIDDefaultsToWorkflowBackend(t *testing.T) {
	t.Setenv("AUTH_CLIENT_ID", "")

	if cfg := ResolveConfigFromEnv(); cfg.ClientID != "workflow-backend" {
		t.Fatalf("expected default client id, got %q", cfg.ClientID)
	}
}
