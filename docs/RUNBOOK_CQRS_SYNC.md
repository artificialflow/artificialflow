# CQRS Sync Runbook

## Signals

Review:

- Sync worker `/health`.
- Query service `/health`.
- Kafka consumer group assignments.
- Debezium connector status.
- Elasticsearch index document counts.

## Checks

```bash
make cqrs-parity-check
make cqrs-e2e-smoke
```

## UI Consistency Contract

- Command API success means a deploy, start, or delete operation was accepted by the write side.
- Workflow and instance lists read from CQRS query projections and are eventually consistent with command writes.
- Single workflow and instance detail reads use command routes and are not subject to list projection lag.
- After command writes that affect query-backed lists, the frontend should show a syncing state and poll the relevant query list until the expected row appears or disappears.
- If frontend polling times out, treat the command as successful but the projection as still syncing; show Retry or Refresh instead of implying failure.
- Dashboard and aggregate views should describe data as a query projection, not as real-time data.
- Frontend polling is user-facing feedback only. Use `make cqrs-parity-check` and `make cqrs-e2e-smoke` for operational validation.

## Common Actions

- Confirm `KAFKA_TOPICS` matches the selected `SYNC_PROJECTION_CONTRACT`.
- Confirm Debezium connector registration when using CDC topics.
- Confirm search backend address and credentials.
- Re-run connector initialization only as a manual recovery path when automatic bootstrap is unavailable.

## Rebuild projection (recovery)

Use when query indexes are corrupt, missing documents after restore, or lag never recovers.

1. Confirm command Postgres is healthy (source of truth).
2. Pause or scale sync-worker to zero if you need a clean reindex window.
3. Delete or recreate the ArtificialFlow search indexes (prefix from `ES_INDEX_PREFIX` / Helm values).
4. Reset the sync-worker consumer group offsets only if you intend a full replay (Kafka CDC + event topics). Prefer documented connector slot/offset recovery for Debezium.
5. Scale sync-worker back up and watch `/health`, consumer lag, and document counts.
6. Run `make cqrs-parity-check` and `make cqrs-e2e-smoke`.
7. In the UI, use **Rebuild projection** (admin) when exposed; it links operators here and triggers the documented rebuild hooks.

See also [UPGRADE.md](UPGRADE.md) and [HA_REFERENCE.md](HA_REFERENCE.md).
