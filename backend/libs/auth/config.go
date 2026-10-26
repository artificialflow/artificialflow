package auth

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	TokenModeJWT           = "jwt"
	TokenModeIntrospection = "introspection"
)

type Config struct {
	InternalIssuerURL string
	ExternalIssuerURL string
	ClientID          string

	TokenValidationMode       string
	IntrospectionURL          string
	IntrospectionClientID     string
	IntrospectionClientSecret string
	IntrospectionAuthMethod   string

	EnforceAudience     bool
	AllowInsecureIssuer bool
	Production          bool
	AllowedAlgorithms   []string
	HTTPTimeout         time.Duration

	ClaimSubjectPath string
	ClaimRolesPath   string
	ClaimTenantPath  string
	ClaimEmailPath   string
	ClaimNamePath    string
}

func (c Config) Enabled() bool {
	switch c.TokenValidationMode {
	case TokenModeIntrospection:
		return strings.TrimSpace(c.IntrospectionURL) != ""
	default:
		return strings.TrimSpace(c.InternalIssuerURL) != ""
	}
}

func ResolveConfigFromEnv() Config {
	cfg := Config{
		ClientID:                  strings.TrimSpace(firstNonEmpty(os.Getenv("AUTH_CLIENT_ID"), "workflow-backend")),
		TokenValidationMode:       normalizeTokenMode(firstNonEmpty(os.Getenv("AUTH_TOKEN_MODE"), TokenModeJWT)),
		IntrospectionAuthMethod:   normalizeIntrospectionAuthMethod(firstNonEmpty(os.Getenv("AUTH_INTROSPECTION_AUTH_METHOD"), "basic")),
		EnforceAudience:           envBoolOrDefault("AUTH_ENFORCE_AUDIENCE", true),
		AllowInsecureIssuer:       envBoolOrDefault("AUTH_ALLOW_INSECURE_ISSUER", false),
		Production:                envBoolOrDefault("AUTH_PRODUCTION", false),
		AllowedAlgorithms:         csvValues(firstNonEmpty(os.Getenv("AUTH_ALLOWED_ALGORITHMS"), "RS256")),
		HTTPTimeout:               envDurationOrDefault("AUTH_HTTP_TIMEOUT_SECONDS", 10*time.Second),
		ClaimSubjectPath:          strings.TrimSpace(firstNonEmpty(os.Getenv("AUTH_CLAIM_SUBJECT_PATH"), "sub")),
		ClaimRolesPath:            strings.TrimSpace(firstNonEmpty(os.Getenv("AUTH_CLAIM_ROLES_PATH"), "roles,realm_access.roles,groups")),
		ClaimTenantPath:           strings.TrimSpace(firstNonEmpty(os.Getenv("AUTH_CLAIM_TENANT_PATH"), "tenant_id")),
		ClaimEmailPath:            strings.TrimSpace(firstNonEmpty(os.Getenv("AUTH_CLAIM_EMAIL_PATH"), "email")),
		ClaimNamePath:             strings.TrimSpace(firstNonEmpty(os.Getenv("AUTH_CLAIM_NAME_PATH"), "name")),
		IntrospectionURL:          strings.TrimSpace(os.Getenv("AUTH_INTROSPECTION_URL")),
		IntrospectionClientID:     strings.TrimSpace(os.Getenv("AUTH_INTROSPECTION_CLIENT_ID")),
		IntrospectionClientSecret: strings.TrimSpace(os.Getenv("AUTH_INTROSPECTION_CLIENT_SECRET")),
	}

	cfg.InternalIssuerURL = strings.TrimSpace(os.Getenv("AUTH_ISSUER_INTERNAL_URL"))
	cfg.ExternalIssuerURL = strings.TrimSpace(os.Getenv("AUTH_ISSUER_PUBLIC_URL"))

	if cfg.InternalIssuerURL == "" {
		cfg.InternalIssuerURL = cfg.ExternalIssuerURL
	}
	if cfg.ExternalIssuerURL == "" {
		cfg.ExternalIssuerURL = cfg.InternalIssuerURL
	}

	if cfg.IntrospectionClientID == "" {
		cfg.IntrospectionClientID = cfg.ClientID
	}

	return cfg
}

func (c Config) Validate() error {
	if !c.Enabled() {
		if c.Production {
			return fmt.Errorf("authentication must be enabled when AUTH_PRODUCTION=true")
		}
		return nil
	}
	if strings.TrimSpace(c.ClientID) == "" {
		return fmt.Errorf("AUTH_CLIENT_ID is required")
	}
	if c.Production && c.HTTPTimeout <= 0 {
		return fmt.Errorf("AUTH_HTTP_TIMEOUT_SECONDS must be positive")
	}
	if c.Production && c.TokenValidationMode == TokenModeJWT && len(c.AllowedAlgorithms) == 0 {
		return fmt.Errorf("AUTH_ALLOWED_ALGORITHMS must not be empty")
	}
	if c.Production && c.TokenValidationMode == TokenModeIntrospection {
		if strings.TrimSpace(c.IntrospectionClientID) == "" || strings.TrimSpace(c.IntrospectionClientSecret) == "" {
			return fmt.Errorf("introspection client credentials are required")
		}
	}
	for name, raw := range map[string]string{
		"issuer":          c.ExternalIssuerURL,
		"internal issuer": c.InternalIssuerURL,
		"introspection":   c.IntrospectionURL,
	} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("%s URL must include scheme and host", name)
		}
		internalBundled := c.TokenValidationMode == TokenModeIntrospection &&
			c.AllowInsecureIssuer && (name == "internal issuer" || name == "introspection") &&
			isClusterLocalHost(parsed.Hostname())
		if c.Production && parsed.Scheme != "https" && !internalBundled {
			return fmt.Errorf("%s URL must use https in production", name)
		}
	}
	if c.Production && !c.EnforceAudience {
		return fmt.Errorf("production requires audience enforcement")
	}
	if c.Production && c.AllowInsecureIssuer && c.TokenValidationMode != TokenModeIntrospection {
		return fmt.Errorf("production JWT mode requires strict issuer validation")
	}
	return nil
}

func isClusterLocalHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host != "" && (!strings.Contains(host, ".") || strings.HasSuffix(host, ".svc") || strings.Contains(host, ".svc."))
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

func normalizeTokenMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case TokenModeIntrospection, "opaque":
		return TokenModeIntrospection
	default:
		return TokenModeJWT
	}
}

func normalizeIntrospectionAuthMethod(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "post":
		return "post"
	default:
		return "basic"
	}
}

func envBoolOrDefault(key string, defaultValue bool) bool {
	val := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch val {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}

func envDurationSeconds(key string) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func envDurationOrDefault(key string, defaultValue time.Duration) time.Duration {
	if value := envDurationSeconds(key); value > 0 {
		return value
	}
	return defaultValue
}

func csvValues(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}
