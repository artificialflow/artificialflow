# Engineering debt register

This register is the only accepted destination for intentional `TODO`, `FIXME`,
or `HACK` markers in production source. Source comments must reference an entry
as `DEBT(DEBT-n)`. The maintainability gate rejects unregistered markers.

## DEBT-1 — Workflow token navigation decomposition

- Owner: workflow-command maintainers
- Target release: 1.2
- Scope: `application/navigation.go:proceedToken`
- Acceptance: split gateway, event, task, and subprocess dispatch into tested
  handlers; no function exceeds 150 lines; BPMN matrix and race gates pass.

## DEBT-2 — BPMN parser phase separation

- Owner: BPMN maintainers
- Target release: 1.2
- Scope: Go and frontend BPMN parsers
- Acceptance: separate XML decoding, reference validation, extension mapping,
  and graph construction; preserve corpus compatibility; parser fuzz and
  round-trip tests pass.

## DEBT-3 — Command handler endpoint modules

- Owner: command API maintainers
- Target release: 1.2
- Scope: `internal/interfaces/http/handler.go`
- Acceptance: move workflow, instance, job, incident, and event endpoint
  families into cohesive files; each production file is at most 1,000 lines;
  route parity tests remain green.

## DEBT-4 — Engine lifecycle and outbox separation

- Owner: workflow-command maintainers
- Target release: 1.2
- Scope: `application/engine.go` and `application/events.go`
- Acceptance: isolate instance lifecycle, variable persistence, outbox relay,
  and timer/message processing behind package-private collaborators; preserve
  transaction and outbox ordering tests.

## DEBT-5 — Modeler state extraction

- Owner: frontend maintainers
- Target release: 1.2
- Scope: `frontend/src/pages/Modeler.tsx`
- Acceptance: extract load/deploy orchestration and graph editing commands into
  hooks with failure-path tests; component function is at most 150 lines and no
  React Hooks lint rules are disabled.
