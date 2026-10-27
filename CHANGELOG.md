# Changelog

All notable changes to ArtificialFlow will be documented in this file.

The format is based on Keep a Changelog, and this project follows Semantic Versioning.

## [Unreleased]

### Breaking

- Removed the bundled IAM provider and the in-app `/identity/management/*`
  APIs, pages, and Node SDK identity-management methods. Users, clients, and
  tokens live in an external OIDC provider.
- Collapsed Docker Compose to a single `docker-compose.yml` and removed the
  extra Compose files and Makefile profile targets.
- The Helm chart bundles Postgres, Elasticsearch, Kafka, and Debezium Connect
  by default; each can be disabled in favor of a managed service.
- Reshaped Helm values: `kafka` and `connect` are top-level,
  `runtime.schedulerMode` replaces `runtime.env.schedulerMode`, service and
  metrics ports are fixed, and extra settings go through `env` maps
  (global, `command.env`, `query.env`, `runtime.env`, `syncWorker.env`).
  Rendering fails on secrets placed in `env`.
- Kafka with Debezium CDC is the only event transport. The `EVENT_BUS_TYPE`
  setting was removed.
- Go toolchain is now 1.26.10 (`go 1.26.0` minimum) in `go.mod`, Dockerfiles,
  and CI.
- Renamed roles to `artificialflowadmin` (full access), `artificialflowbusiness`
  (modeler: deploy/read workflows and decisions), `artificialflowclient`
  (SDK/API clients) and the new `artificialflowanalytics` (placeholder, no API
  access). The old `artificialflow admin`/`modeler`/`client` names are no
  longer recognized; rename them in your IAM provider before upgrading.
- Removed `AUTHORIZATION_MODE` and the `artificialflow:<resource>:<action>`
  scopes. Authorization is role-only.
- Removed the `/inbox*` API, the `X-ArtificialFlow-Acting-*` headers and the
  RFC 8693 `act` claim. ArtificialFlow no longer authenticates end users of
  client applications. Added `GET /tasks` (filter by `assignee`,
  `candidateUser`, `candidateGroup`, `includeUnassigned`, `processInstanceKey`,
  `state`, `limit`). Instance task claim/complete are open to client tokens and
  take `{ "assignee": "..." }`; completing requires the claiming assignee
  unless the caller is an admin. `UserTask` adds `processInstanceKey` and
  `processDefinitionKey`.
- Instance reads, variables and history are no longer filtered per principal;
  they require the admin or client role.
- The console Task Inbox page is replaced by an Analytics placeholder. Business
  users see only Processes, Decisions and the modeler. With auth disabled the
  console runs as admin.
- SDKs: removed the inbox, acting-user and `adminClaimTask`/`adminCompleteTask`
  methods. Added `searchTasks`, `claimUserTask(instanceId, executionId,
  assignee)` and `completeUserTask(instanceId, executionId, assignee)` in the
  Node, .NET (`...Async`) and Java SDKs.
- `IAM_DEPLOYMENT_MODE` and Helm `iam.mode` are now booleans and fully decide
  auth in the command and query services and the console: `true` (external
  OIDC provider; Helm default; fails startup without an issuer) or `false`
  (no IAM provider, any configured issuer ignored, local console sign-in,
  tokenless API; Compose default). Any other value, including `external` or
  `disabled`, fails startup with an error naming `true` and `false`.
  `GET /identity/config` returns `deployment_mode` as a boolean, and the SDK
  models follow. `CONSOLE_ADMIN_USERNAME`/`CONSOLE_ADMIN_PASSWORD` are passed
  only when the mode is `false` (Compose: `compose/iam-false.env`; Helm:
  `iam.consoleAdmin`, password in the env Secret) and are ignored otherwise.
  With `iam.mode: false` the chart no longer renders issuer or frontend OIDC
  settings and sets `AUTH_PRODUCTION=false`.
