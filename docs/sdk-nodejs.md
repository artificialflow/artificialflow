# Node.js SDK

Package: `@artificialflow/nodejs-sdk` (TypeScript, Node.js 20+).

The SDK is a thin client over the gateway HTTP contract described in
[API.md](API.md). Anything it does can also be done with plain HTTP. The
[.NET](sdk-dotnet.md) and [Java](sdk-java.md) SDKs expose the same operations
with the same names.

## Install

```bash
npm install @artificialflow/nodejs-sdk
```

From the repository:

```bash
cd clients/nodejs-sdk
npm ci
npm run build
npm test
npm run validate:package
```

## Create a client

```ts
import { ArtificialFlowClient } from '@artificialflow/nodejs-sdk';

const client = new ArtificialFlowClient({
  baseUrl: 'https://app.example.com/api/v1',
});
```

| Option | Default | Notes |
| :--- | :--- | :--- |
| `baseUrl` | `http://localhost:9100/api/v1` | Gateway command API. `/api` (unversioned) also works. |
| `queryBaseUrl` | `${baseUrl}/query` | Set only when the query service has a different address. |
| `token` | — | Static token string or `() => string \| Promise<string>` callback. |
| `auth` | — | Built-in OAuth client-credentials provider. |
| `headers` | — | Extra headers (object or async callback) on every request. |
| `timeoutMs` | — | Per-request timeout. |

Configure at most one of `token` and `auth`; the SDK rejects both together.

## Authentication

What to configure depends on the deployment's `IAM_DEPLOYMENT_MODE`
(see [iam.md](iam.md)).

### `IAM_DEPLOYMENT_MODE=false` (no IAM provider)

Configure neither `token` nor `auth`. The SDK sends no `Authorization` header
and every call runs as `artificialflowadmin`.

```ts
const client = new ArtificialFlowClient({ baseUrl: 'http://localhost:9100/api/v1' });
```

### `IAM_DEPLOYMENT_MODE=true` (external OIDC provider)

In the provider:

1. Create a confidential machine-to-machine application with the OAuth 2.0
   client-credentials grant.
2. Assign only `artificialflowclient`, mapped into the claim configured by
   `AUTH_CLAIM_ROLES_PATH`.
3. Include the ArtificialFlow backend audience (`AUTH_CLIENT_ID`, normally
   `workflow-backend`).
4. Keep access tokens short-lived and rotate the secret.

**SDK-managed client credentials** (cached and refreshed before expiry):

```ts
const client = new ArtificialFlowClient({
  baseUrl: 'https://app.example.com/api/v1',
  auth: {
    type: 'oauth-client-credentials',
    profile: {
      type: 'oauth-client-credentials',
      tokenUrl: 'https://<your-oidc-provider>/oauth2/token',
      clientId: process.env.ARTIFICIALFLOW_CLIENT_ID!,
      clientSecret: process.env.ARTIFICIALFLOW_CLIENT_SECRET!,
      // scopes: ['...'],  // only when the provider requires them
    },
    refreshSkewMs: 30_000,
  },
});
```

The built-in provider sends `client_id` and `client_secret` in the form body
and expects a Bearer `access_token` with numeric `expires_in`. Token URLs must
be HTTPS except loopback addresses. For Keycloak the token URL is
`https://<host>/realms/<realm>/protocol/openid-connect/token`.

**Provider library callback**, when the provider needs HTTP Basic client
authentication or extra `audience`/`resource` parameters:

```ts
const client = new ArtificialFlowClient({
  baseUrl: 'https://app.example.com/api/v1',
  token: () => getProviderAccessToken(), // raw token, no "Bearer " prefix
});
```

**Static token**, for short smoke tests only:

```ts
const client = new ArtificialFlowClient({ token: process.env.ARTIFICIALFLOW_TOKEN });
```

`client.setToken(value)` replaces any configured provider with that raw token
for later requests.

## Operations

Method names match the OpenAPI `operationId`s.

| Area | Methods |
| :--- | :--- |
| Health and identity | `commandHealth`, `queryHealth`, `getIdentity`, `getQueryIdentity`, `getIdentityConfig` |
| Workflows | `deployWorkflow`, `listWorkflows`, `searchWorkflows`, `getWorkflow`, `deleteWorkflow` |
| Decisions | `deployDecision`, `listDecisions`, `evaluateDecision` |
| Instances | `startInstance`, `listInstances`, `listCompletedInstances`, `getInstance`, `deleteInstance`, `updateInstanceVariables`, `completeInstanceStep` |
| Instance migration (admin) | `dryRunInstanceMigration`, `migrateInstance` |
| Query (projected, eventually consistent) | `searchInstances`, `getProjectedInstance`, `searchWorkflows` |
| Events | `publishMessage`, `publishSignal`, `publishEscalation` |
| User tasks | `searchTasks`, `listInstanceTasks`, `claimUserTask`, `completeUserTask` |
| Jobs | `getJobCapabilities`, `activateJobs`, `completeJob`, `failJob`, `extendJobLock`, `createWorker` |
| Operations (admin) | `listInstanceJobs`, `retryJob`, `listIncidents`, `resolveIncident`, `getEngineMetrics` |

`getEngineMetrics` calls `/internal/metrics`, which the public gateway does not
route; point the client at the command service directly to use it.

Every method accepts a trailing `RequestOptions` with `correlationId`,
`idempotencyKey`, `headers`, and an `AbortSignal`.

### Deploy and start

