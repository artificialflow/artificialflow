# External IAM: Auth0

Set `IAM_DEPLOYMENT_MODE=true` (Helm: `iam.mode: true`, the default) and then:

1. Create API with identifier matching `AUTH_CLIENT_ID` (e.g. `workflow-backend`).
2. Create SPA application for the frontend; Allowed Callback/Logout/Web Origins = ArtificialFlow origin.
3. Create roles `artificialflowadmin`, `artificialflowbusiness`, `artificialflowclient`, `artificialflowanalytics`.
4. Add an Auth0 Action / Rule to put role names into a claim ArtificialFlow reads, e.g. `https://artificialflow.io/roles`, then:

```text
AUTH_CLAIM_ROLES_PATH=https://artificialflow.io/roles,roles
```

5. Issuer:

```text
AUTH_ISSUER_PUBLIC_URL=https://<tenant>.auth0.com/
FRONTEND_AUTH_OIDC_AUTHORITY=https://<tenant>.auth0.com/
FRONTEND_AUTH_OIDC_CLIENT_ID=<spa client id>
```

Use M2M applications with `artificialflowclient` only for workers/SDK.

See [../iam.md](../iam.md).
