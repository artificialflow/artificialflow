package dto

type IdentityConfigResponse struct {
	DeploymentMode          bool     `json:"deployment_mode"`
	ProviderName            string   `json:"provider_name"`
	AuthEnabled             bool     `json:"auth_enabled"`
	FrontendAuthEnabled     bool     `json:"frontend_auth_enabled"`
	FrontendOIDCAuthority   string   `json:"frontend_oidc_authority"`
	FrontendOIDCClientID    string   `json:"frontend_oidc_client_id"`
	TokenValidationMode     string   `json:"token_validation_mode"`
	InternalIssuerURL       string   `json:"internal_issuer_url"`
	ExternalIssuerURL       string   `json:"external_issuer_url"`
	ClientID                string   `json:"client_id"`
	IntrospectionURL        string   `json:"introspection_url"`
	IntrospectionClientID   string   `json:"introspection_client_id"`
	IntrospectionAuthMethod string   `json:"introspection_auth_method"`
	EnforceAudience         bool     `json:"enforce_audience"`
	AllowInsecureIssuer     bool     `json:"allow_insecure_issuer"`
	ClaimSubjectPath        string   `json:"claim_subject_path"`
	ClaimRolesPath          string   `json:"claim_roles_path"`
	ClaimTenantPath         string   `json:"claim_tenant_path"`
	ClaimEmailPath          string   `json:"claim_email_path"`
	ClaimNamePath           string   `json:"claim_name_path"`
	StandardRoles           []string `json:"standard_roles"`
}
