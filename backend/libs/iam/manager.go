package iam

import (
	"os"
	"strings"
)

type FrontendAuthConfig struct {
	Enabled       bool   `json:"enabled"`
	OIDCAuthority string `json:"oidc_authority"`
	OIDCClientID  string `json:"oidc_client_id"`
}

func ResolveFrontendAuthConfigFromEnv() FrontendAuthConfig {
	authority := strings.TrimSpace(os.Getenv("FRONTEND_AUTH_OIDC_AUTHORITY"))
	clientID := strings.TrimSpace(os.Getenv("FRONTEND_AUTH_OIDC_CLIENT_ID"))
	return FrontendAuthConfig{
		Enabled:       authority != "" && clientID != "",
		OIDCAuthority: authority,
		OIDCClientID:  clientID,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
