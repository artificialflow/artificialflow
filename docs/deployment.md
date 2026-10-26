# Deployment Guide

ArtificialFlow has exactly two deployment paths with the same topology:

| Path | File | Use |
| :--- | :--- | :--- |
| Docker Compose | `docker-compose.yml` (no `.env` needed) | local development, evaluation, single host |
| Helm | `charts/artificialflow` + one values file | Kubernetes, production |

Both run Postgres, Elasticsearch, Kafka, Debezium Connect, and the ArtificialFlow
services (command, runtime, query, sync-worker, frontend, gateway). Kafka +
Debezium is the only event transport. Elasticsearch 8 is the only supported
search backend. ArtificialFlow does not ship an IAM
product: users, SDK clients, roles, and API tokens live in your external OIDC
provider.

`IAM_DEPLOYMENT_MODE` (Helm: `iam.mode`) is a boolean:

- `true` (Helm default): an external OIDC provider is used. The issuer URLs,
  `AUTH_CLIENT_ID`, and `AUTH_CLAIM_ROLES_PATH` are required. Console users and
  SDK clients sign in through that provider.
- `false` (Compose default): no IAM provider and no OIDC issuer. The console
  uses a local sign-in, `admin` / `admin` by default
  (`CONSOLE_ADMIN_USERNAME` / `CONSOLE_ADMIN_PASSWORD`, Helm
  `iam.consoleAdmin`), and the APIs and SDKs need no token. For local/CI runs.

Any other value, including the old `external` / `disabled`, fails startup with
an error naming the allowed values `true` and `false`. `CONSOLE_ADMIN_*` are
passed to the command service only when the mode is `false`.

## 1. Prepare the OIDC provider

1. Create a backend API resource/audience, e.g. `workflow-backend`
   (`AUTH_CLIENT_ID` / `iam.auth.clientId`).
2. Create a public browser client with Authorization Code + PKCE and no
   secret, e.g. `workflow-frontend`. Register the ArtificialFlow origin
   (`http://localhost:9100` for Compose) as redirect, silent redirect,
   post-logout redirect, and web origin.
3. Create a confidential client with the client-credentials grant for SDKs,
   workers, and automation.
