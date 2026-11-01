# Operations Guide

## Health Checks

- `/livez` is process-only and never calls an external dependency. A failure
  means the container's HTTP loop is unavailable and restart is appropriate.
- Command `/readyz` checks PostgreSQL connectivity and the embedded migration
  ledger/checksums. Query `/readyz` checks search. `/health` remains a
  compatibility alias for readiness.
- Runtime serves private `/livez` and `/readyz` on its metrics listener;
  readiness checks PostgreSQL and schema compatibility.
- Sync `/readyz` stays `503` until topic/connector bootstrap completes, checks
  search on every probe, and can additionally fail on stale consumption when
  `SYNC_HEALTH_FAIL_ON_STALE=true`. Its `/livez` is process-only.
- Frontend `/readyz` verifies its generated static artifact exists. Gateway
  `/readyz` proxies that frontend check; gateway `/livez` is local.

Readiness does not call IAM because issuer availability is not required to
validate already cached JWT keys in every mode. Production acceptance must
separately verify authenticated `/identity/me`, broker/connector status, and a
write-to-query projection. Metrics and health listeners remain cluster-private.

## Metrics and Internal Signals

See [OBSERVABILITY.md](OBSERVABILITY.md) for exact series, bounded labels,
trace propagation, collection ports, dashboard installation, and known
measurement limits.

### Dependency readiness

For `ArtificialFlowDependencyUnavailable`, identify the `service` and
`dependency` label, check the private `/readyz`, then validate network policy,
DNS/TLS, credentials, and dependency saturation. Do not restart repeatedly:
liveness deliberately remains healthy during dependency outages.

### Schema readiness

For `ArtificialFlowSchemaIncompatible`, stop rollout traffic, run
`workflow-migrate validate`, and read the startup logs of the newest command
or runtime pod. Never bypass an unknown version or checksum mismatch. Apply
the expand migration with the versioned migration binary before restarting
workloads.

### API SLO alerts

Correlate the error-ratio or latency alert with route/status panels and traces.
Check dependency readiness and saturation first. Preserve a sample trace and
the exact PromQL window in the incident. Maintenance exclusions must have been
declared before the window.

### Outbox and projection alerts

Outbox age/backlog indicates command-to-broker publication delay. Inspect
publisher dependency errors, retry rate, and terminal failures. A dead letter
requires payload/schema compatibility review and an explicit replay decision.
Projection freshness is only time since the last consumed message; compare it
with Kafka exporter committed-offset/high-watermark lag before declaring user
impact. Never delete a DLQ or reset offsets without preserving evidence.

### Runtime alerts

Any fenced claim failure can indicate owner overlap, clock/database pressure,
or a stale writer. Capture pod identities, database timestamps, lease TTL,
claim generation, and the affected operation. Pause multi-active runtime if
stale-fence mutation is suspected.

## Backups

Production deployments must back up:

- Postgres.
- Search indexes only as an optimization; PostgreSQL/event history remains the
  authoritative input for a projection rebuild.
- IAM configuration and secrets.
- Helm values and release manifests.

Target objectives are PostgreSQL RPO ≤ 5 minutes and RTO ≤ 60 minutes;
projection RPO is zero from authoritative retained state and rebuild RTO ≤ 4
hours. These are objectives, not claims: record measured start/end timestamps,
dataset size, backup age, and parity output for every isolated drill.

### PostgreSQL backup

Use a transactionally consistent managed snapshot or:

```bash
PG_DSN="$SOURCE_DSN" BACKUP_FILE=/secure/backup/artificialflow.dump \
  bash scripts/recovery/postgres_backup.sh
```

Encrypt and replicate the dump and `.sha256` sidecar outside the failure
domain. The script uses custom format, verifies the archive catalog, and never
prints the DSN. Application database backups do not include IAM secrets,
broker retention/offsets, Helm values, or encryption keys.

### Isolated restore and validation

1. Provision an empty, access-restricted target with the same or newer
   supported PostgreSQL major version. Keep application traffic disabled.
2. Restore only after positively identifying the isolated target:

```bash
RESTORE_PG_DSN="$TARGET_DSN" BACKUP_FILE=/secure/backup/artificialflow.dump \
  ALLOW_DESTRUCTIVE_RESTORE=true \
  bash scripts/recovery/postgres_restore.sh
```

3. Run `PG_DSN="$TARGET_DSN" workflow-migrate validate`. Never run application
   traffic against a schema that fails version, checksum, or fingerprint
   validation.
4. Compare source/pre-backup and restored fingerprints:

```bash
PG_DSN="$TARGET_DSN" bash scripts/recovery/dataset_fingerprint.sh
```

The fingerprint includes migration checksums, per-table counts/checksums, and
per-tenant counts. It is integrity evidence, not a cryptographic backup proof
or a substitute for domain-level functional tests.
5. Rotate credentials used for the drill, run tenant-scoped authenticated
   smoke tests, and retain the signed release evidence.

### Projection rebuild

Create a new versioned index, replay authoritative events/CDC from the retained
point, wait for the consumer high-watermark, then compare tenant plus stable
keys:

```bash
SOURCE_KEYS_COMMAND='...' PROJECTION_KEYS_COMMAND='...' \
  bash scripts/recovery/projection_parity.sh
```

Only atomically switch the query alias after zero missing/extra keys, matching
tenant sets, sampled document payload checks, and query smoke tests. Keep the
old index until rollback expires. Use `make init-connector` only as a manual
local/development path; production connector configuration and offsets must be
versioned and reviewed.

For a disposable recovery drill or a maintenance-window deployment that still
uses direct canonical index names, stop command, runtime, and sync-worker
writers before running:

```bash
PG_DSN="$RESTORED_DSN" ES_ADDR="$ISOLATED_SEARCH_URL" \
  bash scripts/recovery/projection_rebuild.sh
PG_DSN="$RESTORED_DSN" ES_ADDR="$ISOLATED_SEARCH_URL" \
  bash scripts/recovery/projection_parity_http.sh
```

This direct-name procedure deletes and recreates the eight projection indexes;
it is not a zero-downtime production alias switch. Keep writers stopped through
parity validation. Before restarting writers, move the projection consumer to
the first event after the authoritative database snapshot. Purging retained
events is allowed only in an isolated drill when parity proves the snapshot
contains their final state; otherwise preserve and replay them. Restart writers
and run an authenticated write/read smoke.

`scripts/test_recovery.sh` runs a disposable PostgreSQL restore, validates
migration compatibility, exact tenant counts, and positive/negative projection
parity. The release gates remain fail-closed and require environment-specific
backup, restore, rebuild, and parity commands; no checked-in report is treated
as live evidence.

## Production Checklist

- TLS enabled.
- Default credentials replaced.
- Strict OIDC audience validation enabled.
- Secrets stored outside source control.
- Resource requests and limits configured.
- Backup and restore tested.
- Monitoring and alerts configured.
