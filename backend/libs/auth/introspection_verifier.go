package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type introspectionVerifier struct {
	client           *http.Client
	config           Config
	introspectionURL string
}

func newIntrospectionVerifier(cfg Config) (TokenVerifier, error) {
	introspectionURL := strings.TrimSpace(cfg.IntrospectionURL)
	if introspectionURL == "" {
		return nil, fmt.Errorf("AUTH_INTROSPECTION_URL is required when AUTH_TOKEN_MODE=introspection")
	}
	normalizedURL, err := validateIntrospectionURL(introspectionURL)
	if err != nil {
		return nil, err
	}
	cfg.IntrospectionURL = normalizedURL
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &introspectionVerifier{
		client:           &http.Client{Timeout: timeout},
		config:           cfg,
		introspectionURL: normalizedURL,
	}, nil
}

func validateIntrospectionURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse AUTH_INTROSPECTION_URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("AUTH_INTROSPECTION_URL must use http or https")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("AUTH_INTROSPECTION_URL must include a host")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("AUTH_INTROSPECTION_URL must not include userinfo")
	}
	return parsed.String(), nil
}

func (v *introspectionVerifier) Verify(ctx context.Context, rawToken string) (*Principal, error) {
	body := url.Values{}
	body.Set("token", rawToken)
	if strings.EqualFold(v.config.IntrospectionAuthMethod, "post") {
		if v.config.IntrospectionClientID != "" {
			body.Set("client_id", v.config.IntrospectionClientID)
		}
		if v.config.IntrospectionClientSecret != "" {
			body.Set("client_secret", v.config.IntrospectionClientSecret)
		}
	}

	// #nosec G107 G704 -- AUTH_INTROSPECTION_URL is normalized by validateIntrospectionURL when the verifier is constructed.
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, v.introspectionURL, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build introspection request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	if host := introspectionRequestHost(v.config, v.introspectionURL); host != "" {
		request.Host = host
	}
	if !strings.EqualFold(v.config.IntrospectionAuthMethod, "post") && v.config.IntrospectionClientID != "" {
		request.SetBasicAuth(v.config.IntrospectionClientID, v.config.IntrospectionClientSecret)
	}

	// #nosec G704 -- request URL is the validated OIDC introspection endpoint configured by the deployer.
	response, err := v.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("token introspection request failed: %w", err)
	}
	defer response.Body.Close()

	payloadRaw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read introspection response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token introspection failed with status=%d", response.StatusCode)
	}

	claims := map[string]any{}
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return nil, fmt.Errorf("decode introspection response: %w", err)
	}
	if !valueAsBool(claims["active"]) {
		return nil, fmt.Errorf("inactive token")
	}
	if err := validateIntrospectionTimes(claims, time.Now().UTC()); err != nil {
		return nil, err
	}

	subject := firstNonEmpty(
		valueAsString(claims["sub"]),
		valueAsString(claims["username"]),
	)
	if subject == "" {
		return nil, fmt.Errorf("active token has no subject")
	}
	principal := principalFromClaims(claims, subject, v.config, TokenModeIntrospection)
	if issuer := valueAsString(claims["iss"]); issuer != "" {
		principal.Issuer = issuer
	}
	if len(principal.Audience) == 0 {
		principal.Audience = claimFirstStringSlice(claims, "aud,client_id,azp")
	}

	if v.config.EnforceAudience && strings.TrimSpace(v.config.ClientID) != "" {
		audienceMatch := false
		for _, aud := range principal.Audience {
			if strings.EqualFold(strings.TrimSpace(aud), strings.TrimSpace(v.config.ClientID)) {
				audienceMatch = true
				break
			}
		}
		if !audienceMatch {
			clientIDClaim := valueAsString(claims["client_id"])
			azpClaim := valueAsString(claims["azp"])
			if !strings.EqualFold(strings.TrimSpace(clientIDClaim), strings.TrimSpace(v.config.ClientID)) &&
				!strings.EqualFold(strings.TrimSpace(azpClaim), strings.TrimSpace(v.config.ClientID)) {
				return nil, fmt.Errorf("token audience/client mismatch")
			}
		}
	}

	return principal, nil
}

func introspectionRequestHost(cfg Config, introspectionURL string) string {
	if !cfg.AllowInsecureIssuer || strings.TrimSpace(cfg.ExternalIssuerURL) == "" {
		return ""
	}
	internal, err := url.Parse(strings.TrimSpace(introspectionURL))
	if err != nil {
		return ""
	}
	external, err := url.Parse(strings.TrimSpace(cfg.ExternalIssuerURL))
	if err != nil || external.Host == "" || internal.Host == external.Host {
		return ""
	}
	return external.Host
}

func validateIntrospectionTimes(claims map[string]any, now time.Time) error {
	if exp, ok := numericDate(claims["exp"]); ok && !now.Before(time.Unix(exp, 0)) {
		return fmt.Errorf("expired token")
	}
	if nbf, ok := numericDate(claims["nbf"]); ok && now.Before(time.Unix(nbf, 0)) {
		return fmt.Errorf("token is not active yet")
	}
	return nil
}

func numericDate(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(typed))
		if err == nil {
			return parsed.Unix(), true
		}
	}
	return 0, false
}

func valueAsBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		normalized := strings.TrimSpace(strings.ToLower(v))
		return normalized == "true" || normalized == "1" || normalized == "yes" || normalized == "on"
	case float64:
		return v != 0
	default:
		return false
	}
}
