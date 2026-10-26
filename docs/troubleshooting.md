# Troubleshooting

## Compose Config Validation

```bash
make smoke
```

This renders `docker-compose.yml` and checks the Kafka wiring without starting
containers.

## A Service Exits At Startup

- `IAM_DEPLOYMENT_MODE` must be `true` or `false`. Old values such as
  `external` or `disabled` fail startup with a message naming the allowed
  values.
- With `true`, the issuer URLs, `AUTH_CLIENT_ID`, and `AUTH_CLAIM_ROLES_PATH`
  are required. `AUTH_PRODUCTION=true` refuses to start with `false`.
- Command and runtime apply migrations at startup. Read their logs for a
  checksum mismatch or an unknown schema version. Never edit an applied
  migration. Run `workflow-migrate validate` from the command image to check
  the schema.
- On `make up-release` from a development branch, the published images may not
  match the branch configuration. Use `make up`, or check out the release tag.

## Cannot Log In

- With `IAM_DEPLOYMENT_MODE=false`, sign in with `admin` / `admin` (or your
  `CONSOLE_ADMIN_USERNAME` / `CONSOLE_ADMIN_PASSWORD`).
- Confirm the frontend runtime config points to the browser-visible OIDC issuer.
- Confirm the backend uses the correct internal and public issuer URLs.
- Confirm the frontend client ID exists in your OIDC provider.
- In production, confirm `AUTH_ENFORCE_AUDIENCE=true` matches issued token audiences.

## Query Results Are Empty

- Confirm the sync worker is healthy: `curl -fsS http://localhost:8092/health`.
- Confirm the Kafka topics exist and have assigned consumers.
- Confirm the Debezium connector and its tasks are `RUNNING`.
- Confirm Elasticsearch is reachable.
- Run the CQRS smoke test:

```bash
make cqrs-e2e-smoke
```

## SDK Calls Are Unauthorized

- Confirm the machine client has only `artificialflowclient`.
- Confirm the role claim path matches `AUTH_CLAIM_ROLES_PATH`.
- Send the raw access token. The SDKs and worker clients add the `Bearer `
  prefix.
- In JWT mode, confirm the signed token issuer matches
  `AUTH_ISSUER_PUBLIC_URL`. In introspection mode, confirm the configured
  introspection endpoint accepts the token and returns `active=true`, a
  subject, the expected audience, and the required role claims.

## Helm Install Fails

- Render templates locally:

```bash
helm lint ./charts/artificialflow
helm template artificialflow ./charts/artificialflow
helm template artificialflow ./charts/artificialflow -f ./charts/artificialflow/values-kind-smoke.yaml
```

- Verify required external secrets exist.
- Verify image repositories and tags are set.
- Verify `iam.mode` is the boolean `true` or `false` (not a string such as
  `"external"`, `"disabled"`, or a `--set-string` value).
- Remove any `migration.*` values from older charts. The chart fails rendering
  while they are set.

## Pods Restart During An Upgrade

Command and runtime migrate before they report ready. If a long migration hits
the startup probe limit, raise `startupProbe.failureThreshold` (window =
`periodSeconds` x `failureThreshold`, 10 minutes by default). Old pods keep
serving until the new pods are ready.
