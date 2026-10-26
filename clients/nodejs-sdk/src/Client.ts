import { Worker, WorkerHandler, WorkerOptions } from './Worker';
import { OAuthClientCredentialsAuthProvider } from './Auth';
import {
    ActivateJobsRequest,
    ActivateJobsResponse,
    EngineMetricsResponse,
    ExtendJobLockRequest,
    FailJobRequest,
    FetchLike,
    FetchResponseLike,
    GetInstanceOptions,
    HealthResponse,
    IdentityConfigResponse,
    IdentityResponse,
    InstanceSearchResponse,
    InstanceMigrationRequest,
    InstanceMigrationResult,
    ListOptions,
    ListWorkflowsOptions,
    PublishEscalationRequest,
    PublishMessageRequest,
    PublishSignalRequest,
    RequestOptions,
    SearchInstancesOptions,
    SearchTasksOptions,
    StartInstanceRequest,
    UpdateVariablesRequest,
    UserTask,
    WorkerCapabilitiesResponse,
    WorkflowDefinition,
    WorkflowInstance,
    WorkflowSearchResponse,
    ArtificialFlowClientOptions,
    CompleteJobRequest,
    DecisionDefinition,
    Incident,
    Job,
    ListIncidentsOptions,
    TokenProvider,
} from './types';

export const WorkerProtocolVersion = 'v1';
export const HeaderWorkerProtocolVersion = 'X-Workflow-Worker-Protocol-Version';
export const HeaderEngineProtocolVersion = 'X-Workflow-Engine-Protocol-Version';
export const IdempotencyKeyHeader = 'Idempotency-Key';

type RequestBody = unknown;
type AuthProvider = {
    getToken(): Promise<string>;
};

export class ArtificialFlowApiError extends Error {
    public readonly status: number;
    public readonly statusText: string;
    public readonly body: string;
    public readonly method: string;
    public readonly path: string;
    public readonly correlationId?: string;
    public readonly problem?: {
        type: string;
        title: string;
        status: number;
        detail?: string;
        instance?: string;
        correlationId?: string;
    };

    constructor(method: string, path: string, response: FetchResponseLike, body: string) {
        super(`${method} ${path} returned ${response.status} ${response.statusText}${body ? `: ${body}` : ''}`);
        this.name = 'ArtificialFlowApiError';
        this.status = response.status;
        this.statusText = response.statusText;
        this.body = body;
        this.method = method;
        this.path = path;
        try {
            const parsed = JSON.parse(body) as ArtificialFlowApiError['problem'];
            if (parsed && typeof parsed === 'object' && typeof parsed.status === 'number' && typeof parsed.title === 'string') {
                this.problem = parsed;
                this.correlationId = parsed.correlationId || response.headers.get('x-correlation-id') || undefined;
                this.message = `${method} ${path} returned ${response.status} ${parsed.title}${parsed.detail ? `: ${parsed.detail}` : ''}`;
            }
        } catch {
            this.correlationId = response.headers.get('x-correlation-id') || undefined;
        }
    }
}

export class ArtificialFlowClient {
    private baseUrl: string;
    private queryBaseUrl: string;
    private token?: TokenProvider;
    private authProvider?: AuthProvider;
    private headers?: ArtificialFlowClientOptions['headers'];
    private timeoutMs: number;
    private fetchImpl: FetchLike;

    constructor(options: ArtificialFlowClientOptions | string = {}) {
        const resolvedOptions = typeof options === 'string' ? { baseUrl: options } : options;
        if (resolvedOptions.token !== undefined && resolvedOptions.auth !== undefined) {
            throw new Error('ArtificialFlowClient options.token and options.auth cannot be used together');
        }
        this.baseUrl = normalizeBaseUrl(resolvedOptions.baseUrl || 'http://localhost:9100/api/v1');
        this.queryBaseUrl = normalizeBaseUrl(resolvedOptions.queryBaseUrl || `${this.baseUrl}/query`);
        this.fetchImpl = resolvedOptions.fetch || defaultFetch();
        this.token = resolvedOptions.token;
        if (resolvedOptions.auth?.type === 'oauth-client-credentials') {
            this.authProvider = new OAuthClientCredentialsAuthProvider(
                resolvedOptions.auth,
                this.fetchImpl,
            );
        }
        this.headers = resolvedOptions.headers;
        this.timeoutMs = resolvedOptions.timeoutMs ?? 30000;
    }

