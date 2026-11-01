using System.Text.Json;
using System.Text.Json.Serialization;

namespace ArtificialFlow.Sdk.Models;

public sealed class HealthResponse
{
    [JsonPropertyName("status")]
    public string Status { get; init; } = "";
}

public sealed class IdentityPrincipal
{
    [JsonPropertyName("subject")]
    public string Subject { get; init; } = "";

    [JsonPropertyName("issuer")]
    public string? Issuer { get; init; }

    [JsonPropertyName("audience")]
    public string[]? Audience { get; init; }

    [JsonPropertyName("email")]
    public string? Email { get; init; }

    [JsonPropertyName("name")]
    public string? Name { get; init; }

    [JsonPropertyName("tenant_id")]
    public string? TenantId { get; init; }

    [JsonPropertyName("roles")]
    public string[]? Roles { get; init; }

    [JsonPropertyName("token_mode")]
    public string? TokenMode { get; init; }
}

public sealed class IdentityResponse
{
    [JsonPropertyName("authenticated")]
    public bool Authenticated { get; init; }

    [JsonPropertyName("principal")]
    public IdentityPrincipal? Principal { get; init; }
}

public sealed class IdentityConfigResponse
{
    [JsonPropertyName("deployment_mode")]
    /// <summary>IAM_DEPLOYMENT_MODE: true = external OIDC provider, false = no IAM provider.</summary>
    public bool? DeploymentMode { get; init; }

    [JsonPropertyName("provider_name")]
    public string? ProviderName { get; init; }

    [JsonPropertyName("auth_enabled")]
    public bool AuthEnabled { get; init; }

    [JsonPropertyName("frontend_auth_enabled")]
    public bool FrontendAuthEnabled { get; init; }

    [JsonPropertyName("frontend_oidc_authority")]
    public string? FrontendOidcAuthority { get; init; }

    [JsonPropertyName("frontend_oidc_client_id")]
    public string? FrontendOidcClientId { get; init; }

    [JsonPropertyName("token_validation_mode")]
    public string? TokenValidationMode { get; init; }

    [JsonPropertyName("internal_issuer_url")]
    public string? InternalIssuerUrl { get; init; }

    [JsonPropertyName("external_issuer_url")]
    public string? ExternalIssuerUrl { get; init; }

    [JsonPropertyName("client_id")]
    public string? ClientId { get; init; }

    [JsonPropertyName("standard_roles")]
    public string[]? StandardRoles { get; init; }
}

public sealed class WorkflowDefinition
{
    [JsonPropertyName("id")]
    public string Id { get; init; } = "";

    [JsonPropertyName("process_definition_id")]
    public string? ProcessDefinitionId { get; init; }

    [JsonPropertyName("name")]
    public string? Name { get; init; }

    [JsonPropertyName("version")]
    public int Version { get; init; }

    [JsonPropertyName("resource_name")]
    public string? ResourceName { get; init; }

    [JsonPropertyName("deployment_id")]
    public string? DeploymentId { get; init; }

    [JsonPropertyName("tenant_id")]
    public string? TenantId { get; init; }

    [JsonPropertyName("resource_checksum")]
    public string? ResourceChecksum { get; init; }

    [JsonPropertyName("bpmn_xml")]
    public string? BpmnXml { get; init; }

    [JsonPropertyName("created_at")]
    public string? CreatedAt { get; init; }
}

public sealed class WorkflowSearchResponse
{
    [JsonPropertyName("workflows")]
    public List<WorkflowDefinition> Workflows { get; init; } = new();

    [JsonPropertyName("total")]
    public long Total { get; init; }
}

public sealed class DecisionDefinition
{
    [JsonPropertyName("key")]
    public string? Key { get; init; }

    [JsonPropertyName("decision_id")]
    public string DecisionId { get; init; } = "";

    [JsonPropertyName("name")]
    public string? Name { get; init; }

    [JsonPropertyName("version")]
    public int Version { get; init; }

    [JsonPropertyName("created_at")]
    public string? CreatedAt { get; init; }

