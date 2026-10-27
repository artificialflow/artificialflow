# Quality Gates

This document defines ArtificialFlow quality gates for pull requests, deep validation, releases, security review, and compatibility-sensitive changes. Agents use this policy to recommend checks and summarize evidence; maintainers decide whether advisory findings become merge-blocking.

## Gate Levels

| Level | Purpose | Blocking policy |
| :--- | :--- | :--- |
| Required PR gates | Fast checks expected for normal PRs. | Merge-blocking when configured as required status checks. |
| Optional/deep gates | Infrastructure, E2E, performance, or broader regression checks. | Advisory unless the changed surface requires them. |
| Release gates | Checks required before publishing a release candidate or final release. | Release-blocking. |
| Security gates | Secret, dependency, SAST, container, and CodeQL checks. | Blocking for high-confidence high/critical findings; advisory or maintainer-reviewed for lower-confidence findings. |
| Compatibility gates | Contract and migration checks for stable or user-facing surfaces. | Blocking when behavior changes without tests, docs, or maintainer approval. |

## Required PR Gates

Recommended required checks for default branch protection:

| Gate | Command or workflow | Expected artifacts |
| :--- | :--- | :--- |
| Go unit tests | `make test-unit` or CI Go Tests. | `reports/unit-raw.json`, `reports/coverage.out`, `reports/coverage.txt` when run locally. |
| BPMN matrix | `make test-bpmn-matrix`. | CI job output or local command output. |
| Frontend lint/test/build | `make test-frontend` for tests; CI also runs `npm --prefix frontend run lint` and `npm --prefix frontend run build`. | `reports/frontend-vitest.json`, `reports/frontend-vitest.txt`, build output. |
| Node.js SDK validation | `npm --prefix clients/nodejs-sdk test`, `npm --prefix clients/nodejs-sdk run validate:package`, and dry-run packs for both canonical and legacy packages. | SDK test output, exact-version wrapper validation, dry-run package listings. |
| Compose smoke | `make smoke` and `bash scripts/validate_compose_identities.sh`. | Canonical/legacy project, volume, image, and config validation output. |
| Helm validation | `make validate-helm`. | Helm lint/templates plus fresh/legacy resource identity and immutable-selector assertions when Helm is installed; YAML parse validation otherwise. |
| Docker build and image scan | CI Docker Build Dry Run and Trivy image scan. | Docker build logs and Trivy results. |
| Security baseline | `.github/workflows/security.yml`. | Gitleaks, govulncheck, npm audit, gosec, Trivy filesystem, CodeQL where available. |

For lightweight local review, a maintainer or agent can start with:

```bash
make test-unit
make test-frontend
make smoke
make validate-helm
```

## Optional And Deep Gates

Run these when the changed surface warrants deeper confidence:

| Gate | Command | Use when |
| :--- | :--- | :--- |
| Integration tests | `make test-integration` | API behavior depends on a running stack or cross-service behavior. |
| Backend E2E | `make test-e2e` | CQRS, worker, command-to-query, or runtime lifecycle behavior changes. |
| Full CQRS smoke | `make cqrs-e2e-smoke` | Event projection, Debezium/Kafka, sync worker, query visibility, or Elasticsearch changes. |
| CQRS parity | `make cqrs-parity-check` | Read model counts, index mappings, or projection replay behavior changes. |
| Worker conformance | `make worker-conformance` | Worker API, SDK worker behavior, idempotency, headers, or job lifecycle changes. |
| Performance | `make test-perf` | Throughput, query performance, worker activation, polling, large variables, or hot paths change. |
| Full orchestrator | `make test-all` | Nightly, pre-release, or broad cross-system validation. |
| Exhaustive tester | `make test-all-functionality` | Maintainer-requested live validation across deployment, BPMN, API, SDK, UI, CQRS, worker, performance, and security surfaces. |
| BPMN exhaustive catalog | `make test-bpmn-exhaustive` | BPMN parser/runtime semantics change or support status needs explicit scenario evidence. |
| Deployment matrix | `make test-deployment-matrix` | Compose, IAM mode, CQRS profile, Helm, Docker, or release overlay behavior changes. |
| Release profile smoke | `make smoke-release` | Docker Compose release overlays or image tag behavior changes. |

`make test-all` starts the full CQRS stack, runs unit/integration/E2E/frontend/performance/security layers, and writes `reports/summary.md`. Use `SKIP_PERF=true` or `SKIP_PLAYWRIGHT=true` only with an explicit reason.

`make test-all-functionality` runs a live-by-default exhaustive harness and writes `reports/all-functionality-report.json` plus `reports/all-functionality-report.md`. It continues by default and records `pass`, `fail`, `warn`, `skip`, or `flaky` evidence per layer. Use skip flags only when the environment cannot provide the dependency, and keep the skip reason visible in the report.

## Release Gates

Before a release candidate or release tag, run the unified suite:

```bash
make test-release-suite
# With a healthy stack:
make test-all
make test-all-functionality
make test-perf          # AUTH_TOKEN + WORKFLOW_ID
make test-dast          # OWASP ZAP baseline vs TARGET_URL
# Optional mega BPMN UAT:
cd tests/e2e/playwright && npx playwright test specs/uat-mega-bpmn.spec.ts
```