    public setToken(token: string | undefined): void {
        this.authProvider = undefined;
        this.token = token;
    }

    public async health(options?: RequestOptions): Promise<HealthResponse> {
        return this.request<HealthResponse>('GET', '/health', undefined, options);
    }

    public async queryHealth(options?: RequestOptions): Promise<HealthResponse> {
        return this.queryRequest<HealthResponse>('GET', '/health', undefined, options);
    }

    public async commandHealth(options?: RequestOptions): Promise<HealthResponse> {
        return this.health(options);
    }

    public async deployWorkflow(bpmnXml: string, options?: RequestOptions): Promise<WorkflowDefinition> {
        return this.request<WorkflowDefinition>('POST', '/workflows', bpmnXml, {
            ...options,
            headers: { ...options?.headers, 'Content-Type': 'application/xml' },
        });
    }

    public async listWorkflows(options: ListWorkflowsOptions & RequestOptions = {}): Promise<WorkflowDefinition[]> {
        if (options.source === 'command') {
            return this.request<WorkflowDefinition[]>('GET', '/workflows', undefined, options);
        }
        const response = await this.searchWorkflows(options);
        return response.workflows;
    }

    public async getWorkflows(options: ListWorkflowsOptions & RequestOptions = {}): Promise<WorkflowDefinition[]> {
        return this.listWorkflows(options);
    }

    public async searchWorkflows(options: ListOptions & RequestOptions = {}): Promise<WorkflowSearchResponse> {
        return this.queryRequest<WorkflowSearchResponse>('GET', `/workflows${queryString(options, ['page', 'pageSize'])}`, undefined, options);
    }

    public async getWorkflow(id: string, options?: RequestOptions): Promise<WorkflowDefinition> {
        return this.request<WorkflowDefinition>('GET', `/workflows/${encodeURIComponent(id)}`, undefined, options);
    }

    public async deleteWorkflow(id: string, options?: RequestOptions): Promise<void> {
        await this.request<void>('DELETE', `/workflows/${encodeURIComponent(id)}`, undefined, options);
    }

    public async listDecisions(options?: RequestOptions): Promise<DecisionDefinition[]> {
        const response = await this.request<{ decisions?: DecisionDefinition[] }>('GET', '/decisions', undefined, options);
        return response.decisions || [];
    }

    public async deployDecision(decisionId: string, name: string, resource: string, options?: RequestOptions): Promise<DecisionDefinition> {
        return this.request<DecisionDefinition>('POST', '/decisions', {
            decision_id: decisionId,
            name,
            resource,
        }, options);
    }

    public async evaluateDecision(id: string, inputs: Record<string, unknown>, options?: RequestOptions): Promise<Record<string, unknown>> {
        const response = await this.request<{ outputs?: Record<string, unknown> }>(
            'POST',
            `/decisions/${encodeURIComponent(id)}/evaluate`,
            { inputs },
            options,
        );
        return response.outputs || {};
    }

    public async startInstance(workflowId: string, context?: Record<string, unknown>, options?: RequestOptions): Promise<WorkflowInstance>;
    public async startInstance(request: StartInstanceRequest, options?: RequestOptions): Promise<WorkflowInstance>;
    public async startInstance(
        workflowIdOrRequest: string | StartInstanceRequest,
        contextOrOptions: Record<string, unknown> | RequestOptions = {},
        maybeOptions: RequestOptions = {},
    ): Promise<WorkflowInstance> {
        const request = typeof workflowIdOrRequest === 'string'
            ? { workflow_id: workflowIdOrRequest, context: contextOrOptions as Record<string, unknown> }
            : workflowIdOrRequest;
        const options = typeof workflowIdOrRequest === 'string' ? maybeOptions : contextOrOptions as RequestOptions;
        return this.request<WorkflowInstance>('POST', '/instances', { ...request, context: request.context || {} }, options);
    }

