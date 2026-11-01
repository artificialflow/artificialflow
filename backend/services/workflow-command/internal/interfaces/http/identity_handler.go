package api

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/interfaces/http/dto"
)

func (h *Handler) getIdentityConfig(w http.ResponseWriter, _ *http.Request) {
	config := h.identityConfig
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dto.IdentityConfigResponse{
		DeploymentMode:          config.IAMEnabled,
		ProviderName:            config.ProviderName,
		AuthEnabled:             config.AuthConfig.Enabled(),
		FrontendAuthEnabled:     config.FrontendConfig.Enabled,
		FrontendOIDCAuthority:   config.FrontendConfig.OIDCAuthority,
		FrontendOIDCClientID:    config.FrontendConfig.OIDCClientID,
		TokenValidationMode:     config.AuthConfig.TokenValidationMode,
		InternalIssuerURL:       config.AuthConfig.InternalIssuerURL,
		ExternalIssuerURL:       config.AuthConfig.ExternalIssuerURL,
		ClientID:                config.AuthConfig.ClientID,
		IntrospectionURL:        config.AuthConfig.IntrospectionURL,
		IntrospectionClientID:   config.AuthConfig.IntrospectionClientID,
		IntrospectionAuthMethod: config.AuthConfig.IntrospectionAuthMethod,
		EnforceAudience:         config.AuthConfig.EnforceAudience,
		AllowInsecureIssuer:     config.AuthConfig.AllowInsecureIssuer,
		ClaimSubjectPath:        config.AuthConfig.ClaimSubjectPath,
		ClaimRolesPath:          config.AuthConfig.ClaimRolesPath,
		ClaimTenantPath:         config.AuthConfig.ClaimTenantPath,
		ClaimEmailPath:          config.AuthConfig.ClaimEmailPath,
		ClaimNamePath:           config.AuthConfig.ClaimNamePath,
		StandardRoles:           auth.StandardRoles(),
	})
}

type consoleLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// consoleLogin checks the console admin credentials when no IAM provider is
// used. It issues no token: in that mode the API itself is unauthenticated.
func (h *Handler) consoleLogin(w http.ResponseWriter, r *http.Request) {
	if h.identityConfig.IAMEnabled {
		http.Error(w, "console login is only available when IAM_DEPLOYMENT_MODE=false; sign in through your OIDC provider", http.StatusNotFound)
		return
	}
	var req consoleLoginRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid login request", http.StatusBadRequest)
		return
	}
	expected := h.identityConfig.ConsoleAdmin
	userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(expected.Username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(req.Password), []byte(expected.Password)) == 1
	if !userOK || !passOK {
		h.log.Warn(r.Context(), "console login rejected", map[string]any{"username": req.Username})
		http.Error(w, "invalid username or password", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"authenticated": true,
		"principal": auth.Principal{
			Subject: expected.Username,
			Name:    expected.Username,
			Roles:   []string{auth.RoleAdmin},
		},
	})
}
