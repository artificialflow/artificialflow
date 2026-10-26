const assert = require('assert');
const {
  ArtificialFlowApiError,
  ArtificialFlowClient,
  HeaderWorkerProtocolVersion,
  WorkerProtocolVersion,
  resolveArtificialFlowEnvironment,
} = require('../dist');

function jsonResponse(status, payload) {
  return {
    ok: status < 400,
    status,
    statusText: status < 400 ? 'OK' : 'Error',
    headers: { get: (name) => name.toLowerCase() === 'content-type' ? 'application/json' : null },
    text: async () => payload === undefined ? '' : JSON.stringify(payload),
  };
}

function textResponse(status, payload) {
  return {
    ok: status < 400,
    status,
    statusText: status < 400 ? 'OK' : 'Error',
    headers: { get: () => 'text/plain' },
    text: async () => payload || '',
  };
}

function createClient(responses) {
  const calls = [];
  const fetch = async (url, init = {}) => {
    calls.push({ url, init });
    const response = responses.shift();
    if (!response) {
      throw new Error(`unexpected request ${init.method || 'GET'} ${url}`);
    }
    return typeof response === 'function' ? response(url, init) : response;
  };
  // Keep one legacy deployment URL fixture to verify endpoint compatibility.
  const client = new ArtificialFlowClient({
    baseUrl: 'http://artificialflow.local/api',
    token: async () => 'token-1',
    fetch,
  });
  return { client, calls };
}

function body(call) {
  return call.init.body ? JSON.parse(call.init.body) : undefined;
}