    [JsonPropertyName("updated_at")]
    public string? UpdatedAt { get; init; }
}

public sealed class WorkflowInstance
{
    [JsonPropertyName("id")]
    public string Id { get; init; } = "";

    [JsonPropertyName("workflow_id")]
    public string? WorkflowId { get; init; }

    [JsonPropertyName("status")]
    public string? Status { get; init; }

    [JsonPropertyName("parent_instance_id")]
    public string? ParentInstanceId { get; init; }

    [JsonPropertyName("parent_execution_id")]
    public string? ParentExecutionId { get; init; }

    [JsonPropertyName("context")]
    public Dictionary<string, JsonElement>? Context { get; init; }

    [JsonPropertyName("created_at")]
    public string? CreatedAt { get; init; }

    [JsonPropertyName("updated_at")]
    public string? UpdatedAt { get; init; }
}

public sealed class InstanceSearchResponse
{
    [JsonPropertyName("instances")]
    public List<WorkflowInstance> Instances { get; init; } = new();

    [JsonPropertyName("total")]
    public long Total { get; init; }
}

public sealed class UserTask
{
    [JsonPropertyName("key")]
    public string Key { get; init; } = "";

    [JsonPropertyName("processInstanceKey")]
    public string ProcessInstanceKey { get; init; } = "";

    [JsonPropertyName("processDefinitionKey")]
    public string? ProcessDefinitionKey { get; init; }

    [JsonPropertyName("elementId")]
    public string? ElementId { get; init; }

    [JsonPropertyName("executionId")]
    public string ExecutionId { get; init; } = "";

    [JsonPropertyName("state")]
    public string? State { get; init; }

    [JsonPropertyName("assignee")]
    public string? Assignee { get; init; }

    [JsonPropertyName("candidateUsers")]
    public string[]? CandidateUsers { get; init; }

    [JsonPropertyName("candidateGroups")]
    public string[]? CandidateGroups { get; init; }

    [JsonPropertyName("claimedBy")]
    public string? ClaimedBy { get; init; }

    [JsonPropertyName("canClaim")]
    public bool CanClaim { get; init; }

    [JsonPropertyName("canComplete")]
    public bool CanComplete { get; init; }

    [JsonPropertyName("dueDate")]
    public string? DueDate { get; init; }

    [JsonPropertyName("createdAt")]
    public string? CreatedAt { get; init; }

    [JsonPropertyName("updatedAt")]
    public string? UpdatedAt { get; init; }
}

public sealed class InstanceMigrationRequest
{
    [JsonPropertyName("sourceDefinitionKey")]
    public string SourceDefinitionKey { get; init; } = "";

    [JsonPropertyName("targetDefinitionKey")]
    public string TargetDefinitionKey { get; init; } = "";

    [JsonPropertyName("elementMapping")]
    public Dictionary<string, string> ElementMapping { get; init; } = new();

    [JsonPropertyName("operationId")]
    public string? OperationId { get; init; }
}

public sealed class InstanceMigrationResult
{
    [JsonPropertyName("operationId")]
    public string OperationId { get; init; } = "";

    [JsonPropertyName("instanceKey")]
    public string InstanceKey { get; init; } = "";

    [JsonPropertyName("dryRun")]
    public bool DryRun { get; init; }

    [JsonPropertyName("status")]
    public string Status { get; init; } = "";

    [JsonPropertyName("reasonCode")]
    public string? ReasonCode { get; init; }

    [JsonPropertyName("message")]
    public string? Message { get; init; }

    [JsonPropertyName("sourceVersion")]
    public int? SourceVersion { get; init; }

    [JsonPropertyName("targetVersion")]
    public int? TargetVersion { get; init; }
}

public sealed class Job
{
    [JsonPropertyName("key")]
    public string Key { get; init; } = "";

    [JsonPropertyName("type")]
    public string Type { get; init; } = "";

    [JsonPropertyName("processInstanceKey")]
    public string? ProcessInstanceKey { get; init; }

    [JsonPropertyName("elementInstanceKey")]
    public string? ElementInstanceKey { get; init; }

