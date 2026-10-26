# ADR-0002: Database-time fenced runtime claims

- Status: Accepted
- Date: 2026-08-06

## Context

The implemented runtime is single-active. It renews
`runtime_scheduler_lease`, but computes expiry from each pod's wall clock and
uses holder identity without a monotonic fencing token. That supports standby
failover but does not safely authorize concurrent schedulers or exclude a
paused former owner after its lease expires.

## Decision

The current single-active lease remains the `1.x` compatibility mode. Any
multi-active runtime is enabled only after work is divided into independently
claimable database rows or stable shards and all side effects enforce fencing.

Claims use Postgres time (`transaction_timestamp()` for one statement or
`clock_timestamp()` when elapsed time within a transaction matters), never pod
time. One atomic statement:

- acquires only unowned or database-expired work;
- increments a monotonic `fence_token` on every ownership transition;
- records owner, database-issued `claimed_at`, and `expires_at`;
- returns the claim key and token.

Renew, complete, release, and state transitions compare both owner and token.
Zero affected rows means ownership was lost and processing stops. Downstream
effects carry `(claim_key, fence_token, operation_id)`; database writes reject
stale tokens and external effects use an idempotency key plus a durable outbox.
No unfenced network call may be treated as exactly once.

Claims are short, bounded batches selected with `FOR UPDATE SKIP LOCKED`.
Transactions do not span user code or connector calls. Reclamation occurs only
after database-time expiry. Operators may scale multi-active replicas only when
claim contention, stale-token rejection, duplicate-effect, and expiry metrics
are available.

## Consequences

- Host clock skew cannot grant overlapping valid claims.
- A delayed former owner cannot commit after a newer owner receives a higher
  token.
- Single-active deployments and existing lease settings keep working while the
  fenced path is introduced additively.
- Failover and chaos tests must cover pause-after-acquire, renewal races,
  database reconnects, and duplicate delivery.