- Removed the remaining bootstrap-era auth settings: `AUTH_CLIENT_ID_FILE`,
  `AUTH_INTROSPECTION_CLIENT_ID_FILE`, `AUTH_INTROSPECTION_CLIENT_SECRET_FILE`,
  their `*_FILE_TIMEOUT_SECONDS` waits, `FRONTEND_AUTH_OIDC_CLIENT_ID_FILE`,
  the `ARTIFICIALFLOW_OIDC_*` and `VITE_OIDC_*` frontend fallbacks, and the
  provider-specific project-role claim parsing. Pass credentials as plain
  environment variables (or from a Kubernetes Secret) and set
  `FRONTEND_AUTH_OIDC_AUTHORITY`/`FRONTEND_AUTH_OIDC_CLIENT_ID` at runtime.
  `GET /identity/config` no longer returns `configuration_source`.
- The Helm chart no longer renders a pre-install/pre-upgrade schema migration
  Job, and the `migration.*` values were removed (rendering fails if they are
  set). Command and runtime apply migrations at startup under the Postgres
  advisory lock, so the application database user needs DDL rights. App
  Deployments roll with `maxUnavailable: 0` and get a `startupProbe`
  (`startupProbe.periodSeconds`/`failureThreshold`, 10 minutes by default) that
  bounds the migration window. Command and runtime retry the Postgres
  connection for up to 2 minutes at startup.

### Security

- Bundled dependencies now use current upstream images: Postgres 15.19,
  Elasticsearch 8.19.23, Apache Kafka 4.4.0, Debezium Connect 3.0.0.Final,
  and nginx 1.31.7. The broker is the Apache Kafka image, with the same
  listener ports the applications already use. The release scan records
  findings in those community images and blocks only when a pin is older
  than `release/upstream-images.tsv`. Debezium scripting is off because no
  connector uses it.
- Patched govulncheck advisories in the Go standard library (Go 1.26.10,
  including CVE-2026-111943 in `crypto/x509`), `github.com/klauspost/compress`
  (1.18.7, CVE-2026-63209), `golang.org/x/net`, `golang.org/x/text`,
  OpenTelemetry (1.45.0), and gRPC (1.83.2).
- `make test-security` now scans the images Compose actually builds (it was
  scanning stale `artificialflow-*` images), builds govulncheck/gosec with the
  repo toolchain, and gates gosec on HIGH/MEDIUM findings only.
- Node SDK requires `@grpc/grpc-js` >= 1.14.5. The frontend's
  `tailwindcss-animate` moved to `devDependencies`, so the production audit no
  longer pulls in the Tailwind 3 build toolchain.

### Added

- No-IAM mode console sign-in: with `IAM_DEPLOYMENT_MODE=false` the console
  shows a local login (`admin` / `admin` by default, set with
  `CONSOLE_ADMIN_USERNAME` / `CONSOLE_ADMIN_PASSWORD`) backed by
  `POST /identity/login`. The API and SDKs still need no token in that mode.
- .NET (`ArtificialFlow.Sdk`) and Java (`io.artificialflow:artificialflow-sdk`) REST SDKs
  with OAuth client-credentials auth and polling job workers.
- Helm `values-kind-smoke.yaml` and real `*.bundled` keys for managed
  dependencies.

### Changed

- Release images and smoke pin Postgres, Kafka, Debezium Connect, and
  Elasticsearch as the dependency images.
- The release chaos scenario `event-bus` is now `kafka`, and its resource
  override `EVENT_BUS_RESOURCE` is now `KAFKA_RESOURCE`.
- Slimmed deployment config: `docker-compose.yml` 343 → ~210 lines and Helm
  templates/values ~3,100 → ~1,300 via a shared workload renderer. Rendered
  topology is unchanged.
- Compose no longer runs a `schema-migrate` service. Command and runtime apply
  the embedded migrations when they open Postgres, under the existing advisory
  lock.
- `docker-compose.yml` is self-contained and no longer needs a `.env`: every
  setting has an inline `${NAME:-default}`. Compose now defaults to
  `IAM_DEPLOYMENT_MODE=false`; set `true` plus `AUTH_ISSUER_*` to enable
  OIDC. `.env.example` was removed and `make` calls plain `docker compose`.
