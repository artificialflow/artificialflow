package iam

import "testing"

func TestParseDeploymentModeAcceptsBooleans(t *testing.T) {
	cases := map[string]struct{ enabled, set bool }{
		"true":   {true, true},
		" true ": {true, true},
		"false":  {false, true},
		"":       {false, false},
	}
	for raw, want := range cases {
		enabled, set, err := parseDeploymentMode(raw)
		if err != nil {
			t.Fatalf("parseDeploymentMode(%q) returned error: %v", raw, err)
		}
		if enabled != want.enabled || set != want.set {
			t.Fatalf("parseDeploymentMode(%q)=(%v,%v), want (%v,%v)", raw, enabled, set, want.enabled, want.set)
		}
	}
}

func TestParseDeploymentModeRejectsOtherValues(t *testing.T) {
	for _, raw := range []string{"external", "disabled", "TRUE", "1", "yes", "off", "unknown"} {
		if _, _, err := parseDeploymentMode(raw); err == nil {
			t.Fatalf("parseDeploymentMode(%q) should fail", raw)
		}
	}
}

func TestResolveDeploymentConfigRejectsLegacyModeStrings(t *testing.T) {
	for _, raw := range []string{"external", "disabled"} {
		t.Setenv("IAM_DEPLOYMENT_MODE", raw)
		_, err := ResolveDeploymentConfigFromEnv()
		if err == nil {
			t.Fatalf("expected IAM_DEPLOYMENT_MODE=%s to fail", raw)
		}
		want := `unsupported IAM_DEPLOYMENT_MODE "` + raw + `": allowed values are true and false`
		if err.Error() != want {
			t.Fatalf("unexpected error %q, want %q", err.Error(), want)
		}
	}
}

func TestResolveDeploymentConfigDefaultsToNoIAM(t *testing.T) {
	t.Setenv("IAM_DEPLOYMENT_MODE", "")
	t.Setenv("IAM_PROVIDER_NAME", "")
	t.Setenv("AUTH_ISSUER_INTERNAL_URL", "")
	t.Setenv("AUTH_ISSUER_PUBLIC_URL", "")
	t.Setenv("FRONTEND_AUTH_OIDC_AUTHORITY", "")
	t.Setenv("FRONTEND_AUTH_OIDC_CLIENT_ID", "")
	config, err := ResolveDeploymentConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.IAMEnabled {
		t.Fatal("expected IAM to be off")
	}
	if config.ProviderName != "None" {
		t.Fatalf("expected provider name None, got %q", config.ProviderName)
	}
}

func TestNoIAMIgnoresConfiguredIssuer(t *testing.T) {
	t.Setenv("IAM_DEPLOYMENT_MODE", "false")
	t.Setenv("AUTH_ISSUER_INTERNAL_URL", "https://login.example.com")
	t.Setenv("AUTH_ISSUER_PUBLIC_URL", "https://login.example.com")
	t.Setenv("FRONTEND_AUTH_OIDC_AUTHORITY", "https://login.example.com")
	t.Setenv("FRONTEND_AUTH_OIDC_CLIENT_ID", "workflow-frontend")
	t.Setenv("CONSOLE_ADMIN_USERNAME", "")
	t.Setenv("CONSOLE_ADMIN_PASSWORD", "")
	config, err := ResolveDeploymentConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.AuthConfig.Enabled() || config.FrontendConfig.Enabled {
		t.Fatalf("IAM_DEPLOYMENT_MODE=false must turn auth off, got api=%v frontend=%v", config.AuthConfig.Enabled(), config.FrontendConfig.Enabled)
	}
	if config.ConsoleAdmin.Username != "admin" || config.ConsoleAdmin.Password != "admin" {
		t.Fatalf("expected admin/admin console login, got %q/%q", config.ConsoleAdmin.Username, config.ConsoleAdmin.Password)
	}
}

func TestIAMEnabledRequiresIssuer(t *testing.T) {
	t.Setenv("IAM_DEPLOYMENT_MODE", "true")
	t.Setenv("AUTH_TOKEN_MODE", "jwt")
	t.Setenv("AUTH_ISSUER_INTERNAL_URL", "")
	t.Setenv("AUTH_ISSUER_PUBLIC_URL", "")
	if _, err := ResolveDeploymentConfigFromEnv(); err == nil {
		t.Fatal("expected IAM_DEPLOYMENT_MODE=true without an issuer to fail")
	}
}

func TestIAMEnabledIgnoresConsoleAdminCredentials(t *testing.T) {
	t.Setenv("IAM_DEPLOYMENT_MODE", "true")
	t.Setenv("AUTH_TOKEN_MODE", "jwt")
	t.Setenv("AUTH_ISSUER_INTERNAL_URL", "https://login.example.com")
	t.Setenv("AUTH_ISSUER_PUBLIC_URL", "https://login.example.com")
	t.Setenv("CONSOLE_ADMIN_USERNAME", "admin")
	t.Setenv("CONSOLE_ADMIN_PASSWORD", "admin")
	config, err := ResolveDeploymentConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !config.IAMEnabled || !config.AuthConfig.Enabled() {
		t.Fatal("expected IAM to be on")
	}
	if config.ConsoleAdmin != (ConsoleAdminCredentials{}) {
		t.Fatalf("console admin credentials must be unset with IAM on, got %#v", config.ConsoleAdmin)
	}
}
