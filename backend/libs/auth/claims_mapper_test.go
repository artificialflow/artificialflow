package auth

import "testing"

func TestPrincipalFromClaims_MapsNestedRolesAndAudience(t *testing.T) {
	claims := map[string]any{
		"sub":   "user-123",
		"email": "user@example.com",
		"name":  "Example User",
		"realm_access": map[string]any{
			"roles": []any{"admin", "reader"},
		},
		"scope": "openid profile email",
		"aud":   []any{"workflow-backend"},
	}

	cfg := Config{
		ClaimSubjectPath: "sub",
		ClaimEmailPath:   "email",
		ClaimNamePath:    "name",
		ClaimRolesPath:   "roles,realm_access.roles,groups",
	}

	principal := principalFromClaims(claims, "", cfg, TokenModeJWT)
	if principal.Subject != "user-123" {
		t.Fatalf("expected mapped subject, got %s", principal.Subject)
	}
	if len(principal.Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(principal.Roles))
	}
	if len(principal.Audience) != 1 || principal.Audience[0] != "workflow-backend" {
		t.Fatalf("expected audience mapping, got %#v", principal.Audience)
	}
}

func TestPrincipalFromClaims_PreservesRoleNamesWithSpaces(t *testing.T) {
	claims := map[string]any{
		"sub":   "user-123",
		"roles": "ArtificialFlowAdmin,finance reviewer",
	}

	principal := principalFromClaims(claims, "", Config{ClaimRolesPath: "roles"}, TokenModeJWT)
	if len(principal.Roles) != 2 {
		t.Fatalf("expected 2 roles, got %#v", principal.Roles)
	}
	if principal.Roles[0] != RoleAdmin {
		t.Fatalf("expected first role %q, got %q", RoleAdmin, principal.Roles[0])
	}
	if principal.Roles[1] != "finance reviewer" {
		t.Fatalf("expected second role %q, got %q", "finance reviewer", principal.Roles[1])
	}
}

func TestPrincipalFromClaims_CanonicalizesMixedStandardRoleClaims(t *testing.T) {
	claims := map[string]any{
		"sub": "user-123",
		"roles": []any{
			"ARTIFICIALFLOWADMIN",
			RoleAdmin,
			"artificialflowclient",
			"finance reviewer",
		},
	}

	principal := principalFromClaims(claims, "", Config{ClaimRolesPath: "roles"}, TokenModeJWT)
	expected := []string{RoleAdmin, RoleClient, "finance reviewer"}
	if len(principal.Roles) != len(expected) {
		t.Fatalf("expected canonical roles %#v, got %#v", expected, principal.Roles)
	}
	for index := range expected {
		if principal.Roles[index] != expected[index] {
			t.Fatalf("expected role %q at index %d, got %q", expected[index], index, principal.Roles[index])
		}
	}
}

func TestPrincipalFromClaims_MapsRoleObjectKeys(t *testing.T) {
	claims := map[string]any{
		"sub": "user-123",
		"roles": map[string]any{
			RoleClient:         map[string]any{},
			"finance reviewer": map[string]any{},
		},
	}

	principal := principalFromClaims(claims, "", Config{ClaimRolesPath: "roles"}, TokenModeJWT)
	if len(principal.Roles) != 2 {
		t.Fatalf("expected 2 roles, got %#v", principal.Roles)
	}
	if !containsString(principal.Roles, RoleClient) {
		t.Fatalf("expected role %q, got %#v", RoleClient, principal.Roles)
	}
	if !containsString(principal.Roles, "finance reviewer") {
		t.Fatalf("expected finance reviewer role, got %#v", principal.Roles)
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
