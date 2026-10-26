/**
 * worker_activate_jobs.js — Simulate external workers polling for and completing jobs.
 */
import http from "k6/http";
import { check, sleep } from "k6";
import { authHeaders } from "../lib/auth.js";

const BASE = __ENV.COMMAND_URL || "http://localhost:8080";
const JOB_TYPE = "worker-perf-task";
const WORKER_BPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:artificialflow="http://artificialflow.io/schema/1.0/bpmn"
  id="Definitions_worker_perf" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="worker-perf-workflow" name="Worker Perf Test" isExecutable="true">
    <bpmn:startEvent id="start1">
      <bpmn:outgoing>flow1</bpmn:outgoing>
    </bpmn:startEvent>
    <bpmn:serviceTask id="task1" name="Worker Work" artificialflow:taskType="${JOB_TYPE}">
      <bpmn:incoming>flow1</bpmn:incoming>
      <bpmn:outgoing>flow2</bpmn:outgoing>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end1">
      <bpmn:incoming>flow2</bpmn:incoming>
    </bpmn:endEvent>
    <bpmn:sequenceFlow id="flow1" sourceRef="start1" targetRef="task1"/>
    <bpmn:sequenceFlow id="flow2" sourceRef="task1" targetRef="end1"/>
  </bpmn:process>
</bpmn:definitions>`;

export function setupWorkerWorkflow() {
  const deployRes = http.post(
    `${BASE}/workflows`,
    WORKER_BPMN,
    { headers: authHeaders({ "Content-Type": "text/xml; charset=utf-8" }) }
  );
  if (deployRes.status !== 200) {
    throw new Error(`worker workflow deployment failed: HTTP ${deployRes.status}`);
  }
  try {
    return JSON.parse(deployRes.body).id;
  } catch (_) {
    throw new Error("worker workflow deployment returned an invalid payload");
  }
}

export function workerActivateJobs(data) {
  const workflowId = data && data.workerWorkflowId;
  if (workflowId) {
    http.post(
      `${BASE}/instances`,
      JSON.stringify({
        workflow_id: workflowId,
        context: { worker_load_test: true, vu: __VU, iter: __ITER },
      }),
      { headers: authHeaders({ "Content-Type": "application/json" }) }
    );
  }

  // Poll for jobs (realistic worker behaviour)
  const worker = `k6-worker-poll-${__VU}`;
  const activateRes = http.post(
    `${BASE}/jobs/activate`,
    JSON.stringify({
      type: JOB_TYPE,
      worker,
      maxJobs: 5,
      lockDurationMs: 30000,
    }),
    {
      headers: authHeaders({ "Content-Type": "application/json" }),
      tags: { name: "activate_jobs" },
    }
  );

  check(activateRes, {
    "activate jobs 200": (r) => r.status === 200,
  });

  if (activateRes.status === 200) {
    let jobs = [];
    try {
      const body = JSON.parse(activateRes.body);
      jobs = Array.isArray(body) ? body : body.jobs || [];
    } catch (_) {}

    for (const job of jobs) {
      const jobKey = job.key || job.id;
      if (!jobKey) continue;

      const completeRes = http.post(
        `${BASE}/jobs/${jobKey}/complete`,
        JSON.stringify({ worker, variables: { done: true } }),
        {
          headers: authHeaders({ "Content-Type": "application/json" }),
          tags: { name: "complete_job" },
        }
      );
      check(completeRes, {
        "complete job 200/204": (r) => r.status === 200 || r.status === 204,
      });
    }
  }

  // Get worker capabilities
  const capsRes = http.get(`${BASE}/jobs/capabilities`, {
    headers: authHeaders(),
    tags: { name: "job_capabilities" },
  });
  check(capsRes, {
    "capabilities 200": (r) => r.status === 200,
  });

  sleep(0.2);
}
