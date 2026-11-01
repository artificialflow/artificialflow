package iam

import (
	"fmt"
	"os"
	"strings"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
)

const deploymentModeEnv = "IAM_DEPLOYMENT_MODE"

const (
	defaultConsoleAdminUsername = "admin"
	defaultConsoleAdminPassword = "admin"
)

type DeploymentConfig struct {
	// IAMEnabled is IAM_DEPLOYMENT_MODE: true means an external OIDC
	// provider authenticates every request; false means no provider, an
	// open API and the local console sign-in.
	IAMEnabled     bool
	ProviderName   string
	AuthConfig     auth.Config
	FrontendConfig FrontendAuthConfig
	// ConsoleAdmin is the console login, set only when IAMEnabled is false.
	ConsoleAdmin ConsoleAdminCredentials
}

// ConsoleAdminCredentials gate the console sign-in screen when no IAM
// provider is used. They do not protect the API, which is open in that mode.
type ConsoleAdminCredentials struct {
	Username string
	Password string
}

func ResolveDeploymentConfigFromEnv() (DeploymentConfig, error) {
	authConfig := auth.ResolveConfigFromEnv()
	frontendConfig := ResolveFrontendAuthConfigFromEnv()
	iamEnabled, set, err := parseDeploymentMode(os.Getenv(deploymentModeEnv))
	if err != nil {
		return DeploymentConfig{}, err
	}
	if !set {
		iamEnabled = authConfig.Enabled() || frontendConfig.Enabled
	}
	config := DeploymentConfig{IAMEnabled: iamEnabled}
	if iamEnabled {
		if !authConfig.Enabled() {
			return DeploymentConfig{}, fmt.Errorf("%s=true requires AUTH_ISSUER_INTERNAL_URL (or AUTH_INTROSPECTION_URL with AUTH_TOKEN_MODE=introspection)", deploymentModeEnv)
		}
		config.ProviderName = firstNonEmpty(os.Getenv("IAM_PROVIDER_NAME"), "External OIDC Provider")
	} else {
		authConfig.InternalIssuerURL = ""
		authConfig.ExternalIssuerURL = ""
		authConfig.IntrospectionURL = ""
		frontendConfig = FrontendAuthConfig{}
		config.ProviderName = firstNonEmpty(os.Getenv("IAM_PROVIDER_NAME"), "None")
		config.ConsoleAdmin = ConsoleAdminCredentials{
			Username: firstNonEmpty(os.Getenv("CONSOLE_ADMIN_USERNAME"), defaultConsoleAdminUsername),
			Password: firstNonEmpty(os.Getenv("CONSOLE_ADMIN_PASSWORD"), defaultConsoleAdminPassword),
		}
	}
	config.AuthConfig = authConfig
	config.FrontendConfig = frontendConfig
	return config, nil
}

// parseDeploymentMode accepts only "true" and "false"; set is false when the
// variable is empty.
func parseDeploymentMode(raw string) (enabled bool, set bool, err error) {
	switch value := strings.TrimSpace(raw); value {
	case "":
		return false, false, nil
	case "true":
		return true, true, nil
	case "false":
		return false, true, nil
	default:
		return false, false, fmt.Errorf("unsupported %s %q: allowed values are true and false", deploymentModeEnv, value)
	}
}
