# Java SDK

Artifact: `io.artificialflow:artificialflow-sdk` (Java 17, Jackson only).

The SDK talks to the gateway over HTTP only and mirrors the
[Node.js SDK](sdk-nodejs.md): method names are the OpenAPI `operationId`s.
Full contract: [API.md](API.md). Package README and examples:
[`clients/java-sdk`](../clients/java-sdk/README.md).

## Install

```xml
<dependency>
  <groupId>io.artificialflow</groupId>
  <artifactId>artificialflow-sdk</artifactId>
  <version>1.2.0</version>
</dependency>
```

From the repository: `mvn -B -f clients/java-sdk/pom.xml verify`.

## Create a client

| `ArtificialFlowClientOptions` | Default | Notes |
| :--- | :--- | :--- |
| `baseUrl` | `http://localhost:9100/api` | Gateway command API; `/api/v1` also works. |
| `queryBaseUrl` | `{baseUrl}/query` | Query API. |
| `token` / `tokenProvider` | — | Static token or custom `TokenProvider` (`StaticTokenProvider` accepts a `Supplier<String>`). |
| `auth` | — | Built-in OAuth client credentials (`OAuthClientCredentialsOptions`). |
| `tenantId` | — | Sends `X-Tenant-Id`; must match the token's tenant claim. |
| `headers`, `timeoutMs` | — | Transport settings. |

Configure at most one credential source. `ArtificialFlowClient` is
`AutoCloseable`.

## Authentication

**`IAM_DEPLOYMENT_MODE=false`** (no IAM provider): configure no credentials.
No `Authorization` header is sent and calls run as `artificialflowadmin`.

```java
ArtificialFlowClient client = new ArtificialFlowClient("http://localhost:9100/api");
```

**`IAM_DEPLOYMENT_MODE=true`** (external OIDC): use a machine client with only
`artificialflowclient` and the backend audience (see [iam.md](iam.md)).

```java
ArtificialFlowClient client = new ArtificialFlowClient(new ArtificialFlowClientOptions()
    .baseUrl("https://app.example.com/api")
    .auth(new OAuthClientCredentialsOptions()
        .tokenUrl("https://<your-oidc-provider>/oauth2/token")
        .clientId(System.getenv("OIDC_CLIENT_ID"))
        .clientSecret(System.getenv("OIDC_CLIENT_SECRET"))));
```

Tokens are cached and refreshed before expiry. If the provider needs HTTP Basic
client authentication or an `audience`/`resource` parameter, mint tokens with
its library and pass a `tokenProvider`. `client.setToken(token)` switches later
calls to a raw token.

## Operations

```java
Models.WorkflowDefinition definition = client.deployWorkflow(Files.readString(Path.of("order-process.bpmn")), null);
Models.WorkflowInstance instance = client.startInstance("order-process", Map.of("orderId", "123"), null);
client.publishMessage("MsgOrderPlaced", "order-123", Map.of("amount", 100), null);
Models.InstanceSearchResponse page = client.searchInstances(new SearchInstancesOptions().state("RUNNING"));
```

| Area | Methods |
| :--- | :--- |
| Health and identity | `commandHealth`, `queryHealth`, `getIdentity`, `getQueryIdentity`, `getIdentityConfig` |
| Workflows | `deployWorkflow`, `listWorkflows`, `searchWorkflows`, `getWorkflow`, `deleteWorkflow` |
| Decisions | `deployDecision`, `listDecisions`, `evaluateDecision` |
| Instances | `startInstance`, `listInstances`, `listCompletedInstances`, `getInstance`, `deleteInstance`, `updateInstanceVariables`, `completeInstanceStep` |
| Instance migration (admin) | `dryRunInstanceMigration`, `migrateInstance` |
| Query (eventually consistent) | `searchInstances`, `getProjectedInstance`, `searchWorkflows` |
| Events | `publishMessage`, `publishSignal`, `publishEscalation` |
| User tasks | `searchTasks`, `listInstanceTasks`, `claimUserTask`, `completeUserTask` |
| Jobs | `getJobCapabilities`, `activateJobs`, `completeJob`, `failJob`, `extendJobLock` |
| Operations (admin) | `listInstanceJobs`, `retryJob`, `listIncidents`, `resolveIncident`, `getEngineMetrics` |

Most methods take a trailing `RequestOptions` (pass `null` for defaults) with
`idempotencyKey`, `correlationId`, `tenantId`, and `headers`.
`getEngineMetrics` needs a direct command-service URL; the gateway does not
route `/internal/metrics`.

## User tasks

Your application signs its end users in with its own IAM and keeps the
`artificialflowclient` credential on its backend:

```java
List<Models.UserTask> tasks = client.searchTasks(new SearchTasksOptions()
    .candidateUser(signedInUser.email())
    .candidateGroups(signedInUser.groups())
    .includeUnassigned(true));

Models.UserTask task = tasks.get(0);
client.claimUserTask(task.processInstanceKey, task.executionId, signedInUser.email(), null);
client.completeUserTask(task.processInstanceKey, task.executionId, signedInUser.email(), null);
```

Filters are OR-combined; `state` is `active` (default), `completed`, or `all`.
Completing requires the assignee that claimed the task.

## Job workers

```java
JobWorker worker = client.createWorker("payment-service",
    (job, context) -> Map.of("paymentStatus", "success"),
    new JobWorkerOptions().workerName("payment-worker").autoStart(true));

Runtime.getRuntime().addShutdownHook(new Thread(worker::stop));
```

`JobWorkerOptions`: `maxJobs`, `pollInterval`, `timeout`, `lockDuration`,
`workerName`, `failRetries`, `onError`, `autoStart`. A thrown handler fails the
job; `context.complete`, `fail`, and `extendLock` give manual control.
`runOnce()` processes a single poll.

## Errors

Non-2xx responses throw `ArtificialFlowApiException` with `getStatus()`,
`getBody()`, `getMethod()`, `getPath()`, `getCorrelationId()`, and the
RFC 9457 `getProblem()` on `/api/v1`. Token failures throw
`OAuthClientCredentialsException`.
