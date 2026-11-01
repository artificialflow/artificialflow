using System.Text.Json;
using System.Text.Json.Serialization;

namespace ArtificialFlow.Sdk;

public sealed class ProblemDetails
{
    [JsonPropertyName("type")]
    public string Type { get; init; } = "about:blank";

    [JsonPropertyName("title")]
    public string Title { get; init; } = "";

    [JsonPropertyName("status")]
    public int Status { get; init; }

    [JsonPropertyName("detail")]
    public string? Detail { get; init; }

    [JsonPropertyName("instance")]
    public string? Instance { get; init; }

    [JsonPropertyName("correlationId")]
    public string? CorrelationId { get; init; }
}

public sealed class ArtificialFlowApiException : Exception
{
    public int Status { get; }
    public string StatusText { get; }
    public string Body { get; }
    public string Method { get; }
    public string Path { get; }
    public string? CorrelationId { get; }
    public ProblemDetails? Problem { get; }

    public ArtificialFlowApiException(
        string method,
        string path,
        int status,
        string? statusText,
        string body,
        string? correlationHeader)
        : base(BuildMessage(method, path, status, statusText, body, out var problem, out var correlationId, correlationHeader))
    {
        Method = method;
        Path = path;
        Status = status;
        StatusText = statusText ?? "";
        Body = body;
        Problem = problem;
        CorrelationId = correlationId;
    }

    private static string BuildMessage(
        string method,
        string path,
        int status,
        string? statusText,
        string body,
        out ProblemDetails? problem,
        out string? correlationId,
        string? correlationHeader)
    {
        problem = null;
        correlationId = correlationHeader;
        if (!string.IsNullOrWhiteSpace(body))
        {
            try
            {
                using var document = JsonDocument.Parse(body);
                if (document.RootElement.ValueKind == JsonValueKind.Object
                    && document.RootElement.TryGetProperty("status", out var statusNode)
                    && statusNode.ValueKind == JsonValueKind.Number
                    && document.RootElement.TryGetProperty("title", out var titleNode)
                    && titleNode.ValueKind == JsonValueKind.String)
                {
                    problem = JsonSerializer.Deserialize<ProblemDetails>(body, JsonUtil.Options);
                    correlationId = problem?.CorrelationId ?? correlationHeader;
                    var detail = problem?.Detail;
                    return $"{method} {path} returned {status} {problem?.Title}{(string.IsNullOrEmpty(detail) ? "" : $": {detail}")}";
                }
            }
            catch (JsonException)
            {
                // Fall through to the generic status message.
            }
        }

        return $"{method} {path} returned {status} {statusText}{(string.IsNullOrEmpty(body) ? "" : $": {body}")}";
    }
}
