package io.artificialflow.sdk.auth;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.net.InetAddress;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Objects;
import java.util.concurrent.CompletableFuture;
import java.util.function.LongSupplier;
import java.util.regex.Pattern;

public final class OAuthClientCredentialsTokenProvider implements TokenProvider {
    static {
        io.artificialflow.sdk.ArtificialFlowClient.allowRestrictedAuthorizationHeader();
    }

    private static final Pattern CONTROL = Pattern.compile("[\\u0000-\\u001F\\u007F]");
    private static final Pattern SCOPE_INVALID = Pattern.compile("[\\u0000-\\u001F\\u007F\\s]");
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final String tokenUrl;
    private final String clientId;
    private final String clientSecret;
    private final List<String> scopes;
    private final int refreshSkewMs;
    private final LongSupplier clock;
    private final HttpClient http;
    private final Object lock = new Object();
    private volatile CachedAccessToken cached;
    private CompletableFuture<String> refreshFuture;

    public OAuthClientCredentialsTokenProvider(OAuthClientCredentialsOptions options) {
        Objects.requireNonNull(options, "options");
        this.tokenUrl = validateTokenUrl(options.getTokenUrl());
        this.clientId = validateCredential(options.getClientId(), 256);
        this.clientSecret = validateCredential(options.getClientSecret(), 4096);
        this.scopes = validateScopes(options.getScopes());
        if (options.getRefreshSkewMs() < 0) {
            throw new OAuthClientCredentialsException("Invalid refreshSkewMs authentication setting");
        }
        this.refreshSkewMs = options.getRefreshSkewMs();
        this.clock = options.getClock() != null ? options.getClock() : System::currentTimeMillis;
        this.http = options.getHttpClient() != null
            ? options.getHttpClient()
            : HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(30)).build();
    }

    @Override
    public String getToken() {
        long now = now();
        CachedAccessToken current = cached;
        if (current != null && now < current.expiresAtMs - refreshSkewMs) {
            return current.token;
        }

        boolean owner = false;
        CompletableFuture<String> inflight;
        synchronized (lock) {
            current = cached;
            if (current != null && now < current.expiresAtMs - refreshSkewMs) {
                return current.token;
            }
            if (refreshFuture == null) {
                refreshFuture = new CompletableFuture<>();
                owner = true;
            }
            inflight = refreshFuture;
        }

        if (owner) {
            try {
                String token = exchangeToken();
                inflight.complete(token);
                return token;
            } catch (RuntimeException error) {
                inflight.completeExceptionally(error);
                throw error;
            } finally {
                synchronized (lock) {
                    if (refreshFuture == inflight) {
                        refreshFuture = null;
                    }
                }
            }
        }

        try {
            return inflight.join();
        } catch (RuntimeException error) {
            Throwable cause = error.getCause() == null ? error : error.getCause();
            if (cause instanceof RuntimeException runtime) {
                throw runtime;
            }
            throw new OAuthClientCredentialsException("OAuth client-credentials token exchange failed");
        }
    }

    private String exchangeToken() {
        StringBuilder form = new StringBuilder()
            .append("grant_type=client_credentials")
            .append("&client_id=").append(enc(clientId))
            .append("&client_secret=").append(enc(clientSecret));
        if (!scopes.isEmpty()) {
            form.append("&scope=").append(enc(String.join(" ", scopes)));
        }

        HttpRequest request = HttpRequest.newBuilder(URI.create(tokenUrl))
            .timeout(Duration.ofSeconds(30))
            .header("Accept", "application/json")
            .header("Content-Type", "application/x-www-form-urlencoded")
            .POST(HttpRequest.BodyPublishers.ofString(form.toString()))
            .build();

        HttpResponse<java.io.InputStream> response;
        try {
            response = http.send(request, HttpResponse.BodyHandlers.ofInputStream());
        } catch (Exception ignored) {
            throw new OAuthClientCredentialsException("OAuth client-credentials token exchange failed");
        }

        if (response.statusCode() < 200 || response.statusCode() >= 300) {
            try (java.io.InputStream ignored = response.body()) {
                // Intentionally unread so error payloads cannot leak into exceptions.
            } catch (Exception ignored) {
                // ignore
            }
            throw new OAuthClientCredentialsException(
                "OAuth client-credentials token exchange was rejected",
                response.statusCode());
        }

        JsonNode payload;
        try (java.io.InputStream body = response.body()) {
            payload = MAPPER.readTree(body);
        } catch (Exception ignored) {
            throw new OAuthClientCredentialsException("OAuth client-credentials token exchange returned an invalid response");
        }

        String accessToken = parseAccessToken(payload);
        long expiresIn = parseExpiresIn(payload);
        cached = new CachedAccessToken(accessToken, now() + expiresIn * 1000L);
        return accessToken;
    }

    private long now() {
        long value;
        try {
            value = clock.getAsLong();
        } catch (RuntimeException ignored) {
            throw new OAuthClientCredentialsException("Authentication clock failed");
        }
        if (value < 0) {
            throw new OAuthClientCredentialsException("Authentication clock returned an invalid value");
        }
        return value;
    }

    static String validateTokenUrl(String value) {
        if (value == null || value.isEmpty() || value.length() > 2048) {
            throw invalidProfile();
        }
        URI uri;
        try {
            uri = URI.create(value);
        } catch (IllegalArgumentException ignored) {
            throw invalidProfile();
        }
        if (!uri.isAbsolute()
            || (uri.getUserInfo() != null && !uri.getUserInfo().isEmpty())
            || (uri.getQuery() != null && !uri.getQuery().isEmpty())
            || (uri.getFragment() != null && !uri.getFragment().isEmpty())) {
            throw invalidProfile();
        }
        boolean loopback = isLoopbackHost(uri.getHost());
        if (!"https".equalsIgnoreCase(uri.getScheme())
            && !("http".equalsIgnoreCase(uri.getScheme()) && loopback)) {
            throw invalidProfile();
        }
        return uri.toString();
    }

    static boolean isLoopbackHost(String host) {
        if (host == null) {
            return false;
        }
        String normalized = host.replace("[", "").replace("]", "");
        if ("localhost".equalsIgnoreCase(normalized) || "127.0.0.1".equals(normalized) || "::1".equals(normalized)) {
            return true;
        }
        try {
            return InetAddress.getByName(normalized).isLoopbackAddress();
        } catch (Exception ignored) {
            return false;
        }
    }

    private static String validateCredential(String value, int maximum) {
        if (value == null || value.isEmpty() || value.length() > maximum || !value.equals(value.trim()) || CONTROL.matcher(value).find()) {
            throw invalidProfile();
        }
        return value;
    }

    private static List<String> validateScopes(List<String> scopes) {
        List<String> source = scopes == null ? List.of() : scopes;
        if (source.size() > 32) {
            throw invalidProfile();
        }
        List<String> copy = new ArrayList<>();
        for (String scope : source) {
            if (scope == null || scope.isEmpty() || scope.length() > 512 || !scope.equals(scope.trim()) || SCOPE_INVALID.matcher(scope).find()) {
                throw invalidProfile();
            }
            copy.add(scope);
        }
        return List.copyOf(copy);
    }

    private static String parseAccessToken(JsonNode payload) {
        if (payload == null || !payload.isObject()) {
            throw new OAuthClientCredentialsException("OAuth client-credentials token exchange returned an invalid response");
        }
        JsonNode token = payload.get("access_token");
        if (token == null || !token.isTextual()) {
            throw new OAuthClientCredentialsException("OAuth client-credentials token exchange returned an invalid response");
        }
        String accessToken = token.asText();
        if (accessToken.isEmpty() || accessToken.length() > 1024 * 1024) {
            throw new OAuthClientCredentialsException("OAuth client-credentials token exchange returned an invalid response");
        }
        JsonNode tokenType = payload.get("token_type");
        if (tokenType != null && (!tokenType.isTextual() || !"bearer".equalsIgnoreCase(tokenType.asText()))) {
            throw new OAuthClientCredentialsException("OAuth client-credentials token exchange returned an invalid response");
        }
        return accessToken;
    }

    private static long parseExpiresIn(JsonNode payload) {
        JsonNode expires = payload.get("expires_in");
        if (expires == null || !expires.canConvertToLong() || expires.isTextual() || expires.asLong() <= 0) {
            throw new OAuthClientCredentialsException("OAuth client-credentials token exchange returned an invalid response");
        }
        return expires.asLong();
    }

    private static String enc(String value) {
        return URLEncoder.encode(value, StandardCharsets.UTF_8);
    }

    private static OAuthClientCredentialsException invalidProfile() {
        return new OAuthClientCredentialsException("Invalid OAuth client-credentials profile");
    }

    private record CachedAccessToken(String token, long expiresAtMs) {}
}