- Kafka topics are created with 3 partitions by default (`kafka.topicPartitions`
  in Helm, `KAFKA_TOPIC_PARTITIONS` for sync-worker). Existing topics keep
  their partition count.
- Sync-worker projects events with one Elasticsearch `_bulk` request per fetched
  batch and commits offsets every second instead of per message. Delivery stays
  at-least-once; failed items fall back to per-message retry and DLQ.

### Fixed

- Runtime latency no longer grows with completed history. Conditional, signal,
  and escalation delivery listed active elements with a full scan of
  `element_instance` on every scheduler tick; migration
  `0006_active_element_scan` adds a partial index on active elements. In the
  4-hour soak the scan reached ~150 ms at 1.75M rows and pushed API p95 from
  15 ms to over 300 ms.
- Concurrent schema migrators no longer deadlock on a fresh database. A
  migrator waiting on the advisory lock held a statement open, which
  `CREATE INDEX CONCURRENTLY` in `0004_outbox_scalability` waited on; waiters
  now poll `pg_try_advisory_lock`.
- Outbox relay publishes each claimed batch in one Kafka write and waits for
  broker acknowledgement (`RequireAll`). It previously wrote one message at a
  time without acks, so large batches hit the runtime task timeout, were
  retried, and published duplicates.
- Event keys now pick the Kafka partition (hash balancer), so events for one
  process instance stay ordered when topics have more than one partition.
- Sync-worker event dedupe keeps a bounded window of recent event IDs instead
  of growing for the life of the process.

- Gateway (Compose and Helm): with `IAM_DEPLOYMENT_MODE=true` the
  Content-Security-Policy now allows `connect-src`/`frame-src` to the OIDC
  provider's origin, so console sign-in no longer fails on the discovery
  request. With `false` the policy stays `'self'` only.
- Helm: the Debezium connector now receives the Postgres host, port, database,
  and password, so CDC events (and Elasticsearch projections) flow on
  Kubernetes.
- Frontend image builds from the repository root in Compose and the release
  dry run; HSTS `max-age` renders as an integer; network policies allow only
  the Kafka, Connect, Postgres, and Elasticsearch ports; kind smoke overrides
  use the real keys.
- Transactional engine copies no longer copy the outbox-count mutex, which
  could deadlock a transaction started while the relay held the lock.
## [1.2.0] - 2026-08-09

### Added

- Added PostgreSQL-time fenced multi-active runtime claims, a three-replica
  production reference topology, and failover evidence.
- Added enforced tenant propagation and isolation across APIs, persistence,
  outbox delivery, projections, workers, and search.
- Added bounded dry-run and transactional process-instance migration with
  stable rejection reasons, audit history, idempotent operations, and an
  explicit support matrix.
- Added versioned schema migrations, dependency-aware readiness, observability,
  recovery automation, release evidence, and supply-chain gates.

### Changed

- Standardized additive `/v1` API aliases, SDK coverage, connector runtime
  behavior, authorization, Kubernetes hardening, and operations UX.
- Raised automated coverage with race, fuzz, mutation, contract, chaos, and
  long-duration release checks.

### Fixed

- Prevented stale runtime owners and foreign tenants from committing timer,
  job, outbox, or migration state.
- Fixed deterministic projection rebuild and canonical variable serialization.

## [1.1.0] - 2026-08-05

### Added

- Expanded executable BPMN coverage for timer schedules, escalation, conditional,
  message/signal start and event sub-process behavior, compensation, transaction
  cancellation, connector input/output mappings, and manual-task waiting.
- Added capability-driven BPMN validation, exhaustive matrix scenarios, and
  completeness regression tests.
- Added richer modeler support for BPMN events, gateways, task properties,
  visual artifacts, diagram history, and connector descriptors.
- Added the language-agnostic HTTP API guide covering command, query, worker,
  inbox, identity, and direct non-SDK integration.
- Added instance variable editing for nested objects and lists, including add,
  edit, and delete controls.
