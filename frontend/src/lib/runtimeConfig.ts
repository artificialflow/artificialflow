export type RuntimeConfig = {
  apiUrl?: string;
  // IAM_DEPLOYMENT_MODE: true = external OIDC provider, false = no provider
  // (local console sign-in). null/undefined when not configured.
  iamMode?: boolean | null;
  oidcAuthority?: string;
  oidcClientId?: string;
};

declare global {
  interface Window {
    __ARTIFICIALFLOW_RUNTIME_CONFIG__?: RuntimeConfig;
  }
}

const trim = (value: string | undefined | null): string => (value || "").trim();

export function resolveRuntimeConfig(canonical: RuntimeConfig = {}) {
  return {
    apiUrl: trim(canonical.apiUrl),
    iamMode: typeof canonical.iamMode === "boolean" ? canonical.iamMode : null,
    oidcAuthority: trim(canonical.oidcAuthority),
    oidcClientId: trim(canonical.oidcClientId),
  };
}

const canonicalRuntime =
  (typeof window !== "undefined" ? window.__ARTIFICIALFLOW_RUNTIME_CONFIG__ : undefined) || {};

export const runtimeConfig = resolveRuntimeConfig(canonicalRuntime);
