# Release maturity rubric

This rubric reports evidence, not aspiration. Score each dimension independently
and report the release at the lowest achieved level. A missing, skipped, stale,
or non-reproducible required result does not pass. Maintainer risk acceptance is
recorded separately and cannot raise the score.

## Levels

### Level 0 — unverified

Source may build locally, but required checks or artifact identity are missing.
This level is not publishable.

### Level 1 — repeatable candidate

All of the following are true:

- clean-checkout backend, frontend, and SDK builds pass;
- required unit, lint, BPMN, contract, Compose, and Helm validations pass;
- version, changelog, migration inventory, and known limitations agree;
- artifacts are immutable and identified by checksum or image digest;
- no confirmed secret or reachable high/critical vulnerability is open.

This level is suitable for internal test environments, not production.

### Level 2 — production candidate

Level 1 passes, plus:

- integration, authenticated end-to-end, worker conformance, CQRS smoke/parity,
  API/OpenAPI lint, migration rehearsal, and release-profile smoke pass;
- performance meets declared thresholds on a recorded dataset and topology;
- DAST, SAST, dependency, filesystem, and image scans meet
  [security policy](QUALITY_GATES.md#security-gates);
- SBOM and provenance exist for every release artifact, signatures verify, and
  installation pins immutable versions;
- upgrade from every supported `1.x` source version and rollback/forward-repair
  procedure are rehearsed without contract or data loss;
- threat-model review has no unowned release-blocking risk;
- Kubernetes workload checks and postconditions in
  [the production reference](KUBERNETES_REFERENCE.md) pass.

This level may enter a production canary with explicit approval.

### Level 3 — production proven

Level 2 passes, plus evidence from a representative production window:

- at least 30 consecutive days of measured SLOs meet every declared target;
- no severity-one incident attributable to the release is unresolved;
- error-budget burn alerts, paging ownership, and runbooks have been exercised;
- runtime failover and dependency-degradation drills pass in the last 90 days;
- an isolated backup restore and projection rebuild meet RPO/RTO in the last
  180 days;
- peak sustained load has at least 30% measured headroom at approved resource
  limits;
- tenant-isolation negative tests pass when strict multi-tenant mode is enabled;
- artifact verification and deployment audit records cover 100% of running
  release workloads.

Level 3 expires when evidence ages beyond its stated window or architecture,
tenant model, persistence, event path, or deployment topology materially
changes.

## Required scorecard

Every release report records `pass`, `fail`, or `not measured` and a durable
evidence link for:

1. source and reproducible builds;
2. functional and BPMN behavior;
3. API, SDK, worker, event, and `1.x` compatibility;
4. schema upgrade, backfill, and recovery;
5. security and threat-model review;
6. artifact SBOM, provenance, signing, and registry identity;
7. performance, capacity, and SLOs;
8. Kubernetes topology, policy, failover, and rollout;
9. backup restore and projection rebuild;
10. documentation, runbooks, ownership, and release approval.

The release report includes commit, tag, artifact digests, environment, commands,
timestamps, tool versions, dataset, deviations, approvers, and expiry dates.
Publication requires every Level 2 row to pass. Level 3 is assigned only after
the observation windows complete; it is never claimed at initial publication.

## Compatibility guardrails

Architecture adoption does not weaken existing contracts. Database changes
follow [ADR-0001](adr/0001-versioned-expand-contract-migrations.md); tenant,
runtime, migration, and versioned HTTP behavior follow the accepted
[architecture decisions](adr/README.md). Unversioned APIs, SDK/worker behavior,
environment variables, chart values, and stored `1.x` data remain supported
according to [STABILITY_POLICY.md](STABILITY_POLICY.md).
