using System.Net;
using System.Text.Json;
using System.Text.RegularExpressions;

namespace ArtificialFlow.Sdk.Auth;

public sealed class OAuthClientCredentialsException : Exception
{
    public int? Status { get; }

    public OAuthClientCredentialsException(string message, int? status = null)
        : base(message)
    {
        Status = status;
    }
}

public sealed class OAuthClientCredentialsOptions
{
    public string TokenUrl { get; init; } = "";
    public string ClientId { get; init; } = "";
    public string ClientSecret { get; init; } = "";
    public IReadOnlyList<string>? Scopes { get; init; }
    public int RefreshSkewMs { get; init; } = 30_000;
    public Func<long>? Clock { get; init; }
    public HttpMessageHandler? HttpMessageHandler { get; init; }
    public HttpClient? HttpClient { get; init; }
}

public sealed class OAuthClientCredentialsTokenProvider : ITokenProvider, IDisposable
{
    private readonly string _tokenUrl;
    private readonly string _clientId;
    private readonly string _clientSecret;
    private readonly string[] _scopes;
    private readonly int _refreshSkewMs;
    private readonly Func<long> _clock;
    private readonly HttpClient _http;
    private readonly bool _ownsHttp;
    private readonly object _gate = new();
    private CachedAccessToken? _cached;
    private Task<string>? _refreshTask;

    public OAuthClientCredentialsTokenProvider(OAuthClientCredentialsOptions options)
    {
        ArgumentNullException.ThrowIfNull(options);
        _tokenUrl = ValidateTokenUrl(options.TokenUrl);
        _clientId = ValidateCredential("clientId", options.ClientId, 256);
        _clientSecret = ValidateCredential("clientSecret", options.ClientSecret, 4096);
        _scopes = ValidateScopes(options.Scopes);
        _refreshSkewMs = ValidateDuration(options.RefreshSkewMs);
        _clock = options.Clock ?? (() => DateTimeOffset.UtcNow.ToUnixTimeMilliseconds());
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
    }

    public async Task<string> GetTokenAsync(CancellationToken cancellationToken = default)
    {
        var now = Now();
        var cached = _cached;
        if (cached != null && now < cached.ExpiresAtMs - _refreshSkewMs)
        {
            return cached.Token;
        }

        Task<string> refresh;
        lock (_gate)
        {
            cached = _cached;
            if (cached != null && now < cached.ExpiresAtMs - _refreshSkewMs)
            {
                return cached.Token;
            }

            _refreshTask ??= ExchangeTokenAsync(cancellationToken);
            refresh = _refreshTask;
        }

        try
        {
            return await refresh.ConfigureAwait(false);
        }
        finally
        {
            lock (_gate)
            {
                if (ReferenceEquals(_refreshTask, refresh))
                {
                    _refreshTask = null;
                }
            }
        }
    }

    public void Dispose()
    {
        if (_ownsHttp)
        {
            _http.Dispose();
        }
    }

    private async Task<string> ExchangeTokenAsync(CancellationToken cancellationToken)
    {
        var pairs = new List<KeyValuePair<string, string>>
        {
            new("grant_type", "client_credentials"),
            new("client_id", _clientId),
            new("client_secret", _clientSecret),
        };
        if (_scopes.Length > 0)
        {
            pairs.Add(new("scope", string.Join(' ', _scopes)));
        }

        using var content = new FormUrlEncodedContent(pairs);
        using var request = new HttpRequestMessage(HttpMethod.Post, _tokenUrl)
        {
            Content = content,
        };
        request.Headers.Accept.ParseAdd("application/json");

        HttpResponseMessage response;
        try
        {
            response = await _http.SendAsync(request, cancellationToken).ConfigureAwait(false);
        }
        catch (Exception)
        {
            throw new OAuthClientCredentialsException("OAuth client-credentials token exchange failed");
        }

        using (response)
        {
            if (!response.IsSuccessStatusCode)
            {
                throw new OAuthClientCredentialsException(
                    "OAuth client-credentials token exchange was rejected",
                    (int)response.StatusCode);
            }

            string payload;
            try
            {
                payload = await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
            }
            catch (Exception)
            {
                throw new OAuthClientCredentialsException("OAuth client-credentials token exchange returned an invalid response");
            }

            var token = ParseTokenResponse(payload);
            var now = Now();
            _cached = new CachedAccessToken(token.AccessToken, now + token.ExpiresInSeconds * 1000L);
            return token.AccessToken;
        }
    }

