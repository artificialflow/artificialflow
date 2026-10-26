using System.Text.Json;
using ArtificialFlow.Sdk.Auth;
using ArtificialFlow.Sdk.Models;
using ArtificialFlow.Sdk.Worker;
using Xunit;

namespace ArtificialFlow.Sdk.Tests;

public sealed class ClientTests
{
    [Fact]
    public async Task Worker_run_once_completes_and_fails()
    {
        var success = CreateClient();
        success.Handler.EnqueueJson(200, new
        {
            jobs = new[]
            {
                new { key = "201", type = "payment", processInstanceKey = "1", elementInstanceKey = "2", processDefinitionKey = "3", elementId = "task", worker = "worker-1", retries = 3, state = "ACTIVATED", createdAt = "2026-01-01T00:00:00Z", updatedAt = "2026-01-01T00:00:00Z" },
            },
        });
        success.Handler.EnqueueText(204, "");
        var worker = success.Client.CreateWorker("payment", async (_, _, _) => new Dictionary<string, object?> { ["paid"] = true }, new JobWorkerOptions { WorkerName = "worker-1" });
        Assert.Equal(1, await worker.RunOnceAsync());
        Assert.EndsWith("/jobs/201/complete", success.Handler.Calls[1].Url);
        Assert.Equal("v1", success.Handler.Calls[0].Headers[ProtocolHeaders.WorkerProtocol]);
        Assert.Equal("v1", success.Handler.Calls[0].Headers[ProtocolHeaders.EngineProtocol]);
        Assert.Contains("\"paid\":true", success.Handler.Calls[1].Body.Replace(" ", ""));

        var failure = CreateClient();
        failure.Handler.EnqueueJson(200, new
        {
            jobs = new[]
            {
                new { key = "202", type = "payment", processInstanceKey = "1", elementInstanceKey = "2", processDefinitionKey = "3", elementId = "task", worker = "worker-1", retries = 2, state = "ACTIVATED", createdAt = "2026-01-01T00:00:00Z", updatedAt = "2026-01-01T00:00:00Z" },
            },
        });
        failure.Handler.EnqueueText(204, "");
        var failing = failure.Client.CreateWorker("payment", (_, _, _) => throw new InvalidOperationException("boom"), new JobWorkerOptions { WorkerName = "worker-1" });
        await failing.RunOnceAsync();
        Assert.EndsWith("/jobs/202/fail", failure.Handler.Calls[1].Url);
        using var failBody = JsonDocument.Parse(failure.Handler.Calls[1].Body);
        Assert.Equal("boom", failBody.RootElement.GetProperty("errorMessage").GetString());
        Assert.Equal(1, failBody.RootElement.GetProperty("retries").GetInt32());
    }

    [Fact]
    public async Task Problem_details_401_exposes_correlation_id()
    {
        var fixture = CreateClient();
        fixture.Handler.Enqueue((_, _) =>
        {
            var response = new HttpResponseMessage(System.Net.HttpStatusCode.Unauthorized)
            {
                Content = new StringContent("""{"type":"about:blank","title":"Unauthorized","status":401,"detail":"token required","correlationId":"corr-problem"}""", System.Text.Encoding.UTF8, "application/problem+json"),
            };
            response.Headers.TryAddWithoutValidation("X-Correlation-ID", "corr-problem");
            return response;
        });

        var error = await Assert.ThrowsAsync<ArtificialFlowApiException>(() => fixture.Client.commandHealthAsync());
        Assert.Equal(401, error.Status);
        Assert.Equal("token required", error.Problem?.Detail);
        Assert.Equal("corr-problem", error.CorrelationId);
        Assert.Contains("Unauthorized", error.Message);
    }

    [Fact]
    public async Task Deploy_workflow_sends_xml()
    {
        var fixture = CreateClient();
        fixture.Handler.EnqueueJson(200, new { id = "1", process_definition_id = "order", name = "Order" });
        await fixture.Client.deployWorkflowAsync("<definitions />");
        Assert.Equal("POST", fixture.Handler.Calls[0].Method);
        Assert.EndsWith("/workflows", fixture.Handler.Calls[0].Url);
        Assert.Contains("application/xml", fixture.Handler.Calls[0].Headers["Content-Type"]);
        Assert.Equal("<definitions />", fixture.Handler.Calls[0].Body);
    }

