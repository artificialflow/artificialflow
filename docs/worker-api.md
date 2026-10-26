# Worker API

Workers use the command service job APIs to activate, complete, fail, and extend
locks for external jobs.

For the full HTTP contract (headers, request/response JSON, idempotency rules,
and a language-agnostic worker loop), see **[API.md — Worker / jobs API](API.md#worker--jobs-api)**.

## Authentication

With `IAM_DEPLOYMENT_MODE=true`, workers send `Authorization: Bearer <token>`
from a machine client that has only `artificialflowclient`. With `false` (no IAM
provider) no token is needed. See [API.md — Authentication](API.md#authentication).

## Compatibility

- Protocol negotiation request header: `X-Workflow-Worker-Protocol-Version` (`v1`).
- Protocol negotiation response header: `X-Workflow-Engine-Protocol-Version`.
- Capabilities endpoint: `GET /jobs/capabilities`.

## Idempotency

Mutation calls should include an `Idempotency-Key` header:

- Complete job (`POST /jobs/{key}/complete`).
- Fail job (`POST /jobs/{key}/fail`).
- Extend lock (`POST /jobs/{key}/extend-lock`).

Retries with the same key should be replay-safe when the original request was
accepted. Scope is operation + job key (for example `jobs.complete:{jobKey}`).

## Official clients

| Language | Client |
| :--- | :--- |
| Any (raw HTTP) | [API.md](API.md#worker--jobs-api) |
| Node.js | [`@artificialflow/nodejs-sdk`](sdk-nodejs.md) `createWorker` |
| .NET | [`ArtificialFlow.Sdk`](sdk-dotnet.md) `JobWorker` |
| Java | [`io.artificialflow:artificialflow-sdk`](sdk-java.md) `JobWorker` |
| Python | [`artificialflow-worker`](sdk-python.md) `Worker` |
| Go | [`backend/libs/worker`](sdk-go.md) `NewWorker` |

## Conformance

Run:

```bash
make worker-conformance
```

Wire changes to `/jobs/*` must keep this suite green. See
[WORKER_CONFORMANCE.md](WORKER_CONFORMANCE.md).
