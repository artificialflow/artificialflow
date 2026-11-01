package io.artificialflow.sdk;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.artificialflow.sdk.auth.OAuthClientCredentialsTokenProvider;
import io.artificialflow.sdk.auth.StaticTokenProvider;
import io.artificialflow.sdk.auth.TokenProvider;
import io.artificialflow.sdk.model.HealthResponse;
import io.artificialflow.sdk.model.Models;
import io.artificialflow.sdk.worker.JobWorker;
import io.artificialflow.sdk.worker.JobWorkerOptions;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;

public class ArtificialFlowClient implements AutoCloseable {
    public static final String WORKER_PROTOCOL_VERSION = "v1";
    public static final String HEADER_WORKER_PROTOCOL = "X-Workflow-Worker-Protocol-Version";
    public static final String HEADER_ENGINE_PROTOCOL = "X-Workflow-Engine-Protocol-Version";
    public static final String HEADER_IDEMPOTENCY = "Idempotency-Key";
    public static final String HEADER_CORRELATION = "X-Correlation-Id";
    public static final String HEADER_TENANT = "X-Tenant-Id";

    static {
        allowRestrictedAuthorizationHeader();
    }

    private static final ObjectMapper MAPPER = new ObjectMapper();

    public static void allowRestrictedAuthorizationHeader() {
        String current = System.getProperty("jdk.httpclient.allowRestrictedHeaders", "");
        if (current.toLowerCase().contains("authorization")) {
            return;
        }
        System.setProperty(
            "jdk.httpclient.allowRestrictedHeaders",
            current.isBlank() ? "authorization" : current + ",authorization");
    }

    private final String baseUrl;
    private final String queryBaseUrl;
    private final HttpClient http;
    private final int timeoutMs;
    private final String tenantId;
    private final Map<String, String> defaultHeaders;
    private TokenProvider tokenProvider;

    public ArtificialFlowClient() {
        this(new ArtificialFlowClientOptions());
    }

    public ArtificialFlowClient(String baseUrl) {
        this(new ArtificialFlowClientOptions().baseUrl(baseUrl));
    }

    public ArtificialFlowClient(ArtificialFlowClientOptions options) {
        if (options.getAuth() != null && (options.getToken() != null || options.getTokenProvider() != null)) {
            throw new IllegalArgumentException("ArtificialFlowClient options.token and options.auth cannot be used together");
        }
        this.baseUrl = normalize(options.getBaseUrl() == null ? "http://localhost:9100/api" : options.getBaseUrl());
        this.queryBaseUrl = normalize(options.getQueryBaseUrl() == null ? this.baseUrl + "/query" : options.getQueryBaseUrl());
        this.timeoutMs = options.getTimeoutMs() <= 0 ? 30_000 : options.getTimeoutMs();
        this.tenantId = options.getTenantId();
        this.defaultHeaders = options.getHeaders() == null ? Map.of() : Map.copyOf(options.getHeaders());
        this.http = HttpClient.newBuilder().connectTimeout(Duration.ofMillis(this.timeoutMs)).build();
        if (options.getAuth() != null) {
            this.tokenProvider = new OAuthClientCredentialsTokenProvider(options.getAuth());
        } else if (options.getTokenProvider() != null) {
            this.tokenProvider = options.getTokenProvider();
        } else if (options.getToken() != null) {
            this.tokenProvider = new StaticTokenProvider(options.getToken());
        }
    }

    public void setToken(String token) {
        this.tokenProvider = token == null ? null : new StaticTokenProvider(token);
    }

    public HealthResponse commandHealth(RequestOptions options) {
        return request(baseUrl, "GET", "/health", null, options, HealthResponse.class, false);
    }

    public HealthResponse queryHealth(RequestOptions options) {
        return request(queryBaseUrl, "GET", "/health", null, options, HealthResponse.class, false);
    }

    public Models.IdentityResponse getIdentity(RequestOptions options) {
        return request(baseUrl, "GET", "/identity/me", null, options, Models.IdentityResponse.class, false);
    }

    public Models.IdentityResponse getQueryIdentity(RequestOptions options) {
        return request(queryBaseUrl, "GET", "/identity/me", null, options, Models.IdentityResponse.class, false);
    }

    public Models.IdentityConfigResponse getIdentityConfig(RequestOptions options) {
        return request(baseUrl, "GET", "/identity/config", null, options, Models.IdentityConfigResponse.class, false);
    }

