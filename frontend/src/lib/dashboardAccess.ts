import type { IdentityConfigResponse, IdentityResponse } from "@/lib/api";
import { isAdmin, isAnalytics, isBusiness } from "@/lib/roles";

export function isIdentityPath(path: string): boolean {
  return path === "/identity" || path.startsWith("/identity/");
}

type PathAccessInput = {
  path: string;
  identity: IdentityResponse | null;
  config: IdentityConfigResponse | null | undefined;
  showDecisionsUi?: boolean;
  showIncidentsUi?: boolean;
};

/**
 * Console page matrix. Admins see everything; business users manage models
 * only (no instances, variables, incidents or client setup); analytics users
 * see the analytics placeholder; client service accounts have no console.
 */
export function isDashboardPathAllowed({
  path,
  identity,
  showDecisionsUi = false,
  showIncidentsUi = false,
}: PathAccessInput): boolean {
  if (path === "/incidents" || path.startsWith("/incidents/")) {
    return showIncidentsUi && isAdmin(identity);
  }
  if (path === "/decisions" && !showDecisionsUi) {
    return false;
  }
  if (isAdmin(identity)) {
    return true;
  }
  if (
    isBusiness(identity) &&
    (path === "/processes" || path === "/decisions" || path === "/modeler" || path.startsWith("/modeler/"))
  ) {
    return true;
  }
  if (isAnalytics(identity) && path === "/analytics") {
    return true;
  }
  return false;
}