    public async listActiveInstances(options?: RequestOptions): Promise<WorkflowInstance[]> {
        return this.request<WorkflowInstance[]>('GET', '/instances', undefined, options);
    }

    public async listInstances(options?: RequestOptions): Promise<WorkflowInstance[]> {
        return this.listActiveInstances(options);
    }

    public async listCompletedInstances(limit = 100, options?: RequestOptions): Promise<WorkflowInstance[]> {
        return this.request<WorkflowInstance[]>('GET', `/instances/history/completed${queryString({ limit }, ['limit'])}`, undefined, options);
    }

    public async getInstances(options: SearchInstancesOptions & RequestOptions = {}): Promise<WorkflowInstance[]> {
        const response = await this.searchInstances(options);
        return response.instances;
    }

    public async searchInstances(options: SearchInstancesOptions & RequestOptions = {}): Promise<InstanceSearchResponse> {
        return this.queryRequest<InstanceSearchResponse>('GET', `/instances${queryString(options, ['workflowId', 'state', 'page', 'pageSize'])}`, undefined, options);
    }

    public async getInstance(id: string, options: GetInstanceOptions = {}): Promise<WorkflowInstance> {
        if (options.source === 'query') {
            return this.queryRequest<WorkflowInstance>('GET', `/instances/${encodeURIComponent(id)}`, undefined, options);
        }
        return this.request<WorkflowInstance>('GET', `/instances/${encodeURIComponent(id)}`, undefined, options);
    }

    public async getProjectedInstance(id: string, options?: RequestOptions): Promise<WorkflowInstance> {
        return this.queryRequest<WorkflowInstance>('GET', `/instances/${encodeURIComponent(id)}`, undefined, options);
    }

    /**
     * Finds user tasks for an end user that your application has already
     * authenticated. assignee, candidateUser and candidateGroups are OR-combined.
     */
    public async searchTasks(search: SearchTasksOptions = {}, options?: RequestOptions): Promise<UserTask[]> {
        const params = new URLSearchParams();
        if (search.assignee) params.set('assignee', search.assignee);
        if (search.candidateUser) params.set('candidateUser', search.candidateUser);
        for (const group of search.candidateGroups || []) params.append('candidateGroup', group);
        if (search.includeUnassigned) params.set('includeUnassigned', 'true');
        if (search.processInstanceKey !== undefined) params.set('processInstanceKey', String(search.processInstanceKey));
        if (search.state) params.set('state', search.state);
        if (search.limit !== undefined) params.set('limit', String(search.limit));
        const query = params.toString();
        const response = await this.request<{ tasks?: UserTask[] }>('GET', `/tasks${query ? `?${query}` : ''}`, undefined, options);
        return response.tasks || [];
    }

    public async updateInstanceVariables(id: string, variables: Record<string, unknown>, options?: RequestOptions): Promise<void> {
        const request: UpdateVariablesRequest = { variables };
        await this.request<void>('POST', `/instances/${encodeURIComponent(id)}/variables`, request, options);
    }

    public async updateVariables(id: string, variables: Record<string, unknown>, options?: RequestOptions): Promise<void> {
        await this.updateInstanceVariables(id, variables, options);
    }

    public async completeTask(instanceId: string, stepId?: string, options?: RequestOptions): Promise<void> {
        await this.request<void>('POST', `/instances/${encodeURIComponent(instanceId)}/complete`, { step_id: stepId }, options);
    }

    public async completeInstanceStep(instanceId: string, stepId?: string, options?: RequestOptions): Promise<void> {
        return this.completeTask(instanceId, stepId, options);
    }

