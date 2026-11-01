package io.artificialflow.sdk;

import io.artificialflow.sdk.auth.OAuthClientCredentialsOptions;
import io.artificialflow.sdk.auth.TokenProvider;
import java.util.Map;

public class ArtificialFlowClientOptions {
    private String baseUrl;
    private String queryBaseUrl;
    private String token;
    private TokenProvider tokenProvider;
    private OAuthClientCredentialsOptions auth;
    private String tenantId;
    private Map<String, String> headers;
    private int timeoutMs = 30_000;

    public String getBaseUrl() {
        return baseUrl;
    }

    public ArtificialFlowClientOptions baseUrl(String baseUrl) {
        this.baseUrl = baseUrl;
        return this;
    }

    public String getQueryBaseUrl() {
        return queryBaseUrl;
    }

    public ArtificialFlowClientOptions queryBaseUrl(String queryBaseUrl) {
        this.queryBaseUrl = queryBaseUrl;
        return this;
    }

    public String getToken() {
        return token;
    }

    public ArtificialFlowClientOptions token(String token) {
        this.token = token;
        return this;
    }

    public TokenProvider getTokenProvider() {
        return tokenProvider;
    }

    public ArtificialFlowClientOptions tokenProvider(TokenProvider tokenProvider) {
        this.tokenProvider = tokenProvider;
        return this;
    }

    public OAuthClientCredentialsOptions getAuth() {
        return auth;
    }

    public ArtificialFlowClientOptions auth(OAuthClientCredentialsOptions auth) {
        this.auth = auth;
        return this;
    }

    public String getTenantId() {
        return tenantId;
    }

    public ArtificialFlowClientOptions tenantId(String tenantId) {
        this.tenantId = tenantId;
        return this;
    }

    public Map<String, String> getHeaders() {
        return headers;
    }

    public ArtificialFlowClientOptions headers(Map<String, String> headers) {
        this.headers = headers;
        return this;
    }

    public int getTimeoutMs() {
        return timeoutMs;
    }

    public ArtificialFlowClientOptions timeoutMs(int timeoutMs) {
        this.timeoutMs = timeoutMs;
        return this;
    }
}