    public List<Models.WorkflowDefinition> listWorkflows(ListWorkflowsOptions options) {
        if (options != null && "command".equals(options.getSource())) {
            return requestList(baseUrl, "GET", "/workflows", null, options, Models.WorkflowDefinition.class, "workflows");
        }
        return searchWorkflows(options).workflows;
    }

    public Models.WorkflowDefinition deployWorkflow(String bpmnXml, RequestOptions options) {
        return request(baseUrl, "POST", "/workflows", bpmnXml, withContentType(options, "application/xml"), Models.WorkflowDefinition.class, true, true);
    }

    public Models.WorkflowSearchResponse searchWorkflows(ListWorkflowsOptions options) {
        String query = queryString(mapOf("page", options == null ? null : options.getPage(), "pageSize", options == null ? null : options.getPageSize()));
        return request(queryBaseUrl, "GET", "/workflows" + query, null, options, Models.WorkflowSearchResponse.class, false);
    }

    public Models.WorkflowDefinition getWorkflow(String id, RequestOptions options) {
        return request(baseUrl, "GET", "/workflows/" + enc(id), null, options, Models.WorkflowDefinition.class, false);
    }

    public void deleteWorkflow(String id, RequestOptions options) {
        request(baseUrl, "DELETE", "/workflows/" + enc(id), null, options, Void.class, false);
    }

    public List<Models.DecisionDefinition> listDecisions(RequestOptions options) {
        return requestList(baseUrl, "GET", "/decisions", null, options, Models.DecisionDefinition.class, "decisions");
    }

    public Models.DecisionDefinition deployDecision(String decisionId, String name, String resource, RequestOptions options) {
        return request(baseUrl, "POST", "/decisions", Map.of(
            "decision_id", decisionId,
            "name", name,
            "resource", resource
        ), options, Models.DecisionDefinition.class, true);
    }

    public Map<String, Object> evaluateDecision(String id, Map<String, Object> inputs, RequestOptions options) {
        JsonNode response = request(baseUrl, "POST", "/decisions/" + enc(id) + "/evaluate", Map.of("inputs", inputs), options, JsonNode.class, true);
        if (response != null && response.has("outputs") && response.get("outputs").isObject()) {
            return MAPPER.convertValue(response.get("outputs"), Map.class);
        }
        return Map.of();
    }

    public List<Models.WorkflowInstance> listInstances(RequestOptions options) {
        return requestList(baseUrl, "GET", "/instances", null, options, Models.WorkflowInstance.class, "instances");
    }

    public Models.WorkflowInstance startInstance(String workflowId, Map<String, Object> context, RequestOptions options) {
        Map<String, Object> body = new LinkedHashMap<>();
        body.put("workflow_id", workflowId);
        body.put("context", context == null ? Map.of() : context);
        return request(baseUrl, "POST", "/instances", body, options, Models.WorkflowInstance.class, true);
    }

    public List<Models.WorkflowInstance> listCompletedInstances(int limit, RequestOptions options) {
        return requestList(baseUrl, "GET", "/instances/history/completed" + queryString(mapOf("limit", limit)), null, options, Models.WorkflowInstance.class, "instances");
    }

    public Models.WorkflowInstance getInstance(String id, RequestOptions options) {
        return request(baseUrl, "GET", "/instances/" + enc(id), null, options, Models.WorkflowInstance.class, false);
    }

    public void deleteInstance(String id, RequestOptions options) {
        request(baseUrl, "DELETE", "/instances/" + enc(id), null, options, Void.class, false);
    }

    public void updateInstanceVariables(String id, Map<String, Object> variables, RequestOptions options) {
        request(baseUrl, "POST", "/instances/" + enc(id) + "/variables", Map.of("variables", variables), options, Void.class, true);
    }

    public Models.InstanceMigrationResult dryRunInstanceMigration(String id, Models.InstanceMigrationRequest migration, RequestOptions options) {
        return request(baseUrl, "POST", "/instances/" + enc(id) + "/migration/dry-run", migration, options, Models.InstanceMigrationResult.class, true);
    }

    public Models.InstanceMigrationResult migrateInstance(String id, Models.InstanceMigrationRequest migration, RequestOptions options) {
        return request(baseUrl, "POST", "/instances/" + enc(id) + "/migration", migration, options, Models.InstanceMigrationResult.class, true);
    }

