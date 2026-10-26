import {
  setup as setupWorkflow,
  workflowThroughput,
} from "./scenarios/workflow_throughput.js";
import http from "k6/http";
import { check } from "k6";
import { Trend } from "k6/metrics";
import { authHeaders } from "./lib/auth.js";
import { cqrsLag } from "./scenarios/cqrs_lag.js";
import { queryReadLoad } from "./scenarios/query_read_load.js";
import {
  setupWorkerWorkflow,
  workerActivateJobs,
} from "./scenarios/worker_activate_jobs.js";

const profile = JSON.parse(open("../profiles/release-soak-v1.json"));
const commandBase = __ENV.COMMAND_URL || "http://localhost:8080";
const soakDuration = __ENV.SOAK_DURATION || profile.traffic.duration;
const outboxLagSeconds = new Trend("outbox_lag_seconds", true);

export const options = {
  scenarios: {
    workflow_throughput: {
      executor: "constant-arrival-rate",
      rate: profile.traffic.workflowStartsPerSecond,
      timeUnit: "1s",
      duration: soakDuration,
      preAllocatedVUs: 150,
      maxVUs: 150,
      exec: "runWorkflowThroughput",
    },
    query_read_load: {
      executor: "constant-arrival-rate",
      rate: profile.traffic.queryReadsPerSecond,
      timeUnit: "1s",
      duration: soakDuration,
      preAllocatedVUs: 300,
      maxVUs: 300,
      exec: "runQueryReadLoad",
    },
    worker_activate_jobs: {
      executor: "constant-arrival-rate",
      rate: profile.traffic.workerActivationsPerSecond,
      timeUnit: "1s",
      duration: soakDuration,
      preAllocatedVUs: 150,
      maxVUs: 150,
      exec: "runWorkerActivateJobs",
    },
    cqrs_visibility: {
      executor: "constant-arrival-rate",
      rate: 1,
      timeUnit: "1s",
      duration: soakDuration,
      preAllocatedVUs: 60,
      maxVUs: 60,
      exec: "runCqrsLag",
    },
    outbox_lag_sample: {
      executor: "constant-arrival-rate",
      rate: 1,
      timeUnit: "1s",
      duration: soakDuration,
      preAllocatedVUs: 10,
      maxVUs: 10,
      exec: "runOutboxLagSample",
    },
  },
  thresholds: {
    http_req_duration: [`p(99)<${profile.thresholds.httpP99MillisecondsMax}`],
    http_req_failed: [`rate<${profile.thresholds.httpFailureRateMax}`],
    checks: [`rate>${profile.thresholds.checkPassRateMin}`],
    cqrs_lag_ms: [`p(99)<${profile.thresholds.projectionLagP99SecondsMax * 1000}`],
    outbox_lag_seconds: [`p(99)<${profile.thresholds.outboxLagP99SecondsMax}`],
    dropped_iterations: ["count==0"],
    "dropped_iterations{scenario:workflow_throughput}": ["count==0"],
    "dropped_iterations{scenario:query_read_load}": ["count==0"],
    "dropped_iterations{scenario:worker_activate_jobs}": ["count==0"],
    "dropped_iterations{scenario:cqrs_visibility}": ["count==0"],
    "dropped_iterations{scenario:outbox_lag_sample}": ["count==0"],
  },
};

export function setup() {
  return {
    ...setupWorkflow(),
    workerWorkflowId: setupWorkerWorkflow(),
  };
}

export function runWorkflowThroughput(data) {
  workflowThroughput(data);
}

export function runQueryReadLoad() {
  queryReadLoad();
}

export function runWorkerActivateJobs(data) {
  workerActivateJobs(data);
}

export function runCqrsLag(data) {
  cqrsLag(data);
}

export function runOutboxLagSample() {
  const response = http.get(`${commandBase}/internal/metrics`, {
    headers: authHeaders(),
    tags: { name: "internal_metrics" },
  });
  const valid = check(response, {
    "internal metrics 200": (result) => result.status === 200,
  });
  if (!valid) {
    return;
  }
  try {
    outboxLagSeconds.add(Number(response.json("outboxPublishLagSec") || 0));
  } catch (_) {
    check(null, { "internal metrics payload valid": () => false });
  }
}