    public async listInstanceTasks(instanceId: string, includeCompleted = false, options?: RequestOptions): Promise<UserTask[]> {
        const response = await this.request<{ tasks?: UserTask[] }>(
            'GET',
            `/instances/${encodeURIComponent(instanceId)}/tasks${queryString({ includeCompleted }, ['includeCompleted'])}`,
            undefined,
            options,
        );
        return response.tasks || [];
    }

    /** Claims a task for assignee. Client tokens must name the end user; admins default to themselves. */
    public async claimUserTask(instanceId: string, executionId: string, assignee?: string, options?: RequestOptions): Promise<UserTask> {
        return this.request<UserTask>(
            'POST',
            `/instances/${encodeURIComponent(instanceId)}/tasks/${encodeURIComponent(executionId)}/claim`,
            assignee ? { assignee } : {},
            options,
        );
    }

    /** Completes a task; assignee must match the claim owner unless the caller is an admin. */
    public async completeUserTask(instanceId: string, executionId: string, assignee?: string, options?: RequestOptions): Promise<void> {
        await this.request<void>(
            'POST',
            `/instances/${encodeURIComponent(instanceId)}/tasks/${encodeURIComponent(executionId)}/complete`,
            assignee ? { assignee } : {},
            options,
        );
    }

    public async deleteInstance(id: string, options?: RequestOptions): Promise<void> {
        await this.request<void>('DELETE', `/instances/${encodeURIComponent(id)}`, undefined, options);
    }

    public async dryRunInstanceMigration(
        id: string,
        migration: InstanceMigrationRequest,
        options?: RequestOptions,
    ): Promise<InstanceMigrationResult> {
        return this.request<InstanceMigrationResult>(
            'POST',
            `/instances/${encodeURIComponent(id)}/migration/dry-run`,
            migration,
            options,
        );
    }

    public async migrateInstance(
        id: string,
        migration: InstanceMigrationRequest,
        options?: RequestOptions,
    ): Promise<InstanceMigrationResult> {
        return this.request<InstanceMigrationResult>(
            'POST',
            `/instances/${encodeURIComponent(id)}/migration`,
            migration,
            options,
        );
    }

    public async publishSignal(signalName: string, payload?: Record<string, unknown>, options?: RequestOptions): Promise<void>;
    public async publishSignal(request: PublishSignalRequest, options?: RequestOptions): Promise<void>;
    public async publishSignal(
        signalNameOrRequest: string | PublishSignalRequest,
        payloadOrOptions: Record<string, unknown> | RequestOptions = {},
        maybeOptions: RequestOptions = {},
    ): Promise<void> {
        const request = typeof signalNameOrRequest === 'string'
            ? { signal_name: signalNameOrRequest, payload: payloadOrOptions as Record<string, unknown> }
            : signalNameOrRequest;
        const options = typeof signalNameOrRequest === 'string' ? maybeOptions : payloadOrOptions as RequestOptions;
        await this.request<void>('POST', '/signals', { ...request, payload: request.payload || {} }, options);
    }

    public async publishMessage(messageName: string, correlationKey: string, payload?: Record<string, unknown>, options?: RequestOptions): Promise<void>;
    public async publishMessage(request: PublishMessageRequest, options?: RequestOptions): Promise<void>;
    public async publishMessage(
        messageNameOrRequest: string | PublishMessageRequest,
        correlationKeyOrOptions: string | RequestOptions = {},
        payload: Record<string, unknown> = {},
        maybeOptions: RequestOptions = {},
    ): Promise<void> {
        const request = typeof messageNameOrRequest === 'string'
            ? { message_name: messageNameOrRequest, correlation_key: correlationKeyOrOptions as string, payload }
            : messageNameOrRequest;
        const options = typeof messageNameOrRequest === 'string' ? maybeOptions : correlationKeyOrOptions as RequestOptions;
        await this.request<void>('POST', '/messages', { ...request, payload: request.payload || {} }, options);
    }

