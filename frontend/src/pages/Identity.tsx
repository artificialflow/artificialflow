import { useEffect, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { api, type IdentityConfigResponse, type IdentityResponse } from "@/lib/api";
import { RefreshCw } from "lucide-react";
import {
  ARTIFICIALFLOW_ADMIN_ROLE,
  ARTIFICIALFLOW_ANALYTICS_ROLE,
  ARTIFICIALFLOW_BUSINESS_ROLE,
  ARTIFICIALFLOW_CLIENT_ROLE,
  STATIC_ARTIFICIALFLOW_ROLES,
} from "@/lib/roles";

function DetailField({ label, value }: { label: string; value?: string }) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="break-all font-mono text-sm">{value || "-"}</dd>
    </div>
  );
}

function deploymentModeLabel(mode?: boolean) {
  switch (mode) {
    case true:
      return "External OIDC";
    case false:
      return "No IAM provider";
    default:
      return "Unknown";
  }
}

export default function Identity() {
  const [identity, setIdentity] = useState<IdentityResponse | null>(null);
  const [config, setConfig] = useState<IdentityConfigResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const loadIdentity = async () => {
    setLoading(true);
    setError(null);
    try {
      const [identityResponse, configResponse] = await Promise.all([
        api.getIdentity(),
        api.getIdentityConfig(),
      ]);
      setIdentity(identityResponse);
      setConfig(configResponse);
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to load identity";
      setError(message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadIdentity();
  }, []);

  if (loading) {
    return <div className="p-4">Loading identity...</div>;
  }

  const principal = identity?.principal;
  const roles = principal?.roles || [];
  const standardRoles = config?.standard_roles || [];

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-3xl font-bold tracking-tight">Identity</h2>
        <Button variant="outline" size="sm" onClick={loadIdentity}>
          <RefreshCw className="mr-2 h-4 w-4" />
          Refresh
        </Button>
      </div>

      {error && (
        <Card>
          <CardContent className="pt-6 text-sm text-destructive">{error}</CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle>OIDC provider</CardTitle>
          <CardDescription>
            ArtificialFlow validates tokens from your existing OIDC provider. Users, SDK clients, and API tokens are created there.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap gap-2">
            <Badge variant={identity?.authenticated ? "default" : "secondary"}>
              {identity?.authenticated ? "Authenticated" : "Not authenticated"}
            </Badge>
            <Badge variant={config?.auth_enabled ? "default" : "secondary"}>
              {config?.auth_enabled ? "Backend auth enabled" : "Backend auth disabled"}
            </Badge>
            <Badge variant="outline">{deploymentModeLabel(config?.deployment_mode)}</Badge>
          </div>
          <dl className="grid gap-3 md:grid-cols-2">
            <DetailField label="Provider" value={config?.provider_name} />
            <DetailField label="Issuer" value={config?.external_issuer_url || config?.frontend_oidc_authority} />
            <DetailField label="Backend client ID" value={config?.client_id} />
            <DetailField label="Frontend client ID" value={config?.frontend_oidc_client_id} />
            <DetailField label="Token validation mode" value={config?.token_validation_mode} />
          </dl>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Roles and SDK clients</CardTitle>
          <CardDescription>
            Users and clients are managed in your OIDC provider. Assign these roles in the claim configured by AUTH_CLAIM_ROLES_PATH.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <div className="flex flex-wrap gap-2">
            {(standardRoles.length > 0 ? standardRoles : STATIC_ARTIFICIALFLOW_ROLES).map((role) => (
              <Badge key={role} variant="outline" className="font-mono">
                {role}
              </Badge>
            ))}
          </div>
          <ul className="list-disc space-y-1 pl-5 text-muted-foreground">
            <li><span className="font-mono">{ARTIFICIALFLOW_ADMIN_ROLE}</span>: full console and API access.</li>
            <li><span className="font-mono">{ARTIFICIALFLOW_BUSINESS_ROLE}</span>: deploy, view and update workflows and decisions in the modeler.</li>
            <li>
              <span className="font-mono">{ARTIFICIALFLOW_CLIENT_ROLE}</span>: SDK and API service accounts. Create a confidential
              client with the client-credentials grant. Your application authenticates its own end users and finds their
              pending work with <span className="font-mono">GET /tasks</span> (filter by assignee, candidateUser or candidateGroup).
            </li>
            <li><span className="font-mono">{ARTIFICIALFLOW_ANALYTICS_ROLE}</span>: reserved for analytics features.</li>
          </ul>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Authenticated principal</CardTitle>
          <CardDescription>
            Claims mapped from the current access token.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {principal ? (
            <dl className="grid gap-3 md:grid-cols-2">
              <DetailField label="Subject" value={principal.subject} />
              <DetailField label="Issuer" value={principal.issuer} />
              <DetailField label="Name" value={principal.name} />
              <DetailField label="Email" value={principal.email} />
              <DetailField label="Tenant" value={principal.tenant_id} />
            </dl>
          ) : (
            <div className="text-sm text-muted-foreground">No authenticated principal is currently available.</div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Roles</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-2">
          {roles.length > 0 ? (
            roles.map((role) => (
              <Badge key={role} variant="outline" className="font-mono">
                {role}
              </Badge>
            ))
          ) : (
            <span className="text-sm text-muted-foreground">No roles mapped</span>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