    private long Now()
    {
        long value;
        try
        {
            value = _clock();
        }
        catch (Exception)
        {
            throw new OAuthClientCredentialsException("Authentication clock failed");
        }

        if (value < 0)
        {
            throw new OAuthClientCredentialsException("Authentication clock returned an invalid value");
        }

        return value;
    }

    internal static string ValidateTokenUrl(string? value)
    {
        if (string.IsNullOrEmpty(value) || value.Length > 2048)
        {
            throw InvalidProfile();
        }

        if (!Uri.TryCreate(value, UriKind.Absolute, out var uri))
        {
            throw InvalidProfile();
        }

        if (!string.IsNullOrEmpty(uri.UserInfo) || !string.IsNullOrEmpty(uri.Query) || !string.IsNullOrEmpty(uri.Fragment))
        {
            throw InvalidProfile();
        }

        var loopback = IsLoopbackHost(uri.Host);
        if ((uri.Scheme != Uri.UriSchemeHttps && !(uri.Scheme == Uri.UriSchemeHttp && loopback)))
        {
            throw InvalidProfile();
        }

        return uri.AbsoluteUri;
    }

    internal static bool IsLoopbackHost(string host)
    {
        var normalized = host.Trim().TrimStart('[').TrimEnd(']');
        if (normalized.Equals("localhost", StringComparison.OrdinalIgnoreCase)
            || normalized is "127.0.0.1" or "::1")
        {
            return true;
        }

        return IPAddress.TryParse(normalized, out var address) && IPAddress.IsLoopback(address);
    }

    private static string ValidateCredential(string name, string? value, int maximum)
    {
        _ = name;
        if (string.IsNullOrEmpty(value)
            || value.Length > maximum
            || value.Trim() != value
            || Regex.IsMatch(value, @"[\u0000-\u001F\u007F]"))
        {
            throw InvalidProfile();
        }

        return value;
    }

    private static string[] ValidateScopes(IReadOnlyList<string>? scopes)
    {
        scopes ??= Array.Empty<string>();
        if (scopes.Count > 32
            || scopes.Any(scope => string.IsNullOrEmpty(scope)
                || scope.Length > 512
                || scope.Trim() != scope
                || Regex.IsMatch(scope, @"[\u0000-\u001F\u007F\s]")))
        {
            throw InvalidProfile();
        }

        return scopes.ToArray();
    }

    private static int ValidateDuration(int value)
    {
        if (value < 0)
        {
            throw new OAuthClientCredentialsException("Invalid refreshSkewMs authentication setting");
        }

        return value;
    }

    private static (string AccessToken, long ExpiresInSeconds) ParseTokenResponse(string payload)
    {
        JsonDocument document;
        try
        {
            document = JsonDocument.Parse(payload);
        }
        catch (JsonException)
        {
            throw new OAuthClientCredentialsException("OAuth client-credentials token exchange returned an invalid response");
        }

        using (document)
        {
            if (document.RootElement.ValueKind != JsonValueKind.Object)
            {
                throw new OAuthClientCredentialsException("OAuth client-credentials token exchange returned an invalid response");
            }

            if (!document.RootElement.TryGetProperty("access_token", out var tokenNode)
                || tokenNode.ValueKind != JsonValueKind.String)
            {
                throw new OAuthClientCredentialsException("OAuth client-credentials token exchange returned an invalid response");
            }

            var accessToken = tokenNode.GetString() ?? "";
            if (accessToken.Length == 0 || accessToken.Length > 1024 * 1024)
            {
                throw new OAuthClientCredentialsException("OAuth client-credentials token exchange returned an invalid response");
            }

            if (document.RootElement.TryGetProperty("token_type", out var tokenType)
                && tokenType.ValueKind == JsonValueKind.String
                && !string.Equals(tokenType.GetString(), "bearer", StringComparison.OrdinalIgnoreCase))
            {
                throw new OAuthClientCredentialsException("OAuth client-credentials token exchange returned an invalid response");
            }

            if (!document.RootElement.TryGetProperty("expires_in", out var expires)
                || expires.ValueKind != JsonValueKind.Number
                || !expires.TryGetInt64(out var expiresIn)
                || expiresIn <= 0)
            {
                throw new OAuthClientCredentialsException("OAuth client-credentials token exchange returned an invalid response");
            }

            return (accessToken, expiresIn);
        }
    }

    private static OAuthClientCredentialsException InvalidProfile()
    {
        return new OAuthClientCredentialsException("Invalid OAuth client-credentials profile");
    }

    private sealed record CachedAccessToken(string Token, long ExpiresAtMs);
}
