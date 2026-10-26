# Architecture decision records

Accepted decisions define the target contract. A decision that describes
behavior not yet present in every service is an adoption constraint, not a claim
that the behavior is already implemented.

- [ADR-0001: Versioned expand/contract database migrations](0001-versioned-expand-contract-migrations.md)
- [ADR-0002: Database-time fenced runtime claims](0002-database-time-fenced-runtime-claims.md)
- [ADR-0003: Tenant resolution and end-to-end scope](0003-tenant-resolution-and-scope.md)
- [ADR-0004: Bounded process-instance migration](0004-bounded-process-instance-migration.md)
- [ADR-0005: Additive `/v1` aliases and RFC 9457 errors](0005-v1-aliases-and-problem-details.md)

Superseding an accepted decision requires a new ADR. Implementations must retain
the `1.x` compatibility surfaces in
[the stability policy](../STABILITY_POLICY.md) until their documented
deprecation window has elapsed.
