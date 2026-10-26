# Architecture

ArtificialFlow is an open-source BPMN workflow engine with a browser modeler, command API, runtime worker, query API, CQRS sync worker, Node.js, .NET, and Java SDKs, and Python and Go worker clients.

## Components

| Component | Responsibility |
| :--- | :--- |
| Frontend | BPMN modeler, dashboards, read-only identity view, and workflow operations. |
| Gateway | Single HTTP entry point that routes `/` to the frontend, `/api/` to command service, and `/api/query/` to query service. |
| Command service | Workflow deployment, instance mutation APIs, worker job and user-task APIs, identity config/me endpoints (plus local console login when IAM is off), outbox, and runtime coordination. |
| Runtime | Background workflow execution loop for timers, jobs, and pending execution paths. Relays the transactional outbox to Kafka in batches. |
| Query service | Read-optimized API backed by Elasticsearch projections. |
| Sync worker | Projects command-side changes from Kafka/Debezium into Elasticsearch with bulk writes. |
| IAM provider | Optional external OIDC provider (`IAM_DEPLOYMENT_MODE=true`). Users, clients, and tokens live in that provider. |
| SDKs ([Node.js](sdk-nodejs.md), [.NET](sdk-dotnet.md), [Java](sdk-java.md)) | Programmatic workflow, decision, user-task, and job-worker API clients. |
| Worker clients ([Python](sdk-python.md), [Go](sdk-go.md)) | Job-worker protocol only. |

Command and runtime apply database migrations at startup under a Postgres
advisory lock. There is no separate migration step.

## Request Flow

1. Users open the frontend through the gateway.
2. With `IAM_DEPLOYMENT_MODE=true` the frontend authenticates with the
   configured OIDC provider; with `false` it uses the local console login and
   the API needs no token.
3. Command APIs handle writes and emit events/outbox records.
4. Runtime and workers execute workflow jobs.
5. Sync worker projects state changes into search indexes.
6. Query APIs serve dashboard, process, and instance views.

## Deployment Modes

There is one Compose file and one Helm chart. Each runs in one of two IAM
modes set by the boolean `IAM_DEPLOYMENT_MODE` (Helm: `iam.mode`):

- `true`: an existing external OIDC provider (Helm default).
- `false`: no IAM provider, console `admin`/`admin`, tokenless API (Compose default).

- **Docker Compose** (`docker-compose.yml`): local/evaluation stack with Postgres, Kafka, Debezium, and Elasticsearch.
- **Helm** (`charts/artificialflow`): Kubernetes deployment with bundled Postgres, Kafka, Debezium, and Elasticsearch enabled by default; disable each to use managed services.

## Production Boundaries

Production deployments should externalize Postgres, Kafka, Elasticsearch, secrets, TLS, backups, and observability.

## Architecture Contracts

- [Accepted ADRs](adr/README.md) define migration, runtime ownership, tenant,
  process-instance migration, and HTTP evolution contracts.
- [Threat model](THREAT_MODEL.md) identifies trust boundaries and required
  controls across the data, execution, operations, and supply-chain paths.
- [Kubernetes production reference](KUBERNETES_REFERENCE.md) defines the
  reference topology, workload review, SLOs, and operational evidence.
- [Release maturity rubric](RELEASE_MATURITY.md) defines measurable candidate
  and production-proven levels.

These are adoption constraints. Existing `1.x` surfaces remain governed by the
[stability policy](STABILITY_POLICY.md); an accepted target does not by itself
mean every service already implements it.

## Maintainability boundaries

- Persistent workflow, element, job, timer, subscription, incident, outbox, and
  migration states use named constants in `backend/libs/model/states.go`.
  Their JSON and database values remain the existing uppercase strings.
- Command HTTP route composition and authorization live in `routes.go`;
  endpoint implementations remain grouped behind `Handler`.
- Oversized legacy files and functions are explicit ratchets rather than hidden
  exceptions. Their ceilings and generated/duplicate exclusions live in
  `scripts/quality/maintainability.json`; accepted decomposition work is tracked
  in `ENGINEERING_DEBT.md`.
- PR CI runs coverage, race, and bounded fuzz checks. Scheduled/deep CI extends
  fuzz duration and mutation-tests the RFC 9457 error boundary. Generated
  protobuf is excluded from coverage and maintainability measurements because
  it is regenerated from schema rather than maintained by hand.
