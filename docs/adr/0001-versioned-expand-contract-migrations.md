# ADR-0001: Versioned expand/contract database migrations

- Status: Accepted, amended 2026-10-22 (see Amendment)
- Date: 2026-08-06

## Context

The original `1.x` startup path used GORM `AutoMigrate`. Multiple replicas could
therefore attempt schema changes, with no durable schema version or serialized
migration owner. Production upgrades must remain compatible with rolling `1.x`
deployments, Debezium capture, the outbox, and search projections.

## Decision

Production schema changes are ordered, immutable migration files recorded in a
schema-history table and applied once by a dedicated pre-upgrade Job. Application
pods never run DDL. Development may retain an explicit convenience migration
mode, disabled in production.

Every rolling upgrade uses three phases:

1. **Expand:** add nullable columns, new tables, compatible indexes, or dual
   representations. Do not rename, drop, narrow, or reinterpret data.
2. **Migrate:** deploy code that reads old and new forms and writes both where
   needed; run restartable, idempotent, rate-limited backfills with recorded
   progress. Update connector capture and projections additively.
3. **Contract:** only after all supported old binaries are retired, backfill and
   projection parity are proven, and the deprecation window has elapsed, remove
   obsolete schema in a later release.

Migration identity, checksum, start/end time, and result are durable. A
Postgres advisory lock serializes runners. Failure stops rollout before
application workloads update. Forward repair is the normal rollback strategy;
restoring a backup requires the tested database-and-event-stream recovery plan.

## Consequences

- A release image declares the schema range it can read and write and refuses
  startup outside that range.
- Migrations that can lock large tables require a measured lock budget,
  statement timeout, cancellation procedure, and rehearsal on representative
  data.
- Debezium, outbox consumers, and projections must tolerate both forms for the
  overlap period.
- Existing `1.x` data, endpoint meanings, environment variables, and Helm values
  remain unchanged during expansion.

## Amendment (2026-10-22): the application owns migrations

The dedicated pre-upgrade Job is removed. Command and runtime apply the
embedded, versioned migrations when they open Postgres, before their HTTP
listener starts, then validate the ledger. Everything else above still holds:
ordered immutable files, checksums, one advisory lock serializing every
runner, and expand/migrate/contract phasing.

Failure still stops the rollout: Deployments roll with `maxUnavailable: 0`, so
a pod whose migration fails never becomes ready and the old pods keep serving.
A Kubernetes `startupProbe` bounds how long a migration may run before
liveness checks begin. Because old pods run against the expanded schema while
new pods start, expand compatibility is mandatory, not advisory.

Consequences of the amendment:

- The application database user needs DDL rights on its schema.
- `workflow-migrate` remains for operators who apply or validate migrations
  out of band; running it first is optional.
- A test compares the GORM models with both the migrated schema and the
  required-schema fingerprint, so a model change without a migration fails CI.
