using System.Net;
using System.Text;
using ArtificialFlow.Sdk.Auth;
using Xunit;

namespace ArtificialFlow.Sdk.Tests;

public sealed class AuthTests
{
    private static readonly OAuthClientCredentialsOptions Profile = new()
    {
        TokenUrl = "https://iam.example/realms/artificialflow/protocol/openid-connect/token",
        ClientId = "artificialflow-sdk-client",
        ClientSecret = "private-client-secret",
        Scopes = new[] { "openid" },
    };

    [Fact]
    public async Task OAuth_caches_and_refreshes_with_skew()
    {
        var now = 1_700_000_000_000L;
        var handler = new StubHttpMessageHandler();
        handler.EnqueueJson(200, new { access_token = "oauth-one", token_type = "Bearer", expires_in = 60 });
        handler.EnqueueJson(200, new { access_token = "oauth-two", token_type = "Bearer", expires_in = 60 });

        using var provider = new OAuthClientCredentialsTokenProvider(new OAuthClientCredentialsOptions
        {
            TokenUrl = Profile.TokenUrl,
            ClientId = Profile.ClientId,
            ClientSecret = Profile.ClientSecret,
            Scopes = Profile.Scopes,
            Clock = () => now,
            RefreshSkewMs = 10_000,
            HttpMessageHandler = handler,
        });

        Assert.Equal("oauth-one", await provider.GetTokenAsync());
        Assert.Equal("oauth-one", await provider.GetTokenAsync());
        Assert.Single(handler.Calls);
        Assert.Equal(Profile.TokenUrl, handler.Calls[0].Url);
        Assert.Equal("POST", handler.Calls[0].Method);
        Assert.Contains("application/x-www-form-urlencoded", handler.Calls[0].Headers["Content-Type"]);
        var form = ParseForm(handler.Calls[0].Body);
        Assert.Equal("client_credentials", form["grant_type"]);
        Assert.Equal(Profile.ClientId, form["client_id"]);
        Assert.Equal(Profile.ClientSecret, form["client_secret"]);
        Assert.Equal("openid", form["scope"]);

        now += 50_001;
        Assert.Equal("oauth-two", await provider.GetTokenAsync());
        Assert.Equal(2, handler.Calls.Count);
    }

    [Fact]
    public async Task OAuth_single_flight_and_failure_recovery_never_leaks_secrets()
    {
        var calls = 0;
        var handler = new StubHttpMessageHandler();
        handler.Enqueue((_, _) =>
        {
            Interlocked.Increment(ref calls);
            return Json(200, new { access_token = "shared-access-token", token_type = "bearer", expires_in = 60 });
        });

        using var provider = new OAuthClientCredentialsTokenProvider(new OAuthClientCredentialsOptions
        {
            TokenUrl = Profile.TokenUrl,
            ClientId = Profile.ClientId,
            ClientSecret = Profile.ClientSecret,
            HttpMessageHandler = handler,
        });

        var pending = new[]
        {
            provider.GetTokenAsync(),
            provider.GetTokenAsync(),
            provider.GetTokenAsync(),
        };
        Assert.Equal(new[] { "shared-access-token", "shared-access-token", "shared-access-token" }, await Task.WhenAll(pending));
        Assert.Equal(1, calls);

        var rejectedReads = 0;
        var attempt = 0;
        var retryHandler = new StubHttpMessageHandler();
        retryHandler.Enqueue((_, _) =>
        {
            attempt++;
            if (attempt == 1)
            {
                return new HttpResponseMessage(HttpStatusCode.Unauthorized)
                {
                    Content = new TrackingStringContent("{\"error\":\"invalid_client\",\"client_secret\":\"private-client-secret\"}", () => Interlocked.Increment(ref rejectedReads)),
                };
            }

            return Json(200, new { access_token = "recovered-token", expires_in = 60 });
        });
        retryHandler.Enqueue((_, _) =>
        {
            attempt++;
            return Json(200, new { access_token = "recovered-token", expires_in = 60 });
        });

        using var retrying = new OAuthClientCredentialsTokenProvider(new OAuthClientCredentialsOptions
        {
            TokenUrl = Profile.TokenUrl,
            ClientId = Profile.ClientId,
            ClientSecret = Profile.ClientSecret,
            HttpMessageHandler = retryHandler,
        });

        var rejected = await Assert.ThrowsAsync<OAuthClientCredentialsException>(() => retrying.GetTokenAsync());
        Assert.Equal(401, rejected.Status);
        Assert.Equal("OAuth client-credentials token exchange was rejected", rejected.Message);
        Assert.DoesNotContain(Profile.ClientSecret, rejected.Message);
        Assert.DoesNotContain(Profile.ClientSecret, rejected.ToString());
        _ = rejectedReads;
        Assert.Equal("recovered-token", await retrying.GetTokenAsync());
        Assert.Equal(2, attempt);
    }

