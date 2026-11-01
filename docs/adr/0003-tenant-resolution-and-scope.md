# ADR-0003: Tenant resolution and end-to-end scope

- Status: Accepted
- Date: 2026-08-06

## Context

Authentication maps a configurable `tenant_id` claim into the principal, while
workflow deployment currently writes tenant `"default"` and persistence
queries are not uniformly tenant-scoped. Existing single-tenant `1.x`
installations and stored rows depend on that default.

## Decision

Tenant mode is an explicit deployment setting:

- **Compatibility mode:** missing tenant claims resolve to `"default"`. This is
  the default for existing and upgraded `1.x` single-tenant installations.
- **Strict mode:** every protected human or machine principal must resolve one
  non-empty tenant from the configured trusted claim. Missing, malformed, or
  conflicting tenant identity is rejected before a handler runs.

Request bodies (including the user-task `assignee`), query parameters, BPMN content, and worker
payloads never select or override tenant. Administrators operate within their
resolved tenant unless a separately audited platform operation explicitly
defines cross-tenant scope.

Tenant is carried in typed request context through command, runtime, workers,
outbox/events, connectors, sync, projections, and query. Every tenant-owned
row and event has a non-null tenant key. Repository interfaces require tenant
scope; reads, writes, uniqueness constraints, idempotency keys, job claims,
message correlation, caches, indexes, object keys, metrics, and deletion all
include it. Unknown and foreign-tenant identifiers return the same not-found
response.

A client token that claims or completes user tasks on behalf of end users acts
only within its own resolved tenant; the `assignee` it supplies cannot change
tenant. Event consumers validate
tenant presence and quarantine invalid records; projection documents and
aliases cannot combine tenants. Backup/restore and export/import preserve
tenant keys and authorization checks.

Strict mode may be enabled only after existing `"default"` data is inventoried,
backfilled where necessary, all repositories and projections pass isolation
tests, and tokens reliably emit the configured claim.

## Consequences

- Existing single-tenant clients remain compatible without adding a claim.
- A client-supplied tenant value is never an authorization mechanism.
- End-to-end tests must attempt cross-tenant reads, mutations, correlations,
  worker completion, user-task search/claim/complete, projection queries, and
  replay.
- Tenant identifiers in telemetry are bounded, access-controlled labels; raw
  tenant data is not emitted to logs or traces.
