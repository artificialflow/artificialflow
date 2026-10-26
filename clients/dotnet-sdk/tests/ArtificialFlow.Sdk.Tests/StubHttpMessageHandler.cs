using System.Net;
using System.Text;

namespace ArtificialFlow.Sdk.Tests;

internal sealed record RecordedRequest(string Method, string Url, Dictionary<string, string> Headers, string Body);

internal sealed class StubHttpMessageHandler : HttpMessageHandler
{
    public List<RecordedRequest> Calls { get; } = new();
    public Queue<Func<HttpRequestMessage, string, HttpResponseMessage>> Responses { get; } = new();

    public void EnqueueJson(int status, object? payload, string? contentType = "application/json", IReadOnlyDictionary<string, string>? headers = null)
    {
        Responses.Enqueue((_, _) =>
        {
            var response = new HttpResponseMessage((HttpStatusCode)status)
            {
                Content = new StringContent(payload == null ? "" : System.Text.Json.JsonSerializer.Serialize(payload), Encoding.UTF8, contentType ?? "text/plain"),
            };
            if (headers != null)
            {
                foreach (var header in headers)
                {
                    response.Headers.TryAddWithoutValidation(header.Key, header.Value);
                }
            }

            return response;
        });
    }

    public void EnqueueText(int status, string body, string contentType = "text/plain")
    {
        Responses.Enqueue((_, _) => new HttpResponseMessage((HttpStatusCode)status)
        {
            Content = new StringContent(body, Encoding.UTF8, contentType),
        });
    }

    public void Enqueue(Func<HttpRequestMessage, string, HttpResponseMessage> factory)
    {
        Responses.Enqueue(factory);
    }

    protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
    {
        var body = request.Content == null ? "" : await request.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
        var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        foreach (var header in request.Headers)
        {
            headers[header.Key] = string.Join(',', header.Value);
        }

        if (request.Content != null)
        {
            foreach (var header in request.Content.Headers)
            {
                headers[header.Key] = string.Join(',', header.Value);
            }
        }

        Calls.Add(new RecordedRequest(request.Method.Method, request.RequestUri?.ToString() ?? "", headers, body));
        if (Responses.Count == 0)
        {
            throw new InvalidOperationException($"unexpected {request.Method} {request.RequestUri}");
        }

        return Responses.Dequeue()(request, body);
    }
}
