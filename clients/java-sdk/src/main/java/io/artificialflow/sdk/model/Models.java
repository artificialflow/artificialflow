package io.artificialflow.sdk.model;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;
import com.fasterxml.jackson.databind.JsonNode;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

public final class Models {
    private Models() {}

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class IdentityPrincipal {
        public String subject;
        public String issuer;
        public List<String> audience;
        public String email;
        public String name;
        @JsonProperty("tenant_id") public String tenantId;
        public List<String> roles;
        @JsonProperty("token_mode") public String tokenMode;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class IdentityResponse {
        public boolean authenticated;
        public IdentityPrincipal principal;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class IdentityConfigResponse {
        /** IAM_DEPLOYMENT_MODE: true = external OIDC provider, false = no IAM provider. */
        @JsonProperty("deployment_mode") public Boolean deploymentMode;
        @JsonProperty("provider_name") public String providerName;
        @JsonProperty("auth_enabled") public boolean authEnabled;
        @JsonProperty("frontend_auth_enabled") public boolean frontendAuthEnabled;
        @JsonProperty("frontend_oidc_authority") public String frontendOidcAuthority;
        @JsonProperty("frontend_oidc_client_id") public String frontendOidcClientId;
        @JsonProperty("standard_roles") public List<String> standardRoles;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class WorkflowDefinition {
        public String id;
        @JsonProperty("process_definition_id") public String processDefinitionId;
        public String name;
        public int version;
        @JsonProperty("resource_name") public String resourceName;
        @JsonProperty("deployment_id") public String deploymentId;
        @JsonProperty("tenant_id") public String tenantId;
        @JsonProperty("bpmn_xml") public String bpmnXml;
        @JsonProperty("created_at") public String createdAt;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class WorkflowSearchResponse {
        public List<WorkflowDefinition> workflows = new ArrayList<>();
        public long total;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class DecisionDefinition {
        public String key;
        @JsonProperty("decision_id") public String decisionId;
        public String name;
        public int version;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class WorkflowInstance {
        public String id;
        @JsonProperty("workflow_id") public String workflowId;
        public String status;
        public JsonNode context;
        @JsonProperty("created_at") public String createdAt;
        @JsonProperty("updated_at") public String updatedAt;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class InstanceSearchResponse {
        public List<WorkflowInstance> instances = new ArrayList<>();
        public long total;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class UserTask {
        public String key;
        public String processInstanceKey;
        public String processDefinitionKey;
        public String elementId;
        public String executionId;
        public String state;
        public String assignee;
        public List<String> candidateUsers;
        public List<String> candidateGroups;
        public boolean canClaim;
        public boolean canComplete;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class InstanceMigrationRequest {
        public String sourceDefinitionKey;
        public String targetDefinitionKey;
        public Map<String, String> elementMapping = new LinkedHashMap<>();
        public String operationId;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class InstanceMigrationResult {
        public String operationId;
        public String instanceKey;
        public boolean dryRun;
        public String status;
        public String message;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class Job {
        public String key;
        public String type;
        public String processInstanceKey;
        public String elementInstanceKey;
        public String processDefinitionKey;
        public String elementId;
        public String worker;
        public int retries;
        public String state;
        public String createdAt;
        public String updatedAt;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class ActivateJobsRequest {
        public String type;
        public String worker;
        public Integer maxJobs;
        public Integer timeoutMs;
        public Integer lockDurationMs;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class ActivateJobsResponse {
        public List<Job> jobs = new ArrayList<>();
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class CompleteJobRequest {
        public String worker;
        public Map<String, Object> variables = new LinkedHashMap<>();
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class FailJobRequest {
        public String worker;
        public String errorMessage;
        public Integer retries;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class ExtendJobLockRequest {
        public String worker;
        public Integer lockDurationMs;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class WorkerCapabilitiesResponse {
        public List<String> protocols = new ArrayList<>();
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class Incident {
        public String id;
        public String key;
        public String state;
        public String processInstanceKey;
    }

    @JsonIgnoreProperties(ignoreUnknown = true)
    public static class EngineMetricsResponse {
        public JsonNode raw;
    }
}
