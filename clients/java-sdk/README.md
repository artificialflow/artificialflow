# ArtificialFlow Java SDK

REST SDK for the ArtificialFlow gateway API. Users, clients, and tokens live in
your external OIDC provider.

## Build

```bash
mvn -B verify
```

## Authentication

Use OAuth2 client credentials or a static access token with the
`artificialflowclient` role.

When ArtificialFlow runs with `IAM_DEPLOYMENT_MODE=false` (no IAM provider), configure no
credentials at all: the SDK sends no `Authorization` header and the API
accepts the call.

```java
ArtificialFlowClient client = new ArtificialFlowClient(new ArtificialFlowClientOptions()
    .baseUrl("http://localhost:9100/api")
    .auth(new OAuthClientCredentialsOptions()
        .tokenUrl("https://login.example.com/oauth2/token")
        .clientId(System.getenv("OIDC_CLIENT_ID"))
        .clientSecret(System.getenv("OIDC_CLIENT_SECRET"))));
```

## User tasks

Your application signs its end users in with its own IAM, then passes their
identity to ArtificialFlow as plain filter values:

```java
List<Models.UserTask> tasks = client.searchTasks(new SearchTasksOptions()
    .candidateUser(signedInUser.email())
    .candidateGroups(signedInUser.groups())
    .includeUnassigned(true));

Models.UserTask task = tasks.get(0);
client.claimUserTask(task.processInstanceKey, task.executionId, signedInUser.email(), null);
client.completeUserTask(task.processInstanceKey, task.executionId, signedInUser.email(), null);
```

`assignee`, `candidateUser` and `candidateGroups` are OR-combined. `state` is
`active` (default), `completed` or `all`. Client tokens must pass the assignee
on claim and complete; completing requires the same assignee that claimed the
task.

## Worker

```java
JobWorker worker = client.createWorker("payment-service", (job, context) -> Map.of(
    "paymentStatus", "success"
), new JobWorkerOptions().workerName("payment-worker").autoStart(true));
```

Method names match OpenAPI `operationId`s. See `examples/SdkSmokeTest.java`,
`examples/WorkerExample.java`, and the full guide with the method table in
[docs/sdk-java.md](../../docs/sdk-java.md).