    public async publishEscalation(escalationCode: string, payload?: Record<string, unknown>, options?: RequestOptions): Promise<void>;
    public async publishEscalation(request: PublishEscalationRequest, options?: RequestOptions): Promise<void>;
    public async publishEscalation(
        escalationCodeOrRequest: string | PublishEscalationRequest,
        payloadOrOptions: Record<string, unknown> | RequestOptions = {},
        maybeOptions: RequestOptions = {},
    ): Promise<void> {
        const request = typeof escalationCodeOrRequest === 'string'
            ? { escalation_code: escalationCodeOrRequest, payload: payloadOrOptions as Record<string, unknown> }
            : escalationCodeOrRequest;
        const options = typeof escalationCodeOrRequest === 'string' ? maybeOptions : payloadOrOptions as RequestOptions;
        await this.request<void>('POST', '/escalations', { ...request, payload: request.payload || {} }, options);
    }

    public async activateJobs(request: ActivateJobsRequest, options?: RequestOptions): Promise<ActivateJobsResponse> {
        return this.request<ActivateJobsResponse>('POST', '/jobs/activate', request, withWorkerHeaders(options));
    }

    public async getWorkerCapabilities(options?: RequestOptions): Promise<WorkerCapabilitiesResponse> {
        return this.request<WorkerCapabilitiesResponse>('GET', '/jobs/capabilities', undefined, withWorkerHeaders(options));
    }

    public async getJobCapabilities(options?: RequestOptions): Promise<WorkerCapabilitiesResponse> {
        return this.getWorkerCapabilities(options);
    }

    public async completeJob(key: string | number, request: CompleteJobRequest, options?: RequestOptions): Promise<void> {
        await this.request<void>('POST', `/jobs/${encodeURIComponent(String(key))}/complete`, request, withWorkerHeaders(options));
    }

    public async failJob(key: string | number, request: FailJobRequest, options?: RequestOptions): Promise<void> {
        await this.request<void>('POST', `/jobs/${encodeURIComponent(String(key))}/fail`, request, withWorkerHeaders(options));
    }

    public async extendJobLock(key: string | number, request: ExtendJobLockRequest, options?: RequestOptions): Promise<void> {
        await this.request<void>('POST', `/jobs/${encodeURIComponent(String(key))}/extend-lock`, request, withWorkerHeaders(options));
    }

    public async listInstanceJobs(instanceId: string, options?: RequestOptions): Promise<Job[]> {
        const response = await this.request<{ jobs?: Job[] }>('GET', `/instances/${encodeURIComponent(instanceId)}/jobs`, undefined, options);
        return response.jobs || [];
    }

    public async retryJob(key: string | number, retries = 3, options?: RequestOptions): Promise<void> {
        await this.request<void>('POST', `/jobs/${encodeURIComponent(String(key))}/retry`, { retries }, options);
    }

    public async listIncidents(options: ListIncidentsOptions & RequestOptions = {}): Promise<Incident[]> {
        const response = await this.request<{ incidents?: Incident[] }>(
            'GET',
            `/incidents${queryString(options, ['processInstanceKey', 'limit'])}`,
            undefined,
            options,
        );
        return response.incidents || [];
    }

    public async resolveIncident(key: string | number, options?: RequestOptions): Promise<Incident> {
        return this.request<Incident>('POST', `/incidents/${encodeURIComponent(String(key))}/resolve`, undefined, options);
    }

    public async getEngineMetrics(options?: RequestOptions): Promise<EngineMetricsResponse> {
        return this.request<EngineMetricsResponse>('GET', '/internal/metrics', undefined, options);
    }

    public async getIdentity(options?: RequestOptions): Promise<IdentityResponse> {
        return this.request<IdentityResponse>('GET', '/identity/me', undefined, options);
    }

    public async getQueryIdentity(options?: RequestOptions): Promise<IdentityResponse> {
        return this.queryRequest<IdentityResponse>('GET', '/identity/me', undefined, options);
    }

