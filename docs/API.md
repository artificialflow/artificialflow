# ArtificialFlow HTTP API

Integration guide for developers who call ArtificialFlow **directly over HTTP**
(without the Node.js, .NET, or Java SDK) or who need to choose between an SDK
and raw API integration.

Stability of these surfaces is covered by [COMPATIBILITY_MATRIX.md](COMPATIBILITY_MATRIX.md)
and [STABILITY_POLICY.md](STABILITY_POLICY.md).

## Runtime tenancy and instance migration

Protected requests are scoped to the tenant resolved from the trusted identity
claim. In the default 1.x compatibility mode a missing claim resolves to
`default`. A conflicting `X-Tenant-ID` assertion is rejected; request bodies
cannot select a tenant. Foreign-tenant identifiers return the same `404` as an
unknown identifier.

Administrators can validate and apply the bounded migration subset:

- `POST /api/instances/{id}/migration/dry-run`
- `POST /api/instances/{id}/migration`

Both accept `sourceDefinitionKey`, `targetDefinitionKey`, and an
`elementMapping` object. The target must be a newer version in the same tenant
and process lineage. Every active element, user task, timer, and message
subscription must be mapped. Nested scopes, compensation, event subprocesses,
multi-instance state, executing external work, and unresolved incidents are
rejected with a stable `reasonCode`. Apply revalidates source definition,
version, and active state in the transaction and writes an immutable audit
event.

The exact supported and rejected state combinations are maintained in
[INSTANCE_MIGRATION.md](INSTANCE_MIGRATION.md). Treat that document as the
release support matrix; behavior outside it is rejected rather than inferred.

## Documentation map