    public void completeInstanceStep(String instanceId, String stepId, RequestOptions options) {
        Map<String, Object> body = new LinkedHashMap<>();
        body.put("step_id", stepId);
        request(baseUrl, "POST", "/instances/" + enc(instanceId) + "/complete", body, options, Void.class, true);
    }

    public List<Models.UserTask> listInstanceTasks(String instanceId, boolean includeCompleted, RequestOptions options) {
        return requestList(baseUrl, "GET", "/instances/" + enc(instanceId) + "/tasks" + queryString(mapOf("includeCompleted", includeCompleted)), null, options, Models.UserTask.class, "tasks");
    }

    /**
     * Finds user tasks for an end user your application has already authenticated.
     * Assignee, candidateUser and candidateGroups are OR-combined.
     */
    public List<Models.UserTask> searchTasks(SearchTasksOptions options) {
        SearchTasksOptions search = options == null ? new SearchTasksOptions() : options;
        String query = queryString(mapOf(
            "assignee", search.getAssignee(),
            "candidateUser", search.getCandidateUser(),
            "candidateGroup", search.getCandidateGroups(),
            "includeUnassigned", Boolean.TRUE.equals(search.getIncludeUnassigned()) ? Boolean.TRUE : null,
            "processInstanceKey", search.getProcessInstanceKey(),
            "state", search.getState(),
            "limit", search.getLimit()
        ));
        return requestList(baseUrl, "GET", "/tasks" + query, null, search, Models.UserTask.class, "tasks");
    }

    /** Claims a task for {@code assignee}. Client tokens must name the end user; admins may pass null to claim for themselves. */
    public Models.UserTask claimUserTask(String instanceId, String executionId, String assignee, RequestOptions options) {
        return request(baseUrl, "POST", "/instances/" + enc(instanceId) + "/tasks/" + enc(executionId) + "/claim", taskAction(assignee), options, Models.UserTask.class, true);
    }

    /** Completes a task; {@code assignee} must match the claim owner unless the caller is an admin. */
    public void completeUserTask(String instanceId, String executionId, String assignee, RequestOptions options) {
        request(baseUrl, "POST", "/instances/" + enc(instanceId) + "/tasks/" + enc(executionId) + "/complete", taskAction(assignee), options, Void.class, true);
    }

    public List<Models.Job> listInstanceJobs(String instanceId, RequestOptions options) {
        return requestList(baseUrl, "GET", "/instances/" + enc(instanceId) + "/jobs", null, options, Models.Job.class, "jobs");
    }

    public void publishSignal(String signalName, Map<String, Object> payload, RequestOptions options) {
        request(baseUrl, "POST", "/signals", Map.of("signal_name", signalName, "payload", payload == null ? Map.of() : payload), options, Void.class, true);
    }

    public void publishMessage(String messageName, String correlationKey, Map<String, Object> payload, RequestOptions options) {
        request(baseUrl, "POST", "/messages", Map.of(
            "message_name", messageName,
            "correlation_key", correlationKey,
            "payload", payload == null ? Map.of() : payload
        ), options, Void.class, true);
    }

    public void publishEscalation(String escalationCode, Map<String, Object> payload, RequestOptions options) {
        request(baseUrl, "POST", "/escalations", Map.of("escalation_code", escalationCode, "payload", payload == null ? Map.of() : payload), options, Void.class, true);
    }

    public Models.WorkerCapabilitiesResponse getJobCapabilities(RequestOptions options) {
        return request(baseUrl, "GET", "/jobs/capabilities", null, withWorkerHeaders(options), Models.WorkerCapabilitiesResponse.class, false);
    }

    public Models.ActivateJobsResponse activateJobs(Models.ActivateJobsRequest body, RequestOptions options) {
        return request(baseUrl, "POST", "/jobs/activate", body, withWorkerHeaders(options), Models.ActivateJobsResponse.class, true);
    }

    public void completeJob(String key, Models.CompleteJobRequest body, RequestOptions options) {
        request(baseUrl, "POST", "/jobs/" + enc(key) + "/complete", body, withWorkerHeaders(options), Void.class, true);
    }

    public void failJob(String key, Models.FailJobRequest body, RequestOptions options) {
        request(baseUrl, "POST", "/jobs/" + enc(key) + "/fail", body, withWorkerHeaders(options), Void.class, true);
    }