    [Fact]
    public async Task Start_instance_posts_workflow_and_context()
    {
        var fixture = CreateClient();
        fixture.Handler.EnqueueJson(200, new { id = "99", workflow_id = "1", status = "RUNNING", context = new { orderId = "A1" } });
        var instance = await fixture.Client.startInstanceAsync("1", new Dictionary<string, object?> { ["orderId"] = "A1" });
        Assert.Equal("99", instance.Id);
        using var body = JsonDocument.Parse(fixture.Handler.Calls[0].Body);
        Assert.Equal("1", body.RootElement.GetProperty("workflow_id").GetString());
        Assert.Equal("A1", body.RootElement.GetProperty("context").GetProperty("orderId").GetString());
        Assert.True(fixture.Handler.Calls[0].Headers.ContainsKey(ProtocolHeaders.IdempotencyKey));
    }

    [Fact]
    public async Task List_incidents_unwraps_wrapper()
    {
        var fixture = CreateClient();
        fixture.Handler.EnqueueJson(200, new { incidents = new[] { new { id = "incident-1", state = "OPEN" } } });
        var incidents = await fixture.Client.listIncidentsAsync(new ListIncidentsOptions { Limit = 25 });
        Assert.Equal("incident-1", incidents[0].Id);
        Assert.Contains("/incidents?limit=25", fixture.Handler.Calls[0].Url);
    }

    [Fact]
    public async Task User_task_search_claim_and_complete_pass_end_user_as_parameters()
    {
        var fixture = CreateClient();
        fixture.Handler.EnqueueJson(200, new { tasks = new[] { new { key = "9", processInstanceKey = "42", executionId = "exec-1", canClaim = true } } });
        var tasks = await fixture.Client.searchTasksAsync(new SearchTasksOptions
        {
            CandidateUser = "alice@example.com",
            CandidateGroups = new[] { "accountant", "finance" },
            IncludeUnassigned = true,
            Limit = 10,
        });
        Assert.Equal("42", tasks[0].ProcessInstanceKey);
        Assert.Contains("/tasks?candidateUser=alice%40example.com&candidateGroup=accountant&candidateGroup=finance&includeUnassigned=true&limit=10", fixture.Handler.Calls[0].Url);

        fixture.Handler.EnqueueJson(200, new { key = "9", executionId = "exec-1" });
        await fixture.Client.claimUserTaskAsync("42", "exec-1", "alice@example.com");
        Assert.EndsWith("/instances/42/tasks/exec-1/claim", fixture.Handler.Calls[1].Url);
        Assert.Contains("\"assignee\":\"alice@example.com\"", fixture.Handler.Calls[1].Body.Replace(" ", ""));

        fixture.Handler.EnqueueText(204, "");
        await fixture.Client.completeUserTaskAsync("42", "exec-1");
        Assert.EndsWith("/instances/42/tasks/exec-1/complete", fixture.Handler.Calls[2].Url);
        Assert.Equal("{}", fixture.Handler.Calls[2].Body);
    }

    [Fact]
    public async Task List_workflows_uses_query_unless_command_source()
    {
        var fixture = CreateClient();
        fixture.Handler.EnqueueJson(200, new { workflows = new[] { new { id = "1", name = "Order" } }, total = 1 });
        fixture.Handler.EnqueueJson(200, new[] { new { id = "1", name = "Order" } });
        var queried = await fixture.Client.listWorkflowsAsync(new ListWorkflowsOptions { Page = 1, PageSize = 10 });
        Assert.Equal("1", queried[0].Id);
        Assert.Contains("/query/workflows?page=1&pageSize=10", fixture.Handler.Calls[0].Url);
        await fixture.Client.listWorkflowsAsync(new ListWorkflowsOptions { Source = "command" });
        Assert.EndsWith("/workflows", fixture.Handler.Calls[1].Url);
        Assert.DoesNotContain("/query/", fixture.Handler.Calls[1].Url);
    }

    [Fact]
    public async Task Token_and_auth_cannot_be_combined()
    {
        Assert.Throws<ArgumentException>(() => new ArtificialFlowClient(new ArtificialFlowClientOptions
        {
            Token = "raw-token",
            Auth = new OAuthClientCredentialsOptions
            {
                TokenUrl = "https://iam.example/token",
                ClientId = "id",
                ClientSecret = "secret",
            },
        }));
    }

    private static (ArtificialFlowClient Client, StubHttpMessageHandler Handler) CreateClient()
    {
        var handler = new StubHttpMessageHandler();
        var client = new ArtificialFlowClient(new ArtificialFlowClientOptions
        {
            BaseUrl = "http://artificialflow.local/api",
            Token = "token-1",
            HttpMessageHandler = handler,
        });
        return (client, handler);
    }
}
