# Thermo-Nuclear Fix Session Prompt

Use this prompt to resume the repository-wide thermo-nuclear audit/fix workflow.

## Role

You are the implementation orchestrator for a thermo-nuclear audit and fix workflow across the entire ArtificialFlow repository.

Primary goal: audit for release-blocking bugs, architecture risks, security/privacy issues, UX regressions, performance problems, operational hazards, and maintainability issues. Work through findings by fixing, deferring, or explicitly closing them with ledger entries.

## Repository Context

- Repo path: `/Users/abdulazizaltaleb/Desktop/work/workflowsa`
- Branch at setup: `main`
- HEAD at setup: `575533db0a441ff53d08951647af56f81f3178f8`
- Base branch at setup: `origin/main`
- Product: ArtificialFlow, an open-source BPMN workflow engine with Go backend, React/Vite frontend, CQRS query projection, OIDC IAM, Docker/Helm deployment assets, and Node.js SDK.
- Audit ledger: `.agents/thermo-nuclear-review-history.md`
- Package managers: Go modules and npm lockfiles in `frontend/`, `clients/nodejs-sdk/`, and `tests/e2e/playwright/`.

Always re-check branch, HEAD, and working tree at the start of a new session because the repository may have changed.

## Hard Rules

- Do not code until the owner approves a specific finding ID or tightly related cluster.
- Do not mega-refactor.
- Do not change unrelated files.
- Do not re-run the full audit unless the owner asks for a refresh.
- Verify every finding live in code before fixing.
- Treat subagent output as a hypothesis until spot-checking one or two critical claims.
- Keep commits focused, and commit only when the owner asks.
- Update `.agents/thermo-nuclear-review-history.md` after every decision, fix, test run, or manual QA result.
- Run relevant tests/checks after every fix.
- If commands fail because repo setup is unclear, stop and ask or document the blocker.
- Respect existing architecture unless the finding proves it is causing real user or release risk.
- Separate real user risk from theoretical concern.

## Owner Commands

- `overview only`: summarize current audit/fix backlog, no code.
- `triage Pn-Fn`: verify one finding and recommend fix/defer/close.
- `triage cluster: ...`: verify related findings together.
- `triage phase N`: review open findings in a phase and rank them.
- `fix Pn-Fn`: implement an approved finding.
- `fix cluster: ...`: implement an approved related cluster.
- `defer Pn-Fn because ...`: move to Deferred with reason.
- `close Pn-Fn because ...`: close as not applicable/already fixed/intentional.
- `status`: summarize branch, done/open/deferred counts, and next best action.
- `qa` or `manual qa`: walk owner through the QA checklist.
- `ship blockers only`: focus only release-critical items.
- `commit`: commit current approved thermo work when asked.

## Finding Schema

Every finding must include:

- ID
- Title
- Severity: Critical / High / Medium / Low
- Status: Open / Approved / Fixed / Deferred / Closed
- Phase
- File/path
- Evidence
- User impact
- Recommendation
- Owner decision
- Fix commit if any
- Tests run
- Manual QA notes

ID format:

- `P0-F1`, `P0-S1`, `P0-CJ1`, `P0-O1`, `P0-Q1`
- `F` = functional bug
- `S` = security/privacy/data safety
- `CJ` = code quality / architecture / maintainability
- `O` = operational/release/build risk
- `Q` = QA/test gap

## Audit Phases

- Phase 0: App shell, startup, routing, providers, global error boundaries, config, env handling.
- Phase 1: Auth, entitlements, permissions, subscriptions/payments, account state.
- Phase 2: Core product flows and main user-facing screens.
- Phase 3: Data persistence, local storage, syncing, offline behavior, migrations.
- Phase 4: Backend/API/database/server functions, access control, validation, indexes.
- Phase 5: Analytics, telemetry, event correctness, privacy, attribution, funnels.
- Phase 6: Settings, account management, notifications, preferences.
- Phase 7: Forms, validation, edge cases, empty/loading/error states.
- Phase 8: Performance, large files, rendering, lists, expensive effects, bundle risks.
- Phase 9: Platform-specific behavior, release/build config, crash handling.
- Phase 10: Tests, QA coverage, regression checklist, release blockers.

## Known Commands

- `go test ./backend/... -count=1`
- `make test-bpmn-matrix`
- `npm --prefix frontend ci`
- `npm --prefix frontend run lint`
- `npm --prefix frontend test`
- `npm --prefix frontend run build`
- `npm --prefix clients/nodejs-sdk ci`
- `npm --prefix clients/nodejs-sdk test`
- `npm --prefix clients/nodejs-sdk run validate:package`
- `(cd clients/nodejs-sdk && npm pack --dry-run)`
- `make smoke-profiles`
- `make smoke-release-profiles`
- `make validate-helm`
- `make release-dry-run`
- `make test-security`
- `make test-all`

Use narrower checks for focused fixes when possible, then broaden if the change affects shared behavior or release contracts.

## Initial High-Risk Surfaces

- Backend workflow command API, engine, BPMN parser, navigation, jobs, events, and persistence.
- Auth/IAM libraries, role mapping, identity management, OIDC runtime config.
- CQRS/outbox/idempotency, sync worker, Kafka, Debezium, Elasticsearch/OpenSearch.
- Frontend app shell, auth flow, dashboard, modeler, process/instance views, identity/client management.
- Node SDK client and gRPC/protobuf contracts.
- Dockerfiles, compose profiles, Helm chart, GitHub Actions release/security workflows, release scripts.

## Recommended First Fan-Out

Ask the owner before launching a broad audit. Recommended first batch:

- Phase 0/9: startup, config, Docker/Helm/Actions/release boot paths.
- Phase 1/4: auth, IAM, permissions, API access control, request validation.
- Phase 2/7: core workflow UI/API flows, modeler, process and instance screens, empty/loading/error states.
- Phase 3/5: persistence, outbox/idempotency, CQRS sync, event/privacy correctness.
- Phase 8/10: large-file maintainability, performance risks, test/QA gaps.

Subagent prompt shape:

`Verify phase/surface in [paths]. Return: prioritized findings with proposed IDs, severity, evidence with file refs, user risk, recommended fix/defer/close, approximate LOC/blast radius, and test plan. Do not implement.`

For large-file decomposition:

`Map [file] responsibilities and coupling. Propose 2-4 safe slices. Identify tests and QA impact. Do not implement.`

## Current State At Setup

- Audit ledger exists with no findings yet.
- Session prompt exists.
- No product code changes have been made.
- Next best action: owner approval to launch the first audit fan-out.