    public void extendJobLock(String key, Models.ExtendJobLockRequest body, RequestOptions options) {
        request(baseUrl, "POST", "/jobs/" + enc(key) + "/extend-lock", body, withWorkerHeaders(options), Void.class, true);
    }

    public void retryJob(String key, int retries, RequestOptions options) {
        request(baseUrl, "POST", "/jobs/" + enc(key) + "/retry", Map.of("retries", retries), options, Void.class, true);
    }

    public List<Models.Incident> listIncidents(ListIncidentsOptions options) {
        String query = queryString(mapOf(
            "processInstanceKey", options == null ? null : options.getProcessInstanceKey(),
            "limit", options == null ? null : options.getLimit()
        ));
        return requestList(baseUrl, "GET", "/incidents" + query, null, options, Models.Incident.class, "incidents");
    }

    public Models.Incident resolveIncident(String key, RequestOptions options) {
        return request(baseUrl, "POST", "/incidents/" + enc(key) + "/resolve", null, options, Models.Incident.class, true);
    }

    public JsonNode getEngineMetrics(RequestOptions options) {
        return request(baseUrl, "GET", "/internal/metrics", null, options, JsonNode.class, false);
    }

    public Models.InstanceSearchResponse searchInstances(SearchInstancesOptions options) {
        String query = queryString(mapOf(
            "workflowId", options == null ? null : options.getWorkflowId(),
            "state", options == null ? null : options.getState(),
            "page", options == null ? null : options.getPage(),
            "pageSize", options == null ? null : options.getPageSize()
        ));
        return request(queryBaseUrl, "GET", "/instances" + query, null, options, Models.InstanceSearchResponse.class, false);
    }

    public Models.WorkflowInstance getProjectedInstance(String id, RequestOptions options) {
        return request(queryBaseUrl, "GET", "/instances/" + enc(id), null, options, Models.WorkflowInstance.class, false);
    }

    public JobWorker createWorker(String type, JobWorker.Handler handler, JobWorkerOptions options) {
        return new JobWorker(this, type, handler, options);
    }

    @Override
    public void close() {
        // HttpClient does not require explicit close on Java 17.
    }

    private RequestOptions withWorkerHeaders(RequestOptions options) {
        RequestOptions copy = options == null ? new RequestOptions() : options.copy();
        Map<String, String> headers = copy.getHeaders() == null ? new LinkedHashMap<>() : new LinkedHashMap<>(copy.getHeaders());
        headers.put(HEADER_WORKER_PROTOCOL, WORKER_PROTOCOL_VERSION);
        headers.put(HEADER_ENGINE_PROTOCOL, WORKER_PROTOCOL_VERSION);
        return copy.headers(headers);
    }

    private static Map<String, String> taskAction(String assignee) {
        return isBlank(assignee) ? Map.of() : Map.of("assignee", assignee);
    }

    private RequestOptions withContentType(RequestOptions options, String contentType) {
        RequestOptions copy = options == null ? new RequestOptions() : options.copy();
        Map<String, String> headers = copy.getHeaders() == null ? new LinkedHashMap<>() : new LinkedHashMap<>(copy.getHeaders());
        headers.put("Content-Type", contentType);
        return copy.headers(headers);
    }

    private <T> List<T> requestList(String base, String method, String path, Object body, RequestOptions options, Class<T> itemType, String wrapper) {
        JsonNode node = request(base, method, path, body, options, JsonNode.class, false);
        if (node == null) {
            return List.of();
        }
        if (node.isArray()) {
            return MAPPER.convertValue(node, MAPPER.getTypeFactory().constructCollectionType(List.class, itemType));
        }
        if (node.isObject() && node.has(wrapper) && node.get(wrapper).isArray()) {
            return MAPPER.convertValue(node.get(wrapper), MAPPER.getTypeFactory().constructCollectionType(List.class, itemType));
        }
        return List.of();
    }

    private <T> T request(String base, String method, String path, Object body, RequestOptions options, Class<T> type, boolean mutation) {
        return request(base, method, path, body, options, type, mutation, false);
    }

