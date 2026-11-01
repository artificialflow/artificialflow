import type { IdentityResponse } from "@/lib/api";

export const ARTIFICIALFLOW_ADMIN_ROLE = "artificialflowadmin";
export const ARTIFICIALFLOW_BUSINESS_ROLE = "artificialflowbusiness";
export const ARTIFICIALFLOW_CLIENT_ROLE = "artificialflowclient";
export const ARTIFICIALFLOW_ANALYTICS_ROLE = "artificialflowanalytics";

export const STATIC_ARTIFICIALFLOW_ROLES = [
  ARTIFICIALFLOW_ADMIN_ROLE,
  ARTIFICIALFLOW_BUSINESS_ROLE,
  ARTIFICIALFLOW_CLIENT_ROLE,
  ARTIFICIALFLOW_ANALYTICS_ROLE,
];

function normalizeRole(role: string) {
  return role.trim().toLowerCase();
}

export function canonicalizeRole(role: string): string {
  const normalized = normalizeRole(role);
  return STATIC_ARTIFICIALFLOW_ROLES.find((standard) => standard === normalized) ?? role.trim();
}

export function canonicalizeRoles(roles: string[]): string[] {
  const seen = new Set<string>();
  return roles.reduce<string[]>((canonical, role) => {
    const value = canonicalizeRole(role);
    const key = normalizeRole(value);
    if (!value || seen.has(key)) return canonical;
    seen.add(key);
    canonical.push(value);
    return canonical;
  }, []);
}

export function identityRoles(identity: IdentityResponse | null): string[] {
  return canonicalizeRoles(identity?.principal?.roles || []);
}

export function hasRole(identity: IdentityResponse | null, role: string): boolean {
  const wanted = normalizeRole(canonicalizeRole(role));
  return identityRoles(identity).some((candidate) => normalizeRole(candidate) === wanted);
}

export function hasAnyRole(identity: IdentityResponse | null, roles: string[]): boolean {
  return roles.some((role) => hasRole(identity, role));
}

export function isAdmin(identity: IdentityResponse | null): boolean {
  return hasRole(identity, ARTIFICIALFLOW_ADMIN_ROLE);
}

export function isBusiness(identity: IdentityResponse | null): boolean {
  return hasRole(identity, ARTIFICIALFLOW_BUSINESS_ROLE);
}

export function isAnalytics(identity: IdentityResponse | null): boolean {
  return hasRole(identity, ARTIFICIALFLOW_ANALYTICS_ROLE);
}

/** Service accounts for SDK/API integrations; they have no console access. */
export function isClientOnly(identity: IdentityResponse | null): boolean {
  return (
    hasRole(identity, ARTIFICIALFLOW_CLIENT_ROLE) &&
    !isAdmin(identity) &&
    !isBusiness(identity) &&
    !isAnalytics(identity)
  );
}
