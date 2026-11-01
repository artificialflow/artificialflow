export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonObject | JsonArray;
export interface JsonObject {
    [key: string]: JsonValue;
}
export interface JsonArray extends Array<JsonValue> {}

export type TokenProvider = string | (() => string | Promise<string>);

export interface OAuthClientCredentialsProfile {
    type: 'oauth-client-credentials';
    tokenUrl: string;
    clientId: string;
    clientSecret: string;
    scopes?: string[];
}

export interface OAuthClientCredentialsAuthOptions {
    type: 'oauth-client-credentials';
    profile: OAuthClientCredentialsProfile;
    refreshSkewMs?: number;
    clock?: () => number;
    fetch?: FetchLike;
}

export type ArtificialFlowAuthOptions = OAuthClientCredentialsAuthOptions;

export interface ArtificialFlowClientBaseOptions {
    baseUrl?: string;
    queryBaseUrl?: string;
    headers?: Record<string, string> | (() => Record<string, string> | Promise<Record<string, string>>);
    timeoutMs?: number;
    fetch?: FetchLike;
}

export type ArtificialFlowClientOptions = ArtificialFlowClientBaseOptions & (
    | { token?: TokenProvider; auth?: never }
    | { token?: never; auth?: ArtificialFlowAuthOptions }
);


export interface RequestOptions {
    correlationId?: string;
    idempotencyKey?: string;
    headers?: Record<string, string>;
    signal?: AbortSignal;
}

export interface FetchResponseLike {
    ok: boolean;
    status: number;
    statusText: string;
    headers: {
        get(name: string): string | null;
    };
    text(): Promise<string>;
}

export type FetchLike = (input: string, init?: {
    method?: string;
    headers?: Record<string, string>;
    body?: string;
    signal?: AbortSignal;
}) => Promise<FetchResponseLike>;

export interface HealthResponse {
    status: string;
    [key: string]: JsonValue;
}

export interface WorkflowDefinition {
    id: string;
    process_definition_id: string;
    name: string;
    version: number;
    resource_name: string;
    deployment_id: string;
    tenant_id: string;
    resource_checksum: string;
    bpmn_xml?: string;
    created_at: string;
    steps?: unknown[];
}

export interface DecisionDefinition {
    key: string;
    decision_id: string;
    name: string;
    version: number;
    created_at: string;
    updated_at: string;
}

export interface Incident {
    key: string;
    id: string;
    processInstanceKey: string;
    elementInstanceKey: string;
    jobKey?: string;
    errorType: string;
    errorMessage: string;
    state: string;
    createdAt: string;
    updatedAt: string;
}

export interface ListIncidentsOptions {
    processInstanceKey?: string | number;
    limit?: number;
}

export interface WorkflowSearchResponse {
    workflows: WorkflowDefinition[];
    total: number;
}

export interface Execution {
    id?: string;
    step_id?: string;
    status?: string;
    start_time?: string;
    [key: string]: unknown;
}

export interface WorkflowInstance {
    id: string;
    workflow_id: string;
    status: string;
    parent_instance_id?: string;
    parent_execution_id?: string;
    context: Record<string, unknown>;
    executions?: Execution[];
    created_at: string;
    updated_at: string;
}

export interface UserTask {
    key: string;
    processInstanceKey: string;
    processDefinitionKey: string;
    elementId: string;
    executionId: string;
    state: string;
    assignee?: string;
    candidateUsers?: string[];
    candidateGroups?: string[];
    claimedBy?: string;
    /** Open and unclaimed. */
    canClaim: boolean;
    /** Open and claimed. */
    canComplete: boolean;
    dueDate?: string;
    createdAt: string;
    updatedAt: string;
}

export interface InstanceSearchResponse {
    instances: WorkflowInstance[];
    total: number;
}

export interface ListOptions {
    page?: number;
    pageSize?: number;
}

export interface ListWorkflowsOptions extends ListOptions {
    source?: 'command' | 'query';
}

export interface SearchInstancesOptions extends ListOptions {
    workflowId?: string;
    state?: string;
}

