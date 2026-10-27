# Compatibility Matrix

ArtificialFlow publishes compatibility guarantees for the surfaces below. The
stable `1.x` contract policy in [STABILITY_POLICY.md](STABILITY_POLICY.md)
applies to all of them. Version pins in this tree are `1.2.0`; each release
updates them together.

## Versioning policy

| Surface | Current line | Compatibility policy |
| :--- | :--- | :--- |
| Platform release | `1.2.x` | Patch releases are backward compatible unless a security fix requires otherwise. Breaking changes require a major bump and the deprecation policy. |
| Command API | `/v1` routes (plus the unversioned `/api` alias) and protobuf API `artificialflow.api.v1` | Additive changes preferred. Breaking changes require release notes and deprecation. |
| Query API | `/v1` HTTP responses | Response fields may be added. Existing field meaning stays stable. |
| Worker API | Protocol headers and `/jobs/*` endpoints | Wire compatibility protected by `make worker-conformance`. |
| User tasks API | `GET /tasks` and `/instances/{id}/tasks*` | End users are passed as filter and `assignee` values by the calling application. ArtificialFlow does not authenticate them. |
| Client libraries | See [SDK compatibility](#sdk-compatibility) | Patch releases preserve public signatures. Deprecations follow the 1.x policy. |
| Docker images | `artificialflow/*:1.2.x` | Image environment variables and exposed ports stay stable within a minor line. |
| Helm chart | `charts/artificialflow` `1.2.x` | Values may be added. Renames and removals fail rendering with a message and are listed in the changelog. |
| IAM mode | `IAM_DEPLOYMENT_MODE` / `iam.mode` boolean | Only `true` and `false` are accepted. |
| Tenant scope | Reserved tenant `default` | Missing claims resolve to `default` in compatibility mode. `TENANT_MODE=strict` is opt-in and rejects missing claims. Client-supplied tenant fields never change scope. |
| Resource authorization | Role matrix in [API.md](API.md#roles) | Role-only. Token scopes are not checked. |
| Runtime scheduling | `compat` or `multi-active` | `compat` keeps one global lease. `multi-active` supports three or more active replicas with fenced per-item claims. |
| Instance migration | [Bounded additive API](INSTANCE_MIGRATION.md) | Dry-run and apply support same-tenant, same-lineage, newer definitions with complete mappings. Completed history is never rewritten. |
| BPMN semantics | See [BPMN_SUPPORT_MATRIX.md](BPMN_SUPPORT_MATRIX.md) | Supported, not-supported, and not-evaluated statuses are part of the contract. |

## Developer HTTP API

Language-agnostic REST integration (command, query, worker, user tasks, auth) is
documented in [API.md](API.md). That guide is the contract for consumers who do
not use an SDK.

## Bake-off supported contracts

| Contract | ArtificialFlow surface | Notes |
| :--- | :--- | :--- |
| Process deploy and start | Command API and modeler | BPMN 2.0 XML with ArtificialFlow extensions. See the external import guide. |
| External workers | `/jobs/*` and every client library | Activate, complete, fail, extend-lock, capabilities, idempotency. |
| Event publish | `/messages`, `/signals`, `/escalations` | Node.js, .NET, and Java SDKs, or HTTP. |
| Human tasks | `GET /tasks` and instance task APIs | Search by assignee, candidate user, or group. Claim and complete with an explicit `assignee`. A due-date breach publishes escalation `user-task.due-date.breached` and creates an SLA incident. |
| Incidents | `GET /incidents` and the Incidents UI | Engine and SLA incidents (`SLA_DUE_DATE_BREACHED`, job failures, ...). |
| Ops history and search | Query API and Elasticsearch | Eventually consistent CQRS projection. |
| Identity | External OIDC, or no IAM for evaluation | Roles: `artificialflowadmin`, `artificialflowbusiness`, `artificialflowclient`, `artificialflowanalytics`. |
| Kubernetes install | Helm chart | Bundled or managed Postgres, Kafka, Debezium Connect, and Elasticsearch. |

See also [EXTERNAL_BPMN_IMPORT.md](EXTERNAL_BPMN_IMPORT.md) and [HA_REFERENCE.md](HA_REFERENCE.md).

## Supported deployment combinations

| Deployment | IAM mode | Dependencies | Status |
| :--- | :--- | :--- | :--- |
| Docker Compose (`make up`) | `IAM_DEPLOYMENT_MODE=false` (default; console `admin`/`admin`, tokenless API) or `true` (your OIDC provider) | Postgres 15.19, Apache Kafka 4.4 (KRaft), Debezium Connect 3.0, Elasticsearch 8.19 | Development and evaluation |
| Docker Compose (`make up-release`) | Same | Same, with published `artificialflow/*` images | Evaluation of a release |
| Helm | `iam.mode: true` (default) or `false` | Bundled StatefulSets by default, or managed services | Production path |

Elasticsearch is the only supported search backend. Kafka with Debezium is the
only event transport.

## Worker protocol

| Item | Compatibility note |
| :--- | :--- |
| Request version header | Workers may send `X-Workflow-Worker-Protocol-Version: v1`. |
| Response version header | The API returns `X-Workflow-Engine-Protocol-Version`. |
| Idempotency | Mutation retries should use `Idempotency-Key`. |
| Capabilities | `GET /jobs/capabilities` lists server-supported worker features. |
| Conformance | Run `make worker-conformance` before changing worker API behavior. |

## SDK compatibility

| Client | Package and version in this tree | Runtime | Scope |
| :--- | :--- | :--- | :--- |
| Node.js | `@artificialflow/nodejs-sdk@1.2.0` | Node.js 20+ | Full API, workers, user tasks |
| .NET | `ArtificialFlow.Sdk` `1.2.0` | .NET 8 | Full API, workers, user tasks |
| Java | `io.artificialflow:artificialflow-sdk:1.2.0` | Java 17+ | Full API, workers, user tasks |
| Python worker | `artificialflow-worker` `0.1.0` (install from source) | Python 3.10+ | Worker protocol only |
| Go worker | `backend/libs/worker` (versioned with the platform) | Go 1.26 | Worker protocol only |

Use a client from the same release as the platform. The SDKs version with the
platform. Every client works against both IAM modes. See
[Node.js](sdk-nodejs.md), [.NET](sdk-dotnet.md), [Java](sdk-java.md),
[Python](sdk-python.md), and [Go](sdk-go.md).

## Release validation

Before publishing a release, run:

```bash
make release-dry-run
make smoke
node scripts/validate_api_sdk_contract.mjs
```

For worker API changes, also run:

```bash
make worker-conformance
```
