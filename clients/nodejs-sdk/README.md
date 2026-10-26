# ArtificialFlow Node.js SDK

TypeScript SDK for the ArtificialFlow gateway API.

## Build

```bash
npm install
npm run build
```

Install the public package with `npm install @artificialflow/nodejs-sdk`.

## Client

The SDK calls ArtificialFlow through the gateway API. With `IAM_DEPLOYMENT_MODE=true` it needs an access token with the `artificialflowclient` role.

Users, clients, and tokens live in your external OIDC provider. ArtificialFlow does not create or manage SDK credentials.

When ArtificialFlow runs with `IAM_DEPLOYMENT_MODE=false` (no IAM provider), configure no
credentials at all: the SDK sends no `Authorization` header and the API
accepts the call.

```ts
const client = new ArtificialFlowClient({ baseUrl: 'http://localhost:9100/api' });
```

Applications that show user tasks to their own end users keep that `artificialflowclient` token on their backend. The application signs end users in with its own IAM and validates their tokens itself; ArtificialFlow never sees end-user tokens. It only receives the end user's identity as plain filter values (see [User tasks](#user-tasks)).

### Authentication

Configure ArtificialFlow against your provider:

- Create or register a backend/API resource for ArtificialFlow.
  - Default audience/client ID: `workflow-backend`
  - Match the ArtificialFlow backend setting `AUTH_CLIENT_ID`
- Configure issuer settings: `AUTH_ISSUER_INTERNAL_URL`, `AUTH_ISSUER_PUBLIC_URL`
- Use `AUTH_TOKEN_MODE=jwt` for JWT access tokens, or `AUTH_TOKEN_MODE=introspection` for opaque tokens
- If `AUTH_ENFORCE_AUDIENCE=true`, issue tokens whose `aud` contains `workflow-backend`
- Create these roles in the provider and map them into `AUTH_CLAIM_ROLES_PATH`:
  - `artificialflowadmin`: full access
  - `artificialflowbusiness`: process-design access (deploy, view, update workflows and decisions)
  - `artificialflowclient`: SDK/API clients
  - `artificialflowanalytics`: reserved for analytics
- Create a machine-to-machine / client-credentials application for the SDK
  - Grant only `artificialflowclient`
  - Prefer short-lived tokens and rotate the client secret in the provider

Standard client-credentials example with automatic token caching and refresh:

```ts
import { ArtificialFlowClient } from '@artificialflow/nodejs-sdk';

const client = new ArtificialFlowClient({
  baseUrl: 'http://localhost:9100/api/v1',
  auth: {
    type: 'oauth-client-credentials',
    profile: {
      type: 'oauth-client-credentials',
      tokenUrl: 'https://login.example.com/oauth2/token',
      clientId: process.env.ARTIFICIALFLOW_CLIENT_ID!,
      clientSecret: process.env.ARTIFICIALFLOW_CLIENT_SECRET!,
    },
  },
});
```

The built-in provider sends `client_id` and `client_secret` in the form body.
If your provider requires HTTP Basic client authentication or a custom
`audience`/`resource` parameter, use its OAuth library and supply
`token: () => getProviderAccessToken()` instead.

Static token example:

```ts
import { ArtificialFlowClient } from '@artificialflow/nodejs-sdk';

const client = new ArtificialFlowClient({
  baseUrl: 'http://localhost:9100/api/v1',
  token: process.env.ARTIFICIALFLOW_TOKEN,
});

const workflows = await client.listWorkflows({ page: 1, pageSize: 100 });
const instance = await client.startInstance('order-process', { orderId: '123' });
await client.publishMessage('MsgOrderPlaced', '123', { amount: 100 });
await client.publishSignal('OrderApproved', { orderId: '123' });
await client.publishEscalation('NEED_HELP', { reason: 'timeout' });
```

Generic client-credentials token mint:

```bash
export ARTIFICIALFLOW_TOKEN="$(
  curl -sS -X POST "$OIDC_TOKEN_URL" \
    -H 'content-type: application/x-www-form-urlencoded' \
    -d grant_type=client_credentials \
    -d client_id="$OIDC_CLIENT_ID" \
    -d client_secret="$OIDC_CLIENT_SECRET" \
  | jq -r '.access_token'
)"
```

