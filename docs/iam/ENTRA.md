# External IAM: Microsoft Entra ID

Set `IAM_DEPLOYMENT_MODE=true` (Helm: `iam.mode: true`, the default) and then:

1. Register app for the API (`workflow-backend`) and expose an Application ID URI / audience.
2. Register SPA app for the frontend with Authorization Code + PKCE; redirect URI = ArtificialFlow origin.
3. Create app roles or security groups mapped to:
   - `artificialflowadmin`
   - `artificialflowbusiness`
   - `artificialflowclient`
   - `artificialflowanalytics`
4. Ensure roles appear in the access token (optional claims / group→role mapping). Configure:

```text
AUTH_CLAIM_ROLES_PATH=roles,groups
AUTH_CLIENT_ID=<api app id uri or client id expected as audience>
```

5. Set issuer URLs to `https://login.microsoftonline.com/<tenant>/v2.0` (public and internal as appropriate).

Machine clients use client credentials against the API app; do not grant `artificialflowadmin` to SDK clients.

See [../iam.md](../iam.md).
