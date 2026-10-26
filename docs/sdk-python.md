# Python worker

`clients/python-sdk` is a small worker client. It polls for jobs of one type,
runs your handler, and completes or fails each job. It covers the worker
protocol (`/jobs/*`) only. For deploys, instances, and user tasks use the
[HTTP API](API.md) or the [Node.js](sdk-nodejs.md), [.NET](sdk-dotnet.md), or
[Java](sdk-java.md) SDK.

- Package: `artificialflow-worker` (import `artificialflow_worker`)
- Requires Python 3.10 or newer
- Not published to PyPI. Install it from a clone.

## Install

```bash
pip install -e clients/python-sdk
```

## Authentication

| `IAM_DEPLOYMENT_MODE` | `token` |
| :--- | :--- |
| `true` | A raw access token (no `Bearer ` prefix) for a client with the `artificialflowclient` role |
| `false` | Any string, for example `""`. The API ignores the header. |

The worker always sends `Authorization: Bearer <token>`. It does not fetch or
refresh tokens. For long-running workers in `true` mode, restart the worker or
build a new `Worker` before the token expires.

## Complete one job

This example works against the local Compose stack. It needs a deployed
process with a service task of type `golden-validate`, for example
[`examples/golden-demo/order-approval.bpmn`](../examples/golden-demo/order-approval.bpmn).

```python
import os
from artificialflow_worker import Worker

def handle(job):
    print("handling job", job["key"], "for element", job.get("elementId"))
    return {"validated": True}

Worker(
    base_url=os.getenv("ARTIFICIALFLOW_BASE_URL", "http://localhost:9100/api"),
    token=os.getenv("ARTIFICIALFLOW_TOKEN", ""),
    job_type="golden-validate",
    worker_name="python-worker-1",
    handler=handle,
).run()
```

`run()` loops until you stop the process.

- The handler gets the job as a `dict` (`key`, `type`, `processInstanceKey`,
  `elementId`, `retries`, ...). Jobs do not carry process variables. Read them
  with `GET /instances/{processInstanceKey}` (the `context` field) if you need
  them.
- Return a `dict` of variables to complete the job.
- Raise an exception to fail the job. The worker sends the exception text and
  `retries - 1`.
- HTTP and network errors are swallowed. The worker sleeps
  `poll_interval_sec` and tries again.

## Options

| Argument | Default | Meaning |
| :--- | :--- | :--- |
| `base_url` | required | Gateway API root, for example `http://localhost:9100/api` |
| `token` | required | See [Authentication](#authentication) |
| `job_type` | required | Service task type to poll |
| `worker_name` | required | Worker name sent on activate, complete, and fail |
| `handler` | required | `callable(job: dict) -> dict` |
| `max_jobs` | `1` | Jobs per activate call |
| `activate_timeout_ms` | `5000` | Long-poll wait. Known issue: it is sent as `timeout` instead of `timeoutMs`, so the server uses its default wait. |
| `lock_duration_ms` | `30000` | Job lock length |
| `poll_interval_sec` | `1.0` | Sleep after an empty poll or an error |

The worker does not extend locks. Keep handlers shorter than
`lock_duration_ms`, or use the [Go worker](sdk-go.md) or an SDK that renews
locks.

## Related

- [Worker API](worker-api.md): the protocol, idempotency, and lock rules
- [Go worker](sdk-go.md)
- [Getting started](getting-started.md#run-a-worker)
