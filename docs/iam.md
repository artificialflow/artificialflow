# IAM Guide

ArtificialFlow integrates with an external OIDC provider. It does not ship an IAM
product. Authentication proves the identity and validates the token;
authorization uses exact ArtificialFlow role names from the configured roles claim.
Any standards-compliant OIDC provider works (Keycloak, Entra ID, Auth0, Okta,
...). Recipes live in [docs/iam/](iam/). Providers that put roles in a different
claim are handled by `AUTH_CLAIM_ROLES_PATH` (a comma-separated list of dotted
claim paths).

## Modes

`IAM_DEPLOYMENT_MODE` (Helm: `iam.mode`) is a boolean, `true` or `false`. Any
other value, including the old `external` / `disabled`, fails startup.

| `IAM_DEPLOYMENT_MODE` | Console login | API and SDKs |
| :--- | :--- | :--- |
| `true` (Helm default) | Your OIDC provider | Bearer token from your OIDC provider; roles from the token |
| `false` (Compose default) | Local sign-in, `admin` / `admin` (`CONSOLE_ADMIN_USERNAME` / `CONSOLE_ADMIN_PASSWORD`) | No token needed; every call runs as admin |

`true` requires the issuer URLs, `AUTH_CLIENT_ID`, and `AUTH_CLAIM_ROLES_PATH`.
`false` has no OIDC issuer and is for local evaluation only: the console login
is a convenience gate and does not protect the API. `CONSOLE_ADMIN_*` are
passed only in that mode. `AUTH_PRODUCTION=true` refuses to start in that mode.

The rest of this guide covers `IAM_DEPLOYMENT_MODE=true`.

## What IAM is used for

ArtificialFlow uses your OIDC provider for two things only:

- Signing in the people who administer ArtificialFlow (console users).
- Issuing tokens to the SDK/API clients that call ArtificialFlow.

