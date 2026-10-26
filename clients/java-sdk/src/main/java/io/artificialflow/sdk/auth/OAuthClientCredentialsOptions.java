package io.artificialflow.sdk.auth;

import java.net.http.HttpClient;
import java.util.List;
import java.util.function.LongSupplier;

public class OAuthClientCredentialsOptions {
    private String tokenUrl;
    private String clientId;
    private String clientSecret;
    private List<String> scopes;
    private int refreshSkewMs = 30_000;
    private LongSupplier clock;
    private HttpClient httpClient;

    public String getTokenUrl() {
        return tokenUrl;
    }

    public OAuthClientCredentialsOptions tokenUrl(String tokenUrl) {
        this.tokenUrl = tokenUrl;
        return this;
    }

    public String getClientId() {
        return clientId;
    }

    public OAuthClientCredentialsOptions clientId(String clientId) {
        this.clientId = clientId;
        return this;
    }

    public String getClientSecret() {
        return clientSecret;
    }

    public OAuthClientCredentialsOptions clientSecret(String clientSecret) {
        this.clientSecret = clientSecret;
        return this;
    }

    public List<String> getScopes() {
        return scopes;
    }

    public OAuthClientCredentialsOptions scopes(List<String> scopes) {
        this.scopes = scopes;
        return this;
    }

    public int getRefreshSkewMs() {
        return refreshSkewMs;
    }

    public OAuthClientCredentialsOptions refreshSkewMs(int refreshSkewMs) {
        this.refreshSkewMs = refreshSkewMs;
        return this;
    }

    public LongSupplier getClock() {
        return clock;
    }

    public OAuthClientCredentialsOptions clock(LongSupplier clock) {
        this.clock = clock;
        return this;
    }

    public HttpClient getHttpClient() {
        return httpClient;
    }

    public OAuthClientCredentialsOptions httpClient(HttpClient httpClient) {
        this.httpClient = httpClient;
        return this;
    }
}
