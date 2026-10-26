# .NET SDK

Package: `ArtificialFlow.Sdk` (`net8.0`, no third-party runtime dependencies).

The SDK talks to the gateway over HTTP only and mirrors the
[Node.js SDK](sdk-nodejs.md): method names are the OpenAPI `operationId`s with
an `Async` suffix. Full contract: [API.md](API.md). Package README and
examples: [`clients/dotnet-sdk`](../clients/dotnet-sdk/README.md).

## Install

```bash
dotnet add package ArtificialFlow.Sdk
```

From the repository: `dotnet test clients/dotnet-sdk/ArtificialFlow.Sdk.sln`.

## Create a client

| `ArtificialFlowClientOptions` | Default | Notes |
| :--- | :--- | :--- |
| `BaseUrl` | `http://localhost:9100/api` | Gateway command API; `/api/v1` also works. |
| `QueryBaseUrl` | `{BaseUrl}/query` | Query API. |
| `Token` / `TokenFactory` / `TokenProvider` | — | Static token, async factory, or custom `ITokenProvider`. |
| `Auth` | — | Built-in OAuth client credentials (`OAuthClientCredentialsOptions`). |
| `TenantId` | — | Sends `X-Tenant-Id`; must match the token's tenant claim. |
| `Headers`, `TimeoutMs` (30 s), `HttpClient`, `HttpMessageHandler` | — | Transport settings. |

Configure at most one credential source.

## Authentication

**`IAM_DEPLOYMENT_MODE=false`** (no IAM provider): configure no credentials.
No `Authorization` header is sent and calls run as `artificialflowadmin`.

```csharp
using var client = new ArtificialFlowClient("http://localhost:9100/api");
```

**`IAM_DEPLOYMENT_MODE=true`** (external OIDC): use a machine client with only
`artificialflowclient` and the backend audience (see [iam.md](iam.md)).

```csharp
using ArtificialFlow.Sdk;
using ArtificialFlow.Sdk.Auth;

using var client = new ArtificialFlowClient(new ArtificialFlowClientOptions
{
    BaseUrl = "https://app.example.com/api",
    Auth = new OAuthClientCredentialsOptions
    {
        TokenUrl = "https://<your-oidc-provider>/oauth2/token",
        ClientId = Environment.GetEnvironmentVariable("ARTIFICIALFLOW_CLIENT_ID")!,
        ClientSecret = Environment.GetEnvironmentVariable("ARTIFICIALFLOW_CLIENT_SECRET")!,
        // Scopes = new[] { "..." },   // only when the provider requires them
    },
});
```

Tokens are cached and refreshed about 30 seconds before expiry
(`RefreshSkewMs`), with a single in-flight refresh. Token URLs must be HTTPS
except loopback. If the provider needs HTTP Basic client authentication or an
`audience`/`resource` parameter, mint tokens with its library and pass
`TokenFactory`. `client.SetToken(token)` switches later calls to a raw token.

## Operations

```csharp
var definition = await client.deployWorkflowAsync(File.ReadAllText("order-process.bpmn"));
var instance = await client.startInstanceAsync("order-process",
    new Dictionary<string, object?> { ["orderId"] = "123" });
await client.publishMessageAsync("MsgOrderPlaced", "order-123",
    new Dictionary<string, object?> { ["amount"] = 100 });
var page = await client.searchInstancesAsync(new SearchInstancesOptions { State = "RUNNING" });
```

| Area | Methods (`…Async`) |
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

`listWorkflowsAsync` reads the query API unless `Source = "command"`.
`getEngineMetricsAsync` needs a direct command-service URL; the gateway does not
route `/internal/metrics`. Mutations accept `RequestOptions` with
`IdempotencyKey`, `CorrelationId`, `TenantId`, and `Headers`.

## User tasks

Your application signs its end users in with its own IAM and keeps the
`artificialflowclient` credential on its backend:

```csharp
var tasks = await client.searchTasksAsync(new SearchTasksOptions
{
    CandidateUser = signedInUser.Email,
    CandidateGroups = signedInUser.Groups,
    IncludeUnassigned = true,
});
var task = tasks[0];
await client.claimUserTaskAsync(task.ProcessInstanceKey, task.ExecutionId, signedInUser.Email);
await client.completeUserTaskAsync(task.ProcessInstanceKey, task.ExecutionId, signedInUser.Email);
```

Filters are OR-combined; `State` is `active` (default), `completed`, or `all`.
Completing requires the assignee that claimed the task.

## Job workers

```csharp
var worker = client.CreateWorker("payment-service", async (job, context, ct) =>
    new Dictionary<string, object?> { ["paymentStatus"] = "success" },
    new JobWorkerOptions { WorkerName = "payment-worker", AutoStart = true });

Console.CancelKeyPress += (_, _) => worker.Stop();
```

`JobWorkerOptions`: `MaxJobs` (32), `PollInterval` (1000 ms), `Timeout`
(5000 ms), `LockDuration` (30 000 ms), `WorkerName`, `FailRetries`, `OnError`,
`AutoStart`. A thrown handler fails the job; `context.CompleteAsync`,
`FailAsync`, and `ExtendLockAsync` give manual control. `RunOnceAsync`
processes a single poll.

## Errors

Non-2xx responses throw `ArtificialFlowApiException` with `Status`, `Body`,
`Method`, `Path`, `CorrelationId`, and the RFC 9457 `Problem` on `/api/v1`.
Secrets and tokens never appear in exception messages.
