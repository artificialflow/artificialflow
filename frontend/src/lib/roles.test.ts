import { describe, expect, it } from "vitest";
import type { IdentityResponse } from "./api";
import {
  ARTIFICIALFLOW_ADMIN_ROLE,
  ARTIFICIALFLOW_ANALYTICS_ROLE,
  ARTIFICIALFLOW_BUSINESS_ROLE,
  ARTIFICIALFLOW_CLIENT_ROLE,
  canonicalizeRoles,
  isAdmin,
  isBusiness,
  isClientOnly,
} from "./roles";

function identity(roles: string[]): IdentityResponse {
  return {
    authenticated: true,
    principal: {
      subject: "user-1",
      roles,
    },
  };
}

describe("role helpers", () => {
  it("canonicalizes standard roles and keeps custom roles", () => {
    expect(
      canonicalizeRoles([
        " ArtificialFlowAdmin ",
        ARTIFICIALFLOW_ADMIN_ROLE,
        "ARTIFICIALFLOWBUSINESS",
        "artificialflowclient",
        "ArtificialFlowAnalytics",
        "Finance Reviewer",
      ]),
    ).toEqual([
      ARTIFICIALFLOW_ADMIN_ROLE,
      ARTIFICIALFLOW_BUSINESS_ROLE,
      ARTIFICIALFLOW_CLIENT_ROLE,
      ARTIFICIALFLOW_ANALYTICS_ROLE,
      "Finance Reviewer",
    ]);
  });

  it("authorizes claims through canonical role helpers", () => {
    expect(isAdmin(identity(["ArtificialFlowAdmin"]))).toBe(true);
    expect(isBusiness(identity(["artificialflowbusiness"]))).toBe(true);
    expect(isClientOnly(identity(["ARTIFICIALFLOWCLIENT"]))).toBe(true);
    expect(isClientOnly(identity(["artificialflowclient", "artificialflowadmin"]))).toBe(false);
  });
});
