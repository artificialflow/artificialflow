using System.Text;
using System.Text.Json;
using ArtificialFlow.Sdk.Auth;
using ArtificialFlow.Sdk.Models;
using ArtificialFlow.Sdk.Worker;

namespace ArtificialFlow.Sdk;

public sealed class ArtificialFlowClient : IDisposable
{
    private readonly string _baseUrl;
    private readonly string _queryBaseUrl;
    private readonly HttpClient _http;
    private readonly bool _ownsHttp;
    private readonly IReadOnlyDictionary<string, string>? _defaultHeaders;
    private readonly string? _tenantId;
    private readonly int _timeoutMs;
    private ITokenProvider? _tokenProvider;
    private OAuthClientCredentialsTokenProvider? _ownedAuth;

    public ArtificialFlowClient(string baseUrl)
        : this(new ArtificialFlowClientOptions { BaseUrl = baseUrl })
    {
    }

    public ArtificialFlowClient(ArtificialFlowClientOptions? options = null)
    {
        options ??= new ArtificialFlowClientOptions();
        var authConfigured = options.Auth != null;
        var tokenConfigured = options.Token != null || options.TokenFactory != null || options.TokenProvider != null;
        if (authConfigured && tokenConfigured)
        {
            throw new ArgumentException("ArtificialFlowClient options.token and options.auth cannot be used together");
        }

        _baseUrl = NormalizeBaseUrl(options.BaseUrl ?? "http://localhost:9100/api");
        _queryBaseUrl = NormalizeBaseUrl(options.QueryBaseUrl ?? $"{_baseUrl}/query");
        _defaultHeaders = options.Headers;
        _tenantId = options.TenantId;
        _timeoutMs = options.TimeoutMs <= 0 ? 30_000 : options.TimeoutMs;

        if (options.HttpClient != null)
        {
            _http = options.HttpClient;
            _ownsHttp = false;
        }
        else if (options.HttpMessageHandler != null)
        {
            _http = new HttpClient(options.HttpMessageHandler, disposeHandler: false);
            _ownsHttp = true;
        }
        else
        {
            _http = new HttpClient();
            _ownsHttp = true;
        }

        if (options.Auth != null)
        {
            var authOptions = new OAuthClientCredentialsOptions
            {
                TokenUrl = options.Auth.TokenUrl,
                ClientId = options.Auth.ClientId,
                ClientSecret = options.Auth.ClientSecret,
                Scopes = options.Auth.Scopes,
                RefreshSkewMs = options.Auth.RefreshSkewMs,
                Clock = options.Auth.Clock,
                HttpMessageHandler = options.Auth.HttpMessageHandler ?? options.HttpMessageHandler,
                HttpClient = options.Auth.HttpClient,
            };
            _ownedAuth = new OAuthClientCredentialsTokenProvider(authOptions);
            _tokenProvider = _ownedAuth;
        }
        else if (options.TokenProvider != null)
        {
            _tokenProvider = options.TokenProvider;
        }
        else if (options.TokenFactory != null)
        {
            _tokenProvider = new StaticTokenProvider(options.TokenFactory);
        }
        else if (!string.IsNullOrEmpty(options.Token))
        {
            _tokenProvider = new StaticTokenProvider(options.Token);
        }
    }

    public void SetToken(string? token)
    {
        _ownedAuth?.Dispose();
        _ownedAuth = null;
        _tokenProvider = string.IsNullOrEmpty(token) ? null : new StaticTokenProvider(token);
    }

    public JobWorker CreateWorker(string type, JobHandler handler, JobWorkerOptions? options = null)
    {
        return new JobWorker(this, type, handler, options);
    }

    public void Dispose()
    {
        _ownedAuth?.Dispose();
        if (_ownsHttp)
        {
            _http.Dispose();
        }
    }

