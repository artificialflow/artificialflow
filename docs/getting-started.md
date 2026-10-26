# Getting Started

This guide starts ArtificialFlow on one machine, runs a process end to end, and
connects a worker. It ends with the path to Kubernetes.

## Compose or Helm

| | Docker Compose | Helm chart |
| :--- | :--- | :--- |
| Use for | Local evaluation and development | Kubernetes, staging, production |
| File | `docker-compose.yml` | `charts/artificialflow` |
| Default IAM | `IAM_DEPLOYMENT_MODE=false` (no IAM) | `iam.mode: true` (external OIDC) |
| Dependencies | Postgres, Elasticsearch, Kafka, Debezium Connect in containers | Bundled StatefulSets by default, or managed services |
| Guide | This page | [Deployment guide](deployment.md#3-helm) |

Both run the same images and the same configuration keys.

## Prerequisites

- Docker Engine or Docker Desktop with Compose v2
- Make, `curl`, and `jq`
- Free local ports `5432`, `8080`, `8081`, `8083`, `8092`, `9092`, `9100`,
  and `9200`
- For a worker: Node.js 20+, Python 3.10+, or Go 1.26

## Start the stack

`docker-compose.yml` needs no `.env`. `IAM_DEPLOYMENT_MODE` is a boolean,
`true` or `false`. Any other value fails startup.

| `IAM_DEPLOYMENT_MODE` | Console login | API and SDKs |
| :--- | :--- | :--- |
| `false` (Compose default) | Local sign-in, `admin` / `admin` (`CONSOLE_ADMIN_USERNAME` / `CONSOLE_ADMIN_PASSWORD`) | No token needed. Every call runs as admin. |
| `true` | Your OIDC provider | Bearer token from your OIDC provider. Roles come from the token. |

`false` is for local evaluation only. The console login does not protect the
API.

Without IAM:

```bash
make up            # build from source
make up-release    # or pull published images (from a release tag checkout)
```

With your OIDC provider:

```bash
IAM_DEPLOYMENT_MODE=true \
AUTH_ISSUER_INTERNAL_URL=https://login.example.com \
AUTH_ISSUER_PUBLIC_URL=https://login.example.com \
make up
```

Before you start in `true` mode, create the backend audience
(`workflow-backend`), the browser client (`workflow-frontend`), a machine
client with the `artificialflowclient` role, and the four roles. Provider
notes:

- [Keycloak](iam/KEYCLOAK.md)
- [Microsoft Entra ID](iam/ENTRA.md)
- [Auth0](iam/AUTH0.md)
- Any other OIDC provider: the [IAM guide](iam.md#external-iam-provisioning-checklist)

Set `AUTH_CLIENT_ID`, `FRONTEND_AUTH_OIDC_CLIENT_ID`, or
`AUTH_CLAIM_ROLES_PATH` if your names differ from the defaults.

## Verify

```bash
curl -fsS http://localhost:9100/api/health
curl -fsS http://localhost:9100/api/query/health
curl -fsS http://localhost:8092/health
curl -fsS http://localhost:8083/connectors/artificialflow-postgres-connector/status
```

Open <http://localhost:9100> and sign in.

| Surface | URL |
| :--- | :--- |
| UI and gateway | <http://localhost:9100> |
| Command API | <http://localhost:9100/api/v1> |
| Query API | <http://localhost:9100/api/v1/query> |

## Run a process

The examples use the demo process
[`examples/golden-demo/order-approval.bpmn`](../examples/golden-demo/order-approval.bpmn).
It has a service task of type `golden-validate` and a user task for
`approver`.

In `true` mode, get a token for your machine client first and add
`-H "Authorization: Bearer $TOKEN"` to each call. Skip this in `false` mode.

```bash
export API=http://localhost:9100/api/v1

curl -fsS -X POST "$API/workflows" \
  -H "Content-Type: application/xml" \
  --data-binary @examples/golden-demo/order-approval.bpmn

INSTANCE_ID=$(curl -fsS -X POST "$API/instances" \
  -H "Content-Type: application/json" \
  -d '{"workflow_id": "golden_order_approval", "context": {"orderId": "ord-1"}}' \
  | jq -r .id)
echo "$INSTANCE_ID"
```

The instance now waits at the `golden-validate` service task for a worker.

## Run a worker

Pick one client. Each completes the waiting `golden-validate` job.

**Node.js** ([guide](sdk-nodejs.md)). The SDK smoke test activates and
completes one job:

```bash
cd clients/nodejs-sdk && npm ci && npm run build && cd ../..
ARTIFICIALFLOW_BASE_URL=http://localhost:9100/api \
ARTIFICIALFLOW_WORKER_JOB_TYPE=golden-validate \
node clients/nodejs-sdk/examples/sdk-smoke-test.js
```

In `true` mode, also set `ARTIFICIALFLOW_TOKEN`.
[`examples/golden-demo/run-bakeoff.mjs`](../examples/golden-demo/run-bakeoff.mjs)
runs the whole demo in one script: deploy, start, worker, and user task.

**Python** ([guide](sdk-python.md)):

```bash
pip install -e clients/python-sdk
python3 - <<'PY'
import os
from artificialflow_worker import Worker

Worker(
    base_url="http://localhost:9100/api",
    token=os.getenv("TOKEN", ""),
    job_type="golden-validate",
    worker_name="python-worker-1",
    handler=lambda job: {"validated": True},
).run()
PY
```

Stop it with Ctrl+C after it completes the job.

**Go** ([guide](sdk-go.md)):

```bash
ARTIFICIALFLOW_BASE_URL=http://localhost:9100/api \
ARTIFICIALFLOW_TOKEN="$TOKEN" \
WORKER_JOB_TYPE=golden-validate \
go run ./examples/go-worker
```

The [.NET](sdk-dotnet.md) and [Java](sdk-java.md) SDKs also include workers.
The protocol is in the [Worker API](worker-api.md).

## Complete the user task

After the worker finishes, the instance waits at the `approveOrder` user task.

```bash
TASK=$(curl -fsS "$API/tasks?processInstanceKey=$INSTANCE_ID" | jq '.tasks[0]')
EXECUTION_ID=$(echo "$TASK" | jq -r .executionId)

curl -fsS -X POST "$API/instances/$INSTANCE_ID/tasks/$EXECUTION_ID/claim" \
  -H "Content-Type: application/json" -d '{"assignee": "approver"}'
curl -fsS -X POST "$API/instances/$INSTANCE_ID/tasks/$EXECUTION_ID/complete" \
  -H "Content-Type: application/json" -d '{"assignee": "approver"}'
```

The query API shows the instance after a short projection delay:

```bash
curl -fsS "$API/query/instances/$INSTANCE_ID"
```

## Smoke test with an SDK

Without the worker setting, the Node.js smoke test checks the principal and
lists workflows:

```bash
ARTIFICIALFLOW_BASE_URL=http://localhost:9100/api \
node clients/nodejs-sdk/examples/sdk-smoke-test.js
```

In `true` mode, set `ARTIFICIALFLOW_TOKEN`, or set `OIDC_TOKEN_URL`,
`OIDC_CLIENT_ID`, and `OIDC_CLIENT_SECRET`. See
[Node.js SDK](sdk-nodejs.md#smoke-test) for the other settings.
`clients/dotnet-sdk/examples/SdkSmokeTest.cs` and
`clients/java-sdk/examples/SdkSmokeTest.java` do the same for .NET and Java.

## Stop

`make down` stops the stack. `make clean` also deletes the volumes.

## Move to Kubernetes

No Helm repository is published. Install the chart from a clone of the release
tag:

```bash
git clone --depth 1 --branch v1.2.0 https://github.com/artificialflow/artificialflow.git
cd artificialflow

# Without a provider, for a smoke test:
helm upgrade --install artificialflow ./charts/artificialflow \
  --namespace artificialflow --create-namespace \
  -f ./charts/artificialflow/values-kind-smoke.yaml
```

For a real install with OIDC, ingress, and managed dependencies, follow the
[deployment guide](deployment.md#3-helm).
