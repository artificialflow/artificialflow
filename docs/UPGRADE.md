# Upgrade Guide

Operator checklist for upgrading ArtificialFlow with durable state.

## Upgrading from 1.2.x to the next release

The next release has breaking configuration changes. The
[changelog](../CHANGELOG.md) lists them all. Do these before you roll:

1. **IAM mode.** Set `IAM_DEPLOYMENT_MODE` (Helm `iam.mode`) to `true` for an
   OIDC provider or `false` for no IAM. `external` and `disabled` fail
   startup.
2. **Bundled IAM removed.** Move users, clients, and roles to your own OIDC
   provider. The `/identity/management/*` APIs and the `*_FILE` auth settings
   are gone.
3. **Roles.** Rename roles in your provider to `artificialflowadmin`,
   `artificialflowbusiness`, `artificialflowclient`, and
   `artificialflowanalytics`. Old names are not recognized. Scopes are no
   longer checked.
4. **End users.** `/inbox*` and the acting headers are gone. Use
   `GET /tasks` and claim or complete with an `assignee`. Update SDK calls to
   `searchTasks`, `claimUserTask`, and `completeUserTask`.
5. **Event transport.** Kafka with Debezium is the only transport. Remove
   `EVENT_BUS_TYPE` and any NATS settings.
6. **Compose.** Use the single `docker-compose.yml`. Remove references to the
   old extra Compose files and profile targets. No `.env` is needed.
7. **Helm values.** Remove `migration.*` (rendering fails while it is set).
   Move `kafka` and `connect` to the top level and extra settings to `env`
   maps. Dependencies are now bundled by default: set
   `<dependency>.bundled.enabled: false` for each managed service you use.
8. **Database rights.** Command and runtime migrate at startup, so the user in
   `PG_DSN` needs DDL rights. Size `startupProbe.failureThreshold` for the
   migration window.

## Runtime tenancy migration

The additive `0002_runtime_tenancy_migration` migration backfills every
tenant-owned command row to the reserved `default` tenant, adds immutable
outbox metadata and fenced per-item claim columns, creates runtime work and
bounded instance-migration audit tables, and installs composite hot-path
indexes. Command and runtime apply it when they start.

Keep `TENANT_MODE` unset and `RUNTIME_SCHEDULER_MODE=compat` during a rolling
1.x upgrade. After all pods run the new schema-aware binary, validate trusted
tenant claims and then optionally enable `TENANT_MODE=strict`. To activate
three runtime replicas, switch all runtime pods to
`RUNTIME_SCHEDULER_MODE=multi-active` together. Roll back to `compat` without
dropping additive columns or tables.

Capture query-plan evidence after representative statistics with:

```bash
psql "$PG_DSN" -v ON_ERROR_STOP=1 -f scripts/explain_runtime_hot_paths.sql
```

## Before you start

1. Read [COMPATIBILITY_MATRIX.md](COMPATIBILITY_MATRIX.md) and release notes for the target version.
2. Confirm external Postgres, Kafka, Elasticsearch, and Debezium Connect are healthy (bundled or managed).
3. Schedule a maintenance window if you expect projection rebuilds.

## SLO checks (pre- and post-upgrade)

| Signal | How to check | Target |
| :--- | :--- | :--- |
| Outbox pending | Metrics `artificialflow_outbox_pending` / [RUNBOOK_OUTBOX_IDEMPOTENCY.md](RUNBOOK_OUTBOX_IDEMPOTENCY.md) | Drain or stable near zero before cutover |
| CQRS lag | Deploy/start then query visibility; sync-worker logs | p99 under nominal load &lt; 15s |
| Runtime lease | Runtime logs show `lease_holder`; only one active tick worker | Failover &lt; lease TTL + tick |

## Upgrade procedure

1. **Backup Postgres** (command database is source of truth).
2. **Pin image tags/digests** in Helm values (`images.*`) or with `ARTIFICIALFLOW_IMAGE_TAG` for Compose.
3. **Review Helm value diffs** for the chart minor/major; apply migration notes (including [PERSISTENT_DATA_RENAME_MIGRATION.md](PERSISTENT_DATA_RENAME_MIGRATION.md) when renaming releases).
4. **Drain outbox** — wait until pending/processing counts are acceptable ([RUNBOOK_OUTBOX_IDEMPOTENCY.md](RUNBOOK_OUTBOX_IDEMPOTENCY.md)).
5. **Roll application services.** Command and runtime apply pending
   migrations when they open Postgres, under one advisory lock, verify
   checksums, and only then start serving. Helm rolls them with
   `maxUnavailable: 0`, so a failed migration leaves the old pods serving and
   the new pod not ready. Raise `startupProbe.failureThreshold` before an
   upgrade whose release notes mention a long migration. Runtime may briefly
   pause ticks during pod replace (lease TTL bounds recovery).
   - To migrate out of band instead, run `./workflow-migrate` from the command
     image, then verify with `./workflow-migrate validate`.
   - The application database user needs DDL rights on its schema.
6. **Verify**:
   - `/health` on command and query
   - OIDC login
   - Deploy a test process, start an instance, activate a job, claim and complete a user task via `GET /tasks`
   - Query projection shows the new instance ([RUNBOOK_CQRS_SYNC.md](RUNBOOK_CQRS_SYNC.md))
7. **If projection is stale or corrupt** — follow CQRS rebuild steps in the CQRS runbook, then re-check lag.

## Operator checklist

- [ ] Postgres backup completed and restore-tested
- [ ] Image tags/digests recorded
- [ ] Outbox drained / lag acceptable
- [ ] Schema is current: new command and runtime pods are ready, or `workflow-migrate validate` passes
- [ ] Helm/Compose upgrade applied
- [ ] Health endpoints green
- [ ] Worker smoke (activate/complete)
- [ ] User-task smoke (claim/complete)
- [ ] Query shows new instance within SLO
- [ ] Runtime lease holder present in logs

## Rollback

1. Redeploy previous image tags/digests. Migrations must be expand-compatible
   during the rollback window, so an application rollback does not require a
   database rollback.
2. Do not edit or delete rows from `artificialflow_schema_migrations`. Never
   modify an applied SQL migration; add the next ordered migration instead.
3. Do not restore Postgres unless a separately reviewed contract migration
   made rollback incompatible. If restoration is unavoidable, restore search
   indexes or rebuild projections afterward.
4. Re-run the operator checklist.

Run `make test-schema-migrations` before release when Docker is available. It
captures unmigrated-binary rejection, concurrent advisory-lock serialization,
fresh install, repeated apply, unknown/partial-legacy rejection, rich v1.1.0
adoption,
row/state/payload invariants, checksum-tamper rejection, additive N-1
validation, GORM model drift against the migrated schema, singular Debezium
table names, and a `pg_dump`/`pg_restore` round-trip with old-application
read-contract evidence.

Every schema change ships as expand, migrate, contract (see
[ADR-0001](adr/0001-versioned-expand-contract-migrations.md)): during a
rollout old pods run against the new schema, so a release may only add
nullable columns, tables, and indexes. Renames and drops wait for a later
release.
