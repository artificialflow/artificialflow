# Agentic QA

ArtificialFlow QA agents translate repository changes into a practical validation plan. They recommend what to run, collect evidence, identify missing coverage, and call out residual risk. They do not decide that a release is ready without maintainer review.

Use this document with `docs/QUALITY_GATES.md`, `docs/STABILITY_POLICY.md`, and `docs/COMPATIBILITY_MATRIX.md`.

## QA Responsibilities

QA agents should:

- Map changed files to user-facing journeys, APIs, deployment assets, and compatibility surfaces.
- Recommend the smallest useful validation set first, then escalate to deep gates when risk is higher.
- Separate command-side success from query/CQRS projection visibility.
- Record commands run, reports produced, skipped checks, and residual risk.
- Maintain a backlog of missing coverage that is useful to contributors.

QA agents should not:

- Run heavy Docker, browser, k6, or release validation unless the task calls for it or a maintainer approves it.
- Mark manual QA complete without human evidence.
- Treat scanner warnings as release-blocking without severity and impact review.
- Add broad tests unrelated to the changed behavior.

## Exhaustive Tester Harness

Use `make test-all-functionality` when a maintainer asks for live, broad validation across deployment models and functional surfaces. The target runs `scripts/test-all-functionality.sh`, which is live-by-default and executes serially to avoid port and container conflicts.

The harness records:

- Machine-readable evidence in `reports/all-functionality-report.json`.
- Human-readable evidence in `reports/all-functionality-report.md`.
- BPMN scenario evidence in `reports/bpmn-matrix-report.json` and `reports/bpmn-matrix-report.md`.
- Per-layer logs under `reports/all-functionality/`.

Supported flags include `--skip-deployments`, `--skip-ui`, `--skip-sdk-live`, `--skip-perf`, `--skip-security`, `--skip-helm-live`, `--allow-helm-live`, `--fail-fast`, `--continue-on-failure`, `--reset-volumes`, `--reports-dir`, and `--wait-timeout-sec`. Skipped live checks must remain visible in the report with a reason; agents should not describe a run as exhaustive when required infrastructure was skipped.

Focused helpers:

- `make test-bpmn-exhaustive` runs the BPMN scenario catalog in `tests/bpmn/matrix/scenarios.yml`.
- `make test-deployment-matrix` runs the live deployment-oriented harness path while skipping UI, SDK live, performance, and security layers.

## Functional QA Matrix

| Surface | Existing evidence | Recommended agent action |
| :--- | :--- | :--- |
| BPMN parser/runtime | `make test-bpmn-matrix`, `make test-bpmn-exhaustive`, backend workflow-command tests, `tests/bpmn/matrix/scenarios.yml`. | Require targeted Go tests for BPMN behavior changes and recommend matrix tests for parser/runtime semantics. Track unsupported and missing XML-runtime coverage as explicit report entries. |
| Command API | Handler tests under workflow-command. | Check deploy/start/task/message/signal/job APIs and idempotency behavior for API changes. |
| Query API and CQRS projection | Query handler tests, sync-worker tests, `make cqrs-e2e-smoke`, `make cqrs-parity-check`. | Require targeted query/sync tests; recommend CQRS smoke when write-to-read visibility changes. |
| External worker API | Protocol/idempotency/capabilities tests, `make worker-conformance`. | Treat worker REST changes as compatibility-sensitive and require conformance evidence. |
| IAM and authorization | Auth library tests, HTTP authorization tests, deployment IAM docs. | Require role/claim tests and manual review for auth mode, audience, or role mapping changes. |
| Frontend modeler/admin UI | Vitest helper/API tests and lightweight Playwright. | Recommend lint/test/build for all frontend changes and deeper Playwright/manual QA for routed UI flows. |
| Node.js SDK | SDK tests, package validation, npm pack dry run. | Require SDK tests and package validation for public method, auth, worker, or packaging changes. |
| Docker Compose and Helm | `make smoke`, `make smoke-release`, `make validate-helm`. | Require compose/Helm validation for deployment asset changes. |

## Non-Functional QA Matrix

| Area | Existing command or evidence | When to run |
| :--- | :--- | :--- |
| Performance | `make test-perf` with k6 scripts. | Worker activation, CQRS query, throughput, variable-size, or hot-path changes. |
| Security | `make test-security`, `.github/workflows/security.yml`. | Auth, IAM, dependency, Dockerfile, GitHub Actions, parser input, or secret-handling changes. |
| Resilience | Outbox/idempotency tests, sync-worker retry tests, CQRS smoke/parity. | Kafka/Debezium/Elasticsearch, outbox, retries, duplicate events, or lock handling changes. |
| Observability | Metrics endpoint tests and health checks. | New services, health states, metrics labels, logs, or operational runbooks. |
| Packaging | `make release-dry-run`, npm package validation, Docker build dry runs. | Release workflows, Dockerfiles, SDK package metadata, versioning, or SBOM/provenance changes. |
| Compatibility | `docs/COMPATIBILITY_MATRIX.md`, `docs/STABILITY_POLICY.md`, worker conformance. | Public API, SDK, Helm values, image env vars, IAM claims, or BPMN semantics changes. |

## E2E Journey Matrix

