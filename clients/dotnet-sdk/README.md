# ArtificialFlow .NET SDK

.NET 8 REST client for the ArtificialFlow gateway API.

The SDK talks to the gateway over HTTP only. Users, clients, and tokens live in
your external OIDC provider. ArtificialFlow does not create or manage SDK
credentials.

## Install

```bash
dotnet add package ArtificialFlow.Sdk
```

The library targets `net8.0` and has no third-party runtime dependencies.

## Authentication

Configure at most one of:

- a static Bearer token
- OAuth 2.0 `client_credentials` (cached, refreshed about 30 seconds before expiry, single-flight)

When ArtificialFlow runs with `IAM_DEPLOYMENT_MODE=false` (no IAM provider), configure neither:
the SDK sends no `Authorization` header and the API accepts the call.

Client secrets and access tokens are never written into exception messages.

Token URLs must be `https`, except loopback `http://localhost`, `http://127.0.0.1`, and `http://[::1]`.

```csharp
using ArtificialFlow.Sdk;
using ArtificialFlow.Sdk.Auth;

var client = new ArtificialFlowClient(new ArtificialFlowClientOptions
{
    BaseUrl = "http://localhost:9100/api",
    Auth = new OAuthClientCredentialsOptions
    {
        TokenUrl = "https://login.example.com/oauth2/token",
        ClientId = Environment.GetEnvironmentVariable("ARTIFICIALFLOW_CLIENT_ID")!,
        ClientSecret = Environment.GetEnvironmentVariable("ARTIFICIALFLOW_CLIENT_SECRET")!,
    },
});
```

The built-in provider sends `client_id` and `client_secret` in the form body.
If your provider needs HTTP Basic client authentication or a custom
`audience`/`resource` parameter, mint a token yourself and pass `Token`.

Static token:

```csharp
var client = new ArtificialFlowClient(new ArtificialFlowClientOptions
{
    BaseUrl = "http://localhost:9100/api",
    Token = Environment.GetEnvironmentVariable("ARTIFICIALFLOW_TOKEN"),
});

var workflows = await client.listWorkflowsAsync(new ListWorkflowsOptions { Page = 1, PageSize = 100 });
var instance = await client.startInstanceAsync("order-process", new Dictionary<string, object?> { ["orderId"] = "123" });
await client.publishMessageAsync("MsgOrderPlaced", "123", new Dictionary<string, object?> { ["amount"] = 100 });
```

Method names match OpenAPI `operationId` values with an `Async` suffix, for
example `commandHealthAsync`, `startInstanceAsync`, and `activateJobsAsync`.

Query methods use `queryBaseUrl`, which defaults to `{baseUrl}/query`.
`listWorkflowsAsync` searches the query API unless `Source = "command"`.

Optional `X-Tenant-Id` is sent when `TenantId` is set on the client or a request.
Mutations send `Idempotency-Key`. Worker calls send
`X-Workflow-Worker-Protocol-Version: v1` and
`X-Workflow-Engine-Protocol-Version: v1`.

## User tasks

ArtificialFlow does not validate your end users. Your application signs them in
with its own IAM, then calls ArtificialFlow with an `artificialflowclient` token
and passes the end user's identity as plain filter values:

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

`Assignee`, `CandidateUser` and `CandidateGroups` are OR-combined. `State` is
`active` (default), `completed` or `all`. Client tokens must pass the assignee
on claim and complete; completing requires the same assignee that claimed the
task.

## Worker

```csharp
var worker = client.CreateWorker("payment-service", async (job, context, _) =>
{
    return new Dictionary<string, object?> { ["paymentStatus"] = "success" };
}, new JobWorkerOptions
{
    WorkerName = "payment-worker",
    MaxJobs = 32,
    PollInterval = 1000,
    Timeout = 5000,
    LockDuration = 30_000,
    AutoStart = true,
});

// context.CompleteAsync / FailAsync / ExtendLockAsync are also available.
```

A thrown handler fails the job. `failRetries` can override the retry count.
`onError` receives poll-loop errors.

## Examples

- `examples/SdkSmokeTest.cs`
- `examples/WorkerExample.cs`

```bash
export ARTIFICIALFLOW_TOKEN="<access-token>"
# optional: ARTIFICIALFLOW_WORKFLOW_KEY, ARTIFICIALFLOW_WORKER_JOB_TYPE
```

RFC 9457 problem details are raised as `ArtificialFlowApiException` with
`CorrelationId` and `Problem`.

Full guide, including the method table: [docs/sdk-dotnet.md](../../docs/sdk-dotnet.md).
