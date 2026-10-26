# ADR-0005: Additive `/v1` aliases and RFC 9457 errors

- Status: Accepted
- Date: 2026-08-06

## Context

The established `1.x` gateway contract exposes unversioned `/api/*` and
`/api/query/*` paths, with errors predominantly returned as `text/plain`.
Those contracts must remain available, while integrations also need an
explicit API version and machine-readable errors.

## Decision

Versioning is introduced with additive aliases:

- command `/api/v1/...` aliases the corresponding `/api/...`;
- query `/api/v1/query/...` aliases the corresponding `/api/query/...`;
- direct development service paths follow the same suffix convention.

Aliases invoke the same authorization, validation, idempotency, transaction,
and response code. Successful response bodies and field meanings remain
identical during `1.x`. The existing unversioned routes remain supported and
are not redirects; adding aliases does not start a removal clock.

Errors from versioned aliases use RFC 9457 Problem Details with
`Content-Type: application/problem+json`. The required members are `type`,
`title`, and `status`; `detail` is safe for the caller, and `instance` is an
opaque request occurrence. Stable extensions may include `code`,
`correlation_id`, `invalid_params`, and `retryable`. Problem `type` is an
absolute documentation URI and its meaning is immutable. Authentication
challenges still include `WWW-Authenticate`; rate and availability errors use
standard retry headers.

Unversioned routes keep their existing status, content type, and body shape
through `1.x`. Internal errors, SQL details, tokens, tenant existence, and
foreign-resource existence are never exposed. Unknown and unauthorized
tenant-scoped resources remain indistinguishable.

The OpenAPI document lists old and versioned paths, reusable Problem Details
schemas, content types, and examples. Contract tests assert route parity for
success and status semantics, and exact problem shape for aliases.

## Consequences

- Existing HTTP, SDK, worker, and user-task clients do not change.
- New clients can pin `v1` and branch on stable problem `code` values instead
  of parsing prose.
- A future incompatible HTTP contract uses a new path version; it does not
  reinterpret `/v1`.
- RFC 9457 rollout is complete only when gateway, command, query, and generated
  clients preserve correlation and content negotiation end to end.