Authorization is role-based. There are no ArtificialFlow-specific scopes. End
users of your own applications never authenticate to ArtificialFlow: your
application validates their tokens with its own IAM, then asks ArtificialFlow
for their tasks with `GET /tasks` and passes the end user as a filter or
`assignee` value (see [API.md](API.md#user-tasks-api)).

## External IAM

Use `IAM_DEPLOYMENT_MODE=true` when your organization already operates an OIDC provider.
ArtificialFlow validates its tokens but does not create or manage identities in that
provider.

Provider recipes:

- [Keycloak](iam/KEYCLOAK.md)
- [Microsoft Entra ID](iam/ENTRA.md)
- [Auth0](iam/AUTH0.md)

### External IAM provisioning checklist

The external IAM administrator must create:

1. A backend API resource or audience matching `AUTH_CLIENT_ID`; the deployment
   examples use `workflow-backend`.
2. A public browser client matching `FRONTEND_AUTH_OIDC_CLIENT_ID`; the examples
   use `workflow-frontend`.
3. A confidential machine-to-machine client for each SDK, worker, or automation
   integration.
4. The four standard ArtificialFlow roles listed below.
5. Token mappings that put assigned roles into one of the configured
   `AUTH_CLAIM_ROLES_PATH` claims.

Configure the browser client for Authorization Code with PKCE and no client
secret. Its redirect URI, silent redirect URI, post-logout redirect URI, and
allowed web origin must be the ArtificialFlow origin:

- Local Compose: `http://localhost:9100`
- Kubernetes: the public HTTPS ArtificialFlow origin, for example
  `https://app.artificialflow.example.io`

The frontend uses the browser origin for all three redirect URIs.

### External IAM role provisioning

Role names are matched case-insensitively after trimming, but providers and
automation should use these exact names. Other spellings, including the
pre-1.x names with spaces, are not recognized.

- `artificialflowadmin`: full access. Platform administrators: every console
  page and every API, including deleting workflows and instances, editing
  variables, incidents, job retry, migrations and engine metrics.
- `artificialflowbusiness`: process designers. Console access to the process
  catalog, decisions and modeler; API access to deploy and read workflows and
  decisions. Instances, variables, deletes, incidents and client setup are
  hidden.
- `artificialflowclient`: SDK, worker, API and automation identities. Starts
  instances, publishes messages/signals/escalations, operates jobs, searches,
  claims and completes user tasks on behalf of end users. No console access.
- `artificialflowanalytics`: reserved for analytics. It opens an Analytics
  placeholder page and has no API access yet.

Recommended assignments:

- ArtificialFlow administrator: `artificialflowadmin`
- Process designer: `artificialflowbusiness`
- SDK/service account, including the backend of an end-user task application:
  `artificialflowclient` only

Do not grant `artificialflowadmin` to an SDK client.

The default role paths are:

```text
roles,realm_access.roles,groups
```

ArtificialFlow checks those comma-separated paths in order. Change
`AUTH_CLAIM_ROLES_PATH` in both the command and query services, or
`iam.auth.claimRolesPath` in Helm, when your provider uses another claim.

Other claim mappings are available for provider-specific tokens:

```text
AUTH_CLAIM_SUBJECT_PATH
AUTH_CLAIM_TENANT_PATH
AUTH_CLAIM_EMAIL_PATH
AUTH_CLAIM_NAME_PATH
```

### External token validation

For JWT access tokens:

```text
AUTH_TOKEN_MODE=jwt
AUTH_ISSUER_INTERNAL_URL=https://login.example.com
AUTH_ISSUER_PUBLIC_URL=https://login.example.com
AUTH_CLIENT_ID=workflow-backend
AUTH_ENFORCE_AUDIENCE=true
AUTH_ALLOW_INSECURE_ISSUER=false
```

The provider must expose OIDC discovery and signing keys, and each token's
audience must contain `workflow-backend` or the configured backend client ID.

For opaque access tokens:

```text
AUTH_TOKEN_MODE=introspection
AUTH_INTROSPECTION_URL=https://login.example.com/oauth2/introspect
AUTH_INTROSPECTION_CLIENT_ID=workflow-introspection
AUTH_INTROSPECTION_CLIENT_SECRET=<secret>
AUTH_INTROSPECTION_AUTH_METHOD=basic
```

Use `post` only if the provider requires credentials in the request body.
Protect the introspection secret with a secret manager.

`AUTH_ISSUER_INTERNAL_URL` is the endpoint reachable by ArtificialFlow services.
`AUTH_ISSUER_PUBLIC_URL` is the browser-visible/token issuer. Keep them equal
and HTTPS when the public endpoint is reachable from ArtificialFlow. If the deployment
requires separate internal and public addresses,
`AUTH_ALLOW_INSECURE_ISSUER=true` enables issuer-aware request rewriting; use
it only with a trusted internal route and an explicit security review.

Production should also set:

```text
AUTH_PRODUCTION=true
AUTH_ALLOWED_ALGORITHMS=RS256
AUTH_HTTP_TIMEOUT_SECONDS=10
TENANT_MODE=strict
```

Production mode fails startup when authentication is disabled, an issuer or
introspection URL is not HTTPS, audience enforcement is disabled, insecure issuer
rewriting is enabled, introspection credentials are absent, or the algorithm
allowlist is empty. OIDC discovery and JWKS requests use bounded timeouts and
refresh keys when the provider rotates signing keys.

## SDK Client Standard

For SDK and automation usage, use a machine identity with only
`artificialflowclient`. Do not use a human username/password flow for long-running
integrations.

Create a provider-native client-credentials application, assign only
`artificialflowclient`, include the ArtificialFlow audience and role claim in its
access tokens, and rotate its secret in the external provider.

The Node.js, .NET, and Java SDKs can acquire, cache, and refresh that token.
Only the access token is sent to ArtificialFlow.

See [Node.js SDK](sdk-nodejs.md), [.NET SDK](sdk-dotnet.md), and
[Java SDK](sdk-java.md) for complete configuration examples. The
[Python](sdk-python.md) and [Go](sdk-go.md) worker clients take a ready access
token and do not refresh it.

## Authentication verification

Health endpoints are public, so a successful health check does not prove IAM is
configured correctly. Verify a real access token:

```bash
curl -fsS \
  -H "Authorization: Bearer ${ARTIFICIALFLOW_TOKEN}" \
  http://localhost:9100/api/identity/me
```

The response must show `authenticated: true`, the expected subject and issuer,
the ArtificialFlow backend audience, and the required role.

- `401 Unauthorized` means the token is missing, expired, has the wrong issuer
  or audience, cannot be introspected, or fails signature validation.
- `403 Forbidden` means the token is valid but does not have a role permitted
  for that operation.
- Browser login loops usually indicate a mismatched issuer, redirect URI,
  client ID, web origin, or HTTP/HTTPS setting.

## Production Hardening

- Use HTTPS issuer URLs.
- Keep audience validation enabled.
- Grant only the minimum ArtificialFlow role required by each identity.
- Keep human and machine identities separate.
- Keep access tokens short-lived.
- Rotate client secrets regularly in the provider.
- Store OIDC client secrets in a secret manager, never source control or
  browser storage.
- Never put SDK client secrets or tokens in frontend code.
- Never run `IAM_DEPLOYMENT_MODE=false` where the API is reachable by untrusted
  clients: the API accepts requests without a token in that mode.
