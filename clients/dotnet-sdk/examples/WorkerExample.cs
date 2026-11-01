// Job-worker example for the ArtificialFlow .NET SDK.
//
//   ARTIFICIALFLOW_TOKEN=<token> \
//   ARTIFICIALFLOW_WORKER_JOB_TYPE=golden-validate \
//   ARTIFICIALFLOW_WORKER_MODE=complete \
//   dotnet run --project examples/WorkerExample/WorkerExample.csproj

using ArtificialFlow.Sdk;
using ArtificialFlow.Sdk.Auth;
using ArtificialFlow.Sdk.Worker;

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
var jobType = Environment.GetEnvironmentVariable("ARTIFICIALFLOW_WORKER_JOB_TYPE");
if (string.IsNullOrWhiteSpace(jobType) || jobType.StartsWith('<'))
{
    throw new InvalidOperationException("Set ARTIFICIALFLOW_WORKER_JOB_TYPE");
}

var mode = (Environment.GetEnvironmentVariable("ARTIFICIALFLOW_WORKER_MODE") ?? "complete").ToLowerInvariant();

using var client = new ArtificialFlowClient(new ArtificialFlowClientOptions
{
    BaseUrl = baseUrl,
    Token = auth == null && !string.IsNullOrWhiteSpace(token) ? token : null,
    Auth = auth,
});

using var worker = client.CreateWorker(jobType, async (job, context, _) =>
{
    Console.WriteLine($"activated job {job.Key} type {job.Type}");
    if (mode == "fail")
    {
        throw new InvalidOperationException("example intentional failure");
    }

    return new Dictionary<string, object?>
    {
        ["processedBy"] = "dotnet-sdk-worker-example",
        ["handledAt"] = DateTime.UtcNow.ToString("o"),
        ["ok"] = true,
    };
}, new JobWorkerOptions
{
    WorkerName = Environment.GetEnvironmentVariable("ARTIFICIALFLOW_WORKER_NAME") ?? "dotnet-sdk-example-worker",
    MaxJobs = 1,
});

await worker.RunOnceAsync();
Console.WriteLine(mode == "fail" ? "fail path exercised" : "complete path exercised");