function wait(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function testHealthAuthHeaders() {
  const { client, calls } = createClient([
    jsonResponse(200, { status: 'ok' }),
    jsonResponse(200, { status: 'ok' }),
    jsonResponse(200, { status: 'ok' }),
  ]);

  await client.health();
  assert.strictEqual(calls[0].url, 'http://artificialflow.local/api/health');
  assert.strictEqual(calls[0].init.method, 'GET');
  assert.strictEqual(calls[0].init.headers.Authorization, 'Bearer token-1');
  assert.ok(calls[0].init.headers['X-Correlation-ID']);

  await client.queryHealth();
  assert.strictEqual(calls[1].url, 'http://artificialflow.local/api/query/health');

  client.setToken('token-2');
  await client.health({ correlationId: 'corr-1', headers: { 'X-Custom': 'yes' } });
  assert.strictEqual(calls[2].init.headers.Authorization, 'Bearer token-2');
  assert.strictEqual(calls[2].init.headers['X-Correlation-ID'], 'corr-1');
  assert.strictEqual(calls[2].init.headers['X-Custom'], 'yes');
}

async function testAllWorkflowOperations() {
  const workflow = { id: '1', process_definition_id: 'order', name: 'Order', version: 1, resource_name: 'order.bpmn', deployment_id: '10', tenant_id: '', resource_checksum: 'abc', created_at: '2026-01-01T00:00:00Z', steps: [] };
  const { client, calls } = createClient([
    jsonResponse(200, workflow),
    jsonResponse(200, [workflow]),
    jsonResponse(200, { workflows: [workflow], total: 1 }),
    jsonResponse(200, { workflows: [workflow], total: 1 }),
    jsonResponse(200, workflow),
    jsonResponse(204),
  ]);

  await client.deployWorkflow('<definitions />');
  assert.strictEqual(calls[0].url, 'http://artificialflow.local/api/workflows');
  assert.strictEqual(calls[0].init.method, 'POST');
  assert.strictEqual(calls[0].init.body, '<definitions />');

  await client.listWorkflows({ source: 'command' });
  assert.strictEqual(calls[1].url, 'http://artificialflow.local/api/workflows');
  assert.strictEqual(calls[1].init.method, 'GET');

  await client.listWorkflows({ page: 1, pageSize: 10 });
  assert.strictEqual(calls[2].url, 'http://artificialflow.local/api/query/workflows?page=1&pageSize=10');

  await client.getWorkflows({ page: 2, pageSize: 20 });
  assert.strictEqual(calls[3].url, 'http://artificialflow.local/api/query/workflows?page=2&pageSize=20');

  await client.getWorkflow('1');
  assert.strictEqual(calls[4].url, 'http://artificialflow.local/api/workflows/1');

  await client.deleteWorkflow('1');
  assert.strictEqual(calls[5].url, 'http://artificialflow.local/api/workflows/1');
  assert.strictEqual(calls[5].init.method, 'DELETE');
}

async function testAllInstanceOperations() {
  const instance = { id: '99', workflow_id: '1', status: 'RUNNING', context: { orderId: 'A1' }, executions: [], created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' };
  const { client, calls } = createClient([
    jsonResponse(200, instance),
    jsonResponse(200, instance),
    jsonResponse(200, [instance]),
    jsonResponse(200, { instances: [instance], total: 1 }),
    jsonResponse(200, { instances: [instance], total: 1 }),
    jsonResponse(200, instance),
    jsonResponse(200, instance),
    jsonResponse(204),
    jsonResponse(204),
    jsonResponse(204),
    jsonResponse(200, { operationId: 'op-1', instanceKey: '99', dryRun: true, status: 'VALIDATED' }),
    jsonResponse(200, { operationId: 'op-1', instanceKey: '99', dryRun: false, status: 'APPLIED' }),
  ]);

  await client.startInstance('1', { orderId: 'A1' });
  assert.strictEqual(calls[0].url, 'http://artificialflow.local/api/instances');
  assert.deepStrictEqual(body(calls[0]), { workflow_id: '1', context: { orderId: 'A1' } });

  await client.startInstance({ workflow_id: '1', context: { orderId: 'A2' } }, { idempotencyKey: 'idem-instance-2' });
  assert.strictEqual(calls[1].url, 'http://artificialflow.local/api/instances');
  assert.strictEqual(calls[1].init.headers['Idempotency-Key'], 'idem-instance-2');
  assert.deepStrictEqual(body(calls[1]), { workflow_id: '1', context: { orderId: 'A2' } });

  await client.listActiveInstances();
  assert.strictEqual(calls[2].url, 'http://artificialflow.local/api/instances');

  await client.getInstances({ workflowId: '1', state: 'RUNNING', page: 1, pageSize: 50 });
  assert.strictEqual(calls[3].url, 'http://artificialflow.local/api/query/instances?workflowId=1&state=RUNNING&page=1&pageSize=50');

  await client.searchInstances({ workflowId: '1' });
  assert.strictEqual(calls[4].url, 'http://artificialflow.local/api/query/instances?workflowId=1');

  await client.getInstance('99');
  assert.strictEqual(calls[5].url, 'http://artificialflow.local/api/instances/99');

  await client.getInstance('99', { source: 'query' });
  assert.strictEqual(calls[6].url, 'http://artificialflow.local/api/query/instances/99');

  await client.updateInstanceVariables('99', { approved: true });
  assert.strictEqual(calls[7].url, 'http://artificialflow.local/api/instances/99/variables');
  assert.deepStrictEqual(body(calls[7]), { variables: { approved: true } });

  await client.completeTask('99', 'approve-task');
  assert.strictEqual(calls[8].url, 'http://artificialflow.local/api/instances/99/complete');
  assert.deepStrictEqual(body(calls[8]), { step_id: 'approve-task' });

  await client.deleteInstance('99');
  assert.strictEqual(calls[9].url, 'http://artificialflow.local/api/instances/99');
  assert.strictEqual(calls[9].init.method, 'DELETE');

  const migration = { sourceDefinitionKey: '1', targetDefinitionKey: '2', elementMapping: { review: 'approve' }, operationId: 'op-1' };
  await client.dryRunInstanceMigration('99', migration);
  assert.strictEqual(calls[10].url, 'http://artificialflow.local/api/instances/99/migration/dry-run');
  assert.deepStrictEqual(body(calls[10]), migration);

  await client.migrateInstance('99', migration);
  assert.strictEqual(calls[11].url, 'http://artificialflow.local/api/instances/99/migration');
  assert.deepStrictEqual(body(calls[11]), migration);
}

async function testUserTaskOperations() {
  const task = {
    key: '501',
    processInstanceKey: '99',
    processDefinitionKey: '1',
    elementId: 'approve',
    executionId: '601',
    state: 'CREATED',
    candidateGroups: ['finance'],
    canClaim: true,
    canComplete: false,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
  };
  const { client, calls } = createClient([
    jsonResponse(200, { tasks: [task] }),
    jsonResponse(200, { tasks: [] }),
    jsonResponse(200, { ...task, state: 'ACTIVATED', claimedBy: 'accountant@example.com', canClaim: false, canComplete: true }),
    textResponse(200, 'Task completed'),
    jsonResponse(200, { ...task, claimedBy: 'admin' }),
  ]);

  const tasks = await client.searchTasks({
    candidateUser: 'accountant@example.com',
    candidateGroups: ['finance', 'ops'],
    includeUnassigned: true,
    processInstanceKey: 99,
    limit: 10,
  });
  assert.deepStrictEqual(tasks, [task]);
  assert.strictEqual(
    calls[0].url,
    'http://artificialflow.local/api/tasks?candidateUser=accountant%40example.com&candidateGroup=finance&candidateGroup=ops&includeUnassigned=true&processInstanceKey=99&limit=10',
  );

  await client.searchTasks();
  assert.strictEqual(calls[1].url, 'http://artificialflow.local/api/tasks');

  await client.claimUserTask('99', '601', 'accountant@example.com');
  assert.strictEqual(calls[2].url, 'http://artificialflow.local/api/instances/99/tasks/601/claim');
  assert.strictEqual(calls[2].init.method, 'POST');
  assert.deepStrictEqual(body(calls[2]), { assignee: 'accountant@example.com' });

  await client.completeUserTask('99', '601', 'accountant@example.com');
  assert.strictEqual(calls[3].url, 'http://artificialflow.local/api/instances/99/tasks/601/complete');
  assert.deepStrictEqual(body(calls[3]), { assignee: 'accountant@example.com' });

  await client.claimUserTask('99', '601');
  assert.deepStrictEqual(body(calls[4]), {});
}

async function testMessagingAndWorkerRequests() {
  const { client, calls } = createClient([
    jsonResponse(204),
    jsonResponse(204),
    jsonResponse(204),
    jsonResponse(200, { jobs: [] }),
    jsonResponse(204),
    jsonResponse(204),
    jsonResponse(204),
    jsonResponse(200, { protocolVersion: 'v1', capabilities: ['activate'] }),
  ]);

  await client.publishSignal('OrderApproved', { orderId: 'A1' });
  assert.strictEqual(calls[0].url, 'http://artificialflow.local/api/signals');
  assert.deepStrictEqual(body(calls[0]), { signal_name: 'OrderApproved', payload: { orderId: 'A1' } });

  await client.publishSignal({ signal_name: 'OrderCanceled', payload: { orderId: 'A2' } });
  assert.strictEqual(calls[1].url, 'http://artificialflow.local/api/signals');
  assert.deepStrictEqual(body(calls[1]), { signal_name: 'OrderCanceled', payload: { orderId: 'A2' } });

  await client.publishMessage('MsgOrderPlaced', 'A1', { amount: 10 });
  assert.strictEqual(calls[2].url, 'http://artificialflow.local/api/messages');
  assert.deepStrictEqual(body(calls[2]), { message_name: 'MsgOrderPlaced', correlation_key: 'A1', payload: { amount: 10 } });

  await client.activateJobs({ type: 'payment', worker: 'worker-1', maxJobs: 2, timeoutMs: 500, lockDurationMs: 30000 });
  assert.strictEqual(calls[3].url, 'http://artificialflow.local/api/jobs/activate');
  assert.strictEqual(calls[3].init.headers.Authorization, 'Bearer token-1');
  assert.strictEqual(calls[3].init.headers[HeaderWorkerProtocolVersion], WorkerProtocolVersion);
  assert.deepStrictEqual(body(calls[3]), { type: 'payment', worker: 'worker-1', maxJobs: 2, timeoutMs: 500, lockDurationMs: 30000 });

  await client.completeJob('101', { worker: 'worker-1', variables: { ok: true } });
  assert.strictEqual(calls[4].url, 'http://artificialflow.local/api/jobs/101/complete');
  assert.strictEqual(calls[4].init.headers[HeaderWorkerProtocolVersion], WorkerProtocolVersion);
  assert.deepStrictEqual(body(calls[4]), { worker: 'worker-1', variables: { ok: true } });

  await client.failJob('101', { worker: 'worker-1', errorMessage: 'failed', retries: 1 });
  assert.strictEqual(calls[5].url, 'http://artificialflow.local/api/jobs/101/fail');
  assert.deepStrictEqual(body(calls[5]), { worker: 'worker-1', errorMessage: 'failed', retries: 1 });

  await client.extendJobLock('101', { worker: 'worker-1', lockDurationMs: 45000 });
  assert.strictEqual(calls[6].url, 'http://artificialflow.local/api/jobs/101/extend-lock');
  assert.deepStrictEqual(body(calls[6]), { worker: 'worker-1', lockDurationMs: 45000 });

  await client.getWorkerCapabilities();
  assert.strictEqual(calls[7].url, 'http://artificialflow.local/api/jobs/capabilities');
}

async function testMessageObjectOverload() {
  const { client, calls } = createClient([
    jsonResponse(204),
  ]);

  await client.publishMessage({ message_name: 'MsgOrderPlaced', correlation_key: 'A1', payload: { amount: 10 } });
  assert.strictEqual(calls[0].url, 'http://artificialflow.local/api/messages');
  assert.deepStrictEqual(body(calls[0]), { message_name: 'MsgOrderPlaced', correlation_key: 'A1', payload: { amount: 10 } });
}

async function testPlatformReadOperations() {
  const { client, calls } = createClient([
    jsonResponse(200, { outboxPending: 0, outboxPublishSuccess: 1, outboxPublishFailure: 0, outboxPublishLagSec: 0, outboxMaxAttempts: 5, idempotencyHit: 0, idempotencyMiss: 1 }),
    jsonResponse(200, { authenticated: true, principal: { subject: 'admin', roles: ['artificialflowadmin'] } }),
    jsonResponse(200, { deployment_mode: true, provider_name: 'OIDC', auth_enabled: true, frontend_auth_enabled: true, frontend_oidc_authority: 'https://issuer.example', frontend_oidc_client_id: '123', token_validation_mode: 'jwt', internal_issuer_url: 'https://issuer.example', external_issuer_url: 'https://issuer.example', client_id: 'workflow-backend', introspection_url: '', introspection_client_id: '', introspection_auth_method: 'none', enforce_audience: true, allow_insecure_issuer: false, claim_subject_path: 'sub', claim_roles_path: 'roles', claim_tenant_path: 'tenant', claim_email_path: 'email', claim_name_path: 'name', standard_roles: ['artificialflowadmin'] }),
  ]);

  await client.getEngineMetrics();
  assert.strictEqual(calls[0].url, 'http://artificialflow.local/api/internal/metrics');

  await client.getIdentity();
  assert.strictEqual(calls[1].url, 'http://artificialflow.local/api/identity/me');
  assert.strictEqual(calls[1].init.headers.Authorization, 'Bearer token-1');

  await client.getIdentityConfig();
  assert.strictEqual(calls[2].url, 'http://artificialflow.local/api/identity/config');
}

async function testWorkerRunOnceCompletesAndFails() {
  const success = createClient([
    jsonResponse(200, { jobs: [{ key: '201', type: 'payment', processInstanceKey: '1', elementInstanceKey: '2', processDefinitionKey: '3', elementId: 'task', worker: 'worker-1', retries: 3, state: 'ACTIVATED', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z' }] }),
    jsonResponse(204),
  ]);
  const worker = success.client.createWorker('payment', async () => ({ paid: true }), { workerName: 'worker-1' });
  const count = await worker.runOnce();
  assert.strictEqual(count, 1);
  assert.strictEqual(success.calls[1].url, 'http://artificialflow.local/api/jobs/201/complete');
  assert.deepStrictEqual(body(success.calls[1]), { worker: 'worker-1', variables: { paid: true } });

  const failure = createClient([
    jsonResponse(200, { jobs: [{ key: '202', type: 'payment', processInstanceKey: '1', elementInstanceKey: '2', processDefinitionKey: '3', elementId: 'task', worker: 'worker-1', retries: 2, state: 'ACTIVATED', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z' }] }),
    jsonResponse(204),
  ]);
  const failingWorker = failure.client.createWorker('payment', async () => { throw new Error('boom'); }, { workerName: 'worker-1' });
  await failingWorker.runOnce();
  assert.strictEqual(failure.calls[1].url, 'http://artificialflow.local/api/jobs/202/fail');
  assert.deepStrictEqual(body(failure.calls[1]), { worker: 'worker-1', errorMessage: 'boom', retries: 1 });

  const lockExtension = createClient([
    jsonResponse(200, { jobs: [{ key: '203', type: 'payment', processInstanceKey: '1', elementInstanceKey: '2', processDefinitionKey: '3', elementId: 'task', worker: 'worker-1', retries: 3, state: 'ACTIVATED', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z' }] }),
    jsonResponse(204),
    jsonResponse(204),
  ]);
  const lockWorker = lockExtension.client.createWorker('payment', async (_job, context) => {
    await context.extendLock(60000);
    return { locked: true };
  }, { workerName: 'worker-1' });
  await lockWorker.runOnce();
  assert.strictEqual(lockExtension.calls[1].url, 'http://artificialflow.local/api/jobs/203/extend-lock');
  assert.deepStrictEqual(body(lockExtension.calls[1]), { worker: 'worker-1', lockDurationMs: 60000 });
  assert.strictEqual(lockExtension.calls[2].url, 'http://artificialflow.local/api/jobs/203/complete');

  const autoStart = createClient([
    jsonResponse(200, { jobs: [] }),
  ]);
  const autoWorker = autoStart.client.createWorker('payment', async () => undefined, { workerName: 'worker-1', autoStart: true, pollIntervalMs: 10000 });
  assert.strictEqual(autoWorker.isRunning(), true);
  await wait(0);
  autoWorker.stop();
  assert.strictEqual(autoWorker.isRunning(), false);
  assert.strictEqual(autoStart.calls[0].url, 'http://artificialflow.local/api/jobs/activate');
}

async function testApiErrors() {
  const problem = {
    type: 'about:blank',
    title: 'Unauthorized',
    status: 401,
    detail: 'token required',
    correlationId: 'corr-problem',
  };
  const { client } = createClient([
    textResponse(500, 'broken'),
    {
      ...jsonResponse(401, problem),
      headers: {
        get: (name) => name.toLowerCase() === 'content-type'
          ? 'application/problem+json'
          : name.toLowerCase() === 'x-correlation-id' ? 'corr-problem' : null,
      },
    },
  ]);
  await assert.rejects(
    () => client.health(),
    (error) => error instanceof ArtificialFlowApiError && error.status === 500 && error.body === 'broken',
  );
  await assert.rejects(
    () => client.health(),
    (error) => error instanceof ArtificialFlowApiError
      && error.problem.detail === 'token required'
      && error.correlationId === 'corr-problem',
  );
}

function testCanonicalExportsAndEnvironmentPrecedence() {
  assert.strictEqual(typeof ArtificialFlowClient, 'function');
  assert.strictEqual(typeof ArtificialFlowApiError, 'function');
  assert.deepStrictEqual(
    resolveArtificialFlowEnvironment({
      ARTIFICIALFLOW_BASE_URL: 'https://api.artificialflow.example.io',
      ARTIFICIALFLOW_TOKEN: 'canonical-token',
    }, () => 123),
    {
      baseUrl: 'https://api.artificialflow.example.io',
      token: 'canonical-token',
      workflowKey: '<WORKFLOW_DEFINITION_KEY_OR_ID_TO_START>',
      businessKey: 'sdk-smoke-123',
      messageName: '<OPTIONAL_BPMN_MESSAGE_NAME>',
      messageCorrelationKey: '<OPTIONAL_MESSAGE_CORRELATION_KEY>',
      workerJobType: '<OPTIONAL_SERVICE_TASK_JOB_TYPE>',
    },
  );

  const apiUrlFallback = resolveArtificialFlowEnvironment({
    ARTIFICIALFLOW_API_URL: 'https://api-url.example/api',
  });
  assert.strictEqual(apiUrlFallback.baseUrl, 'https://api-url.example/api');

  const baseUrlWins = resolveArtificialFlowEnvironment({
    ARTIFICIALFLOW_BASE_URL: 'https://base.example/api',
    ARTIFICIALFLOW_API_URL: 'https://api-url.example/api',
  });
  assert.strictEqual(baseUrlWins.baseUrl, 'https://base.example/api');
}

async function testDecisionIncidentRetryOperations() {
  const { client, calls } = createClient([
    jsonResponse(200, { decisions: [{ decision_id: 'routing' }] }),
    jsonResponse(200, { outputs: { route: 'manual' } }),
    jsonResponse(200, { incidents: [{ id: 'incident-1' }] }),
    jsonResponse(200, { key: '10', id: 'incident-1', state: 'RESOLVED' }),
    jsonResponse(204),
  ]);

  assert.strictEqual((await client.listDecisions())[0].decision_id, 'routing');
  assert.deepStrictEqual(await client.evaluateDecision('routing', { amount: 10 }), { route: 'manual' });
  assert.strictEqual((await client.listIncidents({ limit: 25 }))[0].id, 'incident-1');
  assert.strictEqual((await client.resolveIncident('10')).state, 'RESOLVED');
  await client.retryJob('job-1', 5);

  assert.ok(calls[2].url.endsWith('/incidents?limit=25'));
  assert.ok(calls[3].url.endsWith('/incidents/10/resolve'));
  assert.deepStrictEqual(body(calls[4]), { retries: 5 });
}

async function main() {
  testCanonicalExportsAndEnvironmentPrecedence();
  await testHealthAuthHeaders();
  await testAllWorkflowOperations();
  await testAllInstanceOperations();
  await testUserTaskOperations();
  await testMessagingAndWorkerRequests();
  await testMessageObjectOverload();
  await testPlatformReadOperations();
  await testWorkerRunOnceCompletesAndFails();
  await testApiErrors();
  await testDecisionIncidentRetryOperations();
  console.log('client.test.js passed');
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
