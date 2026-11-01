import { createLogger, generateCorrelationId } from './logger';
import { runtimeConfig } from './runtimeConfig';
import { markQueryProjectionSampled } from './cqrsStatus';

const API_BASE_URL = (runtimeConfig.apiUrl || "/api").replace(/\/+$/, "");
const log = createLogger('api');

let accessToken: string | null = null;

export const setAccessToken = (token: string | null) => {
  accessToken = token;
};

export class ConsoleLoginError extends Error {}

/** Checks the console admin credentials when IAM is disabled. No token is issued. */
export async function consoleLogin(username: string, password: string): Promise<IdentityPrincipal> {
  const correlationId = generateCorrelationId();
  const response = await fetch(`${API_BASE_URL}/identity/login`, {
    method: 'POST',
    headers: { ...getHeaders(correlationId), 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
  if (response.status === 401) {
    throw new ConsoleLoginError('Invalid username or password');
  }
  if (!response.ok) {
    log.error('console login failed', { correlationId, status: response.status });
    throw new ConsoleLoginError(`Sign-in failed (${response.status})`);
  }
  const body = (await response.json()) as { principal: IdentityPrincipal };
  return body.principal;
}

const getHeaders = (correlationId: string) => {
  const headers: Record<string, string> = {
    'X-Correlation-ID': correlationId,
  };
  if (accessToken) {
    headers['Authorization'] = `Bearer ${accessToken}`;
  }
  return headers;
};

export interface WorkflowDefinition {
  id: string;
  process_definition_id: string;
  name: string;
  version: number;
  resource_name: string;
  deployment_id: string;
  tenant_id: string;
  resource_checksum: string;
  bpmn_xml: string;
  created_at: string;
}

export interface WorkflowInstance {
  id: string;
  workflow_id: string;
  status: "PENDING" | "RUNNING" | "COMPLETED" | "FAILED";
  current_step?: string;
  parent_instance_id?: string;
  parent_execution_id?: string;
  context: Record<string, unknown>;
  executions: Execution[];
  created_at: string;
  updated_at: string;
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
  status: "VALIDATED" | "APPLIED" | "REJECTED";
  reasonCode?: string;
  message?: string;
  sourceVersion?: number;
  targetVersion?: number;
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

export interface Execution {
  id: string;
  step_id: string;
  status: string;
  parent_id?: string;
  start_time: string;
  element_instance_key?: string;
  task?: UserTask;
}

export interface UserTask {
  key: string;
  elementId: string;
  executionId: string;
  state: string;
  assignee?: string;
  candidateUsers?: string[];
  candidateGroups?: string[];
  claimedBy?: string;
  canClaim: boolean;
  canComplete: boolean;
  dueDate?: string;
  createdAt: string;
  updatedAt: string;
}

export interface InstanceJob {
  key: string | number;
  type: string;
  elementId: string;
  worker?: string;
  retries: number;
  state: string;
  lockExpirationTime?: string;
  dueDate?: string;
  breachedAt?: string;
  createdAt: string;
  updatedAt: string;
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
  resolvedAt?: string;
}

export interface DecisionDefinition {
  key: string;
  decision_id: string;
  name: string;
  version: number;
  created_at?: string;
  updated_at?: string;
}

export const api = {
  // Workflows
  /**
   * Reads the CQRS query projection. Command writes may take a few seconds to
   * appear here; use cqrsSync helpers after deploy/delete flows.
   */
  getWorkflows: async (): Promise<WorkflowDefinition[]> => {
    const correlationId = generateCorrelationId();
    const endTimer = log.time('getWorkflows');
    log.debug('fetching workflows', { correlationId });

    // CQRS: Read from Query Service
    const response = await fetch(`${API_BASE_URL}/query/workflows?page=1&pageSize=100`, {
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      log.error('failed to fetch workflows', { status: response.status, statusText: response.statusText });
      throw new Error(`Failed to fetch workflows: ${response.statusText}`);
    }
    const data = await response.json();
    const workflows = data.workflows || [];
    log.info('workflows fetched', { count: workflows.length });
    endTimer();
    markQueryProjectionSampled();

    return workflows.map((w: Record<string, unknown>) => {
        if (w.id && w.process_definition_id) return w as unknown as WorkflowDefinition;

        return {
            id: String(w.key),
            process_definition_id: w.bpmnProcessId,
            name: w.bpmnProcessId, // Use ID as name if not separate
            version: w.version,
            resource_name: w.resourceName,
            deployment_id: String(w.deploymentKey),
            tenant_id: w.tenantId,
            resource_checksum: w.resourceChecksum,
            bpmn_xml: w.resource, // Resource is bytes/string
            created_at: w.createdAt
        } as WorkflowDefinition;
    });
  },

  getWorkflow: async (id: string): Promise<WorkflowDefinition> => {
    const correlationId = generateCorrelationId();
    log.debug('fetching workflow', { workflowId: id, correlationId });

    const response = await fetch(`${API_BASE_URL}/workflows/${id}`, {
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      log.error('failed to fetch workflow', { workflowId: id, status: response.status });
      throw new Error(`Failed to fetch workflow ${id}: ${response.statusText}`);
    }
    log.debug('workflow fetched', { workflowId: id });
    return response.json();
  },

  deployWorkflow: async (bpmnXml: string): Promise<WorkflowDefinition> => {
    const correlationId = generateCorrelationId();
    const endTimer = log.time('deployWorkflow');
    log.info('deploying workflow', { xmlSize: bpmnXml.length, correlationId });

    const headers = getHeaders(correlationId);
    headers["Content-Type"] = "application/xml";

    const response = await fetch(`${API_BASE_URL}/workflows`, {
      method: "POST",
      headers: headers,
      body: bpmnXml,
    });
    if (!response.ok) {
      log.error('failed to deploy workflow', { status: response.status, statusText: response.statusText });
      const detail = (await response.text()).trim() || response.statusText;
      throw new Error(`Failed to deploy workflow: ${detail}`);
    }
    const result = await response.json();
    log.info('workflow deployed', { workflowId: result.id, workflowName: result.name });
    endTimer();
    return result;
  },

  deleteWorkflow: async (id: string): Promise<void> => {
    const correlationId = generateCorrelationId();
    log.info('deleting workflow', { workflowId: id, correlationId });

    const response = await fetch(`${API_BASE_URL}/workflows/${id}`, {
      method: "DELETE",
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      log.error('failed to delete workflow', { workflowId: id, status: response.status });
      throw new Error(`Failed to delete workflow: ${response.statusText}`);
    }
    log.info('workflow deleted', { workflowId: id });
  },

  // Instances
  /**
   * Reads the command-side active instance list so user/task ownership scoping
   * is enforced for the platform UI.
   */
  getInstances: async (): Promise<WorkflowInstance[]> => {
    const correlationId = generateCorrelationId();
    const endTimer = log.time('getInstances');
    log.debug('fetching instances', { correlationId });

    const response = await fetch(`${API_BASE_URL}/instances`, {
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      log.error('failed to fetch instances', { status: response.status, statusText: response.statusText });
      throw new Error(`Failed to fetch instances: ${response.statusText}`);
    }
    const data = await response.json();
    log.info('instances fetched', { count: Array.isArray(data) ? data.length : 0 });
    endTimer();
    markQueryProjectionSampled();
    return Array.isArray(data) ? data : [];
  },

  getEngineMetrics: async (): Promise<{
    outboxPending: number;
    outboxPublishSuccess: number;
    outboxPublishFailure: number;
    outboxPublishLagSec: number;
    outboxMaxAttempts: number;
    idempotencyHit: number;
    idempotencyMiss: number;
  }> => {
    const correlationId = generateCorrelationId();
    const response = await fetch(`${API_BASE_URL}/internal/metrics`, {
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      throw new Error(`Failed to fetch engine metrics: ${response.statusText}`);
    }
    return response.json();
  },

  listDecisions: async (): Promise<DecisionDefinition[]> => {
    const correlationId = generateCorrelationId();
    const response = await fetch(`${API_BASE_URL}/decisions`, {
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      const detail = (await response.text()).trim() || response.statusText;
      throw new Error(`Failed to list decisions: ${detail}`);
    }
    const data = await response.json();
    return data.decisions || [];
  },

  deployDecision: async (payload: {
    decision_id: string;
    name: string;
    resource: string;
  }): Promise<DecisionDefinition> => {
    const correlationId = generateCorrelationId();
    const headers = getHeaders(correlationId);
    headers["Content-Type"] = "application/json";
    const response = await fetch(`${API_BASE_URL}/decisions`, {
      method: "POST",
      headers,
      body: JSON.stringify(payload),
    });
    if (!response.ok) {
      const detail = (await response.text()).trim() || response.statusText;
      throw new Error(`Failed to deploy decision: ${detail}`);
    }
    return response.json();
  },

  evaluateDecision: async (
    decisionId: string,
    inputs: Record<string, unknown>,
  ): Promise<Record<string, unknown>> => {
    const correlationId = generateCorrelationId();
    const headers = getHeaders(correlationId);
    headers["Content-Type"] = "application/json";
    const response = await fetch(`${API_BASE_URL}/decisions/${encodeURIComponent(decisionId)}/evaluate`, {
      method: "POST",
      headers,
      body: JSON.stringify({ inputs }),
    });
    if (!response.ok) {
      const detail = (await response.text()).trim() || response.statusText;
      throw new Error(`Failed to evaluate decision: ${detail}`);
    }
    const data = await response.json();
    return data.outputs || data;
  },

  getCompletedInstanceHistory: async (): Promise<WorkflowInstance[]> => {
    const correlationId = generateCorrelationId();
    log.debug('fetching completed instance history', { correlationId });

    const response = await fetch(`${API_BASE_URL}/instances/history/completed?limit=100`, {
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      log.error('failed to fetch completed instance history', { status: response.status, statusText: response.statusText });
      throw new Error(`Failed to fetch completed instance history: ${response.statusText}`);
    }
    return response.json();
  },

  startInstance: async (
    workflowId: string,
    context: Record<string, unknown> = {},
    version?: number,
  ): Promise<WorkflowInstance> => {
    const correlationId = generateCorrelationId();
    const endTimer = log.time('startInstance');
    log.info('starting instance', { workflowId, version, contextKeys: Object.keys(context), correlationId });

    const headers = getHeaders(correlationId);
    headers["Content-Type"] = "application/json";

    const response = await fetch(`${API_BASE_URL}/instances`, {
      method: "POST",
      headers: headers,
      body: JSON.stringify({
        workflow_id: workflowId,
        context: context,
        ...(version && version > 0 ? { version } : {}),
      }),
    });
    if (!response.ok) {
      log.error('failed to start instance', { workflowId, status: response.status });
      throw new Error(`Failed to start instance: ${response.statusText}`);
    }
    const result = await response.json();
    log.info('instance started', { instanceId: result.id, workflowId });
    endTimer();
    return result;
  },

  getInstance: async (id: string): Promise<WorkflowInstance> => {
    const correlationId = generateCorrelationId();
    log.debug('fetching instance', { instanceId: id, correlationId });

    const response = await fetch(`${API_BASE_URL}/instances/${id}`, {
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      log.error('failed to fetch instance', { instanceId: id, status: response.status });
      throw new Error(`Failed to fetch instance ${id}: ${response.statusText}`);
    }
    log.debug('instance fetched', { instanceId: id });
    return response.json();
  },

  updateInstanceVariables: async (id: string, variables: Record<string, unknown>): Promise<void> => {
    const correlationId = generateCorrelationId();
    log.info('updating instance variables', { instanceId: id, variableNames: Object.keys(variables), correlationId });

    const headers = getHeaders(correlationId);
    headers["Content-Type"] = "application/json";

    const response = await fetch(`${API_BASE_URL}/instances/${id}/variables`, {
      method: "POST",
      headers: headers,
      body: JSON.stringify({ variables }),
    });
    if (!response.ok) {
      log.error('failed to update instance variables', { instanceId: id, status: response.status });
      throw new Error(`Failed to update instance variables: ${response.statusText}`);
    }
    log.info('instance variables updated', { instanceId: id, variableCount: Object.keys(variables).length });
  },

  completeTask: async (instanceId: string, stepId?: string): Promise<void> => {
    const correlationId = generateCorrelationId();
    log.info('completing task', { instanceId, stepId, correlationId });

    const headers = getHeaders(correlationId);
    headers["Content-Type"] = "application/json";

    const response = await fetch(`${API_BASE_URL}/instances/${instanceId}/complete`, {
      method: "POST",
      headers: headers,
      body: JSON.stringify({ step_id: stepId }),
    });
    if (!response.ok) {
      log.error('failed to complete task', { instanceId, stepId, status: response.status });
      throw new Error(`Failed to complete task: ${response.statusText}`);
    }
    log.info('task completed', { instanceId, stepId });
  },

  claimUserTask: async (instanceId: string, executionId: string): Promise<UserTask> => {
    const correlationId = generateCorrelationId();
    log.info('claiming user task', { instanceId, executionId, correlationId });

    const response = await fetch(`${API_BASE_URL}/instances/${instanceId}/tasks/${executionId}/claim`, {
      method: "POST",
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      const detail = (await response.text()).trim() || response.statusText;
      throw new Error(`Failed to take task: ${detail}`);
    }
    return response.json();
  },

  listUserTasks: async (instanceId: string, options: { includeCompleted?: boolean } = {}): Promise<UserTask[]> => {
    const correlationId = generateCorrelationId();
    log.debug('fetching user tasks', { instanceId, includeCompleted: options.includeCompleted, correlationId });

    const params = new URLSearchParams();
    if (options.includeCompleted) {
      params.set("includeCompleted", "true");
    }
    const query = params.toString();
    const response = await fetch(`${API_BASE_URL}/instances/${instanceId}/tasks${query ? `?${query}` : ""}`, {
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      const detail = (await response.text()).trim() || response.statusText;
      throw new Error(`Failed to load user tasks: ${detail}`);
    }
    const data = await response.json();
    return data.tasks || [];
  },

  completeUserTask: async (instanceId: string, executionId: string): Promise<void> => {
    const correlationId = generateCorrelationId();
    log.info('completing user task', { instanceId, executionId, correlationId });

    const response = await fetch(`${API_BASE_URL}/instances/${instanceId}/tasks/${executionId}/complete`, {
      method: "POST",
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      const detail = (await response.text()).trim() || response.statusText;
      throw new Error(`Failed to complete user task: ${detail}`);
    }
    log.info('user task completed', { instanceId, executionId });
  },

  listInstanceJobs: async (instanceId: string): Promise<InstanceJob[]> => {
    const correlationId = generateCorrelationId();
    const response = await fetch(`${API_BASE_URL}/instances/${instanceId}/jobs`, {
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      const detail = (await response.text()).trim() || response.statusText;
      throw new Error(`Failed to load jobs: ${detail}`);
    }
    const data = await response.json();
    return data.jobs || [];
  },

  listIncidents: async (options?: {
    processInstanceKey?: string | number;
    limit?: number;
  }): Promise<Incident[]> => {
    const correlationId = generateCorrelationId();
    const params = new URLSearchParams();
    if (options?.processInstanceKey != null && String(options.processInstanceKey).trim()) {
      params.set("processInstanceKey", String(options.processInstanceKey));
    }
    if (options?.limit != null) {
      params.set("limit", String(options.limit));
    }
    const qs = params.toString();
    const response = await fetch(`${API_BASE_URL}/incidents${qs ? `?${qs}` : ""}`, {
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      const detail = (await response.text()).trim() || response.statusText;
      throw new Error(`Failed to load incidents: ${detail}`);
    }
    const data = await response.json();
    return data.incidents || [];
  },

  retryJob: async (jobKey: string | number, retries = 3): Promise<void> => {
    const correlationId = generateCorrelationId();
    const headers = getHeaders(correlationId);
    headers["Content-Type"] = "application/json";
    const response = await fetch(`${API_BASE_URL}/jobs/${jobKey}/retry`, {
      method: "POST",
      headers,
      body: JSON.stringify({ retries }),
    });
    if (!response.ok) {
      const detail = (await response.text()).trim() || response.statusText;
      throw new Error(`Failed to retry job: ${detail}`);
    }
  },

  resolveIncident: async (incidentKey: string | number): Promise<Incident> => {
    const correlationId = generateCorrelationId();
    const response = await fetch(`${API_BASE_URL}/incidents/${encodeURIComponent(String(incidentKey))}/resolve`, {
      method: "POST",
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      const detail = (await response.text()).trim() || response.statusText;
      throw new Error(`Failed to resolve incident: ${detail}`);
    }
    return response.json();
  },

  failJob: async (jobKey: string | number, worker = "ops-admin", errorMessage = "Failed by operator"): Promise<void> => {
    const correlationId = generateCorrelationId();
    const headers = getHeaders(correlationId);
    headers["Content-Type"] = "application/json";
    const response = await fetch(`${API_BASE_URL}/jobs/${jobKey}/fail`, {
      method: "POST",
      headers,
      body: JSON.stringify({ worker, errorMessage, retries: 0 }),
    });
    if (!response.ok) {
      const detail = (await response.text()).trim() || response.statusText;
      throw new Error(`Failed to fail job: ${detail}`);
    }
  },

  deleteInstance: async (id: string): Promise<void> => {
    const correlationId = generateCorrelationId();
    log.info('deleting instance', { instanceId: id, correlationId });

    const response = await fetch(`${API_BASE_URL}/instances/${id}`, {
      method: "DELETE",
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      log.error('failed to delete instance', { instanceId: id, status: response.status });
      throw new Error(`Failed to delete instance: ${response.statusText}`);
    }
    log.info('instance deleted', { instanceId: id });
  },

  migrateInstance: async (
    id: string,
    request: InstanceMigrationRequest,
    dryRun = false,
  ): Promise<InstanceMigrationResult> => {
    const correlationId = generateCorrelationId();
    const headers = getHeaders(correlationId);
    headers["Content-Type"] = "application/json";
    const suffix = dryRun ? "/dry-run" : "";
    const response = await fetch(`${API_BASE_URL}/instances/${encodeURIComponent(id)}/migration${suffix}`, {
      method: "POST",
      headers,
      body: JSON.stringify(request),
    });
    const body = await response.json().catch(() => null) as InstanceMigrationResult | null;
    if (!response.ok && !body) {
      throw new Error(`Failed to migrate instance: ${response.statusText}`);
    }
    if (!body) {
      throw new Error("Migration endpoint returned an empty response");
    }
    return body;
  },

  getIdentity: async (): Promise<IdentityResponse> => {
    const correlationId = generateCorrelationId();
    log.debug('fetching identity', { correlationId });

    const response = await fetch(`${API_BASE_URL}/identity/me`, {
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      log.error('failed to fetch identity', { correlationId, status: response.status });
      throw new Error(`Failed to fetch identity: ${response.statusText}`);
    }
    return response.json();
  },

  getIdentityConfig: async (): Promise<IdentityConfigResponse> => {
    const correlationId = generateCorrelationId();
    log.debug('fetching identity config', { correlationId });

    const response = await fetch(`${API_BASE_URL}/identity/config`, {
      headers: getHeaders(correlationId),
    });
    if (!response.ok) {
      log.error('failed to fetch identity config', { correlationId, status: response.status });
      throw new Error(`Failed to fetch identity config: ${response.statusText}`);
    }
    return response.json();
  },

};