    [Fact]
    public void OAuth_rejects_http_non_loopback_token_url()
    {
        var error = Assert.Throws<OAuthClientCredentialsException>(() => new OAuthClientCredentialsTokenProvider(new OAuthClientCredentialsOptions
        {
            TokenUrl = "http://iam.example/token",
            ClientId = Profile.ClientId,
            ClientSecret = Profile.ClientSecret,
        }));
        Assert.Equal("Invalid OAuth client-credentials profile", error.Message);
        Assert.DoesNotContain(Profile.ClientSecret, error.ToString());
    }

    [Fact]
    public void OAuth_allows_http_loopback_token_url()
    {
        using var provider = new OAuthClientCredentialsTokenProvider(new OAuthClientCredentialsOptions
        {
            TokenUrl = "http://127.0.0.1/token",
            ClientId = Profile.ClientId,
            ClientSecret = Profile.ClientSecret,
            HttpMessageHandler = new StubHttpMessageHandler(),
        });
        Assert.NotNull(provider);
    }

    [Fact]
    public async Task Client_health_uses_oauth_bearer_token()
    {
        var handler = new StubHttpMessageHandler();
        handler.Enqueue((request, _) => (request.RequestUri?.AbsoluteUri ?? "").Contains("/token", StringComparison.Ordinal)
            ? Json(200, new { access_token = "oauth-client-token", token_type = "Bearer", expires_in = 60 })
            : Json(200, new { status = "ok" }));
        handler.EnqueueJson(200, new { status = "ok" });

        using var client = new ArtificialFlowClient(new ArtificialFlowClientOptions
        {
            BaseUrl = "https://api.artificialflow.example.io",
            Auth = new OAuthClientCredentialsOptions
            {
                TokenUrl = Profile.TokenUrl,
                ClientId = Profile.ClientId,
                ClientSecret = Profile.ClientSecret,
            },
            HttpMessageHandler = handler,
        });

        var health = await client.commandHealthAsync();
        Assert.Equal("ok", health.Status);
        Assert.Equal(2, handler.Calls.Count);
        Assert.Equal("Bearer oauth-client-token", handler.Calls[1].Headers["Authorization"]);
    }

    [Fact]
    public async Task Invalid_token_responses_do_not_include_secrets()
    {
        foreach (var payload in new object[]
        {
            new { },
            new { access_token = "", expires_in = 60 },
            new { access_token = "token", token_type = "Basic", expires_in = 60 },
            new { access_token = "token", expires_in = 0 },
            new { access_token = "token", expires_in = "60" },
        })
        {
            var handler = new StubHttpMessageHandler();
            handler.EnqueueJson(200, payload);
            using var provider = new OAuthClientCredentialsTokenProvider(new OAuthClientCredentialsOptions
            {
                TokenUrl = Profile.TokenUrl,
                ClientId = Profile.ClientId,
                ClientSecret = Profile.ClientSecret,
                HttpMessageHandler = handler,
            });
            var error = await Assert.ThrowsAsync<OAuthClientCredentialsException>(() => provider.GetTokenAsync());
            Assert.Equal("OAuth client-credentials token exchange returned an invalid response", error.Message);
            Assert.DoesNotContain(Profile.ClientSecret, error.ToString());
        }
    }

    private static HttpResponseMessage Json(int status, object payload)
    {
        return new HttpResponseMessage((HttpStatusCode)status)
        {
            Content = new StringContent(System.Text.Json.JsonSerializer.Serialize(payload), Encoding.UTF8, "application/json"),
        };
    }

    private static Dictionary<string, string> ParseForm(string body)
    {
        return body.Split('&', StringSplitOptions.RemoveEmptyEntries)
            .Select(part => part.Split('=', 2))
            .ToDictionary(part => Uri.UnescapeDataString(part[0].Replace('+', ' ')), part => part.Length > 1 ? Uri.UnescapeDataString(part[1].Replace('+', ' ')) : "");
    }

    private sealed class TrackingStringContent : StringContent
    {
        private readonly Action _onRead;

        public TrackingStringContent(string content, Action onRead)
            : base(content, Encoding.UTF8, "application/json")
        {
            _onRead = onRead;
        }

        protected override Task<Stream> CreateContentReadStreamAsync()
        {
            _onRead();
            return base.CreateContentReadStreamAsync();
        }

        protected override Task SerializeToStreamAsync(Stream stream, TransportContext? context)
        {
            _onRead();
            return base.SerializeToStreamAsync(stream, context);
        }
    }
}
