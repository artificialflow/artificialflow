# Outbox and Idempotency Runbook

## Signals

Review:

- `/health`
- `/internal/metrics`
- Outbox publish lag.
- Idempotency cleanup counters.
- Terminal failed outbox records.

## Common Actions

- If publish lag grows, inspect Kafka/Debezium Connect connectivity and command service logs.
- If terminal failed records appear, review payloads and downstream broker errors.
- If idempotency storage grows unexpectedly, verify cleanup configuration and retention.

## Relevant Settings

- `OUTBOX_RELAY_ENABLED`
- `OUTBOX_RELAY_INTERVAL`
- `OUTBOX_RELAY_TIMEOUT`
- `OUTBOX_RELAY_BATCH_SIZE`
- `OUTBOX_RELAY_MAX_ATTEMPTS`
- `IDEMPOTENCY_CLEANUP_ENABLED`
- `IDEMPOTENCY_RETENTION`
- `IDEMPOTENCY_CLEANUP_INTERVAL`
