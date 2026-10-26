# ArtificialFlow Python worker SDK

Minimal Python client for the ArtificialFlow worker protocol (`/jobs/*`).

## Install

```bash
pip install -e clients/python-sdk
```

## Example

```python
from artificialflow_worker import Worker

def handle(job):
    return {"ok": True, "elementId": job.get("elementId")}

Worker(
    base_url="http://localhost:9100/api",
    token="...",
    job_type="golden-validate",
    worker_name="python-sample",
    handler=handle,
).run()
```

`token` is a raw access token with the `artificialflowclient` role when the
deployment runs with `IAM_DEPLOYMENT_MODE=true`. With `IAM_DEPLOYMENT_MODE=false`
(no IAM provider) the API ignores the header, so pass `token=""`.

This package covers the worker protocol only. For workflow, instance, and
user-task calls use the [HTTP API](../../docs/API.md) or the Node.js, .NET, or
Java SDK.

Requires Python 3.10+. Full guide: [docs/sdk-python.md](../../docs/sdk-python.md).
