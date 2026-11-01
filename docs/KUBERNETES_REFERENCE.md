# Kubernetes production reference

This is the review rubric for a production Helm installation. It supplements
the [deployment guide](deployment.md); values are objectives to validate for a
specific environment, not capacity guarantees.

## Reference topology

- One production namespace per environment, with separate service accounts and
  default-deny ingress/egress policy.
- TLS ingress exposes only the gateway. Command, query, runtime, sync, search,
  event transport, databases, metrics, and administration endpoints remain
  private.
- At least two failure domains for gateway, frontend, command, and query pods,
  using topology spread or anti-affinity and PodDisruptionBudgets.
- Runtime uses three active replicas in `multi-active` mode. PostgreSQL-time
  claims, monotonic generations, and compare-and-set completion fence timer,
  SLA, conditional-scan, maintenance, and outbox work. The `compat` mode remains
  available for rolling upgrades from older 1.x deployments.
- Sync workers use a stable consumer group and scale no higher than useful
  event partitions. Projection rebuild uses an isolated, rate-limited workload.
- Production Postgres, Kafka, Debezium Connect when used, search, IAM,
  secret management, backups, and telemetry are externally operated HA
  services. Do not place their failure quorum in one cluster failure domain.

The base chart preserves compatibility defaults. Apply
`values-production-reference.yaml` for the validated topology: three
multi-active runtime replicas, two sync workers, and a runtime PDB requiring two
available replicas. Environment values must still pin image digests and set
measured resources before approval.

The chart enables default-deny NetworkPolicies. Before rollout, populate
`networkPolicy.external.postgresqlCIDRs`, `brokerCIDRs`, `searchCIDRs`,
`iamCIDRs`, and `telemetryCIDRs` with operator-controlled destination CIDRs and
set `networkPolicy.ingressNamespace` to the ingress controller namespace.
Policies cannot safely derive stable egress rules from optional DNS hostnames;
empty lists intentionally leave those external dependencies unreachable. If a
provider has no stable CIDRs, use an approved egress proxy/gateway and allow
only that endpoint.

`gateway.hsts.enabled` is accepted only with chart-managed TLS ingress. Disable
it when TLS assurance and HSTS are managed by an external edge. Configure
`cors.allowedOrigins` as a comma-separated exact-origin allowlist; wildcard
origins are rejected by release validation.

All workloads use distinct `/livez` and `/readyz` semantics documented in
[operations.md](operations.md). Runtime health is served safely on its private
metrics listener; it is not a public API. Set `monitoring.enabled=true` only in
a cluster with Prometheus Operator CRDs and Grafana dashboard sidecar
discovery. The resulting Services remain `ClusterIP`, and ingress exposes only
the gateway.

## Workload rubric

An installation is production-ready only when every item below has evidence:

- **Identity:** dedicated service accounts; no default service account or
  unnecessary token mount; workload and cloud permissions are least privilege.
- **Scheduling:** requests and limits are set from load evidence; replicated
  services spread across failure domains; priority and eviction behavior are
  documented.
- **Health:** startup, readiness, and liveness checks have distinct purposes.
  Readiness removes a pod that cannot serve its dependencies without causing a
  restart loop. Runtime ownership health and sync freshness are observable.
- **Availability:** PDBs and rollout surge/unavailable settings preserve the
  required serving replicas. Node drain and active-runtime deletion drills pass.
- **Scaling:** HPA is limited to stateless services and uses a nonzero floor.
  Runtime is not CPU-autoscaled. Sync scaling respects partition count and lag.
- **Security:** TLS, default-deny network policy, allowed egress, non-root
  execution, seccomp, dropped capabilities, read-only filesystems where
  supported, secret references, and admission checks are verified from rendered
  manifests and the live cluster.
- **State and recovery:** no application rollout performs concurrent DDL.
  Database, event offsets, search rebuild, IAM, and retained volumes have
  recovery point/time objectives and a successful restore drill.
- **Rollout:** schema expansion finishes first; canary or rolling application
  rollout proves authenticated command and query traffic; rollback criteria,
  version skew, and projection compatibility are recorded.
- **Observability:** metrics, structured logs, traces, audit events, alerts,
  dashboards, ownership, and runbook links exist for every SLO and dependency.

## SLO and drill rubric

Use a rolling 30-day window unless a stricter organizational standard applies.
Exclude only predeclared maintenance; dependency failures count when users are
affected.

<!-- markdownlint-disable MD013 -->

| Objective | Indicator and target | Required evidence |
| :--- | :--- | :--- |
| Command availability | ≥ 99.9% valid authenticated command requests do not return unexpected `5xx` | Gateway/service metrics split by route and status |
| Query availability | ≥ 99.9% valid authenticated query requests do not return unexpected `5xx` | Gateway/service metrics split by route and status |
| Command latency | p95 < 150 ms and p99 < 250 ms for the agreed baseline payload, excluding asynchronous completion | Histogram and reproducible load profile with at least 10,000 command observations |
| Query latency | p95 < 750 ms and p99 < 1.5 s for the agreed baseline query | Histogram and reproducible load profile |
| Projection freshness | 99% of accepted command changes visible in query ≤ 5 s; p99 lag reported | End-to-end event timestamp samples plus committed-offset/high-watermark lag |
| Runtime recovery | Active-owner loss resumes eligible work within `lease TTL + tick interval + 10 s` | Quarterly pod-loss drill with timestamps |
| Durable recovery | Postgres RPO ≤ 5 min and RTO ≤ 60 min; projection RPO 0 from authoritative state and RTO ≤ 4 h | At least semiannual isolated restore/rebuild drill |
| Release correctness | 100% of required postconditions pass before traffic and no stale-fence mutation occurs | Release report, parity check, and chaos evidence |

<!-- markdownlint-enable MD013 -->

For each SLO, record owner, data source, exact query, alert threshold, error
budget, and runbook. A target with no continuously collected indicator is
`not measured`, not passing. Capacity approval also records peak tested rate,
dataset size, payload distribution, pod resources, dependency sizing, and
sustained-test duration.

## Acceptance evidence

Retain the rendered Helm diff, policy/admission results, image digest and
signature verification, vulnerability results, load report, authenticated
deploy/start/query smoke, worker smoke, projection parity, runtime failover,
node drain, dependency interruption, and restore report. Review topology after
material traffic, data-shape, tenant, connector, or dependency changes.