    [JsonPropertyName("processDefinitionKey")]
    public string? ProcessDefinitionKey { get; init; }

    [JsonPropertyName("elementId")]
    public string? ElementId { get; init; }

    [JsonPropertyName("worker")]
    public string? Worker { get; init; }

    [JsonPropertyName("retries")]
    public int Retries { get; init; }

    [JsonPropertyName("state")]
    public string? State { get; init; }

    [JsonPropertyName("lockExpirationTime")]
    public string? LockExpirationTime { get; init; }

    [JsonPropertyName("createdAt")]
    public string? CreatedAt { get; init; }

    [JsonPropertyName("updatedAt")]
    public string? UpdatedAt { get; init; }
}

public sealed class ActivateJobsRequest
{
    [JsonPropertyName("type")]
    public string Type { get; init; } = "";

    [JsonPropertyName("worker")]
    public string Worker { get; init; } = "";

    [JsonPropertyName("maxJobs")]
    public int? MaxJobs { get; init; }

    [JsonPropertyName("timeoutMs")]
    public int? TimeoutMs { get; init; }

    [JsonPropertyName("lockDurationMs")]
    public int? LockDurationMs { get; init; }
}

public sealed class ActivateJobsResponse
{
    [JsonPropertyName("jobs")]
    public List<Job> Jobs { get; init; } = new();
}

public sealed class CompleteJobRequest
{
    [JsonPropertyName("worker")]
    public string Worker { get; init; } = "";

    [JsonPropertyName("variables")]
    public Dictionary<string, object?>? Variables { get; init; }
}

public sealed class FailJobRequest
{
    [JsonPropertyName("worker")]
    public string Worker { get; init; } = "";

    [JsonPropertyName("errorMessage")]
    public string ErrorMessage { get; init; } = "";

    [JsonPropertyName("retries")]
    public int? Retries { get; init; }
}

public sealed class ExtendJobLockRequest
{
    [JsonPropertyName("worker")]
    public string Worker { get; init; } = "";

    [JsonPropertyName("lockDurationMs")]
    public int LockDurationMs { get; init; }
}

public sealed class WorkerCapabilitiesResponse
{
    [JsonPropertyName("protocolVersion")]
    public string ProtocolVersion { get; init; } = "";

    [JsonPropertyName("capabilities")]
    public string[] Capabilities { get; init; } = Array.Empty<string>();
}

public sealed class Incident
{
    [JsonPropertyName("key")]
    public string? Key { get; init; }

    [JsonPropertyName("id")]
    public string? Id { get; init; }

    [JsonPropertyName("processInstanceKey")]
    public string? ProcessInstanceKey { get; init; }

    [JsonPropertyName("elementInstanceKey")]
    public string? ElementInstanceKey { get; init; }

    [JsonPropertyName("jobKey")]
    public string? JobKey { get; init; }

    [JsonPropertyName("errorType")]
    public string? ErrorType { get; init; }

    [JsonPropertyName("errorMessage")]
    public string? ErrorMessage { get; init; }

    [JsonPropertyName("state")]
    public string? State { get; init; }

    [JsonPropertyName("createdAt")]
    public string? CreatedAt { get; init; }

    [JsonPropertyName("updatedAt")]
    public string? UpdatedAt { get; init; }
}

public sealed class EngineMetricsResponse
{
    [JsonPropertyName("outboxPending")]
    public long OutboxPending { get; init; }

    [JsonPropertyName("outboxPublishSuccess")]
    public long OutboxPublishSuccess { get; init; }

    [JsonPropertyName("outboxPublishFailure")]
    public long OutboxPublishFailure { get; init; }

    [JsonPropertyName("outboxPublishLagSec")]
    public long OutboxPublishLagSec { get; init; }

    [JsonPropertyName("outboxMaxAttempts")]
    public long OutboxMaxAttempts { get; init; }

    [JsonPropertyName("idempotencyHit")]
    public long IdempotencyHit { get; init; }

    [JsonPropertyName("idempotencyMiss")]
    public long IdempotencyMiss { get; init; }
}