    public async getIdentityConfig(options?: RequestOptions): Promise<IdentityConfigResponse> {
        return this.request<IdentityConfigResponse>('GET', '/identity/config', undefined, options);
    }

    public createWorker(type: string, handler: WorkerHandler, options: WorkerOptions = {}): Worker {
        return new Worker(this, type, handler, options);
    }

    public close(): void {}

    private request<T>(method: string, path: string, body?: RequestBody, options: RequestOptions = {}): Promise<T> {
        return this.doRequest<T>(this.baseUrl, method, path, body, options);
    }

    private queryRequest<T>(method: string, path: string, body?: RequestBody, options: RequestOptions = {}): Promise<T> {
        return this.doRequest<T>(this.queryBaseUrl, method, path, body, options);
    }

    private async doRequest<T>(baseUrl: string, method: string, path: string, body?: RequestBody, options: RequestOptions = {}): Promise<T> {
        const controller = options.signal ? undefined : new AbortController();
        const timeout = controller ? setTimeout(() => controller.abort(), this.timeoutMs) : undefined;
        try {
            const headers = await this.resolveHeaders(options);
            const requestBody = typeof body === 'string' ? body : body === undefined ? undefined : JSON.stringify(body);
            if (requestBody !== undefined && !headers['Content-Type']) {
                headers['Content-Type'] = 'application/json';
            }
            const response = await this.fetchImpl(`${baseUrl}${path}`, {
                method,
                headers,
                body: requestBody,
                signal: options.signal || controller?.signal,
            });
            const responseText = await response.text();
            if (!response.ok) {
                throw new ArtificialFlowApiError(method, path, response, responseText);
            }
            if (response.status === 204 || responseText.trim() === '') {
                return undefined as T;
            }
            const contentType = response.headers.get('content-type') || '';
            if (contentType.includes('application/json')) {
                return JSON.parse(responseText) as T;
            }
            return responseText as T;
        } finally {
            if (timeout) {
                clearTimeout(timeout);
            }
        }
    }

    private async resolveHeaders(options: RequestOptions): Promise<Record<string, string>> {
        const headers: Record<string, string> = {
            Accept: 'application/json',
            'X-Correlation-ID': options.correlationId || generateCorrelationId(),
        };
        const extraHeaders = await resolveMaybe(this.headers);
        Object.assign(headers, extraHeaders || {});
        const token = this.authProvider
            ? await this.authProvider.getToken()
            : await resolveMaybe(this.token);
        if (token) {
            headers.Authorization = `Bearer ${token}`;
        }
        if (options.idempotencyKey) {
            headers[IdempotencyKeyHeader] = options.idempotencyKey;
        }
        Object.assign(headers, options.headers || {});
        return headers;
    }
}

function normalizeBaseUrl(value: string): string {
    return value.replace(/\/+$/, '');
}

function defaultFetch(): FetchLike {
    const fetchImpl = globalThis.fetch;
    if (!fetchImpl) {
        throw new Error('No fetch implementation available. Use Node.js 18+ or pass options.fetch.');
    }
    return fetchImpl as unknown as FetchLike;
}

async function resolveMaybe<T>(value: T | (() => T | Promise<T>) | undefined): Promise<T | undefined> {
    if (typeof value === 'function') {
        return (value as () => T | Promise<T>)();
    }
    return value;
}

function generateCorrelationId(): string {
    return `sdk-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

function queryString(options: object, keys: string[]): string {
    const params = new URLSearchParams();
    const source = options as Record<string, unknown>;
    for (const key of keys) {
        const value = source[key];
        if (value !== undefined && value !== null && value !== '') {
            params.set(String(key), String(value));
        }
    }
    const encoded = params.toString();
    return encoded ? `?${encoded}` : '';
}

function withWorkerHeaders(options: RequestOptions = {}): RequestOptions {
    return {
        ...options,
        headers: {
            [HeaderWorkerProtocolVersion]: WorkerProtocolVersion,
            ...options.headers,
        },
    };
}