- Added the process-definition version to the instance details header.

### Changed

- Hardened HTTP/webhook connector validation, redirect handling, descriptors,
  and runtime input resolution.
- Updated the Node.js SDK and public API types for the additive `1.1` surfaces.
- Hid the Incidents console page while preserving the admin incident HTTP API.
- Updated quickstart and Compose documentation for direct API and SDK
  consumers.

### Fixed

- Fixed empty object/list values being impossible to edit from instance details.
- Fixed intermittent browser-session loss after transient OIDC renewal or
  session-monitor errors.
- Fixed frontend route/access handling for hidden Decisions and Incidents pages.
- Fixed BPMN parser/runtime edge cases covered by the `1.1` capability matrix.

### Security

- Revalidate connector redirect destinations against the configured allowlist.
- Updated the frontend nginx runtime base to remove fix-available high-severity
  Alpine package vulnerabilities from the release image.
- Preserve release secret scanning, dependency audits, SAST, DAST, image scans,
  SBOM/provenance, and signing as publication-blocking gates.

## [1.0.0] - 2026-07-30

1.0.0 GA — freeze tip after partner/self-run bake-off and RC Phase T.

## [1.0.0-rc.1] - 2026-07-29

1.0 freeze candidate (same product tip as 0.4.0 plus RC version pins).

## [0.4.0] - 2026-07-28

### Added

- Runtime Postgres scheduler lease, HA reference, capacity snapshot, CQRS lag k6 scenario, golden-demo bake-off.
- Browser Task Inbox, instance job ops/retry, CQRS status banner, Go/Python worker samples, external IAM recipes (Keycloak/Entra/Auth0).
- DMN-lite JSON decision tables (API/runtime; Decisions UI behind feature flag), official connectors (HTTP/Kafka/email/webhook/S3), process start versioning.
- Modeler: editable element IDs, labeled palette (no horizontal scroll), inline deploy/BPMN validation errors, Tier-3 visual artifacts.
- Send Task support via external jobs (default `io.artificialflow.connector.send`).
- Terminate end event and link catch/throw; clear parse errors for escalation/conditional/cancel/event sub-process.
- Release test suite (`make test-release-suite`), DAST (`make test-dast`), mega BPMN UAT fixture/spec.
- Helm managed-deps example values; Node SDK worker processing example; gateway BPMN examples.
- Partner bake-off scorecard for design-partner self-hosted evaluation.

### Changed

- BPMN support matrix tiered (Tier-1/2/3) for honest Supported / Not supported claims.
- Compatibility matrix and release pins target `v0.4.x` toward `1.0.0`.

## [0.3.0] - 2026-07-22

### Changed

- Moved the coordinated ArtificialFlow transition release to an unpublished version across npm, container, Compose, and Helm artifacts.
- Hardened release collision checks and persistent-state migration guidance.

## [0.2.0] - 2026-07-22

### Added

- Production SDK authentication for external OIDC providers, including introspection, role validation, credential rotation, and HTTP/gRPC propagation.
- Workflow history and a reusable Playwright UAT harness.
- Exhaustive BPMN, deployment-matrix, CQRS, Compose, Helm, and release validation gates.

### Changed

- Hardened transactional workflow event handling, variable persistence, job idempotency, CQRS projection behavior, and operational feedback.
- Upgraded Go, gRPC, React Router, and Node.js SDK dependencies.
- Required the full CQRS end-to-end smoke test for pull requests.

### Security

- Repository ignore rules hardened for local secrets, generated tokens, binaries, reports, and dependency folders.
- Addressed high-severity Go and npm dependency findings and expanded secret, static-analysis, vulnerability, image, and CodeQL gates.

## [0.1.1] - 2026-05-17

### Changed

- Renamed project branding, package scope, Docker namespace, Helm chart, and repository metadata to FlowGo.

### Added

- First FlowGo-branded release target for source, Docker images, Helm chart, and Node.js SDK.

## [0.1.0] - 2026-05-12

### Added

- Initial public release target for source, Docker images, Helm chart, and Node.js SDK.
