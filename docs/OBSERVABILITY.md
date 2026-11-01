# Observability

## Endpoints and collection

Command and query expose Prometheus on the private `:9090/metrics` listener.
Runtime exposes metrics and health on its private metrics listener (the chart
sets `:9090`; the binary default is `:9091`). Sync exposes them on its private
`:8092` service. The chart does not route any of these listeners through the
gateway or ingress.

Set `monitoring.enabled=true` only when Prometheus Operator CRDs and Grafana
sidecar discovery are installed. This creates four `ServiceMonitor` resources,
a `PrometheusRule`, and the `ArtificialFlow Overview` dashboard ConfigMap.

The authenticated JSON engine metrics remain available to admins:

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:9100/api/internal/metrics
```

## Metric contract

| Signal | Source | Use |
| :--- | :--- | :--- |
| Series | Meaning |
| :--- | :--- |
| `artificialflow_http_requests_total{service,method,route,status}` | Bounded-template HTTP request/error rate |
| `artificialflow_http_request_duration_seconds{service,method,route}` | HTTP latency histogram |
| `artificialflow_outbox_pending` | Pending publication count |
| `artificialflow_outbox_publish_lag_seconds` | Age of the oldest pending message seen by the relay |
| `artificialflow_outbox_publish_{success,failure}_total` | Publication outcomes |
| `artificialflow_outbox_retries_total` / `artificialflow_outbox_dead_letters_total` | Scheduled retries and terminal failures |
| `artificialflow_projection_messages_total` / `artificialflow_projection_{success,failure,retries,dead_letters}_total` | Projection consumer outcomes |
| `artificialflow_projection_freshness_seconds` | Wall time since the last consumed message; `-1` before the first message |
| `artificialflow_dependency_ready{service,dependency}` | Last bounded dependency probe result |
| `artificialflow_schema_ready{service}` | PostgreSQL migration/checksum compatibility |
| `artificialflow_runtime_failures_total{reason}` | Claim, fencing, repository, timer, and job failures |
| `artificialflow_work_lag_seconds{service,kind}` | Timer/job/projection lag where the service can measure it |
| `artificialflow_instance_migration_outcomes_total{mode,outcome}` | Dry-run/apply outcomes |

Labels intentionally exclude tenant IDs, workflow/instance/job IDs, URLs,
broker topics, error strings, and credentials. Route labels use mux templates.

## Tracing

Set `OTEL_EXPORTER_OTLP_ENDPOINT` to an internal OTLP/HTTP collector. Command
and query create inbound server spans; Elasticsearch calls use an instrumented
transport. W3C `traceparent`, `tracestate`, and baggage are carried through the
outbox envelope and Kafka headers and extracted by sync-worker. Gateway
forwards trace and correlation headers. Tenant context is carried separately
as the validated envelope `tenant_id`; bearer tokens, connector secrets,
payloads, and arbitrary error text are never span attributes.

Connector workers retain the context supplied to their handler for variable
loading, outbound calls, completion, and failure. The current HTTP polling
worker protocol does not serialize remote trace context in activated jobs, so
command-to-connector traces can be separate traces; correlation remains
available through job/operation IDs in structured logs.

## SLOs and alerts

The checked-in rules implement the command/query 99.9% availability and p95
latency thresholds in [KUBERNETES_REFERENCE.md](KUBERNETES_REFERENCE.md).
They also alert on dependency/schema readiness, outbox backlog/age/dead
letters, projection staleness/dead letters, and runtime fencing failures.
Every alert links to [operations.md](operations.md). Tune backlog and
freshness thresholds only with retained load and recovery evidence.

Projection freshness is not broker offset lag. Kafka committed-offset/high
watermark lag still requires Kafka exporter metrics, and connector health
requires the connector platform's metrics. A missing exporter is `not
measured`, never healthy evidence.

Frontend samples projection freshness after query-backed list fetches and
shows it in the header banner. See [CAPACITY.md](CAPACITY.md) and
[UPGRADE.md](UPGRADE.md) for CQRS lag validation.
