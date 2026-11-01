# Threat model

## Scope and assumptions

This model covers the gateway, frontend, command and query APIs, runtime, sync
worker, SDK/worker clients, connectors, Postgres, event transport, search
projections, IAM, backups, telemetry, and build/release chain. Postgres command
state is authoritative; search is eventually consistent. Compose defaults are
untrusted development settings. Production uses TLS, externalized stateful
services, secret management, and verified release artifacts.

Protected assets include identities and credentials, tenant data, BPMN and
decision definitions, process state and variables, jobs and human tasks,
connector secrets and effects, audit history, event and projection integrity,
backups, and release signing material.

## Trust boundaries and data flow

1. Browser, SDK, worker, and connector traffic crosses the public network into
   the gateway.
2. The gateway crosses into command/query service identities and private
   service networks.
3. IAM token issuance and validation cross an independently administered trust
   boundary.
4. Command/runtime cross into authoritative Postgres; runtime and external
   workers cross an execution/delegation boundary.
5. Outbox, Kafka, and Debezium cross an asynchronous, replayable delivery
   boundary.
6. Sync crosses from events into non-authoritative search projections.
7. Connectors cross from workflow data into arbitrary external systems.
8. Backup and telemetry pipelines cross into operator-controlled systems with
   different retention and access policies.
9. Source, dependencies, CI, registry, chart, and cluster admission form the
   software supply-chain boundary.

No private network, header, event, projection, or backup location is trusted
solely because of where it runs.

## Threats and required controls

### Identity, tenant isolation, and delegation

- Validate issuer, audience, expiry, signature or introspection result, role,
  and configured tenant claim at every public API. Reject ambiguous claims.
- Resolve tenant from trusted identity only and enforce
  [end-to-end tenant scope](adr/0003-tenant-resolution-and-scope.md), including
  repositories, jobs, events, projections, caches, exports, and restores.
- Machine workers use least-privilege identities. End users of client
  applications are never authenticated by ArtificialFlow; a client passes them
  as `assignee` or filter values, which cannot elevate role or change tenant.
  Record both the client subject and the assignee in immutable audit data.
- Return indistinguishable not-found responses for missing and foreign-tenant
  resources. Test direct object reference, correlation, list/filter, replay,
  and user-task search/claim/complete paths.

### Runtime and workers

- Treat jobs, BPMN, variables, messages, and worker results as hostile input;
  enforce size, schema, retry, lock, and execution-time bounds.
- Use idempotency and transactional outbox for retried effects. Multi-active
  execution requires
  [database-time fenced claims](adr/0002-database-time-fenced-runtime-claims.md);
  stale owners cannot mutate state.
- Do not log secrets or full sensitive variables. Prevent one worker type from
  activating or completing work outside its authorized type and tenant.

### Connectors and outbound access

- Connectors run with separate identities, minimal network egress, bounded
  redirects/DNS resolution, request size/time limits, and destination
  allowlists to limit SSRF and exfiltration.
- Connector credentials come from secret references, not BPMN, ConfigMaps,
  events, logs, or images. Redact request/response data and rotate credentials.
- External calls use durable idempotency keys where supported; retries must not
  imply exactly-once delivery.

### Kafka, CDC, and projections

- Authenticate and encrypt producers, consumers, brokers, and Debezium. Apply
  topic/stream ACLs per service and environment.
- Assume duplicate, delayed, reordered, replayed, oversized, and malformed
  events. Validate schema/version and tenant, deduplicate by stable event ID,
  quarantine invalid records, and bound retry/dead-letter growth.
- Search is not an authorization source. Query authorization and tenant filters
  are applied before results return. Projection replay is deterministic and
  parity is measured against Postgres.

### Backups and recovery

- Encrypt backups in transit and at rest with separately controlled keys;
  restrict create/read/delete permissions and prevent routine workloads from
  deleting recovery copies.
- Define retention, immutability, tenant-aware export/restore, and secure
  disposal. Restore tests use isolated networks and verify Postgres, event
  offsets, projections, IAM state, and tenant isolation before traffic opens.
- Never attach old and new writers to the same restored mutable state.

### Telemetry and operations

- Treat logs, traces, metrics, crash dumps, and support bundles as sensitive.
  Redact tokens, authorization headers, connector secrets, user content, and
  variables; use bounded tenant labels and access-controlled retention.
- Correlation IDs are opaque and non-authoritative. Alerts and dashboards
  expose operational status without granting data-plane access.
- Administrative actions, delegation, migration, secret rotation, and restore
  operations require attributable audit events protected from application
  users.

### Supply chain and deployment

- Pin dependencies and release images; scan source, dependencies, IaC, and
  images. Produce SBOM and provenance, sign artifacts, verify them before
  rollout, and protect CI tokens and release environments.
- Review third-party actions and build scripts, use least-privilege ephemeral
  builders, prohibit secrets in build arguments/layers, and separate artifact
  creation from publication approval.
- Apply non-root containers, read-only filesystems where supported, resource
  limits, service accounts, network policy, secret stores, admission policy,
  and restricted infrastructure endpoints.

## Verification and review

Before each release, run the security gates in
[QUALITY_GATES.md](QUALITY_GATES.md), tenant negative tests, worker conformance,
projection parity, backup restore evidence, and artifact verification. Review
this model when a trust boundary, IAM/tenant rule, connector, event schema,
deployment mode, or data store changes. Track residual risks and owners in the
release evidence; confirmed vulnerabilities follow [SECURITY.md](../SECURITY.md).
