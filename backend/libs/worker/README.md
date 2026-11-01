# Worker SDK (Go)

Public Go worker library for ArtificialFlow external jobs (`github.com/artificialflow/artificialflow/backend/libs/worker`).

Sample binary: [`examples/go-worker`](../../../examples/go-worker). Guide: [docs/sdk-go.md](../../../docs/sdk-go.md).

Minimal external worker SDK for ADR-0006.

## Features

- Activate jobs (`POST /jobs/activate`)
- Discover engine worker capabilities (`GET /jobs/capabilities`)
- Complete jobs (`POST /jobs/{key}/complete`)
- Fail jobs (`POST /jobs/{key}/fail`)
- Extend lock (`POST /jobs/{key}/extend-lock`)
- Optional lock renewal heartbeat while handlers run (`LockRenewInterval`)
- Worker loop with handler callback (`Run` / `RunOnce`)
- Protocol negotiation header (`X-Workflow-Worker-Protocol-Version`, current: `v1`)

## Protocol compatibility

- SDK requests send `X-Workflow-Worker-Protocol-Version: v1`.
- Engine worker endpoints return `X-Workflow-Engine-Protocol-Version: v1`.
- To discover endpoint capabilities dynamically, call `GetCapabilities`.
- For safe retries on mutation endpoints (`complete`, `fail`, `extend-lock`), send `Idempotency-Key`.

### Idempotency contract notes

- Idempotency replay scope is operation + job key (for example `jobs.complete:{jobKey}`).
- Reusing the same `Idempotency-Key` for the same operation scope returns a replay-safe `200` response.
- Use distinct keys across different operations (`complete` vs `fail` vs `extend-lock`) even for the same job.

## Quick start

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
		BaseURL: "http://localhost:9100/api",
		// Required only with IAM_DEPLOYMENT_MODE=true: a raw access token
		// with the artificialflowclient role.
		BearerToken: os.Getenv("ARTIFICIALFLOW_TOKEN"),
	})
	if err != nil {
		log.Fatal(err)
	}

	w, err := worker.NewWorker(client, worker.WorkerConfig{
		JobType:    "external-payment",
		WorkerName: "payments-worker-1",
		MaxJobs:    5,
		Handler: func(ctx context.Context, job model.Job) (map[string]any, error) {
			// business logic
			return map[string]any{"approved": true}, nil
		},
		ActivateTimeout: 5 * time.Second,
		LockDuration:    30 * time.Second,
		// Optional: renew lock every 10s while handler is still processing.
		// Keep this lower than LockDuration.
		LockRenewInterval: 10 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := w.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
```
