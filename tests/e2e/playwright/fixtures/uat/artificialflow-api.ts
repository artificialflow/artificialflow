import type { Page } from "@playwright/test";
import { accessTokenFromBrowser } from "./auth";

export interface WorkflowDefinition {
  id: string;
  process_definition_id?: string;
  bpmnProcessId?: string;
  name?: string;
}

export interface WorkflowInstance {
  id: string;
  workflow_id: string;
  status: string;
  current_step?: string;
  context?: Record<string, unknown>;
  executions?: Array<{ id: string; step_id: string; status: string; start_time?: string; task?: UserTask }>;
}

export interface WorkerJob {
  key: number;
  type: string;
  worker: string;
  state: string;
}

export interface UserTask {
  key: string;
  processInstanceKey: string;
  elementId: string;
  executionId: string;
  state: string;
  assignee?: string;
  candidateUsers?: string[];
  candidateGroups?: string[];
  claimedBy?: string;
  canClaim: boolean;
  canComplete: boolean;
}

/** An end user authenticated by the client application, not by ArtificialFlow. */
export interface EndUser {
  username: string;
  email: string;
  groups: string[];
}

export interface TaskSearch {
  assignee?: string;
  candidateUser?: string;
  candidateGroups?: string[];
  processInstanceKey?: string;
  includeUnassigned?: boolean;
  state?: "active" | "completed" | "all";
}

export class ArtificialFlowApi {
  private token: string | null = null;

  constructor(private readonly page: Page, private readonly baseUrl = process.env.FRONTEND_URL || "http://localhost:9100") {}

  resetToken(): void {
    this.token = null;
  }

  async request<T>(path: string, options: RequestInit = {}, expectedStatus?: number): Promise<T> {
    if (!this.token) {
      this.token = await accessTokenFromBrowser(this.page);
    }
    const result = await this.page.evaluate(async ({ url, token, options }) => {
      const response = await fetch(url, {
        ...options,
        headers: {
          ...(options.headers || {}),
          Authorization: `Bearer ${token}`,
        },
      });
      const text = await response.text();
      let body: unknown = text;
      try {
        body = text ? JSON.parse(text) : null;
      } catch {
        // Keep plain text bodies as text.
      }
      return { ok: response.ok, status: response.status, body, text };
    }, { url: `${this.baseUrl}/api${path}`, token: this.token, options });

    if (expectedStatus && result.status !== expectedStatus) {
      throw new Error(`Expected ${path} to return ${expectedStatus}, got ${result.status}: ${result.text}`);
    }
    if (!expectedStatus && !result.ok) {
      throw new Error(`Request ${path} failed with ${result.status}: ${result.text}`);
    }
    return result.body as T;
  }

  async requestWithToken<T>(path: string, token: string, options: RequestInit = {}, expectedStatus?: number): Promise<T> {
    const result = await this.page.evaluate(async ({ url, token, options }) => {
      const response = await fetch(url, {
        ...options,
        headers: {
          ...(options.headers || {}),
          Authorization: `Bearer ${token}`,
        },
      });
      const text = await response.text();
      let body: unknown = text;
      try {
        body = text ? JSON.parse(text) : null;
      } catch {
        // Keep plain text bodies as text.
      }
      return { ok: response.ok, status: response.status, body, text };
    }, { url: `${this.baseUrl}/api${path}`, token, options });

    if (expectedStatus && result.status !== expectedStatus) {
      throw new Error(`Expected ${path} to return ${expectedStatus}, got ${result.status}: ${result.text}`);
    }
    if (!expectedStatus && !result.ok) {
      throw new Error(`Request ${path} failed with ${result.status}: ${result.text}`);
    }
    return result.body as T;
  }

