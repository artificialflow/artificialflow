# Stability Policy

ArtificialFlow `1.x` maintains the compatibility surfaces listed below.

## Versioning

- Public releases use Semantic Versioning.
- Before `1.0.0`, minor versions may still include breaking changes when documented in release notes.
- From `1.0.0` onward, breaking changes on compatibility surfaces require a deprecation cycle and a major version bump.

## Compatibility Surfaces

Compatibility-sensitive surfaces include:

- Worker REST API (`/jobs/*` protocol headers, capabilities, activate/complete/fail/extend-lock).
- User tasks REST API (`GET /tasks`, `/instances/{id}/tasks*`).
- Command and Query HTTP API `v1` response field meaning.
- Protobuf package `artificialflow.api.v1`.
- Node.js SDK public API (`@artificialflow/nodejs-sdk`), including user-task helpers.
- Go worker library public API (`backend/libs/worker` / published module path).
- Docker image environment variables and exposed ports.
- Helm chart values under `charts/artificialflow`.
- IAM role names (`artificialflowadmin`, `artificialflowbusiness`, `artificialflowclient`, `artificialflowanalytics`) and claim mapping behavior.
- BPMN parser/runtime semantics documented in `docs/BPMN_SUPPORT_MATRIX.md`.

## Freeze tip (v1.0.0-rc.1)

RC freeze tip is tagged `v1.0.0-rc.1` on `main` after the 1.0.0-rc.1 version-pin commit (2026-07-29). Compatibility surfaces above are frozen for the RC → GA window; only security and RC-blocking defects land without a new RC tag.

## 1.x Contract Policy

For `1.0.0` and later `1.x` releases:

- Additive changes are preferred on all compatibility surfaces.
- Removals or meaning changes require: replacement behavior, migration notes, and at least one minor release of deprecation overlap.
- Security fixes may break compatibility when no safe alternative exists; such breaks must be called out in release notes.

## Deprecation

Where possible, deprecations should include:

- Replacement behavior.
- Migration notes.
- At least one release cycle of overlap.
