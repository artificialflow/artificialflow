# Golden demo

Bake-off path: **service worker + user task + boundary timer**.

## Process

File: [`order-approval.bpmn`](order-approval.bpmn)

1. Start instance with variables `{ "orderId": "…" }`.
2. External worker type `golden-validate` completes the service task.
3. User `approver` (or candidate group `approvers`) claims/completes the user task via `GET /tasks` and the claim/complete routes.
4. Optional: boundary timer `PT1H` on the user task demonstrates SLA/timer handling.

## Run

Without IAM (Compose default), no token is needed:

```bash
make up            # or make up-release from a release tag checkout
node ./examples/golden-demo/run-bakeoff.mjs
```

With `IAM_DEPLOYMENT_MODE=true`, mint a token for a client with the
`artificialflowclient` role first:

```bash
# Reads OIDC_TOKEN_URL, OIDC_CLIENT_ID, OIDC_CLIENT_SECRET (optional OIDC_SCOPE)
# and writes .local/bakeoff.token:
node ./scripts/mint_bakeoff_token.mjs
export ARTIFICIALFLOW_BASE_URL=http://localhost:9100/api
export ARTIFICIALFLOW_TOKEN="$(cat .local/bakeoff.token)"
export ARTIFICIALFLOW_END_USER=approver

node ./examples/golden-demo/run-bakeoff.mjs
```

The script also deploys [`invoice_decision.json`](invoice_decision.json) and
continues if that fails.

Lease failover drill (Compose):

```bash
./scripts/runtime-lease-failover-compose.sh
```

## Capacity numbers

After the stack is warm, run `make test-perf` with `AUTH_TOKEN` / `WORKFLOW_ID` set and fill [docs/CAPACITY.md](../../docs/CAPACITY.md).

## Success criteria

- Instance reaches the user task after the worker completes.
- The user task is found with `GET /tasks` and claimed/completed with `assignee` set to the end user.
- Query/history UI shows the completed instance within CQRS lag SLO.
- Runtime lease failover unit test passes (`go test ./backend/services/workflow-command/internal/infrastructure/persistence -run LeaseFailover`).
