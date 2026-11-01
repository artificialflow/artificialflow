# High Availability Reference

This document describes the supported ArtificialFlow HA topology for production Kubernetes deployments.

## Supported replica topology

| Component | Recommended replicas | Notes |
| :--- | :--- | :--- |
| Gateway | ≥ 2 | Stateless NGINX; safe to scale. |
| Frontend | ≥ 2 | Stateless; safe to scale. |
| Command API | ≥ 2 | Stateless HTTP/gRPC; Postgres is the consistency boundary. |
| Query API | ≥ 2 | Stateless reads from Elasticsearch. |
| Sync worker | ≥ 1 (Kafka consumer group) | Scale up to the topic partition count (`kafka.topicPartitions`, default 3); extra replicas sit idle. Use a stable consumer group id. |
| Runtime | 3 | `multi-active` uses independently claimed timer, SLA, conditional-scan, maintenance, and outbox rows. `compat` retains the 1.x single-active scheduler. |

External dependencies (Postgres, Kafka, Elasticsearch, Debezium Connect for Kafka CDC) must themselves be HA according to your platform standards.

## Runtime modes

Set `runtime.schedulerMode=multi-active` and `replicaCount.runtime=3` for
active-active runtime processing. Claims are selected in bounded batches with
`FOR UPDATE SKIP LOCKED`; PostgreSQL issues claim time and expiry. Every owner
transition increments `claim_generation`, and completion compares tenant,
owner, generation, and unexpired database time. A paused or stale replica
therefore cannot commit after another replica reclaims the row.

`RUNTIME_CLAIM_BATCH_SIZE` defaults to `100`. Keep claim TTL greater than twice
the tick interval and shorter than the operational recovery objective.

### 1.x compatibility mode

The runtime process acquires a row lease in `runtime_scheduler_lease` before each tick (`TryAcquireRuntimeSchedulerLease`).

- Holder identity: `RUNTIME_LEASE_HOLDER` (default `hostname-pid`).
- TTL: `RUNTIME_LEASE_TTL` (default `15s`, at least `2 × RUNTIME_TICK_INTERVAL`).
- A replica renews its own lease; another replica steals only after expiry.

The default `RUNTIME_SCHEDULER_MODE=compat` preserves the global
`runtime_scheduler_lease` behavior for existing deployments. Its expiry is
also calculated using PostgreSQL time. Use it during rolling upgrades, then
switch all runtime replicas to `multi-active` together.

### Failover verification

Unit coverage: `TestRuntimeSchedulerLeaseFailover` in the persistence package.

Compose drill (no Kubernetes required):

```bash
# Stack must already be up (make up or make up-release)
./scripts/runtime-lease-failover-compose.sh
# Optional: RUNTIME_SERVICE=workflow-runtime ./scripts/runtime-lease-failover-compose.sh
```

The script restarts the runtime container and checks it resumes within `RUNTIME_LEASE_TTL + RUNTIME_TICK_INTERVAL` (+ buffer). Record measured RTO in [CAPACITY.md](CAPACITY.md).

Manual Kubernetes check:

1. Set `runtime.schedulerMode=multi-active` and scale runtime to 3.
2. Confirm all three owners acquire distinct work and claim generations rise.
3. Pause one owner beyond the TTL while the other two continue.
4. Resume it and confirm stale completion is rejected with `runtime claim lease lost`.

Helm ships PodDisruptionBudgets for command/query/frontend/gateway when replicas ≥ 2. Do not PDB-protect a single runtime replica as if it were multi-active.

## Failure modes

| Failure | Expected behavior |
| :--- | :--- |
| Command pod loss | In-flight requests fail; clients retry with `Idempotency-Key`. |
| Runtime pod loss | Other replicas continue and reclaim expired per-item work using database time. |
| Sync worker loss | Projection lag grows until consumers catch up; command writes remain durable. |
| Postgres unavailable | Writes and runtime ticks fail; restore Postgres first. |
| Elasticsearch unavailable | Query/UI degraded; command path continues. |
| Kafka/Debezium unavailable | Projection lag / outbox backlog; follow [RUNBOOK_CQRS_SYNC.md](RUNBOOK_CQRS_SYNC.md) and [RUNBOOK_OUTBOX_IDEMPOTENCY.md](RUNBOOK_OUTBOX_IDEMPOTENCY.md). |

## Capacity evidence

Performance scenarios live under `tests/performance/k6/`:

- `workflow_throughput` — deploy/start load
- `worker_activate_jobs` — job activation
- `query_read_load` — query API reads
- `cqrs_lag` — write-to-query visibility lag samples

Run:

```bash
make test-perf
```

Publish p50/p99 from the k6 JSON report with each release that claims capacity numbers. See [CAPACITY.md](CAPACITY.md) for the latest published snapshot format.

## Helm guidance

- Use three runtime replicas with `multi-active`; keep one active plus optional standbys only while using `compat`.
- Prefer PDB on command, query, gateway, and frontend.
- Prefer managed, HA Postgres, Kafka, Debezium Connect, and Elasticsearch in production. Set `<dependency>.bundled.enabled: false` and give their addresses; the bundled StatefulSets are single-replica.
- Configure ingress TLS and secrets per [deployment.md](deployment.md).
