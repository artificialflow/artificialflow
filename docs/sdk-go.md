# Go worker

`backend/libs/worker` is the Go worker library. It polls for jobs of one type,
runs your handler, and completes or fails each job. It can also renew job locks
while a handler runs. It covers the worker protocol (`/jobs/*`) only. For
deploys, instances, and user tasks use the [HTTP API](API.md) or the
[Node.js](sdk-nodejs.md), [.NET](sdk-dotnet.md), or [Java](sdk-java.md) SDK.

- Import: `github.com/artificialflow/artificialflow/backend/libs/worker`
- Job type: `github.com/artificialflow/artificialflow/backend/libs/model`
- Sample binary: [`examples/go-worker`](../examples/go-worker/main.go)
- Package reference: [backend/libs/worker/README.md](../backend/libs/worker/README.md)

## Authentication

| `IAM_DEPLOYMENT_MODE` | `ClientConfig.BearerToken` |
| :--- | :--- |
| `true` | A raw access token (no `Bearer ` prefix) for a client with the `artificialflowclient` role |
| `false` | Leave empty. No header is sent. |

The client does not fetch or refresh tokens. For long-running workers in
`true` mode, build a new client before the token expires, or pass an
`HTTPClient` whose transport adds a fresh token.

## Complete one job

This example works against the local Compose stack. It needs a deployed
process with a service task of type `golden-validate`, for example
[`examples/golden-demo/order-approval.bpmn`](../examples/golden-demo/order-approval.bpmn).

```go
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/libs/worker"
)

func main() {
	client, err := worker.NewClient(worker.ClientConfig{
		BaseURL:     "http://localhost:9100/api",
		BearerToken: os.Getenv("ARTIFICIALFLOW_TOKEN"),
	})
	if err != nil {
		log.Fatal(err)
	}

	w, err := worker.NewWorker(client, worker.WorkerConfig{
		JobType:    "golden-validate",
		WorkerName: "go-worker-1",
		Handler: func(ctx context.Context, job model.Job) (map[string]any, error) {
			log.Printf("handling job %d for element %s", job.Key, job.ElementID)
			return map[string]any{"validated": true}, nil
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		log.Fatal(err)
	}
}
```

`RunOnce` makes one activate call and handles the jobs it gets. `Run(ctx)`
loops until `ctx` is cancelled, then returns `nil`. The sample in
[`examples/go-worker`](../examples/go-worker/main.go) uses `Run` with signal
handling:

```bash
ARTIFICIALFLOW_BASE_URL=http://localhost:9100/api \
WORKER_JOB_TYPE=golden-validate \
go run ./examples/go-worker
```

Handler rules:

- Return variables to complete the job.
- Return an error to fail the job with that message.
- A `context.Canceled` or `context.DeadlineExceeded` error leaves the job
  locked. It is retried after the lock expires.
- Jobs do not carry process variables. Read them with
  `GET /instances/{processInstanceKey}` (the `context` field) if you need them.

## Options

`ClientConfig`:

| Field | Default | Meaning |
| :--- | :--- | :--- |
| `BaseURL` | required | Gateway API root, for example `http://localhost:9100/api` |
| `BearerToken` | empty | See [Authentication](#authentication) |
| `HTTPClient` | 30 second timeout | Custom transport, TLS, or proxy |

`WorkerConfig`:

| Field | Default | Meaning |
| :--- | :--- | :--- |
| `JobType` | required | Service task type to poll |
| `WorkerName` | required | Worker name sent on every call |
| `Handler` | required | `func(ctx, model.Job) (map[string]any, error)` |
| `MaxJobs` | `1` | Jobs per activate call |
| `ActivateTimeout` | 5 s | Long-poll wait |
| `LockDuration` | 30 s | Job lock length |
| `LockRenewInterval` | off | Extend the lock on this interval while the handler runs |
| `ErrorBackoff` | none | Sleep after a failed loop iteration in `Run` |
| `OnError` | none | Called with each loop error in `Run` |

The client also exposes `ActivateJobs`, `CompleteJob`, `FailJob`,
`ExtendJobLock`, and `GetCapabilities` for custom loops.

## Related

- [Worker API](worker-api.md): the protocol, idempotency, and lock rules
- [Python worker](sdk-python.md)
- [Getting started](getting-started.md#run-a-worker)