    private <T> T request(String base, String method, String path, Object body, RequestOptions options, Class<T> type, boolean mutation, boolean rawBody) {
        try {
            HttpRequest.Builder builder = HttpRequest.newBuilder(URI.create(base + path))
                .timeout(Duration.ofMillis(timeoutMs));
            Map<String, String> headers = resolveHeaders(options, mutation);
            String contentType = headers.getOrDefault("Content-Type", body == null ? null : (rawBody ? "application/xml" : "application/json"));
            headers.forEach((key, value) -> {
                if (!"Content-Type".equalsIgnoreCase(key)) {
                    builder.header(key, value);
                }
            });
            if (body == null) {
                builder.method(method, HttpRequest.BodyPublishers.noBody());
            } else {
                String payload = rawBody && body instanceof String text ? text : MAPPER.writeValueAsString(body);
                builder.header("Content-Type", contentType);
                builder.method(method, HttpRequest.BodyPublishers.ofString(payload));
            }
            HttpResponse<String> response = http.send(builder.build(), HttpResponse.BodyHandlers.ofString());
            if (response.statusCode() < 200 || response.statusCode() >= 300) {
                throw new ArtificialFlowApiException(
                    method,
                    path,
                    response.statusCode(),
                    "",
                    response.body(),
                    response.headers().firstValue(HEADER_CORRELATION).orElse(null)
                );
            }
            if (type == Void.class || response.body() == null || response.body().isBlank()) {
                return null;
            }
            if (type == JsonNode.class) {
                return type.cast(MAPPER.readTree(response.body()));
            }
            return MAPPER.readValue(response.body(), type);
        } catch (ArtificialFlowApiException error) {
            throw error;
        } catch (Exception error) {
            throw new IllegalStateException(method + " " + path + " failed", error);
        }
    }

    private Map<String, String> resolveHeaders(RequestOptions options, boolean mutation) {
        Map<String, String> headers = new LinkedHashMap<>(defaultHeaders);
        headers.put("Accept", "application/json");
        headers.put(HEADER_ENGINE_PROTOCOL, WORKER_PROTOCOL_VERSION);
        headers.put(HEADER_CORRELATION, options != null && options.getCorrelationId() != null
            ? options.getCorrelationId()
            : "sdk-" + Long.toHexString(System.currentTimeMillis()) + "-" + UUID.randomUUID().toString().substring(0, 8));
        if (tokenProvider != null) {
            String token = tokenProvider.getToken();
            if (token != null && !token.isBlank()) {
                headers.put("Authorization", "Bearer " + token);
            }
        }
        String tenant = options != null && options.getTenantId() != null ? options.getTenantId() : tenantId;
        if (tenant != null && !tenant.isBlank()) {
            headers.put(HEADER_TENANT, tenant);
        }
        if (options != null && options.getIdempotencyKey() != null) {
            headers.put(HEADER_IDEMPOTENCY, options.getIdempotencyKey());
        } else if (mutation) {
            headers.put(HEADER_IDEMPOTENCY, UUID.randomUUID().toString().replace("-", ""));
        }
        if (options != null && options.getHeaders() != null) {
            headers.putAll(options.getHeaders());
        }
        return headers;
    }

    private static String normalize(String value) {
        return value.endsWith("/") ? value.substring(0, value.length() - 1) : value;
    }

    private static String enc(String value) {
        return URLEncoder.encode(value, StandardCharsets.UTF_8).replace("+", "%20");
    }

    private static boolean isBlank(String value) {
        return value == null || value.isBlank();
    }

    private static Map<String, Object> mapOf(Object... pairs) {
        Map<String, Object> map = new LinkedHashMap<>();
        for (int i = 0; i < pairs.length; i += 2) {
            map.put((String) pairs[i], pairs[i + 1]);
        }
        return map;
    }

    private static String queryString(Map<String, Object> values) {
        List<String> parts = new ArrayList<>();
        values.forEach((key, value) -> {
            if (value == null || (value instanceof String text && text.isEmpty())) {
                return;
            }
            if (value instanceof Iterable<?> items) {
                items.forEach(item -> {
                    if (item != null && !String.valueOf(item).isEmpty()) {
                        parts.add(key + "=" + enc(String.valueOf(item)));
                    }
                });
                return;
            }
            String formatted = value instanceof Boolean flag ? (flag ? "true" : "false") : enc(String.valueOf(value));
            parts.add(key + "=" + formatted);
        });
        return parts.isEmpty() ? "" : "?" + String.join("&", parts);
    }
}
