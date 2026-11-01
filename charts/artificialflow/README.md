# ArtificialFlow Helm Chart

One chart for every Kubernetes environment: external OIDC, Kafka + Debezium
CDC, and bundled Postgres, Elasticsearch, Kafka, and Debezium Connect that can
each be swapped for a managed service. See [docs/deployment.md](../../docs/deployment.md)
for the full guide and configuration reference.

## Install

No Helm repository is published. Run these commands from a clone of the release
tag; the default image tags match the chart `appVersion`.

```bash
helm upgrade --install artificialflow ./charts/artificialflow \
  --namespace artificialflow --create-namespace \
  --set iam.auth.issuerInternalUrl=https://login.example.com \
  --set iam.auth.issuerPublicUrl=https://login.example.com \
  --set ingress.host=app.example.com \
  --set cors.allowedOrigins=https://app.example.com
```

- Production topology: add `-f values-production-reference.yaml`.
- Smoke test without a provider: `-f values-kind-smoke.yaml` (`iam.mode: false`, no ingress).
  With `iam.mode: false` the console signs in with `iam.consoleAdmin` (default
  admin/admin) and the API and SDKs work without a token. `iam.mode` must be
  the boolean `true` (default) or `false`; use `--set`, not `--set-string`.
- Managed dependencies: [examples/values-managed-deps.yaml](examples/values-managed-deps.yaml).

Tokens must carry the backend audience (`iam.auth.clientId`) and the exact
roles `artificialflowadmin`, `artificialflowbusiness`, `artificialflowclient`, or
`artificialflowanalytics` in the claim named by `iam.auth.claimRolesPath`.

## Layout

| File | Renders |
| :--- | :--- |
| `templates/_components.tpl` | the table of all workloads (apps + bundled data plane) |
| `templates/_helpers.tpl` | names, addresses, validation, and the shared workload renderer |
| `templates/workloads.yaml` | a Service + Deployment/StatefulSet per enabled component |
| `templates/env-configmap.yaml` | shared non-secret environment |
| `templates/secrets.yaml` | `PG_DSN`/password Secret |
| `templates/gateway.yaml`, `ingress.yaml` | nginx gateway config and ingress |
| `templates/networkpolicy.yaml`, `pdb.yaml`, `hpa.yaml`, `monitoring.yaml` | hardening and opt-in observability |

Extra service settings go in `env:` (all services) or `<component>.env:`
(one container) instead of dedicated values keys. Secret names are rejected
there; use `postgresql.existingSecret`.

## Behavior

- Command and runtime apply schema migrations at startup under a Postgres
  advisory lock, then validate the ledger. App Deployments roll with
  `maxUnavailable: 0`, so a failed migration leaves the old pods serving;
  `startupProbe` bounds the migration window.
- The gateway serves the frontend at `/`, command at `/api/`, and query at
  `/api/query/`; everything else stays `ClusterIP`. Metrics listen on private
  `metrics` ports (sync-worker on `http`); `monitoring.enabled=true` adds
  ServiceMonitors, alert rules, and a Grafana dashboard when the Prometheus
  Operator CRDs exist.
- Default-deny NetworkPolicies allow only the required paths; external
  endpoints need `networkPolicy.external.*CIDRs`.
- PDBs cover components with two or more replicas; HPAs are opt-in via
  `autoscaling.enabled` (runtime scaling stays explicit; see
  [HA_REFERENCE.md](../../docs/HA_REFERENCE.md)).
- Bundled StatefulSets keep their PVCs on uninstall (`helm.sh/resource-policy: keep`).
- `compatibility.legacyResourceNames` preserves names and selectors when
  upgrading a release created by the former chart.

## Validate

```bash
make validate-helm
```
