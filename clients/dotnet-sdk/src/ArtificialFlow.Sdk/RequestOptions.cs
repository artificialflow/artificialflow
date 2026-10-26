using ArtificialFlow.Sdk.Auth;

namespace ArtificialFlow.Sdk;

public record RequestOptions
{
    public string? CorrelationId { get; init; }
    public string? IdempotencyKey { get; init; }
    public string? TenantId { get; init; }
    public IReadOnlyDictionary<string, string>? Headers { get; init; }
}

public sealed record SearchTasksOptions : RequestOptions
{
    /// <summary>Tasks assigned to or claimed by this user.</summary>
    public string? Assignee { get; init; }
    /// <summary>Tasks this user may act on: assignee, claim owner or candidate user.</summary>
    public string? CandidateUser { get; init; }
    /// <summary>Tasks offered to any of these groups.</summary>
    public IReadOnlyList<string>? CandidateGroups { get; init; }
    /// <summary>Also return open tasks with no assignee, candidates or claim.</summary>
    public bool? IncludeUnassigned { get; init; }
    public string? ProcessInstanceKey { get; init; }
    /// <summary>active (default), completed or all.</summary>
    public string? State { get; init; }
    public int? Limit { get; init; }
}

public sealed record ListOptions : RequestOptions
{
    public int? Page { get; init; }
    public int? PageSize { get; init; }
}

public sealed record ListWorkflowsOptions : RequestOptions
{
    public int? Page { get; init; }
    public int? PageSize { get; init; }
    public string? Source { get; init; }
}

public sealed record SearchInstancesOptions : RequestOptions
{
    public string? WorkflowId { get; init; }
    public string? State { get; init; }
    public int? Page { get; init; }
    public int? PageSize { get; init; }
}

public sealed record GetInstanceOptions : RequestOptions
{
    public string? Source { get; init; }
}

public sealed record ListIncidentsOptions : RequestOptions
{
    public string? ProcessInstanceKey { get; init; }
    public int? Limit { get; init; }
}

public sealed class ArtificialFlowClientOptions
{
    public string? BaseUrl { get; init; }
    public string? QueryBaseUrl { get; init; }
    public string? Token { get; init; }
    public Func<CancellationToken, Task<string>>? TokenFactory { get; init; }
    public ITokenProvider? TokenProvider { get; init; }
    public OAuthClientCredentialsOptions? Auth { get; init; }
    public string? TenantId { get; init; }
    public IReadOnlyDictionary<string, string>? Headers { get; init; }
    public int TimeoutMs { get; init; } = 30_000;
    public HttpClient? HttpClient { get; init; }
    public HttpMessageHandler? HttpMessageHandler { get; init; }
}

public static class ProtocolHeaders
{
    public const string Version = "v1";
    public const string WorkerProtocol = "X-Workflow-Worker-Protocol-Version";
    public const string EngineProtocol = "X-Workflow-Engine-Protocol-Version";
    public const string IdempotencyKey = "Idempotency-Key";
    public const string TenantId = "X-Tenant-Id";
    public const string CorrelationId = "X-Correlation-ID";
}
