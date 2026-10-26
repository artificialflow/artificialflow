// Smoke-test the ArtificialFlow .NET SDK against a running gateway.
//
//   ARTIFICIALFLOW_TOKEN=<token> dotnet run --project examples/SdkSmokeTest/SdkSmokeTest.csproj
// or copy this file into a console app that references ArtificialFlow.Sdk.
//
// Set at most one of ARTIFICIALFLOW_TOKEN or OIDC_TOKEN_URL + OIDC_CLIENT_ID + OIDC_CLIENT_SECRET.
// Set neither when IAM is disabled.

using ArtificialFlow.Sdk;
using ArtificialFlow.Sdk.Auth;

var baseUrl = Environment.GetEnvironmentVariable("ARTIFICIALFLOW_BASE_URL")
    ?? Environment.GetEnvironmentVariable("ARTIFICIALFLOW_API_URL")
    ?? "http://localhost:9100/api";
var token = Environment.GetEnvironmentVariable("ARTIFICIALFLOW_TOKEN");
var tokenUrl = Environment.GetEnvironmentVariable("OIDC_TOKEN_URL") ?? Environment.GetEnvironmentVariable("ARTIFICIALFLOW_OIDC_TOKEN_URL");
var clientId = Environment.GetEnvironmentVariable("OIDC_CLIENT_ID") ?? Environment.GetEnvironmentVariable("ARTIFICIALFLOW_CLIENT_ID");
var clientSecret = Environment.GetEnvironmentVariable("OIDC_CLIENT_SECRET") ?? Environment.GetEnvironmentVariable("ARTIFICIALFLOW_CLIENT_SECRET");
var auth = !string.IsNullOrWhiteSpace(tokenUrl) && !string.IsNullOrWhiteSpace(clientId) && !string.IsNullOrWhiteSpace(clientSecret)
    ? new OAuthClientCredentialsOptions { TokenUrl = tokenUrl, ClientId = clientId, ClientSecret = clientSecret }
    : null;
if (!string.IsNullOrWhiteSpace(token) && auth != null)
{
    throw new InvalidOperationException("Set at most one of ARTIFICIALFLOW_TOKEN or OIDC_TOKEN_URL/OIDC_CLIENT_ID/OIDC_CLIENT_SECRET.");
}

using var client = new ArtificialFlowClient(new ArtificialFlowClientOptions
{
    BaseUrl = baseUrl,
    Token = auth == null && !string.IsNullOrWhiteSpace(token) ? token : null,
    Auth = auth,
});

Console.WriteLine("1. Checking current authenticated principal...");
Console.WriteLine(System.Text.Json.JsonSerializer.Serialize(await client.getIdentityAsync(), new System.Text.Json.JsonSerializerOptions { WriteIndented = true }));

Console.WriteLine("2. Listing workflows...");
var workflows = await client.listWorkflowsAsync(new ListWorkflowsOptions { Page = 1, PageSize = 20 });
Console.WriteLine(System.Text.Json.JsonSerializer.Serialize(workflows, new System.Text.Json.JsonSerializerOptions { WriteIndented = true }));

var workflowKey = Environment.GetEnvironmentVariable("ARTIFICIALFLOW_WORKFLOW_KEY");
if (!string.IsNullOrWhiteSpace(workflowKey) && !workflowKey.StartsWith('<'))
{
    Console.WriteLine("3. Starting workflow instance...");
    var instance = await client.startInstanceAsync(workflowKey, new Dictionary<string, object?>
    {
        ["businessKey"] = Environment.GetEnvironmentVariable("ARTIFICIALFLOW_BUSINESS_KEY") ?? $"sdk-smoke-{DateTimeOffset.UtcNow.ToUnixTimeMilliseconds()}",
        ["source"] = "dotnet-sdk-smoke-test",
    });
    Console.WriteLine(System.Text.Json.JsonSerializer.Serialize(instance, new System.Text.Json.JsonSerializerOptions { WriteIndented = true }));
}
else
{
    Console.WriteLine("3. Skipping startInstance. Set ARTIFICIALFLOW_WORKFLOW_KEY to test workflow start.");
}

var messageName = Environment.GetEnvironmentVariable("ARTIFICIALFLOW_MESSAGE_NAME");
var correlationKey = Environment.GetEnvironmentVariable("ARTIFICIALFLOW_MESSAGE_CORRELATION_KEY");
if (!string.IsNullOrWhiteSpace(messageName) && !messageName.StartsWith('<') && !string.IsNullOrWhiteSpace(correlationKey) && !correlationKey.StartsWith('<'))
{
    Console.WriteLine("4. Publishing BPMN message...");
    await client.publishMessageAsync(messageName, correlationKey, new Dictionary<string, object?> { ["source"] = "dotnet-sdk-smoke-test" });
}
else
{
    Console.WriteLine("4. Skipping publishMessage. Set ARTIFICIALFLOW_MESSAGE_NAME and ARTIFICIALFLOW_MESSAGE_CORRELATION_KEY to test messages.");
}

var jobType = Environment.GetEnvironmentVariable("ARTIFICIALFLOW_WORKER_JOB_TYPE");
if (!string.IsNullOrWhiteSpace(jobType) && !jobType.StartsWith('<'))
{
    Console.WriteLine("5. Activating one worker job...");
    var worker = client.CreateWorker(jobType, async (job, _, _) =>
    {
        Console.WriteLine($"Received job: {job.Key} {job.Type}");
        return new Dictionary<string, object?>
        {
            ["handledBy"] = "dotnet-sdk-smoke-test",
            ["handledAt"] = DateTime.UtcNow.ToString("o"),
        };
    }, new ArtificialFlow.Sdk.Worker.JobWorkerOptions { WorkerName = "dotnet-sdk-smoke-worker", MaxJobs = 1 });
    Console.WriteLine($"Completed jobs: {await worker.RunOnceAsync()}");
}
else
{
    Console.WriteLine("5. Skipping worker test. Set ARTIFICIALFLOW_WORKER_JOB_TYPE to activate and complete one job.");
}

Console.WriteLine("SDK smoke test completed.");
