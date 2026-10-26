package io.artificialflow.sdk;

import java.util.LinkedHashMap;
import java.util.Map;

public class RequestOptions {
    private String correlationId;
    private String idempotencyKey;
    private String tenantId;
    private Map<String, String> headers;

    public String getCorrelationId() {
        return correlationId;
    }

    public RequestOptions correlationId(String correlationId) {
        this.correlationId = correlationId;
        return this;
    }

    public String getIdempotencyKey() {
        return idempotencyKey;
    }

    public RequestOptions idempotencyKey(String idempotencyKey) {
        this.idempotencyKey = idempotencyKey;
        return this;
    }

    public String getTenantId() {
        return tenantId;
    }

    public RequestOptions tenantId(String tenantId) {
        this.tenantId = tenantId;
        return this;
    }

    public Map<String, String> getHeaders() {
        return headers;
    }

    public RequestOptions headers(Map<String, String> headers) {
        this.headers = headers == null ? null : new LinkedHashMap<>(headers);
        return this;
    }

    public RequestOptions copy() {
        return new RequestOptions()
            .correlationId(correlationId)
            .idempotencyKey(idempotencyKey)
            .tenantId(tenantId)
            .headers(headers);
    }
}