  async searchTasks(token: string, search: TaskSearch): Promise<UserTask[]> {
    const params = new URLSearchParams();
    if (search.assignee) params.set("assignee", search.assignee);
    if (search.candidateUser) params.set("candidateUser", search.candidateUser);
    for (const group of search.candidateGroups || []) params.append("candidateGroup", group);
    if (search.processInstanceKey) params.set("processInstanceKey", search.processInstanceKey);
    if (search.includeUnassigned) params.set("includeUnassigned", "true");
    if (search.state) params.set("state", search.state);
    const data = await this.requestWithToken<{ tasks?: UserTask[] }>(`/tasks?${params.toString()}`, token);
    return data.tasks || [];
  }

  async claimTaskFor(instanceId: string, executionId: string, token: string, assignee: string, expectedStatus = 200): Promise<UserTask | string> {
    return this.requestWithToken<UserTask | string>(`/instances/${instanceId}/tasks/${executionId}/claim`, token, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ assignee }),
    }, expectedStatus);
  }

  async completeTaskFor(instanceId: string, executionId: string, token: string, assignee: string, expectedStatus = 200): Promise<string> {
    return this.requestWithToken<string>(`/instances/${instanceId}/tasks/${executionId}/complete`, token, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ assignee }),
    }, expectedStatus);
  }

  async deploy(xml: string, expectedStatus?: number): Promise<WorkflowDefinition> {
    return this.request<WorkflowDefinition>("/workflows", {
      method: "POST",
      headers: { "Content-Type": "text/xml; charset=utf-8" },
      body: xml,
    }, expectedStatus);
  }

  async deployExpectingRejection(xml: string): Promise<{ status: number; text: string }> {
    if (!this.token) {
      this.token = await accessTokenFromBrowser(this.page);
    }
    return this.page.evaluate(async ({ url, token, xml }) => {
      const response = await fetch(url, {
        method: "POST",
        headers: {
          "Content-Type": "text/xml; charset=utf-8",
          Authorization: `Bearer ${token}`,
        },
        body: xml,
      });
      return { status: response.status, text: await response.text() };
    }, { url: `${this.baseUrl}/api/workflows`, token: this.token, xml });
  }

  async startInstance(workflowId: string, context: Record<string, unknown> = {}): Promise<WorkflowInstance> {
    return this.request<WorkflowInstance>("/instances", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ workflow_id: workflowId, context }),
    });
  }

  async getInstance(instanceId: string): Promise<WorkflowInstance> {
    return this.request<WorkflowInstance>(`/instances/${instanceId}`);
  }

  async updateVariables(instanceId: string, variables: Record<string, unknown>): Promise<void> {
    await this.request<string>(`/instances/${instanceId}/variables`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ variables }),
    }, 200);
  }

  async completeTask(instanceId: string, stepId?: string): Promise<void> {
    await this.request<string>(`/instances/${instanceId}/complete`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ step_id: stepId }),
    }, 200);
  }

  async claimUserTask(instanceId: string, executionId: string, expectedStatus = 200): Promise<UserTask | string> {
    return this.request<UserTask | string>(`/instances/${instanceId}/tasks/${executionId}/claim`, {
      method: "POST",
    }, expectedStatus);
  }

  async completeUserTask(instanceId: string, executionId: string, expectedStatus = 200): Promise<string> {
    return this.request<string>(`/instances/${instanceId}/tasks/${executionId}/complete`, {
      method: "POST",
    }, expectedStatus);
  }

  async publishMessage(messageName: string, correlationKey = "", payload: Record<string, unknown> = {}): Promise<void> {
    await this.request<string>("/messages", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message_name: messageName, correlation_key: correlationKey, payload }),
    }, 200);
  }

  async publishSignal(signalName: string, payload: Record<string, unknown> = {}): Promise<void> {
    await this.request<string>("/signals", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ signal_name: signalName, payload }),
    }, 200);
  }

  async activateJobs(type: string, worker: string, maxJobs = 1): Promise<WorkerJob[]> {
    const body = await this.request<{ jobs: WorkerJob[] }>("/jobs/activate", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Workflow-Worker-Protocol-Version": "v1",
      },
      body: JSON.stringify({ type, worker, maxJobs, timeoutMs: 100, lockDurationMs: 5_000 }),
    });
    return body.jobs || [];
  }

  async completeJob(key: number, worker: string, variables: Record<string, unknown> = {}): Promise<void> {
    await this.request<string>(`/jobs/${key}/complete`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Workflow-Worker-Protocol-Version": "v1",
      },
      body: JSON.stringify({ worker, variables }),
    }, 200);
  }

  async failJob(key: number, worker: string, retries = 1): Promise<void> {
    await this.request<string>(`/jobs/${key}/fail`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Workflow-Worker-Protocol-Version": "v1",
      },
      body: JSON.stringify({ worker, errorMessage: "UAT temporary failure", retries }),
    }, 200);
  }

  async extendJobLock(key: number, worker: string): Promise<void> {
    await this.request<string>(`/jobs/${key}/extend-lock`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Workflow-Worker-Protocol-Version": "v1",
      },
      body: JSON.stringify({ worker, lockDurationMs: 5_000 }),
    }, 200);
  }

  async jobsCapabilities(): Promise<{ protocolVersion: string; capabilities: string[] }> {
    return this.request<{ protocolVersion: string; capabilities: string[] }>("/jobs/capabilities", {
      headers: { "X-Workflow-Worker-Protocol-Version": "v1" },
    });
  }

  async unsupportedWorkerProtocolStatus(): Promise<{ status: number; text: string }> {
    if (!this.token) {
      this.token = await accessTokenFromBrowser(this.page);
    }
    return this.page.evaluate(async ({ url, token }) => {
      const response = await fetch(url, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Workflow-Worker-Protocol-Version": "v999",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ type: "uat-no-jobs", worker: "uat-worker", maxJobs: 1, timeoutMs: 10, lockDurationMs: 1_000 }),
      });
      return { status: response.status, text: await response.text() };
    }, { url: `${this.baseUrl}/api/jobs/activate`, token: this.token });
  }

  async identityConfig(): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>("/identity/config");
  }

  async identityMe(): Promise<Record<string, unknown>> {
    return this.request<Record<string, unknown>>("/identity/me");
  }

  async deleteWorkflow(id: string): Promise<void> {
    await this.request<void>(`/workflows/${id}`, { method: "DELETE" }, 204).catch(() => undefined);
  }

  async deleteInstance(id: string): Promise<void> {
    await this.request<void>(`/instances/${id}`, { method: "DELETE" }, 204).catch(() => undefined);
  }

  async waitForWorkflowInQuery(workflowId: string, processId: string): Promise<void> {
    await this.waitFor(async () => {
      const data = await this.request<{ workflows: Array<Record<string, unknown>> }>("/query/workflows?page=1&pageSize=100");
      return (data.workflows || []).some((workflow) =>
        String(workflow.id || workflow.key) === String(workflowId) ||
        workflow.process_definition_id === processId ||
        workflow.bpmnProcessId === processId
      );
    }, `workflow ${workflowId} to appear in query projection`);
  }

  async waitForInstanceStatus(instanceId: string, predicate: (instance: WorkflowInstance) => boolean): Promise<WorkflowInstance> {
    let last: WorkflowInstance | null = null;
    await this.waitFor(async () => {
      last = await this.getInstance(instanceId);
      return predicate(last);
    }, `instance ${instanceId} to match status predicate`, () => {
      if (!last) return "";
      const active = (last.executions || [])
        .filter((execution) => execution.status === "ACTIVE")
        .map((execution) => execution.step_id || execution.id)
        .join(", ");
      return `; last status=${last.status}; active=[${active}]`;
    });
    if (!last) {
      throw new Error(`Timed out waiting for instance ${instanceId} without observing an instance`);
    }
    return last;
  }

  private async waitFor(predicate: () => Promise<boolean>, description: string, describeLast?: () => string): Promise<void> {
    const deadline = Date.now() + Number(process.env.UAT_WAIT_TIMEOUT_MS || 90_000);
    while (Date.now() < deadline) {
      if (await predicate()) return;
      await this.page.waitForTimeout(1_000);
    }
    throw new Error(`Timed out waiting for ${description}${describeLast ? describeLast() : ""}`);
  }
}
