package auth

import (
	"net/http"
	"strings"
)

// Roles are assigned in the customer's OIDC provider and are the complete
// authorization model. Matching is case-insensitive; no other spellings are
// accepted.
const (
	// RoleAdmin has full access to the console and every API operation.
	RoleAdmin = "artificialflowadmin"
	// RoleBusiness deploys, views and updates workflow and decision models.
	// It cannot see runtime instance data or perform operational actions.
	RoleBusiness = "artificialflowbusiness"
	// RoleClient is assigned to SDK/API service accounts that run workflows,
	// workers and task applications.
	RoleClient = "artificialflowclient"
	// RoleAnalytics is reserved for analytics features and currently grants
	// console sign-in only.
	RoleAnalytics = "artificialflowanalytics"
)

func StandardRoles() []string {
	return []string{RoleAdmin, RoleBusiness, RoleClient, RoleAnalytics}
}

func CanonicalRole(role string) string {
	trimmed := strings.TrimSpace(role)
	for _, standard := range StandardRoles() {
		if strings.EqualFold(trimmed, standard) {
			return standard
		}
	}
	return trimmed
}

func CanonicalizeRoles(roles []string) []string {
	if len(roles) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(roles))
	canonical := make([]string, 0, len(roles))
	for _, role := range roles {
		normalized := CanonicalRole(role)
		if normalized == "" {
			continue
		}
		key := strings.ToLower(normalized)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		canonical = append(canonical, normalized)
	}
	return canonical
}

func IsStandardRole(role string) bool {
	canonical := CanonicalRole(role)
	for _, standard := range StandardRoles() {
		if canonical == standard {
			return true
		}
	}
	return false
}

func RequireAuthenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := PrincipalFromContext(r.Context()); !ok {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (p Principal) HasRole(role string) bool {
	required := CanonicalRole(role)
	if required == "" {
		return false
	}
	for _, candidate := range p.Roles {
		if strings.EqualFold(CanonicalRole(candidate), required) {
			return true
		}
	}
	return false
}

func (p Principal) HasAnyRole(roles ...string) bool {
	for _, role := range roles {
		if p.HasRole(role) {
			return true
		}
	}
	return false
}

func RequireAnyRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			if principal.HasAnyRole(roles...) {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "Forbidden", http.StatusForbidden)
		})
	}
}