## User tasks

Your application authenticates the end user, then asks ArtificialFlow for the
tasks that user can act on:

```ts
import { ArtificialFlowClient } from '@artificialflow/nodejs-sdk';

const client = new ArtificialFlowClient({
  baseUrl: process.env.ARTIFICIALFLOW_BASE_URL,
  token: process.env.ARTIFICIALFLOW_TOKEN,
});

const tasks = await client.searchTasks({
  candidateUser: signedInUser.email,
  candidateGroups: signedInUser.groups,
  includeUnassigned: true,
});

const task = tasks[0];
await client.claimUserTask(task.processInstanceKey, task.executionId, signedInUser.email);
await client.completeUserTask(task.processInstanceKey, task.executionId, signedInUser.email);
```

`assignee`, `candidateUser` and `candidateGroups` are OR-combined. `state` is
`active` (default), `completed` or `all`. Client tokens must pass the assignee
on claim and complete; completing requires the same assignee that claimed the
task. Do not expose the SDK token to the browser.

## Standalone smoke test

Use `examples/sdk-smoke-test.js` to verify the SDK against a running ArtificialFlow deployment.

Build the SDK first:

```bash
npm install
npm run build
```

Required input (set at most one authentication option; none with `IAM_DEPLOYMENT_MODE=false`):

- `ARTIFICIALFLOW_BASE_URL`
  - ArtificialFlow gateway API URL.
  - Local default: `http://localhost:9100/api`
- `ARTIFICIALFLOW_TOKEN`
  - Raw token text with the `artificialflowclient` role; never prefix it with `Bearer `.
  - Issue it from your provider's client-credentials application.
- `OIDC_TOKEN_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`
  - Alternative to `ARTIFICIALFLOW_TOKEN`. The smoke test exchanges them for an access token.

Optional inputs:

- `ARTIFICIALFLOW_WORKFLOW_KEY`
  - Workflow definition key or ID to start.
- `ARTIFICIALFLOW_BUSINESS_KEY`
  - Business key for the started instance.
- `ARTIFICIALFLOW_MESSAGE_NAME`
  - BPMN message name to publish.
- `ARTIFICIALFLOW_MESSAGE_CORRELATION_KEY`
  - Correlation key for the message.
- `ARTIFICIALFLOW_WORKER_JOB_TYPE`
  - Service-task job type to activate and complete once.

```bash
export ARTIFICIALFLOW_TOKEN="<access-token>"
node examples/sdk-smoke-test.js
```

Run with workflow start, message publish, and one worker activation:

```bash
ARTIFICIALFLOW_BASE_URL="http://localhost:9100/api" \
ARTIFICIALFLOW_TOKEN="<access-token>" \
ARTIFICIALFLOW_WORKFLOW_KEY="order-process" \
ARTIFICIALFLOW_BUSINESS_KEY="order-123" \
ARTIFICIALFLOW_MESSAGE_NAME="MsgOrderPlaced" \
ARTIFICIALFLOW_MESSAGE_CORRELATION_KEY="order-123" \
ARTIFICIALFLOW_WORKER_JOB_TYPE="payment-service" \
node examples/sdk-smoke-test.js
```

## Worker

```ts
const worker = client.createWorker('payment-service', async (job) => {
  return { paymentStatus: 'success' };
}, {
  workerName: 'payment-worker',
  autoStart: true,
});

process.on('SIGINT', () => worker.stop());
```

## API coverage

- Workflow deploy/list/search/get/delete
- Decision-table deploy/list/evaluate
- Instance start/list/search/get/update variables/complete step/delete
- Bounded instance migration dry-run and apply (admin)
- User task search/claim/complete
- Message, signal, and escalation publish
- Job activation, listing, completion, failure, lock extension, administrative retry, capabilities
- Incident listing and administrative resolution, with the response wrapper normalized to an array
- Engine metrics (command service directly; the gateway does not route `/internal/metrics`)
- Identity config and current principal

Full guide: [docs/sdk-nodejs.md](../../docs/sdk-nodejs.md).
