# Contributing to ArtificialFlow

Thank you for your interest in contributing! We want to make this project a robust open-source solution for workflow orchestration.

## Getting Started

1.  **Fork the repository** on GitHub.
2.  **Clone your fork** locally.
3.  **Start the environment**:
    ```bash
    make up
    ```
    This starts the single Compose stack from `docker-compose.yml`.
    No `.env` is needed; auth is disabled by default. Set
    `IAM_DEPLOYMENT_MODE=true` plus the `AUTH_ISSUER_*` URLs to test with OIDC.

    Debezium connector bootstrap is handled automatically by `sync-worker` startup. Manual recovery remains available via `make init-connector`.

## Development Workflow

### Backend (Go)
The backend services are located in `backend/`.
- **Requirements**: Go 1.26+
- **Command Service**: `backend/services/workflow-command`
- **Query Service**: `backend/services/workflow-query`
- **Sync Worker**: `backend/services/sync-worker`

To rebuild the backend after changes:
```bash
make up
```

Validate compose config quickly:
```bash
make smoke
```

### Frontend (React)
Located in `frontend/`.
To rebuild the frontend after changes:
```bash
make up
# or run the dev server on port 5173:
npm --prefix frontend run dev
```

### Clients
- **Node.js SDK**: `clients/nodejs-sdk` (`npm --prefix clients/nodejs-sdk test`)
- **.NET SDK**: `clients/dotnet-sdk` (`make sdk-dotnet`)
- **Java SDK**: `clients/java-sdk` (`make sdk-java`)
- **Python worker**: `clients/python-sdk`
- **Go worker**: `backend/libs/worker`

When you change an API route, update `docs/openapi.yaml` and the SDKs, then run
`node scripts/validate_api_sdk_contract.mjs`.

## Running Tests

We have a suite of shell scripts to verify functionality:
```bash
./demo.sh               # Runs a full deployment & execution cycle
```

CI/Security workflows:

- Main CI pipeline: `.github/workflows/ci.yml`
- Security/dependency scan pipeline: `.github/workflows/security.yml`

Contributor-facing quality gate docs:

- Agentic SDLC operating model: `docs/AGENTIC_SDLC.md`
- Agentic QA matrix: `docs/AGENTIC_QA.md`
- Quality gate policy: `docs/QUALITY_GATES.md`
- Labels and triage taxonomy: `docs/LABELS.md`

For most PRs, start with the fastest relevant checks and include the commands/results in the PR description:

```bash
make test-unit
make quality-fast
make test-frontend
make smoke
make validate-helm
```

`quality-fast` is fail-closed and runs the maintainability ratchet, calibrated
Go coverage floors, race detection for concurrency-sensitive packages, and
three bounded fuzz targets. Current statement floors are stored in
`scripts/quality/coverage_thresholds.json`: generated protobuf is excluded from
the 37.5% aggregate floor, while critical packages have independent floors from
20% (workflow application baseline) through 78% (BPMN parser). Floors are
ratchets, not targets; coverage improvements should raise them.

Maintainability defaults are 1,000 production-file lines, 3,000 test-file
lines, and 150 function lines. Existing exceptions are capped at their measured
baseline in `scripts/quality/maintainability.json`, so they may shrink but not
grow. Exact duplicate blocks exclude generated code and low-level UI
primitives. Production `TODO`/`FIXME`/`HACK` comments must reference
`docs/ENGINEERING_DEBT.md` with an owner, target release, and acceptance test.

Run deeper checks when the changed surface requires them:

- Backend/runtime behavior: `make test-bpmn-matrix`
- Worker API behavior: `make worker-conformance`
- CQRS/query/sync behavior: `make cqrs-e2e-smoke` and `make cqrs-parity-check`
- Integration/E2E behavior: `make test-integration` and `make test-e2e`
- Performance-sensitive changes: `make test-perf`
- Security-sensitive changes: `make test-security`
- Release-sensitive changes: `make smoke-release` and `make release-dry-run`
- Extended parser/input fuzzing: `FUZZ_TIME=45s make quality-fuzz`
- Critical mutation target: `make quality-mutation` (Gremlins v0.6.0,
  80% efficacy and mutant-coverage floors)

The scheduled `quality-deep.yml` workflow runs extended fuzzing and required
mutation testing. A local optional mutation run reports an honest skip when
Gremlins is absent; `make quality-mutation` and scheduled CI install/run the
pinned tool and fail closed. If a heavy check is not run, explain why and
describe the residual risk.

Mutation evidence is valid only when the report contains evaluated mutants and
meets both thresholds. Tool crashes, timeouts, empty reports, and missing tools
are not passes. Gremlins may be less stable on local Darwin toolchains; the
Linux scheduled result is the release-facing result, and remains unmeasured
until that workflow actually completes.

## Pull Request Process

1.  Create a feature branch (`git checkout -b feature/amazing-feature`).
2.  Commit your changes.
3.  Push to the branch.
4.  Open a Pull Request.

## Coding Standards

- **Go**: Follow standard Go conventions (`gofmt`, `go vet`).
- **Commits**: Use clear, descriptive commit messages.

## Architecture and Contract Guidelines

To keep the OSS engine maintainable as it grows, follow these boundaries:

1. **Model layers must stay separated**
   - Domain/persistence models stay in engine/repository boundaries.
   - Public API payloads go through DTO/mapper layers.
   - Avoid returning persistence structs directly from handlers.
   - Keep mapping rules centralized in mapper/adapter modules (do not scatter ad-hoc conversions in handlers).

   Allowed dependency direction:
   - `domain/repository -> application -> interfaces/http(dtos+mappers)`
   - `interfaces/http` may depend on `application` abstractions, but should not persist raw storage models directly.

2. **Worker API compatibility is explicit**
   - Preserve backward compatibility for worker REST contract (`/jobs/*`).
   - If you add or change worker behavior, update capabilities and protocol docs.

3. **Event evolution must remain additive when possible**
   - Prefer adding fields over reinterpreting/removing existing fields.
   - Document behavior changes in ADRs/release notes.

4. **Contract tests are required for boundary changes**
   - For worker contract changes, run:
     - `go test ./backend/services/workflow-command/internal/interfaces/http -run 'Idempotency|Protocol|Capabilities' -count=1`
     - `go test ./backend/libs/worker -count=1`
   - For query mapping/state normalization changes, run:
     - `go test ./backend/services/workflow-query/internal/interfaces/http -run 'NormalizeInstanceStateFilter|MapInstanceStatus|GetInstance_ReturnsProjectedResponse|SearchInstances_RunningStateNormalizedToActive' -count=1`