- [Choose HTTP or an SDK](#when-to-use-the-api-vs-an-sdk)
- [Base URLs and gateway routing](#base-url-and-gateway)
- [OpenAPI, Swagger UI, and API tools](#openapi-swagger-ui-and-api-tools)
- [Authentication and authorization](#authentication)
- [Request, response, error, and CQRS conventions](#common-conventions)
- [End-to-end quickstart](#quickstart-end-to-end)
- [Command API](#command-api-reference)
- [Worker/jobs API](#worker--jobs-api)
- [User tasks API](#user-tasks-api)
- [Query API](#query-api-reference)
- [Integration architectures](#integration-patterns)
- [Production-readiness checklist](#production-integration-checklist)
- [Complete endpoint catalog](#endpoint-catalog-cheat-sheet)

---

## When to use the API vs an SDK

| Situation | Recommended path |
| :--- | :--- |
| Ruby, PHP, or any language without an official SDK | **HTTP API** (this document) |
| Custom gateway, BFF, or ESB that already speaks REST | **HTTP API** |
| Minimal dependency / air-gapped clients that cannot pull SDK packages | **HTTP API** |
| Node.js / TypeScript automation, workers, task apps | [`@artificialflow/nodejs-sdk`](sdk-nodejs.md) |
| .NET automation, workers, task apps | [`ArtificialFlow.Sdk`](sdk-dotnet.md) |
| Java automation, workers, task apps | [`artificialflow-sdk`](sdk-java.md) |
| Python workers (jobs only) | [`artificialflow-worker`](sdk-python.md) |
| Go workers (jobs only) | [`backend/libs/worker`](sdk-go.md) |
| Human operators / process designers | ArtificialFlow console (UI), not raw API |

The SDKs are thin clients over the same HTTP contract. Anything the SDK can do,
you can do with `curl`, `fetch`, or your language’s HTTP library against the
gateway.

---

## Base URL and gateway

Prefer the **gateway**. It is the supported public entrypoint for Compose and
Helm.

| Environment | Command (writes) | Query (reads) |
| :--- | :--- | :--- |
| Local Compose | `http://localhost:9100/api/v1` | `http://localhost:9100/api/v1/query` |
| Production (example) | `https://app.example.com/api/v1` | `https://app.example.com/api/v1/query` |
| Direct service (dev only) | `http://localhost:8080/v1` | `http://localhost:8081/v1` |

Every route is served twice:

- **`/api/v1/...` (recommended):** errors are RFC 9457 Problem Details.
- **`/api/...` (unversioned compatibility alias):** same handlers and payloads,
  but errors keep their 1.x plain-text bodies.

Gateway path rewriting:

- `POST /api/v1/instances` → command `/v1/instances`
- `GET /api/v1/query/instances` → query `/v1/instances`
- `POST /api/instances` → command `/instances`
- `GET /api/query/instances` → query `/instances`

Worker compatibility is also negotiated with the
`X-Workflow-Worker-Protocol-Version` header; platform releases follow SemVer.

Public health (no auth):

```bash
curl -sS http://localhost:9100/api/health
curl -sS http://localhost:9100/api/query/health
```

---

## OpenAPI, Swagger UI, and API tools

The machine-readable contract is checked in at
[`docs/openapi.yaml`](openapi.yaml) and served by the gateway at
`/api/openapi.yaml`. It covers the supported gateway paths for
the Command, Query, Worker, and User tasks APIs. Treat the
checked-in document and this guide as the release contract.

### Recommended interactive documentation

| Tool | Recommended use |
| :--- | :--- |
| [Swagger Editor](https://editor.swagger.io/) | Inspect and try the checked-in OpenAPI document |
| [Redocly CLI](https://redocly.com/docs/cli/) | Lint the contract and render a polished reference site |
| [Postman](https://www.postman.com/) or [Insomnia](https://insomnia.rest/) | Import OpenAPI, configure environments, and exercise calls |
| [Bruno](https://www.usebruno.com/) | Git-friendly local API collections without a hosted workspace |
| [OpenAPI Generator](https://openapi-generator.tech/) | Generate typed clients for Java, C#, Python, Go, and other languages |

Local preview without changing the application:

```bash
# Swagger UI
docker run --rm -p 9191:8080 \
  -e SWAGGER_JSON=/contract/openapi.yaml \
  -v "$PWD/docs:/contract:ro" \
  swaggerapi/swagger-ui
# Open http://localhost:9191

# Redoc
npx @redocly/cli preview-docs docs/openapi.yaml
```

Import `docs/openapi.yaml` into Postman, Insomnia, Bruno, or Stoplight and set:

- `server` to `http://localhost:9100/api/v1` for versioned local Compose routes
  (`/api` remains an additive compatibility alias);
- Bearer authentication to a short-lived machine access token;
- worker header `X-Workflow-Worker-Protocol-Version: v1`;
- an `Idempotency-Key` for job mutation requests.

### Runtime Swagger status

The command service exposes a deprecated legacy Swaggo route at
`/api/swagger/index.html`. It requires API authentication and only includes
handlers carrying Swaggo annotations, so it is not the complete API
contract and should not be used for client generation. Use
the gateway-served [`docs/openapi.yaml`](openapi.yaml) for complete tooling
imports. The partial Swaggo route is scheduled for removal in `v1.3.0`.

### Contract validation

Run a mature OpenAPI linter before publishing generated clients:

```bash
npx @redocly/cli lint docs/openapi.yaml
```

Generated clients remain consumers of the HTTP contract; they do not replace
the compatibility and deprecation rules in
[STABILITY_POLICY.md](STABILITY_POLICY.md).

---

## Authentication

Authentication depends on the deployment's `IAM_DEPLOYMENT_MODE`
(Helm: `iam.mode`). `GET /identity/config` reports it as the boolean
`deployment_mode`.

| `IAM_DEPLOYMENT_MODE` | API behavior |
| :--- | :--- |
| `true` (external OIDC; Helm default) | Every route except health requires `Authorization: Bearer <access-token>` issued by the configured provider. Roles come from the token. |
| `false` (no IAM provider; Compose default) | No token is needed. Every request runs as the `artificialflowadmin` principal `local-disabled-auth` in tenant `default`. Any `Authorization` header is ignored. |

Never expose a `false` deployment to untrusted networks: anyone who can reach
the gateway has admin access. The rest of this section applies to `true`.

```http
Authorization: Bearer <access-token>
```

### Machine / integration identity

Integrations must use a non-human identity with **only** the role
`artificialflowclient`.

**External OIDC (client credentials)**

```bash
TOKEN=$(curl -sS -X POST "$TOKEN_URL" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=client_credentials" \
  -d "client_id=$CLIENT_ID" \
  -d "client_secret=$CLIENT_SECRET" \
  -d "audience=workflow-backend" \
  | jq -r .access_token)
```

Audience / claim paths must match the deployment. Provider recipes:
[Keycloak](iam/KEYCLOAK.md), [Entra](iam/ENTRA.md), [Auth0](iam/AUTH0.md).

### Verify the token

```bash
curl -sS http://localhost:9100/api/identity/me \
  -H "Authorization: Bearer $TOKEN"
```

```json
{
  "authenticated": true,
  "principal": {
    "subject": "…",
    "roles": ["artificialflowclient"],
    "email": "…",
    "name": "…"
  }
}
```

### Roles

| Role | Use for API integrations |
| :--- | :--- |
| `artificialflowclient` | **Default for workers, automation and task apps.** Start instances, variables, jobs, messages/signals/escalations, decision evaluation, user task search/claim/complete, query reads. |
| `artificialflowbusiness` | Deploy/list workflows and decisions (modeler). **Cannot** start instances, read instances or edit variables. |
| `artificialflowadmin` | Full access, including deletes, incidents, job retry, migrations and metrics. **Do not** put admin on a long-lived SDK/worker client. |
| `artificialflowanalytics` | Reserved; no API access yet. |

Authorization is role-only; token scopes are not checked.

---

## Common conventions

### Headers

| Header | When |
| :--- | :--- |
| `Authorization: Bearer …` | All protected routes when `IAM_DEPLOYMENT_MODE=true`; omit when `false` |
| `Content-Type: application/json` | JSON bodies |
| `Content-Type: application/xml` | BPMN deploy (`POST /workflows`) |
| `X-Correlation-ID` | Optional request tracing |
| `Idempotency-Key` | Job mutations (`complete` / `fail` / `extend-lock`), max 128 chars |
| `X-Workflow-Worker-Protocol-Version` | Worker routes; send `v1` |

Worker responses include `X-Workflow-Engine-Protocol-Version: v1`.

### Errors

Versioned `/api/v1` errors use RFC 9457 Problem Details with
`Content-Type: application/problem+json`, including stable `status`, `detail`,
`instance`, and `correlationId` fields. Unversioned `/api` routes retain their
plain-text `http.Error` bodies throughout the 1.x compatibility line.

| Status | Typical meaning |
| ---: | :--- |
| `400` | Validation / bad id / unsupported protocol version |
| `401` | Missing or invalid token (`IAM_DEPLOYMENT_MODE=true` only) |
| `403` | Authenticated but wrong role (`IAM_DEPLOYMENT_MODE=true` only) |
| `404` | Unknown workflow / instance / job |
| `409` | Conflict (e.g. task already claimed, or completed by a different assignee) |
| `413` | BPMN body too large (limit ~20 MB) |
| `500` | Server error |

Treat non-2xx as failure. Versioned clients should branch on `status` and stable
problem fields; compatibility clients may continue reading the plain-text body.

### Command vs query

| Concern | Use |
| :--- | :--- |
| Deploy, start, cancel, jobs, user tasks, messages | **Command** `/api/...` |
| Dashboard search, paginated instance lists | **Query** `/api/query/...` |

Query is **eventually consistent** (CQRS projection). After a write, poll query
or read the command instance until the projection catches up. See
[RUNBOOK_CQRS_SYNC.md](RUNBOOK_CQRS_SYNC.md).

### IDs

Most resource keys are returned as **JSON strings** even when numeric
internally (`"id": "1234567890"`). Path parameters accept those string forms.
Query instance ids and `workflowId` filters must be **numeric** strings.

---

## Quickstart (end-to-end)

Replace `$API` with `http://localhost:9100/api/v1` and set `$TOKEN`. With
`IAM_DEPLOYMENT_MODE=false`, drop the `Authorization` headers.

```bash
export API=http://localhost:9100/api/v1
export TOKEN=…

# 1. Verify identity
curl -sS "$API/identity/me" -H "Authorization: Bearer $TOKEN"

# 2. Deploy BPMN
curl -sS -X POST "$API/workflows" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/xml" \
  --data-binary @process.bpmn

# 3. Start an instance (BPMN process id → latest version)
curl -sS -X POST "$API/instances" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "workflow_id": "order_approval",
    "context": { "orderId": "ord-1", "amount": 1500 }
  }'

# 4. Activate and complete an external job
curl -sS -X POST "$API/jobs/activate" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Workflow-Worker-Protocol-Version: v1" \
  -d '{
    "type": "payment",
    "worker": "payments-1",
    "maxJobs": 5,
    "timeoutMs": 5000,
    "lockDurationMs": 30000
  }'

curl -sS -X POST "$API/jobs/$JOB_KEY/complete" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Workflow-Worker-Protocol-Version: v1" \
  -H "Idempotency-Key: complete-$JOB_KEY-1" \
  -d '{ "worker": "payments-1", "variables": { "paid": true } }'

# 5. Search projected instances
curl -sS "$API/query/instances?state=RUNNING&page=1&pageSize=20" \
  -H "Authorization: Bearer $TOKEN"
```

A scripted bake-off that exercises deploy → start → worker → user task lives at
[`examples/golden-demo/run-bakeoff.mjs`](../examples/golden-demo/run-bakeoff.mjs).

---

## Command API reference

Base: `{gateway}/api`

Roles: **A** = admin, **B** = business, **C** = client.

### Identity

#### `GET /identity/me`

Any role. Returns `{ "authenticated": true, "principal": { … } }` for the
caller. With `IAM_DEPLOYMENT_MODE=true` a missing or invalid token gets `401`;
with `false` the principal is always `local-disabled-auth` with
`artificialflowadmin`.

#### `GET /identity/config`

Any role. Returns the deployment's IAM settings so a BFF or console can tell how
the stack is wired. Secrets are never returned.

```json
{
  "deployment_mode": true,
  "provider_name": "External OIDC Provider",
  "auth_enabled": true,
  "frontend_auth_enabled": true,
  "frontend_oidc_authority": "https://login.example.com/realms/acme",
  "frontend_oidc_client_id": "workflow-frontend",
  "token_validation_mode": "jwt",
  "internal_issuer_url": "https://login.example.com/realms/acme",
  "external_issuer_url": "https://login.example.com/realms/acme",
  "client_id": "workflow-backend",
  "enforce_audience": true,
  "claim_roles_path": "roles,realm_access.roles,groups",
  "standard_roles": ["artificialflowadmin", "artificialflowbusiness", "artificialflowclient", "artificialflowanalytics"]
}
```

`deployment_mode` is a boolean: `true` means an external OIDC provider, `false`
means no IAM provider (`provider_name` defaults to `None` and the issuer and
frontend fields are empty). Override the display name with `IAM_PROVIDER_NAME`. Users, SDK clients, and API tokens are created in the
external OIDC provider; ArtificialFlow has no user-management API.

#### `POST /identity/login`

Public. Console sign-in when `IAM_DEPLOYMENT_MODE=false`. Checks the body
against `CONSOLE_ADMIN_USERNAME` / `CONSOLE_ADMIN_PASSWORD` (default
`admin` / `admin`) and issues **no token**, because the API is already
unauthenticated in that mode.

```json
{ "username": "admin", "password": "admin" }
```

| Status | Meaning |
| ---: | :--- |
| `200` | `{ "authenticated": true, "principal": { "subject": "admin", "roles": ["artificialflowadmin"], … } }` |
| `400` | Malformed body or unknown fields |
| `401` | Wrong username or password |
| `404` | `IAM_DEPLOYMENT_MODE=true`: sign in through the OIDC provider instead (without a token the auth check answers `401` first) |

Integrations never need this endpoint.

---

### Workflows (process definitions)

#### `POST /workflows` — deploy BPMN

| | |
| :--- | :--- |
| Roles | A \| B \| C |
| Body | Raw BPMN 2.0 XML |
| Content-Type | `application/xml` |
| Max size | ~20 MB |

```bash
curl -sS -X POST "$API/workflows" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/xml" \
  --data-binary @order-approval.bpmn
```

**Response** `200` — `WorkflowDefinitionResponse`:

```json
{
  "id": "123",
  "process_definition_id": "order_approval",
  "name": "Order approval",
  "version": 1,
  "resource_name": "…",
  "deployment_id": "…",
  "tenant_id": "",
  "resource_checksum": "…",
  "bpmn_xml": "…",
  "created_at": "2026-07-30T12:00:00Z",
  "steps": []
}
```

Camunda/Zeebe attributes are **not** executed natively. Lint/remap with
[EXTERNAL_BPMN_IMPORT.md](EXTERNAL_BPMN_IMPORT.md) / the Camunda import guide
before claiming compatibility.

#### `GET /workflows`

Roles: A \| B \| C. Returns an array of workflow definitions.

#### `GET /workflows/{id}`

Roles: A \| B \| C. `{id}` may be the numeric definition key or BPMN process id.

#### `DELETE /workflows/{id}`

Roles: **A** only. `204` on success.

---

### Instances

#### `POST /instances` — start

Roles: A \| C.

```json
{
  "workflow_id": "order_approval",
  "version": 2,
  "context": { "orderId": "ord-1" }
}
```

| Field | Required | Notes |
| :--- | :--- | :--- |
| `workflow_id` | yes | Numeric definition key **or** BPMN process id string |
| `version` | no | When `workflow_id` is a BPMN process id, pin that version; ignored for definition keys |
| `context` | no | Initial process variables (`map[string]any`) |

See [PROCESS_VERSIONING.md](PROCESS_VERSIONING.md).

**Response** `200` — `WorkflowInstanceResponse`:

```json
{
  "id": "456",
  "workflow_id": "123",
  "status": "ACTIVE",
  "context": { "orderId": "ord-1" },
  "created_at": "…",
  "updated_at": "…",
  "executions": [
    {
      "id": "…",
      "step_id": "…",
      "status": "…",
      "start_time": "…",
      "task": {
        "key": "…",
        "elementId": "ApproveTask",
        "executionId": "…",
        "state": "CREATED",
        "canClaim": true,
        "canComplete": false,
        "createdAt": "…",
        "updatedAt": "…"
      }
    }
  ]
}
```

#### `GET /instances`

Roles: A \| C. Active instances from the
command store (authoritative, not the search projection).

#### `GET /instances/history/completed?limit=`

Roles: A \| C. Default/capped limit 100.

#### `GET /instances/{id}`

Roles: A \| C. Full instance + executions + task metadata.

#### `DELETE /instances/{id}`

Roles: **A**. Cancels/deletes. `204`.

#### `POST /instances/{id}/variables`

Roles: A \| C.

```json
{ "variables": { "approved": true, "note": "ok" } }
```

#### `POST /instances/{id}/complete`

Roles: A \| C. Legacy step completion by `step_id` — **not** the user task
claim/complete path.

```json
{ "step_id": "Activity_1" }
```

#### `GET /instances/{id}/tasks?includeCompleted=`

Roles: A \| C. User tasks of one instance; see [User tasks API](#user-tasks-api).

```json
{ "tasks": [ /* UserTaskResponse */ ] }
```

#### `GET /instances/{id}/jobs`

Roles: A.

---

### Messages, signals, escalations

Roles: A \| C. Empty body fields are allowed; payload defaults to `{}`.

#### `POST /messages`

```json
{
  "message_name": "PaymentReceived",
  "correlation_key": "ord-1",
  "payload": { "amount": 1500 }
}
```

#### `POST /signals`

```json
{
  "signal_name": "GlobalCancel",
  "payload": {}
}
```

#### `POST /escalations`

```json
{
  "escalation_code": "NeedManager",
  "payload": {}
}
```

Successful publish typically returns an empty JSON object / no meaningful body.
Use instance state or query search to observe the effect.

---

### Decisions (DMN-lite)

JSON decision tables referenced by `artificialflow:decisionRef` on business-rule
tasks. Details: [DMN.md](DMN.md).

#### `POST /decisions`

Roles: A \| B \| C.

```json
{
  "decision_id": "invoice_decision",
  "name": "Invoice routing",
  "resource": "{\"id\":\"invoice_decision\",\"hitPolicy\":\"FIRST\",\"rules\":[…]}"
}
```

`resource` is a **string** containing the JSON decision table (or the raw JSON
text of the file).

#### `GET /decisions`

```json
{
  "decisions": [
    {
      "key": "1",
      "decision_id": "invoice_decision",
      "name": "Invoice routing",
      "version": 1,
      "created_at": "…",
      "updated_at": "…"
    }
  ]
}
```

#### `POST /decisions/{id}/evaluate`

Roles: A \| C. `{id}` is the decision id (e.g. `invoice_decision`).

```json
{ "inputs": { "amount": 1500 } }
```

```json
{ "outputs": { "result": "manager" } }
```

---

### Incidents, retry, metrics

| Method | Path | Roles | Notes |
| :--- | :--- | :--- | :--- |
| `GET` | `/incidents?processInstanceKey=&limit=` | A | Default limit 100 |
| `POST` | `/incidents/{key}/resolve` | A | Returns the resolved `IncidentResponse`; audited |
| `POST` | `/jobs/{key}/retry` | A | Optional body `{ "retries": 3 }`; audited |
| `GET` | `/internal/metrics` | A | Outbox + idempotency counters. The gateway answers `404`; call the command service directly (port `8080`) from inside the cluster or Compose network |

`IncidentResponse`:

```json
{
  "key": "…",
  "id": "…",
  "processInstanceKey": "…",
  "elementInstanceKey": "…",
  "jobKey": "…",
  "errorType": "…",
  "errorMessage": "…",
  "state": "…",
  "createdAt": "…",
  "resolvedAt": null
}
```

---

## Worker / jobs API

External service tasks become lockable jobs. This is the primary integration
surface for workers in **any** language.

Protocol version: **`v1`**.

Also see the short [worker-api.md](worker-api.md) and
[WORKER_CONFORMANCE.md](WORKER_CONFORMANCE.md).

### `GET /jobs/capabilities`

Roles: A \| C.

```json
{
  "protocolVersion": "v1",
  "capabilities": ["activate", "complete", "fail", "extend-lock"]
}
```

### `POST /jobs/activate`

Roles: A \| C.

```json
{
  "type": "payment",
  "worker": "payments-1",
  "maxJobs": 5,
  "timeoutMs": 5000,
  "lockDurationMs": 30000
}
```

| Field | Meaning |
| :--- | :--- |
| `type` | Job type / topic (matches BPMN `taskType`) |
| `worker` | Stable worker name; must match on complete/fail/extend |
| `maxJobs` | Max jobs to activate in this call |
| `timeoutMs` | Long-poll / activate wait budget |
| `lockDurationMs` | How long the lock is held after activation |

**Response:**

```json
{
  "jobs": [
    {
      "key": "789",
      "type": "payment",
      "processInstanceKey": "456",
      "elementInstanceKey": "…",
      "processDefinitionKey": "123",
      "elementId": "ServiceTask_Pay",
      "worker": "payments-1",
      "retries": 3,
      "state": "ACTIVATED",
      "lockExpirationTime": "…",
      "createdAt": "…",
      "updatedAt": "…"
    }
  ]
}
```

### `POST /jobs/{key}/complete`

Roles: A \| C. Send `Idempotency-Key`.

```json
{
  "worker": "payments-1",
  "variables": { "paid": true, "txId": "…" }
}
```

### `POST /jobs/{key}/fail`

Roles: A \| C. Send `Idempotency-Key`.

```json
{
  "worker": "payments-1",
  "errorMessage": "gateway timeout",
  "retries": 2
}
```

Omit `retries` to let the engine decrement; set explicitly to override remaining
retries.

### `POST /jobs/{key}/extend-lock`

Roles: A \| C. Send `Idempotency-Key`. Use while long handlers still run.

```json
{
  "worker": "payments-1",
  "lockDurationMs": 30000
}
```

### Idempotency rules

- Scope is operation + job key, e.g. `jobs.complete:{jobKey}`.
- Replay of the same key for the same scope returns a safe `200`.
- Use **distinct** keys for complete vs fail vs extend-lock, even on the same job.
- Recommended key shape: `{operation}-{jobKey}-{attempt}` (≤ 128 chars).

### Minimal worker loop (any language)

1. `GET /jobs/capabilities` once at startup (optional).
2. Loop: `POST /jobs/activate` with your job `type`.
3. For each job: run business logic.
4. On success: `POST /jobs/{key}/complete` with `Idempotency-Key`.
5. On failure: `POST /jobs/{key}/fail` with `Idempotency-Key`.
6. If work may exceed the lock: periodically `extend-lock`.

Official connectors (HTTP, Kafka, email, …) are themselves workers against this
same API — see [`connectors/README.md`](../connectors/README.md).

gRPC `JobWorkerService` exists on the command process (`GRPC_ADDR`, default
`:50051`) but is **not** published through the nginx gateway. Prefer HTTP
`/jobs/*` unless you control pod networking.

---

## User tasks API

ArtificialFlow does not authenticate the end users of your applications. Your
application signs them in with its own IAM and validates their tokens. Its
backend then calls ArtificialFlow with an `artificialflowclient` token and
passes the end user's identity as plain values: filters on search, `assignee`
on claim and complete. Never expose the client token to the browser.

### Routes

| Method | Path | Roles | Notes |
| :--- | :--- | :--- | :--- |
| `GET` | `/tasks` | A \| C | Search tasks across instances |
| `GET` | `/instances/{id}/tasks?includeCompleted=` | A \| C | Tasks of one instance |
| `POST` | `/instances/{id}/tasks/{executionId}/claim` | A \| C | Body `{ "assignee": "…" }` → `UserTaskResponse` |
| `POST` | `/instances/{id}/tasks/{executionId}/complete` | A \| C | Body `{ "assignee": "…" }` → `204` |

### `GET /tasks`

| Query param | Notes |
| :--- | :--- |
| `assignee` | Tasks assigned to or claimed by this user |
| `candidateUser` | Tasks this user may act on: assignee, claim owner or candidate user |
| `candidateGroup` | Repeatable or comma-separated; tasks offered to any of these groups |
| `includeUnassigned` | `true` also returns open tasks with no assignee, candidates or claim |
| `processInstanceKey` | Restrict to one instance |
| `state` | `active` (default), `completed`, `all` |
| `limit` | 1–500, default 100 |

`assignee`, `candidateUser` and `candidateGroup` are OR-combined. With none of
them, every task in the tenant matches. Matching is case-insensitive.

### Claim and complete

Client tokens must send `assignee`. Admin tokens may omit it to act as
themselves (email, then subject, then name) and may reassign a claimed task.
Completing requires the same assignee that claimed the task unless the caller
is an admin. Unknown body fields are rejected with `400`; an unknown task
returns `404`; a task already claimed by someone else, or not yet claimed,
returns `409`.

### Example

```bash
curl -sS "$API/tasks?candidateUser=approver@example.com&candidateGroup=approvers&includeUnassigned=true" \
  -H "Authorization: Bearer $CLIENT_TOKEN"

curl -sS -X POST \
  "$API/instances/$INSTANCE_ID/tasks/$EXECUTION_ID/claim" \
  -H "Authorization: Bearer $CLIENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{ "assignee": "approver@example.com" }'

curl -sS -X POST \
  "$API/instances/$INSTANCE_ID/tasks/$EXECUTION_ID/complete" \
  -H "Authorization: Bearer $CLIENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{ "assignee": "approver@example.com" }'
```

`UserTaskResponse` fields: `key`, `processInstanceKey`, `processDefinitionKey`,
`elementId`, `executionId`, `state`, `assignee`, `candidateUsers`,
`candidateGroups`, `claimedBy`, `canClaim`, `canComplete`, `dueDate`,
`createdAt`, `updatedAt`.

---

## Query API reference

Base: `{gateway}/api/query`

Eventually consistent search / dashboard reads.

### `GET /query/instances`

Roles: A \| C.

| Query param | Default | Notes |
| :--- | :--- | :--- |
| `workflowId` | — | Numeric definition key only |
| `state` | — | `RUNNING` (mapped to `ACTIVE`), `COMPLETED`, `FAILED`, … |
| `page` | `1` | |
| `pageSize` | `20` | |

```json
{
  "instances": [
    {
      "id": "456",
      "workflow_id": "123",
      "status": "RUNNING",
      "context": {},
      "created_at": "…",
      "updated_at": "…"
    }
  ],
  "total": 1
}
```

Projected instances omit the rich `executions` / task tree. Use command
`GET /instances/{id}` or `GET /tasks` for task detail.

### `GET /query/instances/{id}`

Roles: A \| C. `{id}` must be a numeric int64 string.

### `GET /query/workflows`

Roles: A \| B \| C. Paginated (`page`, `pageSize`). Omits `bpmn_xml` / `steps`.

```json
{
  "workflows": [
    {
      "id": "123",
      "process_definition_id": "order_approval",
      "name": "order_approval",
      "version": 1,
      "resource_name": "…",
      "deployment_id": "…",
      "tenant_id": "",
      "resource_checksum": "…",
      "created_at": "…"
    }
  ],
  "total": 1
}
```

---

## Integration patterns

### 1. Language-agnostic process client

```
Obtain machine token (OAuth client credentials; skip when IAM_DEPLOYMENT_MODE=false)
        │
        ▼
POST /workflows  (optional CI deploy)
        │
        ▼
POST /instances  { workflow_id, context }
        │
        ├─► POST /messages | /signals | /escalations   (correlation)
        │
        └─► GET /query/instances                       (status dashboards)
```

### 2. External worker (any language)

Implement the [worker loop](#minimal-worker-loop-any-language) against
`/jobs/*`. Job types must match BPMN `artificialflow:taskType` (or the engine’s
default connector types such as `io.artificialflow.connector.http`).

### 3. Human-task BFF

```
Browser ──your IAM──► Your backend ──Bearer client token──► GET /tasks, claim, complete
                          │
                          └── end user's id/groups from your own validated session
```

Never ship the machine token or client secret to the browser.

### 4. Decision tables from CI

Deploy JSON tables with `POST /decisions`, then reference
`artificialflow:decisionRef` from business-rule tasks in BPMN.

### 5. When an SDK is still better

Use the [Node.js](sdk-nodejs.md), [.NET](sdk-dotnet.md), or
[Java](sdk-java.md) SDK when you want built-in token refresh, worker loop
helpers, and typed methods without reimplementing
them. The SDK base URL is the same gateway `…/api`.

---

## Production integration checklist

### Identity and secrets

- Use a dedicated machine identity with only `artificialflowclient`.
- Keep client secrets in a secret manager.
- Obtain short-lived tokens server-side; never store a machine token in browser
  JavaScript or mobile application storage.
- Validate issuer and audience in production. Do not enable insecure issuer
  settings outside local development.
- Rotate credentials and test revocation before go-live.

### HTTP client behavior

- Set connect, request, and total-operation timeouts explicitly.
- Reuse connections and enable TLS certificate validation.
- Send `X-Correlation-ID` and record it with your own trace/request id.
- Retry only transient network failures, `429`, and selected `5xx` responses
  with exponential backoff and jitter.
- Do not blindly retry non-idempotent workflow starts or message publication.
  Persist a business operation id in process variables/correlation data and
  reconcile before resubmitting.
- For job complete/fail/extend-lock, reuse the same `Idempotency-Key` only when
  replaying the same operation and payload.

### Worker reliability

- Use a stable worker name per logical worker pool.
- Set lock duration above normal execution time and extend it before expiry.
- Bound `maxJobs` by worker concurrency; activation is not a concurrency
  controller.
- Treat lock ownership errors as terminal for that activation and do not commit
  external side effects twice.
- Make business-side effects idempotent using the job key or a domain operation
  key.
- Implement graceful shutdown: stop activation, finish or fail owned work, then
  exit before locks expire.

### CQRS reads

- Read command `GET /instances/{id}` when immediate post-write consistency is
  required.
- Poll Query endpoints with a bounded timeout when waiting for projection
  visibility; do not assume read-after-write consistency.
- Paginate Query results and avoid treating page totals as transactionally
  current.
- Monitor sync-worker health, projection lag, and parity operational checks.

### Compatibility and rollout

- Pin the ArtificialFlow minor line and review release notes before upgrading.
- Validate `GET /jobs/capabilities` and protocol version `v1` at worker startup.
- Diff [`docs/openapi.yaml`](openapi.yaml) in CI and regenerate clients only
  from reviewed contract changes.
- Run contract tests against a staging gateway using production IAM claim
  mappings.
- Keep a rollback path for deployment manifests and generated clients.

---

## Endpoint catalog (cheat sheet)

### Command (`/api`)

| Method | Path | Roles |
| :--- | :--- | :--- |
| `GET` | `/health` | public |
| `GET` | `/identity/me` | any role |
| `GET` | `/identity/config` | any role |
| `POST` | `/identity/login` | public; `IAM_DEPLOYMENT_MODE=false` only |
| `POST` | `/workflows` | A\|B\|C |
| `GET` | `/workflows` | A\|B\|C |
| `GET` | `/workflows/{id}` | A\|B\|C |
| `DELETE` | `/workflows/{id}` | A |
| `POST` | `/decisions` | A\|B\|C |
| `GET` | `/decisions` | A\|B\|C |
| `POST` | `/decisions/{id}/evaluate` | A\|C |
| `POST` | `/instances` | A\|C |
| `GET` | `/instances` | A\|C |
| `GET` | `/instances/history/completed` | A\|C |
| `GET` | `/instances/{id}` | A\|C |
| `DELETE` | `/instances/{id}` | A |
| `POST` | `/instances/{id}/migration/dry-run` | A |
| `POST` | `/instances/{id}/migration` | A |
| `POST` | `/instances/{id}/variables` | A\|C |
| `POST` | `/instances/{id}/complete` | A\|C |
| `GET` | `/tasks` | A\|C |
| `GET` | `/instances/{id}/tasks` | A\|C |
| `POST` | `/instances/{id}/tasks/{executionId}/claim` | A\|C |
| `POST` | `/instances/{id}/tasks/{executionId}/complete` | A\|C |
| `GET` | `/instances/{id}/jobs` | A |
| `GET` | `/incidents` | A |
| `POST` | `/incidents/{key}/resolve` | A |
| `POST` | `/jobs/{key}/retry` | A |
| `POST` | `/signals` | A\|C |
| `POST` | `/messages` | A\|C |
| `POST` | `/escalations` | A\|C |
| `GET` | `/jobs/capabilities` | A\|C |
| `POST` | `/jobs/activate` | A\|C |
| `POST` | `/jobs/{key}/complete` | A\|C |
| `POST` | `/jobs/{key}/fail` | A\|C |
| `POST` | `/jobs/{key}/extend-lock` | A\|C |
| `GET` | `/internal/metrics` | A (not routed by the gateway) |

Roles assume `IAM_DEPLOYMENT_MODE=true`; with `false` every call runs as admin.
Each path is also served under `/api/v1`.

### Query (`/api/query`)

| Method | Path | Roles |
| :--- | :--- | :--- |
| `GET` | `/health` | public |
| `GET` | `/identity/me` | any role |
| `GET` | `/instances` | A\|C |
| `GET` | `/instances/{id}` | A\|C |
| `GET` | `/workflows` | A\|B\|C |

---

## Related documentation

| Doc | Topic |
| :--- | :--- |
| [iam.md](iam.md) | IAM modes, roles, and external OIDC |
| [sdk-nodejs.md](sdk-nodejs.md) | Official Node.js client |
| [sdk-dotnet.md](sdk-dotnet.md) | Official .NET client |
| [sdk-java.md](sdk-java.md) | Official Java client |
| [sdk-python.md](sdk-python.md) | Python worker client |
| [sdk-go.md](sdk-go.md) | Go worker client |
| [worker-api.md](worker-api.md) | Worker protocol summary |
| [DMN.md](DMN.md) | Decision tables |
| [PROCESS_VERSIONING.md](PROCESS_VERSIONING.md) | Start by id / version |
| [BPMN_SUPPORT_MATRIX.md](BPMN_SUPPORT_MATRIX.md) | Executable BPMN surface |
| [COMPATIBILITY_MATRIX.md](COMPATIBILITY_MATRIX.md) | Compatibility guarantees |
| [architecture.md](architecture.md) | CQRS / gateway model |

Authoritative Go DTOs:
`backend/services/workflow-command/internal/interfaces/http/dto/dto.go` and
query mappers under
`backend/services/workflow-query/internal/interfaces/http/`.
