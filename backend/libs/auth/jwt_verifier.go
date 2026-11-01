package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/logger"

	"github.com/coreos/go-oidc/v3/oidc"
)

type jwtVerifier struct {
	verifier *oidc.IDTokenVerifier
	config   Config
}

func newJWTVerifier(ctx context.Context, cfg Config, log *logger.Logger) (TokenVerifier, error) {
	if strings.TrimSpace(cfg.InternalIssuerURL) == "" {
		return nil, fmt.Errorf("AUTH_ISSUER_INTERNAL_URL is required for jwt token mode")
	}

	httpTimeout := cfg.HTTPTimeout
	if httpTimeout <= 0 {
		httpTimeout = 10 * time.Second
	}
	allowedAlgorithms := cfg.AllowedAlgorithms
	if len(allowedAlgorithms) == 0 {
		allowedAlgorithms = []string{"RS256"}
	}
	initCtx := oidc.ClientContext(ctx, &http.Client{Timeout: httpTimeout})
	discoveryIssuer := cfg.InternalIssuerURL
	if cfg.AllowInsecureIssuer && cfg.InternalIssuerURL != cfg.ExternalIssuerURL && cfg.ExternalIssuerURL != "" {
		log.Warn(ctx, "insecure issuer URL mode enabled", map[string]any{
			"internal_issuer": cfg.InternalIssuerURL,
			"external_issuer": cfg.ExternalIssuerURL,
		})
		discoveryIssuer = cfg.ExternalIssuerURL
		rewriteClient, err := newIssuerRewriteHTTPClient(cfg.ExternalIssuerURL, cfg.InternalIssuerURL)
		if err != nil {
			return nil, err
		}
		rewriteClient.Timeout = httpTimeout
		initCtx = oidc.ClientContext(ctx, rewriteClient)
	}

	var provider *oidc.Provider
	var err error
	for i := 0; i < 30; i++ {
		provider, err = oidc.NewProvider(initCtx, discoveryIssuer)
		if err == nil {
			break
		}
		log.Info(ctx, "waiting for identity provider...", map[string]any{"url": discoveryIssuer, "attempt": i + 1})
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}

	if !cfg.EnforceAudience {
		log.Warn(ctx, "audience validation disabled", map[string]any{"client_id": cfg.ClientID})
	}

	return &jwtVerifier{
		verifier: provider.VerifierContext(initCtx, &oidc.Config{
			ClientID:             cfg.ClientID,
			SkipClientIDCheck:    !cfg.EnforceAudience,
			SupportedSigningAlgs: append([]string(nil), allowedAlgorithms...),
		}),
		config: cfg,
	}, nil
}

type issuerRewriteTransport struct {
	base     http.RoundTripper
	external *url.URL
	internal *url.URL
}

func newIssuerRewriteHTTPClient(externalIssuer, internalIssuer string) (*http.Client, error) {
	externalURL, err := url.Parse(externalIssuer)
	if err != nil {
		return nil, fmt.Errorf("parse external issuer URL: %w", err)
	}
	internalURL, err := url.Parse(internalIssuer)
	if err != nil {
		return nil, fmt.Errorf("parse internal issuer URL: %w", err)
	}
	if externalURL.Scheme == "" || externalURL.Host == "" {
		return nil, fmt.Errorf("external issuer URL must include scheme and host")
	}
	if internalURL.Scheme == "" || internalURL.Host == "" {
		return nil, fmt.Errorf("internal issuer URL must include scheme and host")
	}
	return &http.Client{
		Transport: &issuerRewriteTransport{
			base:     http.DefaultTransport,
			external: externalURL,
			internal: internalURL,
		},
	}, nil
}

func (t *issuerRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	transport := t.base
	if transport == nil {
		transport = http.DefaultTransport
	}
	if req.URL != nil && t.external != nil && t.internal != nil && req.URL.Scheme == t.external.Scheme && req.URL.Host == t.external.Host {
		clone := req.Clone(req.Context())
		rewritten := *clone.URL
		rewritten.Scheme = t.internal.Scheme
		rewritten.Host = t.internal.Host
		clone.URL = &rewritten
		clone.Host = t.external.Host
		return transport.RoundTrip(clone)
	}
	return transport.RoundTrip(req)
}

func (v *jwtVerifier) Verify(ctx context.Context, rawToken string) (*Principal, error) {
	idToken, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return nil, err
	}

	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("failed to parse claims: %w", err)
	}

	principal := principalFromClaims(claims, idToken.Subject, v.config, TokenModeJWT)
	principal.Issuer = strings.TrimSpace(idToken.Issuer)
	principal.Audience = dedupeStrings(append(principal.Audience, idToken.Audience...))
	return principal, nil
}