export interface GetInstanceOptions extends RequestOptions {
    source?: 'command' | 'query';
}

export interface SearchTasksOptions {
    /** Tasks assigned to or claimed by this user. */
    assignee?: string;
    /** Tasks this user may act on: assignee, claim owner or candidate user. */
    candidateUser?: string;
    /** Tasks offered to any of these groups. */
    candidateGroups?: string[];
    /** Also return open tasks with no assignee, candidates or claim. */
    includeUnassigned?: boolean;
    processInstanceKey?: string | number;
    state?: 'active' | 'completed' | 'all';
    limit?: number;
}

export interface StartInstanceRequest {
    workflow_id: string;
    context?: Record<string, unknown>;
}

export interface UpdateVariablesRequest {
    variables: Record<string, unknown>;
}

export interface CompleteTaskRequest {
    step_id?: string;
}

export interface InstanceMigrationRequest {
    sourceDefinitionKey: string;
    targetDefinitionKey: string;
    elementMapping: Record<string, string>;
    operationId?: string;
}

export interface InstanceMigrationResult {
    operationId: string;
    instanceKey: string;
    dryRun: boolean;
    status: 'VALIDATED' | 'APPLIED' | 'REJECTED';
    reasonCode?: string;
    message?: string;
    sourceVersion?: number;
    targetVersion?: number;
}

export interface PublishSignalRequest {
    signal_name: string;
    payload?: Record<string, unknown>;
}

export interface PublishMessageRequest {
    message_name: string;
    correlation_key: string;
    payload?: Record<string, unknown>;
}

export interface PublishEscalationRequest {
    escalation_code: string;
    payload?: Record<string, unknown>;
}

export interface Job {
    key: string;
    type: string;
    processInstanceKey: string;
    elementInstanceKey: string;
    processDefinitionKey: string;
    elementId: string;
    worker: string;
    retries: number;
    state: string;
    lockExpirationTime?: string | null;
    createdAt: string;
    updatedAt: string;
}

export interface ActivateJobsRequest {
    type: string;
    worker: string;
    maxJobs?: number;
    timeoutMs?: number;
    lockDurationMs?: number;
}

export interface ActivateJobsResponse {
    jobs: Job[];
}

export interface CompleteJobRequest {
    worker: string;
    variables?: Record<string, unknown>;
}

export interface FailJobRequest {
    worker: string;
    errorMessage: string;
    retries?: number;
}

export interface ExtendJobLockRequest {
    worker: string;
    lockDurationMs: number;
}

export interface WorkerCapabilitiesResponse {
    protocolVersion: string;
    capabilities: string[];
}

export interface EngineMetricsResponse {
    outboxPending: number;
    outboxPublishSuccess: number;
    outboxPublishFailure: number;
    outboxPublishLagSec: number;
    outboxMaxAttempts: number;
    idempotencyHit: number;
    idempotencyMiss: number;
}

export interface IdentityPrincipal {
    subject: string;
    issuer?: string;
    audience?: string[];
    email?: string;
    name?: string;
    tenant_id?: string;
    roles?: string[];
    token_mode?: string;
    claims?: Record<string, unknown>;
}

export interface IdentityResponse {
    authenticated: boolean;
    principal?: IdentityPrincipal;
}

export interface IdentityConfigResponse {
    /** IAM_DEPLOYMENT_MODE: true = external OIDC provider, false = no IAM provider. */
    deployment_mode: boolean;
    provider_name: string;
    auth_enabled: boolean;
    frontend_auth_enabled: boolean;
    frontend_oidc_authority: string;
    frontend_oidc_client_id: string;
    token_validation_mode: string;
    internal_issuer_url: string;
    external_issuer_url: string;
    client_id: string;
    introspection_url: string;
    introspection_client_id: string;
    introspection_auth_method: string;
    enforce_audience: boolean;
    allow_insecure_issuer: boolean;
    claim_subject_path: string;
    claim_roles_path: string;
    claim_tenant_path: string;
    claim_email_path: string;
    claim_name_path: string;
    standard_roles: string[];
}
