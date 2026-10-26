# External IAM: Keycloak

Set `IAM_DEPLOYMENT_MODE=true` (Helm: `iam.mode: true`, the default) and then:

1. Create realm (or use existing).
2. Create confidential client `workflow-backend` (audience / client id for API).
3. Create public PKCE client `workflow-frontend` with redirect URIs:
   - `https://<artificialflow-origin>/`
   - silent / post-logout same origin
4. Create roles (exact names):
   - `artificialflowadmin`
   - `artificialflowbusiness`
   - `artificialflowclient`
   - `artificialflowanalytics`
5. Map realm/client roles into a token claim path ArtificialFlow can read, for example `realm_access.roles`, or set:

```text
AUTH_CLAIM_ROLES_PATH=realm_access.roles
```

6. Point ArtificialFlow Helm/Compose at:

```text
AUTH_ISSUER_PUBLIC_URL=https://keycloak.example/realms/<realm>
AUTH_ISSUER_INTERNAL_URL=<same or cluster-internal>
AUTH_CLIENT_ID=workflow-backend
FRONTEND_AUTH_OIDC_AUTHORITY=https://keycloak.example/realms/<realm>
FRONTEND_AUTH_OIDC_CLIENT_ID=workflow-frontend
```

See [../iam.md](../iam.md) for the full checklist.