```ts
import { readFile } from 'node:fs/promises';

await client.deployWorkflow(await readFile('order-process.bpmn', 'utf8'));
const instance = await client.startInstance('order-process', { orderId: '123' });
```

`startInstance` accepts a definition key or a BPMN process id (which starts the
latest version). To pin a version of a process id, call `POST /instances` with
`version` over HTTP; see [PROCESS_VERSIONING.md](PROCESS_VERSIONING.md).

### Publish events

```ts
await client.publishMessage('MsgOrderPlaced', 'order-123', { amount: 100 });
await client.publishSignal('OrderApproved', { orderId: '123' });
await client.publishEscalation('NEED_HELP', { reason: 'timeout' });
```

Escalations correlate to escalation catch/boundary events, escalation start
events, and matching event sub-processes. User-task due-date breaches publish
`user-task.due-date.breached` from the runtime scheduler.

### Decisions

```ts
await client.deployDecision('invoice_decision', 'Invoice routing', JSON.stringify(table));
const outputs = await client.evaluateDecision('invoice_decision', { amount: 1500 });
```

### Bounded instance migration (admin)

```ts
const migration = {
  sourceDefinitionKey: '101',
  targetDefinitionKey: '202',
  elementMapping: { review: 'review_v2' },
  operationId: crypto.randomUUID(),
};
await client.dryRunInstanceMigration('9001', migration);
await client.migrateInstance('9001', migration);
```

Rejected requests return the stable migration `reasonCode`. See
[INSTANCE_MIGRATION.md](INSTANCE_MIGRATION.md).

## User-task applications

ArtificialFlow does not authenticate the end users of your application. Sign
them in with your own IAM and validate their tokens in your backend. Keep the
`artificialflowclient` credential in that backend and pass the end user's
identity as plain values:

```ts
const tasks = await client.searchTasks({
  candidateUser: signedInUser.email,
  candidateGroups: signedInUser.groups,
  includeUnassigned: true,
});

const task = tasks[0];
await client.claimUserTask(task.processInstanceKey, task.executionId, signedInUser.email);
await client.completeUserTask(task.processInstanceKey, task.executionId, signedInUser.email);
```

`assignee`, `candidateUser`, and `candidateGroups` are OR-combined; `state` is
`active` (default), `completed`, or `all`. Client tokens must pass the assignee
on claim and complete, and completing requires the assignee that claimed the
task. See [API.md](API.md#user-tasks-api).

## Job workers

```ts
const worker = client.createWorker('payment-service', async (job, context) => {
  return { paymentStatus: 'success' };          // completes the job with these variables
}, {
  workerName: 'payment-worker',
  maxJobs: 32,
  pollIntervalMs: 1000,
  timeoutMs: 5000,
  lockDurationMs: 30_000,
  autoStart: true,
});

process.on('SIGINT', () => worker.stop());
```

A thrown handler fails the job; `failRetries(job, error)` can override the
remaining retries and `onError` receives poll-loop errors. Inside the handler,
`context.complete`, `context.fail`, and `context.extendLock` give manual
control. Job mutations send `Idempotency-Key` and the worker protocol header
`X-Workflow-Worker-Protocol-Version: v1`.

## Errors

Non-2xx responses throw `ArtificialFlowApiError` with `status`, `body`,
`method`, `path`, `correlationId`, and, on `/api/v1`, the parsed RFC 9457
`problem`. Token acquisition failures throw `OAuthClientCredentialsAuthError`.

- `401`: token missing, expired, or wrong issuer/audience (`IAM_DEPLOYMENT_MODE=true`).
- `403`: valid token without a permitted role.
- `409`: user task already claimed by someone else, or completed by a different assignee.

## Smoke test

`examples/sdk-smoke-test.js` checks a running deployment. It reads
`ARTIFICIALFLOW_BASE_URL` (default `http://localhost:9100/api`) and at most one
credential: `ARTIFICIALFLOW_TOKEN` (raw token, no `Bearer `), or
`OIDC_TOKEN_URL` + `OIDC_CLIENT_ID` + `OIDC_CLIENT_SECRET`. Set none with
`IAM_DEPLOYMENT_MODE=false`.

```bash
cd clients/nodejs-sdk && npm ci && npm run build

ARTIFICIALFLOW_BASE_URL=http://localhost:9100/api \
ARTIFICIALFLOW_WORKFLOW_KEY=order-process \
ARTIFICIALFLOW_BUSINESS_KEY=order-123 \
ARTIFICIALFLOW_MESSAGE_NAME=MsgOrderPlaced \
ARTIFICIALFLOW_MESSAGE_CORRELATION_KEY=order-123 \
ARTIFICIALFLOW_WORKER_JOB_TYPE=payment-service \
  node examples/sdk-smoke-test.js
```

The optional `ARTIFICIALFLOW_*` inputs start an instance, publish a message,
and activate and complete one job. The test passes when it reads the principal,
lists workflows, and finishes every configured step.

## Operational requirements

- Store OIDC client secrets in a secret manager; never ship them or tokens to
  browser code.
- Keep machine identities limited to `artificialflowclient` and use separate
  credentials per application or worker.
- Rotate credentials with overlap and test the replacement before revocation.
- Do not log tokens or client secrets.
- Stop workers cleanly on shutdown so locked jobs can be recovered.

## Publishing

Releases publish from a signed tag through GitHub Actions with
`npm publish --provenance`. Prereleases such as `1.3.0-rc.1` use the npm
dist-tag `next`; stable versions use `latest`. Manual workflow dispatches only
validate and dry-run the package unless maintainers enable publishing.