    public Task<HealthResponse> commandHealthAsync(RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<HealthResponse>(HttpMethod.Get, _baseUrl, "/health", null, options, cancellationToken);

    public Task<IdentityResponse> getIdentityAsync(RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<IdentityResponse>(HttpMethod.Get, _baseUrl, "/identity/me", null, options, cancellationToken);

    public Task<IdentityConfigResponse> getIdentityConfigAsync(RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<IdentityConfigResponse>(HttpMethod.Get, _baseUrl, "/identity/config", null, options, cancellationToken);

    public async Task<List<WorkflowDefinition>> listWorkflowsAsync(ListWorkflowsOptions? options = null, CancellationToken cancellationToken = default)
    {
        if (string.Equals(options?.Source, "command", StringComparison.OrdinalIgnoreCase))
        {
            return await RequestAsync<List<WorkflowDefinition>>(HttpMethod.Get, _baseUrl, "/workflows", null, options, cancellationToken).ConfigureAwait(false)
                ?? new List<WorkflowDefinition>();
        }

        var search = await searchWorkflowsAsync(options == null ? null : new ListOptions
        {
            Page = options.Page,
            PageSize = options.PageSize,
            CorrelationId = options.CorrelationId,
            IdempotencyKey = options.IdempotencyKey,
            TenantId = options.TenantId,
            Headers = options.Headers,
        }, cancellationToken).ConfigureAwait(false);
        return search.Workflows ?? new List<WorkflowDefinition>();
    }

    public Task<WorkflowDefinition> deployWorkflowAsync(string bpmnXml, RequestOptions? options = null, CancellationToken cancellationToken = default)
    {
        var headers = MergeHeaders(options, new Dictionary<string, string> { ["Content-Type"] = "application/xml" });
        return RequestAsync<WorkflowDefinition>(
            HttpMethod.Post,
            _baseUrl,
            "/workflows",
            bpmnXml,
            (options ?? new RequestOptions()) with { Headers = headers },
            cancellationToken,
            rawBody: true);
    }

    public Task<WorkflowSearchResponse> searchWorkflowsAsync(ListOptions? options = null, CancellationToken cancellationToken = default)
    {
        var query = QueryString(("page", options?.Page), ("pageSize", options?.PageSize));
        return RequestAsync<WorkflowSearchResponse>(HttpMethod.Get, _queryBaseUrl, $"/workflows{query}", null, options, cancellationToken);
    }

    public Task<WorkflowDefinition> getWorkflowAsync(string id, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<WorkflowDefinition>(HttpMethod.Get, _baseUrl, $"/workflows/{Uri.EscapeDataString(id)}", null, options, cancellationToken);

    public Task deleteWorkflowAsync(string id, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<object>(HttpMethod.Delete, _baseUrl, $"/workflows/{Uri.EscapeDataString(id)}", null, options, cancellationToken);

    public async Task<List<DecisionDefinition>> listDecisionsAsync(RequestOptions? options = null, CancellationToken cancellationToken = default)
    {
        var payload = await RequestAsync<JsonElement>(HttpMethod.Get, _baseUrl, "/decisions", null, options, cancellationToken).ConfigureAwait(false);
        return UnwrapList<DecisionDefinition>(payload, "decisions");
    }

    public Task<DecisionDefinition> deployDecisionAsync(string decisionId, string name, string resource, RequestOptions? options = null, CancellationToken cancellationToken = default)
    {
        return RequestAsync<DecisionDefinition>(HttpMethod.Post, _baseUrl, "/decisions", new
        {
            decision_id = decisionId,
            name,
            resource,
        }, options, cancellationToken, mutation: true);
    }

    public async Task<Dictionary<string, JsonElement>> evaluateDecisionAsync(string id, IReadOnlyDictionary<string, object?> inputs, RequestOptions? options = null, CancellationToken cancellationToken = default)
    {
        var payload = await RequestAsync<JsonElement>(
            HttpMethod.Post,
            _baseUrl,
            $"/decisions/{Uri.EscapeDataString(id)}/evaluate",
            new { inputs },
            options,
            cancellationToken,
            mutation: true).ConfigureAwait(false);
        if (payload.ValueKind == JsonValueKind.Object && payload.TryGetProperty("outputs", out var outputs) && outputs.ValueKind == JsonValueKind.Object)
        {
            return outputs.Deserialize<Dictionary<string, JsonElement>>(JsonUtil.Options) ?? new Dictionary<string, JsonElement>();
        }

        return new Dictionary<string, JsonElement>();
    }

    public Task<List<WorkflowInstance>> listInstancesAsync(RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<List<WorkflowInstance>>(HttpMethod.Get, _baseUrl, "/instances", null, options, cancellationToken);

    public Task<WorkflowInstance> startInstanceAsync(string workflowId, IReadOnlyDictionary<string, object?>? context = null, RequestOptions? options = null, CancellationToken cancellationToken = default)
    {
        return RequestAsync<WorkflowInstance>(HttpMethod.Post, _baseUrl, "/instances", new
        {
            workflow_id = workflowId,
            context = context ?? new Dictionary<string, object?>(),
        }, options, cancellationToken, mutation: true);
    }

    public Task<List<WorkflowInstance>> listCompletedInstancesAsync(int limit = 100, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<List<WorkflowInstance>>(HttpMethod.Get, _baseUrl, $"/instances/history/completed{QueryString(("limit", limit))}", null, options, cancellationToken);

    public Task<WorkflowInstance> getInstanceAsync(string id, GetInstanceOptions? options = null, CancellationToken cancellationToken = default)
    {
        var baseUrl = string.Equals(options?.Source, "query", StringComparison.OrdinalIgnoreCase) ? _queryBaseUrl : _baseUrl;
        return RequestAsync<WorkflowInstance>(HttpMethod.Get, baseUrl, $"/instances/{Uri.EscapeDataString(id)}", null, options, cancellationToken);
    }

    public Task deleteInstanceAsync(string id, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<object>(HttpMethod.Delete, _baseUrl, $"/instances/{Uri.EscapeDataString(id)}", null, options, cancellationToken, mutation: true);

    public Task updateInstanceVariablesAsync(string id, IReadOnlyDictionary<string, object?> variables, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<object>(HttpMethod.Post, _baseUrl, $"/instances/{Uri.EscapeDataString(id)}/variables", new { variables }, options, cancellationToken, mutation: true);

    public Task<InstanceMigrationResult> dryRunInstanceMigrationAsync(string id, InstanceMigrationRequest migration, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<InstanceMigrationResult>(HttpMethod.Post, _baseUrl, $"/instances/{Uri.EscapeDataString(id)}/migration/dry-run", migration, options, cancellationToken, mutation: true);

    public Task<InstanceMigrationResult> migrateInstanceAsync(string id, InstanceMigrationRequest migration, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<InstanceMigrationResult>(HttpMethod.Post, _baseUrl, $"/instances/{Uri.EscapeDataString(id)}/migration", migration, options, cancellationToken, mutation: true);

    public Task completeInstanceStepAsync(string instanceId, string? stepId = null, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<object>(HttpMethod.Post, _baseUrl, $"/instances/{Uri.EscapeDataString(instanceId)}/complete", new { step_id = stepId }, options, cancellationToken, mutation: true);

    public async Task<List<UserTask>> listInstanceTasksAsync(string instanceId, bool includeCompleted = false, RequestOptions? options = null, CancellationToken cancellationToken = default)
    {
        var payload = await RequestAsync<JsonElement>(
            HttpMethod.Get,
            _baseUrl,
            $"/instances/{Uri.EscapeDataString(instanceId)}/tasks{QueryString(("includeCompleted", includeCompleted))}",
            null,
            options,
            cancellationToken).ConfigureAwait(false);
        return UnwrapList<UserTask>(payload, "tasks");
    }

    /// <summary>
    /// Finds user tasks for an end user your application has already authenticated.
    /// Assignee, CandidateUser and CandidateGroups are OR-combined.
    /// </summary>
    public async Task<List<UserTask>> searchTasksAsync(SearchTasksOptions? search = null, CancellationToken cancellationToken = default)
    {
        search ??= new SearchTasksOptions();
        var pairs = new List<(string Key, object? Value)>
        {
            ("assignee", search.Assignee),
            ("candidateUser", search.CandidateUser),
        };
        foreach (var group in search.CandidateGroups ?? Array.Empty<string>())
        {
            pairs.Add(("candidateGroup", group));
        }
        pairs.Add(("includeUnassigned", search.IncludeUnassigned == true ? true : null));
        pairs.Add(("processInstanceKey", search.ProcessInstanceKey));
        pairs.Add(("state", search.State));
        pairs.Add(("limit", search.Limit));
        var payload = await RequestAsync<JsonElement>(HttpMethod.Get, _baseUrl, $"/tasks{QueryString(pairs.ToArray())}", null, search, cancellationToken).ConfigureAwait(false);
        return UnwrapList<UserTask>(payload, "tasks");
    }

    /// <summary>Claims a task for <paramref name="assignee"/>. Client tokens must name the end user; admins default to themselves.</summary>
    public Task<UserTask> claimUserTaskAsync(string instanceId, string executionId, string? assignee = null, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<UserTask>(HttpMethod.Post, _baseUrl, $"/instances/{Uri.EscapeDataString(instanceId)}/tasks/{Uri.EscapeDataString(executionId)}/claim", TaskAction(assignee), options, cancellationToken, mutation: true);

    /// <summary>Completes a task; <paramref name="assignee"/> must match the claim owner unless the caller is an admin.</summary>
    public Task completeUserTaskAsync(string instanceId, string executionId, string? assignee = null, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<object>(HttpMethod.Post, _baseUrl, $"/instances/{Uri.EscapeDataString(instanceId)}/tasks/{Uri.EscapeDataString(executionId)}/complete", TaskAction(assignee), options, cancellationToken, mutation: true);

    public async Task<List<Job>> listInstanceJobsAsync(string instanceId, RequestOptions? options = null, CancellationToken cancellationToken = default)
    {
        var payload = await RequestAsync<JsonElement>(HttpMethod.Get, _baseUrl, $"/instances/{Uri.EscapeDataString(instanceId)}/jobs", null, options, cancellationToken).ConfigureAwait(false);
        if (payload.ValueKind == JsonValueKind.Array)
        {
            return payload.Deserialize<List<Job>>(JsonUtil.Options) ?? new List<Job>();
        }

        return UnwrapList<Job>(payload, "jobs");
    }

    public Task publishSignalAsync(string signalName, IReadOnlyDictionary<string, object?>? payload = null, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<object>(HttpMethod.Post, _baseUrl, "/signals", new { signal_name = signalName, payload = payload ?? new Dictionary<string, object?>() }, options, cancellationToken, mutation: true);

    public Task publishMessageAsync(string messageName, string correlationKey, IReadOnlyDictionary<string, object?>? payload = null, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<object>(HttpMethod.Post, _baseUrl, "/messages", new { message_name = messageName, correlation_key = correlationKey, payload = payload ?? new Dictionary<string, object?>() }, options, cancellationToken, mutation: true);

    public Task publishEscalationAsync(string escalationCode, IReadOnlyDictionary<string, object?>? payload = null, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<object>(HttpMethod.Post, _baseUrl, "/escalations", new { escalation_code = escalationCode, payload = payload ?? new Dictionary<string, object?>() }, options, cancellationToken, mutation: true);

    public Task<WorkerCapabilitiesResponse> getJobCapabilitiesAsync(RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<WorkerCapabilitiesResponse>(HttpMethod.Get, _baseUrl, "/jobs/capabilities", null, WithWorkerHeaders(options), cancellationToken);

    public Task<ActivateJobsResponse> activateJobsAsync(ActivateJobsRequest request, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<ActivateJobsResponse>(HttpMethod.Post, _baseUrl, "/jobs/activate", request, WithWorkerHeaders(options), cancellationToken, mutation: true);

    public Task completeJobAsync(string key, CompleteJobRequest request, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<object>(HttpMethod.Post, _baseUrl, $"/jobs/{Uri.EscapeDataString(key)}/complete", request, WithWorkerHeaders(options), cancellationToken, mutation: true);

    public Task failJobAsync(string key, FailJobRequest request, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<object>(HttpMethod.Post, _baseUrl, $"/jobs/{Uri.EscapeDataString(key)}/fail", request, WithWorkerHeaders(options), cancellationToken, mutation: true);

    public Task extendJobLockAsync(string key, ExtendJobLockRequest request, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<object>(HttpMethod.Post, _baseUrl, $"/jobs/{Uri.EscapeDataString(key)}/extend-lock", request, WithWorkerHeaders(options), cancellationToken, mutation: true);

    public Task retryJobAsync(string key, int retries = 3, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<object>(HttpMethod.Post, _baseUrl, $"/jobs/{Uri.EscapeDataString(key)}/retry", new { retries }, options, cancellationToken, mutation: true);

    public async Task<List<Incident>> listIncidentsAsync(ListIncidentsOptions? options = null, CancellationToken cancellationToken = default)
    {
        var query = QueryString(("processInstanceKey", options?.ProcessInstanceKey), ("limit", options?.Limit));
        var payload = await RequestAsync<JsonElement>(HttpMethod.Get, _baseUrl, $"/incidents{query}", null, options, cancellationToken).ConfigureAwait(false);
        return UnwrapList<Incident>(payload, "incidents");
    }

    public Task<Incident> resolveIncidentAsync(string key, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<Incident>(HttpMethod.Post, _baseUrl, $"/incidents/{Uri.EscapeDataString(key)}/resolve", null, options, cancellationToken, mutation: true);

    public Task<EngineMetricsResponse> getEngineMetricsAsync(RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<EngineMetricsResponse>(HttpMethod.Get, _baseUrl, "/internal/metrics", null, options, cancellationToken);

    public Task<HealthResponse> queryHealthAsync(RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<HealthResponse>(HttpMethod.Get, _queryBaseUrl, "/health", null, options, cancellationToken);

    public Task<IdentityResponse> getQueryIdentityAsync(RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<IdentityResponse>(HttpMethod.Get, _queryBaseUrl, "/identity/me", null, options, cancellationToken);

    public Task<InstanceSearchResponse> searchInstancesAsync(SearchInstancesOptions? options = null, CancellationToken cancellationToken = default)
    {
        var query = QueryString(
            ("workflowId", options?.WorkflowId),
            ("state", options?.State),
            ("page", options?.Page),
            ("pageSize", options?.PageSize));
        return RequestAsync<InstanceSearchResponse>(HttpMethod.Get, _queryBaseUrl, $"/instances{query}", null, options, cancellationToken);
    }

    public Task<WorkflowInstance> getProjectedInstanceAsync(string id, RequestOptions? options = null, CancellationToken cancellationToken = default)
        => RequestAsync<WorkflowInstance>(HttpMethod.Get, _queryBaseUrl, $"/instances/{Uri.EscapeDataString(id)}", null, options, cancellationToken);

    private async Task<T> RequestAsync<T>(
        HttpMethod method,
        string baseUrl,
        string path,
        object? body,
        RequestOptions? options,
        CancellationToken cancellationToken,
        bool rawBody = false,
        bool mutation = false)
    {
        using var request = new HttpRequestMessage(method, $"{baseUrl}{path}");
        foreach (var header in await ResolveHeadersAsync(options, mutation, cancellationToken).ConfigureAwait(false))
        {
            if (header.Key.Equals("Content-Type", StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }

            request.Headers.TryAddWithoutValidation(header.Key, header.Value);
        }

        if (body != null)
        {
            if (rawBody && body is string text)
            {
                request.Content = new StringContent(text, Encoding.UTF8, "application/xml");
            }
            else
            {
                request.Content = new StringContent(JsonSerializer.Serialize(body, JsonUtil.Options), Encoding.UTF8, "application/json");
            }
        }

        using var timeoutCts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeoutCts.CancelAfter(_timeoutMs);
        HttpResponseMessage response;
        try
        {
            response = await _http.SendAsync(request, timeoutCts.Token).ConfigureAwait(false);
        }
        catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
        {
            throw new TimeoutException($"Request {method} {path} timed out after {_timeoutMs}ms");
        }

        using (response)
        {
            var responseText = await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
            if (!response.IsSuccessStatusCode)
            {
                var correlation = response.Headers.TryGetValues(ProtocolHeaders.CorrelationId, out var values)
                    ? values.FirstOrDefault()
                    : null;
                throw new ArtificialFlowApiException(method.Method, path, (int)response.StatusCode, response.ReasonPhrase, responseText, correlation);
            }

            if (response.StatusCode == System.Net.HttpStatusCode.NoContent || string.IsNullOrWhiteSpace(responseText))
            {
                return default!;
            }

            var contentType = response.Content.Headers.ContentType?.MediaType ?? "";
            if (contentType.Contains("json", StringComparison.OrdinalIgnoreCase) || responseText.TrimStart().StartsWith("{") || responseText.TrimStart().StartsWith("["))
            {
                if (typeof(T) == typeof(JsonElement))
                {
                    return (T)(object)JsonSerializer.Deserialize<JsonElement>(responseText, JsonUtil.Options);
                }

                return JsonSerializer.Deserialize<T>(responseText, JsonUtil.Options)!;
            }

            if (typeof(T) == typeof(string))
            {
                return (T)(object)responseText;
            }

            return default!;
        }
    }

    private async Task<Dictionary<string, string>> ResolveHeadersAsync(RequestOptions? options, bool mutation, CancellationToken cancellationToken)
    {
        var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase)
        {
            ["Accept"] = "application/json",
            [ProtocolHeaders.CorrelationId] = options?.CorrelationId ?? GenerateCorrelationId(),
            [ProtocolHeaders.EngineProtocol] = ProtocolHeaders.Version,
        };

        if (_defaultHeaders != null)
        {
            foreach (var header in _defaultHeaders)
            {
                headers[header.Key] = header.Value;
            }
        }

        if (_tokenProvider != null)
        {
            var token = await _tokenProvider.GetTokenAsync(cancellationToken).ConfigureAwait(false);
            if (!string.IsNullOrEmpty(token))
            {
                headers["Authorization"] = $"Bearer {token}";
            }
        }

        var tenantId = options?.TenantId ?? _tenantId;
        if (!string.IsNullOrEmpty(tenantId))
        {
            headers[ProtocolHeaders.TenantId] = tenantId;
        }

        if (!string.IsNullOrEmpty(options?.IdempotencyKey))
        {
            headers[ProtocolHeaders.IdempotencyKey] = options.IdempotencyKey!;
        }
        else if (mutation)
        {
            headers[ProtocolHeaders.IdempotencyKey] = Guid.NewGuid().ToString("N");
        }

        if (options?.Headers != null)
        {
            foreach (var header in options.Headers)
            {
                headers[header.Key] = header.Value;
            }
        }

        return headers;
    }

    private static RequestOptions WithWorkerHeaders(RequestOptions? options)
    {
        var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase)
        {
            [ProtocolHeaders.WorkerProtocol] = ProtocolHeaders.Version,
            [ProtocolHeaders.EngineProtocol] = ProtocolHeaders.Version,
        };
        if (options?.Headers != null)
        {
            foreach (var header in options.Headers)
            {
                headers[header.Key] = header.Value;
            }
        }

        return (options ?? new RequestOptions()) with { Headers = headers };
    }

    private static Dictionary<string, string> TaskAction(string? assignee)
        => string.IsNullOrWhiteSpace(assignee) ? new Dictionary<string, string>() : new Dictionary<string, string> { ["assignee"] = assignee };

    private static List<T> UnwrapList<T>(JsonElement payload, string property)
    {
        if (payload.ValueKind == JsonValueKind.Object && payload.TryGetProperty(property, out var items) && items.ValueKind == JsonValueKind.Array)
        {
            return items.Deserialize<List<T>>(JsonUtil.Options) ?? new List<T>();
        }

        if (payload.ValueKind == JsonValueKind.Array)
        {
            return payload.Deserialize<List<T>>(JsonUtil.Options) ?? new List<T>();
        }

        return new List<T>();
    }

    private static IReadOnlyDictionary<string, string> MergeHeaders(RequestOptions? options, Dictionary<string, string> extra)
    {
        var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        if (options?.Headers != null)
        {
            foreach (var header in options.Headers)
            {
                headers[header.Key] = header.Value;
            }
        }

        foreach (var header in extra)
        {
            headers[header.Key] = header.Value;
        }

        return headers;
    }

    private static string QueryString(params (string Key, object? Value)[] pairs)
    {
        var parts = new List<string>();
        foreach (var (key, value) in pairs)
        {
            if (value == null || value is string s && s.Length == 0)
            {
                continue;
            }

            var formatted = value switch
            {
                bool flag => flag ? "true" : "false",
                _ => Uri.EscapeDataString(Convert.ToString(value, System.Globalization.CultureInfo.InvariantCulture) ?? ""),
            };
            parts.Add($"{key}={formatted}");
        }

        return parts.Count == 0 ? "" : "?" + string.Join('&', parts);
    }

    private static string NormalizeBaseUrl(string value) => value.TrimEnd('/');

    private static string GenerateCorrelationId()
        => $"sdk-{DateTimeOffset.UtcNow.ToUnixTimeMilliseconds():x}-{Guid.NewGuid().ToString("N")[..8]}";
}
