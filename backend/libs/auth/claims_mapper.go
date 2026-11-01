package auth

import (
	"fmt"
	"strings"
)

func principalFromClaims(claims map[string]any, fallbackSubject string, cfg Config, tokenMode string) *Principal {
	principal := &Principal{
		Subject:   firstNonEmpty(claimFirstString(claims, cfg.ClaimSubjectPath), fallbackSubject),
		Email:     claimFirstString(claims, cfg.ClaimEmailPath),
		Name:      claimFirstString(claims, cfg.ClaimNamePath),
		TenantID:  claimFirstString(claims, cfg.ClaimTenantPath),
		Roles:     claimFirstRoleSlice(claims, cfg.ClaimRolesPath),
		TokenMode: tokenMode,
		Claims:    claims,
	}

	if principal.Subject == "" {
		principal.Subject = claimFirstString(claims, "sub")
	}
	if len(principal.Roles) == 0 {
		principal.Roles = claimFirstRoleSlice(claims, "roles,realm_access.roles,groups")
	}

	principal.Audience = claimFirstStringSlice(claims, "aud")
	principal.Roles = CanonicalizeRoles(principal.Roles)
	principal.Audience = dedupeStrings(principal.Audience)
	return principal
}

func claimFirstString(claims map[string]any, pathsCSV string) string {
	for _, path := range splitAndTrim(pathsCSV) {
		value, ok := claimByPath(claims, path)
		if !ok {
			continue
		}
		if parsed := valueAsString(value); parsed != "" {
			return parsed
		}
	}
	return ""
}

func claimFirstStringSlice(claims map[string]any, pathsCSV string) []string {
	for _, path := range splitAndTrim(pathsCSV) {
		value, ok := claimByPath(claims, path)
		if !ok {
			continue
		}
		parsed := valueAsStringSlice(value)
		if len(parsed) > 0 {
			return parsed
		}
	}
	return nil
}

func claimFirstRoleSlice(claims map[string]any, pathsCSV string) []string {
	for _, path := range splitAndTrim(pathsCSV) {
		value, ok := claimByPath(claims, path)
		if !ok {
			continue
		}
		if parsed := valueAsRoleSlice(value); len(parsed) > 0 {
			return parsed
		}
	}
	return nil
}

func claimByPath(claims map[string]any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	parts := strings.Split(path, ".")
	var current any = claims
	for _, part := range parts {
		nextMap, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, exists := nextMap[strings.TrimSpace(part)]
		if !exists {
			return nil, false
		}
		current = next
	}
	return current, true
}

func valueAsString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
}

func valueAsStringSlice(value any) []string {
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		separators := []string{",", " "}
		parts := []string{trimmed}
		for _, separator := range separators {
			next := make([]string, 0, len(parts))
			for _, part := range parts {
				next = append(next, strings.Split(part, separator)...)
			}
			parts = next
		}
		return dedupeStrings(parts)
	case []string:
		return dedupeStrings(v)
	case []any:
		items := make([]string, 0, len(v))
		for _, item := range v {
			if parsed := valueAsString(item); parsed != "" {
				items = append(items, parsed)
			}
		}
		return dedupeStrings(items)
	default:
		return nil
	}
}

func valueAsRoleSlice(value any) []string {
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		return dedupeStrings(strings.Split(trimmed, ","))
	case map[string]any:
		items := make([]string, 0, len(v))
		for key := range v {
			items = append(items, key)
		}
		return dedupeStrings(items)
	default:
		return valueAsStringSlice(value)
	}
}

func splitAndTrim(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