Also:

```bash
make smoke
make smoke-release
make validate-helm
make validate-migration-release
make release-dry-run
```

Also verify:

- CI and Security workflows pass on the release commit.
- Legacy-brand allowlisting, release-version consistency, generated protobuf drift, and release-workflow assertions pass.
- Node SDK tests, exact canonical/wrapper version validation, pack/install checks, both `npm pack --dry-run` checks, and SBOM generation pass.
- Docker image release workflow produces one build per image, matching canonical/legacy manifest digests, scan results, SBOM/provenance, and signatures when publishing is enabled.
- Release notes include changelog summary, known limitations, compatibility notes, upgrade notes, Docker image references, npm package version, and security notes.

Release gates are blocking for release publication. A maintainer may accept a known limitation only if it is documented in release notes and does not violate the security policy.

## Security Gates

Security gates include:

| Gate | Source | Blocking rule |
| :--- | :--- | :--- |
| Secret scan | Gitleaks in `.github/workflows/security.yml`. | Block confirmed secrets or credentials. |
| Go vulnerabilities | `govulncheck` and `make test-security`. | Block reachable high/critical vulnerabilities unless a documented maintainer exception exists. |
| npm vulnerabilities | npm audit for `frontend` and `clients/nodejs-sdk`. | Block production high/critical vulnerabilities. |
| Go SAST | gosec in security workflow and `make test-security`. | Block high-confidence high severity findings; review medium findings. |
| Container/filesystem scan | Trivy filesystem/image scans. | Block high/critical findings in ArtificialFlow images. Scan community images and block only when their pin is not the current stable tag in `release/upstream-images.tsv`. |
| CodeQL | Security workflow for public repositories. | Block actionable high/critical alerts after maintainer review. |
| DAST | `make test-dast` (OWASP ZAP baseline against live gateway). | Block unallowlisted High findings; allowlist in `tests/security/dast-allowlist.txt`. |
| Dependency updates | Dependabot and dependency policy. | Require review for runtime, auth, parser, Docker, GitHub Actions, and release tooling changes. |

SDK credential changes additionally require a live OAuth client-credentials exchange against an external OIDC provider, role/audience validation, client-secret rotation, token refresh before expiry, tokenless operation with `IAM_DEPLOYMENT_MODE=false`, and artifact redaction checks across the Node.js, .NET, and Java SDKs.

Security agents must keep vulnerability details private when disclosure could harm users.

## Compatibility-Sensitive Surfaces

Changes to these surfaces require explicit compatibility review:

- Worker REST API and protocol headers.
- Node.js SDK public API and package metadata.
- Command and query HTTP response shapes.
- Docker image environment variables, exposed ports, and image names.
- Helm chart values and rendered Kubernetes resources.
- IAM role names, claim mapping, token validation, and authorization behavior.
- BPMN parser/runtime semantics.
- CQRS event payloads, outbox format, projection mapping, and read model states.

Expected evidence:

- Contract tests or conformance checks for the changed surface.
- Documentation updates in the relevant `docs/` file.
- Migration or deprecation notes for breaking changes.
- Maintainer approval for accepted compatibility risk.

## Expected Artifacts

Agents and maintainers should preserve or upload:

- `reports/summary.md`
- `reports/unit.md`, `reports/unit-raw.json`, `reports/coverage.txt`
- `reports/integration.md`, `reports/integration-raw.json`
- `reports/e2e.md`, `reports/e2e-*.txt`
- `reports/frontend.md`, `reports/frontend-vitest.json`, Playwright traces/screenshots/results
- `reports/performance.md`, `reports/k6.json`, `reports/perf-raw.txt`
- `reports/security.md`, scanner raw outputs, SARIF where available
- `reports/all-functionality-report.json`, `reports/all-functionality-report.md`, and `reports/all-functionality/` logs for exhaustive tester runs
- `reports/bpmn-matrix-report.json` and `reports/bpmn-matrix-report.md` for BPMN scenario catalog evidence
- Release dry-run logs, SDK package dry-run output, SBOM/provenance files

Workflow summaries should include links to artifacts and a short pass/fail/skip table. If a gate did not run, record why.

## Merge-Blocking Vs Advisory Findings

Merge-blocking by default:

- Failing required CI or Security checks.
- Confirmed correctness bug in changed behavior.
- Missing test evidence for compatibility-sensitive changes.
- Confirmed secret, auth bypass, data exposure, or high/critical reachable vulnerability.
- Release or deployment changes without smoke/Helm validation.
- Public API/SDK/worker changes without docs or compatibility notes.

Advisory by default:

- Low-risk docs wording suggestions.
- Non-critical maintainability improvements.
- Optional coverage improvements unrelated to the changed behavior.
- Performance concerns without evidence of regression.
- Scanner findings that are unreachable, low severity, or require maintainer risk review.

Agents should label each finding as blocking, advisory, question, or follow-up and cite the evidence used.
