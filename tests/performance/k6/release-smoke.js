import {
  setup as setupWorkflow,
  workflowThroughput,
} from "./scenarios/workflow_throughput.js";
import { queryReadLoad } from "./scenarios/query_read_load.js";
import { workerActivateJobs } from "./scenarios/worker_activate_jobs.js";

export const options = {
  scenarios: {
    workflow_throughput: {
      executor: "constant-arrival-rate",
      rate: 2,
      timeUnit: "1s",
      duration: "2m",
      preAllocatedVUs: 4,
      maxVUs: 20,
      exec: "runWorkflowThroughput",
    },
    query_read_load: {
      executor: "constant-arrival-rate",
      rate: 5,
      timeUnit: "1s",
      duration: "2m",
      preAllocatedVUs: 6,
      maxVUs: 30,
      exec: "runQueryReadLoad",
    },
    worker_activate_jobs: {
      executor: "constant-arrival-rate",
      rate: 2,
      timeUnit: "1s",
      duration: "2m",
      preAllocatedVUs: 4,
      maxVUs: 20,
      exec: "runWorkerActivateJobs",
    },
  },
  thresholds: {
    http_req_duration: ["p(95)<500"],
    http_req_failed: ["rate<0.01"],
    checks: ["rate>0.99"],
    dropped_iterations: ["count==0"],
  },
};

export function setup() {
  return setupWorkflow();
}

export function runWorkflowThroughput(data) {
  workflowThroughput(data);
}

export function runQueryReadLoad() {
  queryReadLoad();
}

export function runWorkerActivateJobs() {
  workerActivateJobs();
}