4. Create the roles in [IAM roles](iam.md#external-iam-role-provisioning) and
   emit them in the claim named by `AUTH_CLAIM_ROLES_PATH` /
   `iam.auth.claimRolesPath`.
5. Include the backend audience in access tokens (audience enforcement is on).
6. Make discovery and JWKS reachable from containers/pods and the public
   issuer reachable from browsers.

Opaque tokens: set `AUTH_TOKEN_MODE=introspection` plus the introspection URL,
client ID, and secret. Provider recipes: [docs/iam/](iam/).

## 2. Docker Compose

Requirements: Docker with Compose v2, enough memory for Elasticsearch + Kafka,
and free ports `5432 8080 8081 8083 8092 9092 9100 9200`.

```bash
make smoke              # validates the rendered config and Kafka wiring
make up                 # builds from source, no IAM (console: admin / admin)
ARTIFICIALFLOW_IMAGE_TAG=1.2.0 make up-release   # or run published images
```

`docker-compose.yml` is self-contained: every setting is written as
`${NAME:-default}`, so no `.env` is required. Override any value from the shell,
a `.env` file (Compose loads it automatically when present), or
`--env-file`. To turn on OIDC:

```bash
IAM_DEPLOYMENT_MODE=true \
AUTH_ISSUER_INTERNAL_URL=https://login.example.com \
AUTH_ISSUER_PUBLIC_URL=https://login.example.com \
make up
```

`AUTH_ISSUER_INTERNAL_URL` is the issuer as reached from
containers and `AUTH_ISSUER_PUBLIC_URL` the one browsers see and tokens carry;
they are usually identical. Set `AUTH_ALLOW_INSECURE_ISSUER=true` only for a
local plain-HTTP provider.

Ready when:

```bash
make ps                                   # nothing exited or unhealthy
curl -fsS http://localhost:9100/api/health
curl -fsS http://localhost:9100/api/query/health
curl -fsS http://localhost:8083/connectors/artificialflow-postgres-connector/status   # RUNNING
curl -fsS -H "Authorization: Bearer ${ARTIFICIALFLOW_TOKEN}" http://localhost:9100/api/identity/me
```

Schema migration runs when the command and runtime processes open Postgres.
Debezium connector registration (sync-worker) is automatic. `make init-connector`
is a recovery action only.
`make down` stops the stack; `make clean` also deletes the named volumes
`artificialflow-postgres-data` and `artificialflow-elasticsearch-data`.

Compose identity (project, network, and volume names) is controlled by
`ARTIFICIALFLOW_COMPOSE_PROJECT_NAME`, `ARTIFICIALFLOW_NETWORK`,
`ARTIFICIALFLOW_PGDATA_VOLUME`, and `ARTIFICIALFLOW_ESDATA_VOLUME`. When
attaching an existing installation, set only names that already exist and
verify with `bash scripts/validate_compose_identities.sh`; a wrong name creates
empty storage.

## 3. Helm

No Helm repository is published. Install the chart from a clone of the release
tag. The chart and image tags (`appVersion`) come from the same release.

```bash
git clone --depth 1 --branch v1.2.0 https://github.com/artificialflow/artificialflow.git
cd artificialflow
helm upgrade --install artificialflow ./charts/artificialflow \
  --namespace artificialflow --create-namespace -f my-values.yaml
```

A minimal `my-values.yaml`:

```yaml
iam:
  mode: true                          # boolean; false = no IAM provider
  auth:
    issuerInternalUrl: https://login.example.com
    issuerPublicUrl: https://login.example.com
    clientId: workflow-backend
  frontend:
    oidcClientId: workflow-frontend   # authority defaults to issuerPublicUrl
ingress:
  host: app.example.com
cors:
  allowedOrigins: https://app.example.com
postgresql:
  password: replace-me                # or existingSecret
```

Add `-f charts/artificialflow/values-production-reference.yaml` for the HA
topology (3 multi-active runtimes, monitoring). For a cluster smoke test without
a provider use `values-kind-smoke.yaml` (`iam.mode: false`; the console signs
in with `iam.consoleAdmin`, stored as `CONSOLE_ADMIN_PASSWORD` in the env
Secret). `iam.mode` must be a YAML boolean: `--set iam.mode=false` works,
`--set-string` or `"external"`/`"disabled"` fails. `make validate-helm` lints and renders
every supported combination; the chart fails fast on missing issuer, client, or
dependency addresses, wildcard CORS, HSTS without TLS, and secret names in the
plain `env` map.

### Migrations

There is no migration Job. Command and runtime apply pending migrations when
they start, under one Postgres advisory lock, and validate the checksums
before they serve.

- `startupProbe.periodSeconds` x `startupProbe.failureThreshold` is the
  migration window (5 s x 120 = 10 minutes by default). Raise it before an
  upgrade with a long migration.
- App Deployments roll with `maxUnavailable: 0`. A failed migration leaves the
  old pods serving and the new pod not ready.
- The database user in `PG_DSN` needs DDL rights on its schema.
- Values from older charts under `migration.*` fail rendering. Remove them.
- To migrate out of band, run `./workflow-migrate` from the command image.

### Kafka partitions

The sync-worker creates the Kafka topics with `kafka.topicPartitions`
partitions (default `3`). Sync-worker replicas beyond the partition count sit
idle. Existing topics keep their partition count.

### Managed dependencies

Each dependency is bundled by default. Disable it and give an address:

```yaml
postgresql: {bundled: {enabled: false}, host: pg.internal, existingSecret: artificialflow-pg}
search:     {bundled: {enabled: false}, address: https://es.internal:9200}
kafka:      {bundled: {enabled: false}, brokers: kafka-1.internal:9092,kafka-2.internal:9092}
connect:    {bundled: {enabled: false}, url: http://debezium-connect.internal:8083}
```

See [values-managed-deps.yaml](../charts/artificialflow/examples/values-managed-deps.yaml).
`postgresql.existingSecret` must contain `PG_DSN`, plus `POSTGRES_PASSWORD`
(bundled Postgres and Debezium) and `AUTH_INTROSPECTION_CLIENT_SECRET`
(introspection mode) when used. Egress to external endpoints needs
`networkPolicy.external.*CIDRs`.

The sync-worker registers the Debezium connector with the configured Postgres
host, port, database, user, and password (`CONNECTOR_DATABASE_*`, password read
from the Secret). To provision the connector yourself, set
`syncWorker.env.CONNECT_BOOTSTRAP_ENABLED: "false"`. Postgres must allow logical
replication and the connector user must read the captured tables and manage
the publication and slot.

### Upgrading the former chart in place

Fresh installs use canonical names. To upgrade a release created by the former
chart without renaming immutable selectors, set
`compatibility.legacyResourceNames.{enabled,fullnameOverride,selectorNameOverride,selectorInstanceOverride}`
from the live manifests. Moving to canonical names is a blue/green cutover:
back up Postgres, search, and PVCs; freeze writes until outbox and projection
lag reach zero; install the canonical release separately; switch traffic after
validation; keep the old release for the rollback window. Never run two
writers against one database, connector slot, or consumer group.

### Ready when

```bash
kubectl -n artificialflow get pods
curl -fsS https://app.example.com/api/health
curl -fsS https://app.example.com/api/query/health
```

Every pod is Ready, OIDC login returns the expected roles from
`/api/identity/me`, the connector and its tasks are `RUNNING`, and a deployed
workflow appears in the query API. `/health` only proves the process runs; the
identity, connector, and write-to-query checks are the real postconditions.

## Configuration reference

Every variable below has a built-in default. Compose passes through only the
ones it lists (`IAM_DEPLOYMENT_MODE`, the issuer URLs, `AUTH_CLIENT_ID`,
`AUTH_CLAIM_ROLES_PATH`, `AUTH_ALLOW_INSECURE_ISSUER`, plus the runtime and
storage settings); to override another one, add it to the `x-auth-env` block.
`CONSOLE_ADMIN_*` are not in that block: the app service loads them from
`compose/iam-false.env`, which is only picked up when
`IAM_DEPLOYMENT_MODE=false`. Helm sets the IAM keys from `iam.*` and takes anything else
through `env:` (all services) or `<component>.env:` (one service).

| Variable | Default | Notes |
| :--- | :--- | :--- |
| `IAM_DEPLOYMENT_MODE` | Compose: `false`, Helm: `true` | `true` or `false`; anything else (including `external`/`disabled`) fails startup. `false` ignores every `AUTH_*` issuer setting; `true` fails startup without an issuer |
| `CONSOLE_ADMIN_USERNAME` / `CONSOLE_ADMIN_PASSWORD` | `admin` / `admin` | console sign-in, passed and read only when `IAM_DEPLOYMENT_MODE=false`; does not protect the API |
| `AUTH_ISSUER_INTERNAL_URL` / `AUTH_ISSUER_PUBLIC_URL` | — | required when `IAM_DEPLOYMENT_MODE=true` |
| `AUTH_CLIENT_ID` | `workflow-backend` | expected audience |
| `AUTH_TOKEN_MODE` | `jwt` | `jwt` or `introspection` |
| `AUTH_INTROSPECTION_URL` / `_CLIENT_ID` / `_CLIENT_SECRET` / `_AUTH_METHOD` | — / — / — / `basic` | introspection only |
| `AUTH_CLAIM_ROLES_PATH` | `roles,realm_access.roles,groups` | comma-separated claim paths |
| `AUTH_CLAIM_SUBJECT_PATH` / `_TENANT_PATH` / `_EMAIL_PATH` / `_NAME_PATH` | `sub` / `tenant_id` / `email` / `name` | |
| `AUTH_ENFORCE_AUDIENCE` | `true` | |
| `AUTH_ALLOWED_ALGORITHMS` | `RS256` | |
| `AUTH_ALLOW_INSECURE_ISSUER` | `false` | local HTTP providers only |
| `AUTH_PRODUCTION` | `false` (Helm: `true` when `iam.mode` is `true`) | |
| `TENANT_MODE` | `compat` | use `strict` in production |
| `FRONTEND_AUTH_OIDC_AUTHORITY` | `AUTH_ISSUER_PUBLIC_URL` | |
| `FRONTEND_AUTH_OIDC_CLIENT_ID` | `workflow-frontend` | |
| `ALLOWED_ORIGINS` / `CORS_ALLOW_CREDENTIALS` | localhost origins / `false` | exact origins in production |
| `RUNTIME_SCHEDULER_MODE` | `compat` | `multi-active` for HA (Helm: `runtime.schedulerMode`) |
| `RUNTIME_TICK_INTERVAL` / `RUNTIME_TASK_TIMEOUT` / `RUNTIME_CLAIM_BATCH_SIZE` / `RUNTIME_LEASE_TTL` | `1s` / `5s` / `100` / `15s` | |
| `OUTBOX_RELAY_INTERVAL` / `OUTBOX_RELAY_BATCH_SIZE` | tick interval / `200` | runtime outbox relay |
| `IDEMPOTENCY_RETENTION` | `168h` | |
| `ES_INDEX_PREFIX` | `artificialflow` | |
| `KAFKA_TOPIC_PARTITIONS` | `3` | sync-worker topic creation (Helm: `kafka.topicPartitions`) |
| `CONNECT_BOOTSTRAP_ENABLED` | `true` | sync-worker registers the Debezium connector |
| `CONNECTOR_JSON` / `CONNECTOR_FILE` | embedded config | full connector override; no passwords in ConfigMaps |
| `SYNC_FRESHNESS_SLO_SEC` / `SYNC_HEALTH_FAIL_ON_STALE` | `120` / `false` | |
| `LOG_LEVEL` | `info` | |

## Production requirements

- Replace every example hostname and credential; never reuse Compose defaults.
- Set `TENANT_MODE=strict` and exact CORS origins; provision the roles in the
  [IAM Guide](iam.md).
- Terminate TLS at the ingress; keep HSTS only when that holds.
- Keep audience validation on, SDK tokens short-lived, and client secrets in a
  secret manager. Never expose SDK tokens to browsers.
- Prefer managed Postgres, Kafka, and Elasticsearch; back them up and test restores.
- Migrations run when command and runtime open Postgres, under one Postgres
  advisory lock, so the application database user needs DDL rights. Ship
  schema changes as expand/contract and keep the Debezium-captured table names
  stable.
- Verify image signatures and SBOM/provenance; see [DOCKER_IMAGES.md](DOCKER_IMAGES.md).

Client authentication: [Node.js](sdk-nodejs.md), [.NET](sdk-dotnet.md),
[Java](sdk-java.md), [Python worker](sdk-python.md), [Go worker](sdk-go.md).
