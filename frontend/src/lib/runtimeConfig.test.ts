import { describe, expect, it } from "vitest";
import { resolveRuntimeConfig, type RuntimeConfig } from "./runtimeConfig";

describe("resolveRuntimeConfig", () => {
  it("trims canonical runtime values", () => {
    expect(
      resolveRuntimeConfig({
        apiUrl: " https://api.artificialflow.example.io ",
        iamMode: true,
        oidcAuthority: "https://iam.artificialflow.example.io",
        oidcClientId: "artificialflow-frontend",
      }),
    ).toEqual({
      apiUrl: "https://api.artificialflow.example.io",
      iamMode: true,
      oidcAuthority: "https://iam.artificialflow.example.io",
      oidcClientId: "artificialflow-frontend",
    });
  });

  it("keeps IAM_DEPLOYMENT_MODE=false", () => {
    expect(resolveRuntimeConfig({ iamMode: false }).iamMode).toBe(false);
  });

  it("returns empty values for missing fields", () => {
    expect(resolveRuntimeConfig({ apiUrl: "/api" })).toEqual({
      apiUrl: "/api",
      iamMode: null,
      oidcAuthority: "",
      oidcClientId: "",
    });
  });

  it("ignores non-boolean IAM modes", () => {
    for (const legacy of ["external", "disabled", "true"]) {
      expect(resolveRuntimeConfig({ iamMode: legacy } as unknown as RuntimeConfig).iamMode).toBeNull();
    }
  });
});