| Journey | Current coverage | Gap or next step |
| :--- | :--- | :--- |
| Deploy BPMN and start instance through command API | Playwright API journey and integration tests. | Add browser modeler deploy journey with validation errors and success state. |
| Worker activates and completes jobs | Worker conformance and backend tests. | Expand real-job conformance cases for complete, fail, extend-lock, timeout, and retry behavior. |
| Command writes appear in query read model | `make cqrs-e2e-smoke`, `make cqrs-parity-check`. | Add failure-mode coverage for lag, duplicate events, replay, and backfill. |
| Frontend dashboard/process/instance navigation | Basic page-load coverage. | Add realistic UI paths for dashboard, processes, instances, empty/loading/error states. |
| IAM login and role boundaries | Unit/handler coverage and deployment docs. | Add external OIDC (Keycloak) E2E checks for `artificialflowadmin`/`artificialflowbusiness`/`artificialflowclient`/`artificialflowanalytics` behavior and the `IAM_DEPLOYMENT_MODE=false` console login. |
| Node SDK workflow and worker use | SDK mocked tests and live smoke example. | Add versioned contract fixtures and release-smoke SDK examples. |
| Release candidate smoke | `make release-dry-run`, release workflows. | Add RC readiness summary that includes artifacts, known limitations, and upgrade notes. |

## Security, Resilience, And Observability Testing

Security QA should include:

- Secret scanning and dependency scanning from `.github/workflows/security.yml`.
- Auth and authorization unit tests for role, claim, token, and audience changes.
- Docker and npm audit review for supply-chain changes.
- Private escalation for real vulnerability reports.

Resilience QA should include:

- Idempotency and outbox regression tests for command-side writes.
- CQRS smoke/parity checks for sync changes.
- Worker lock and retry tests for job lifecycle changes.
- Explicit skipped-test notes when infrastructure is unavailable.

Observability QA should include:

- Health endpoint behavior for service startup and degraded dependencies.
- Required metrics names, labels, and cardinality review.
- Logs that help diagnose failures without exposing secrets.
- Diagnostic artifact expectations for CQRS, Playwright, k6, and release dry runs.

## Compatibility Testing

Compatibility-sensitive changes require additional care:

- Worker REST API: run `make worker-conformance` and update `docs/worker-api.md` when behavior changes.
- Node.js SDK: run SDK tests, package validation, and `npm pack --dry-run`.
- Docker env vars and Compose profiles: run `make smoke` and `make smoke-release`.
- Helm values: run `make validate-helm` and document migration notes for renamed or removed values.
- IAM roles and claims: run auth tests and update `docs/iam.md` if behavior changes.
- BPMN semantics: run `make test-bpmn-matrix` and update `docs/BPMN_SUPPORT_MATRIX.md` for support changes.

## Regression Testing

Agents should prefer targeted regression tests for narrow changes:

| Changed area | First checks | Escalate when |
| :--- | :--- | :--- |
| `backend/services/workflow-command` | `go test ./backend/services/workflow-command/... -count=1` | BPMN/runtime/API behavior crosses compatibility surfaces. |
| `backend/services/workflow-query` or `backend/services/sync-worker` | `go test ./backend/services/workflow-query/... ./backend/services/sync-worker/... -count=1` | Projection behavior, index mapping, or event ordering changes. |
| `backend/libs/auth` | Targeted auth tests plus relevant HTTP authorization tests. | Token validation, claims, roles, or external IAM behavior changes. |
| `frontend/` | `npm --prefix frontend run lint`, `npm --prefix frontend test`, `npm --prefix frontend run build`. | Routed UI, auth, modeler, or API-facing behavior changes. |
| `clients/nodejs-sdk/` | `npm --prefix clients/nodejs-sdk test`, package validation, `npm pack --dry-run`. | Public API, auth, worker behavior, or release packaging changes. |
| `.github/`, `scripts/`, Docker, Helm | `make smoke`, `make validate-helm`, shell syntax checks. | Release, security, image, or deploy behavior changes. |

## Test Evidence Expectations

Every QA plan or review should include:

- Changed surfaces and risk level.
- Commands already run with pass/fail/skip status.
- Artifacts produced, such as `reports/summary.md`, coverage, Playwright traces, k6 JSON, security reports, or release dry-run output.
- Exhaustive harness artifacts when run: `reports/all-functionality-report.json`, `reports/all-functionality-report.md`, `reports/bpmn-matrix-report.json`, and `reports/bpmn-matrix-report.md`.
- Manual QA requested or completed.
- Residual risks and maintainer decisions.

If a command is too heavy for the current context, the agent should say so and recommend when to run it.

## Changed-File Risk Rules

Use these defaults when planning validation:

- Docs-only changes: markdown/link validation if available; no Docker tests unless docs affect release, deployment, or security instructions.
- Test-only changes: run the changed test package or test runner; inspect whether coverage claims changed.
- Public API, SDK, worker, IAM, Docker env, Helm values, and BPMN semantics: require compatibility review.
- Release workflows, Dockerfiles, package metadata, and signing/SBOM paths: require release-readiness review.
- Security workflow, auth, dependency, GitHub Actions, or secret-handling changes: require security triage.
- CQRS/outbox/sync changes: require command-side tests plus projection smoke/parity when infrastructure behavior changes.

## Missing Coverage Backlog

High-priority gaps:

- Browser E2E for modeler deploy, dashboard, processes, instances, task/job completion, and error states.
- IAM E2E for external OIDC login and client_credentials token flows, including admin/business/client/analytics role boundaries.
- CQRS failure-mode tests for Kafka, Debezium Connect, Elasticsearch, replay, lag, duplicate events, and schema changes.
- Worker conformance expansion with real jobs and negative mutation cases.
- Frontend component tests for pages, forms, loading, empty, sync-lag, and error states.

Medium-priority gaps:

- k6 soak and high-cardinality workflow scenarios.
- Large variable payload behavior and worker long-poll concurrency.
- Helm rendered manifest assertions for resource requests, security context, service accounts, and network exposure.
- Required metrics/log fields and correlation IDs.
- Cross-version worker/SDK contract fixtures.

Agents should turn this backlog into small, labeled issues rather than one broad QA epic.
