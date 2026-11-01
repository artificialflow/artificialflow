import { describe, expect, it } from "vitest";
import type { IdentityConfigResponse, IdentityResponse } from "./api";
import { isDashboardPathAllowed, isIdentityPath } from "./dashboardAccess";

function identity(roles: string[]): IdentityResponse {
  return {
    authenticated: true,
    principal: { subject: "user-1", roles },
  };
}

function config(mode: boolean): IdentityConfigResponse {
  return {
    deployment_mode: mode,
    provider_name: "test",
    auth_enabled: true,
    frontend_auth_enabled: true,
    frontend_oidc_authority: "https://login.example.com",
    frontend_oidc_client_id: "workflow-frontend",
    token_validation_mode: "jwt",
    internal_issuer_url: "",
    external_issuer_url: "",
    client_id: "",
    introspection_url: "",
    introspection_client_id: "",
    introspection_auth_method: "",
    enforce_audience: false,
    allow_insecure_issuer: false,
    claim_subject_path: "",
    claim_roles_path: "",
    claim_tenant_path: "",
    claim_email_path: "",
    claim_name_path: "",
    standard_roles: [],
  };
}

const allowed = (path: string, roles: string[], options: { decisions?: boolean; incidents?: boolean } = {}) =>
  isDashboardPathAllowed({
    path,
    identity: identity(roles),
    config: config(true),
    showDecisionsUi: options.decisions ?? true,
    showIncidentsUi: options.incidents ?? true,
  });

describe("dashboardAccess", () => {
  it("recognizes the identity page", () => {
    expect(isIdentityPath("/identity")).toBe(true);
    expect(isIdentityPath("/processes")).toBe(false);
  });

  it("gives admins every page", () => {
    for (const path of ["/", "/processes", "/decisions", "/modeler", "/instances", "/instances/1", "/history", "/incidents", "/identity", "/analytics"]) {
      expect(allowed(path, ["artificialflowadmin"])).toBe(true);
    }
  });

  it("limits business users to modeling pages", () => {
    const business = ["ArtificialFlowBusiness"];
    for (const path of ["/processes", "/decisions", "/modeler"]) {
      expect(allowed(path, business)).toBe(true);
    }
    for (const path of ["/", "/instances", "/instances/1", "/history", "/incidents", "/identity", "/analytics"]) {
      expect(allowed(path, business)).toBe(false);
    }
  });

  it("gives analytics users the analytics placeholder only", () => {
    expect(allowed("/analytics", ["artificialflowanalytics"])).toBe(true);
    expect(allowed("/processes", ["artificialflowanalytics"])).toBe(false);
  });

  it("denies client service accounts and custom roles", () => {
    for (const roles of [["artificialflowclient"], ["finance reviewer"]]) {
      expect(allowed("/processes", roles)).toBe(false);
      expect(allowed("/identity", roles)).toBe(false);
    }
  });

  it("respects disabled decisions and incidents UIs", () => {
    expect(allowed("/incidents", ["artificialflowadmin"], { incidents: false })).toBe(false);
    expect(allowed("/decisions", ["artificialflowadmin"], { decisions: false })).toBe(false);
  });
});
