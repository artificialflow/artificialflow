# Agentic SDLC

ArtificialFlow uses agents as maintainer assistants, not as autonomous owners. Agents can read repository context, propose labels, summarize risks, plan validation, and draft review comments. Humans approve public writes, generated commits, releases, security disclosures, and compatibility decisions.

This document defines the operating model for issue triage, pull requests, quality gates, community work, dependency/security review, and release readiness.

## Principles

- Keep humans in control of merge, release, disclosure, and roadmap decisions.
- Treat agent output as advisory evidence until a maintainer reviews it.
- Prefer small, auditable comments and check summaries over broad generated changes.
- Never expose secrets, private vulnerability details, tokens, logs with credentials, or contributor private data.
- Do not run untrusted pull request code with repository secrets.
- Preserve ArtificialFlow compatibility-sensitive surfaces: worker REST API, Node.js SDK public API, Docker image environment variables, Helm values, IAM roles/claims, and BPMN parser/runtime semantics.

## Agent Roles

| Agent | Main responsibility | Human approval required before |
| :--- | :--- | :--- |
| Issue triage agent | Classify new issues, request missing repro data, suggest labels and owner area. | Adding labels/comments automatically in public repos unless explicitly enabled. |
| Community maintainer agent | Find stale questions, duplicates, docs gaps, good-first-issue candidates, and discussion follow-ups. | Closing issues, changing roadmap language, or promising timelines. |
| PR review agent | Review correctness, compatibility, tests, security, docs, and operational risk. | Requesting changes as a maintainer or pushing code. |
| Quality gate agent | Compare changed files against required and deep validation gates. | Marking advisory findings as merge-blocking. |
| QA agent | Build changed-path QA plans and evidence expectations. | Claiming release readiness or waiving tests. |
| CI failure investigator | Summarize failed jobs, likely root causes, and focused local commands. | Masking failures, changing gates, or applying fixes. |
| Security triage agent | Triage dependency alerts and security workflow findings. | Public disclosure, mitigation acceptance, or dependency upgrade merges. |
| Release readiness agent | Check release candidate evidence, docs, SBOM/provenance, and known limitations. | Publishing images, npm packages, tags, or GitHub releases. |

Reusable prompts live under `.agents/`. The shared policy/config file is `.agents/agentic-sdlc.yml`.

## GitHub Issue Lifecycle

1. New issue is opened or edited.
2. Issue triage agent reads the issue body, templates, relevant docs, recent duplicates, and labels.
3. Agent classifies the issue as bug, feature, docs, support, security question, dependency, QA gap, or operations.
4. Agent proposes labels using `docs/LABELS.md`, identifies missing reproduction evidence, and suggests an owner area.
5. Maintainer applies labels, asks clarifying questions, moves sensitive security content to private reporting, or closes duplicates.

Expected triage evidence:

- ArtificialFlow version or commit.
- Deployment mode: Docker Compose or Helm (`IAM_DEPLOYMENT_MODE=true` external OIDC, or `false` no IAM), or local development.
- A minimal BPMN file, API payload, SDK snippet, screenshot, or command log when relevant.
- Expected behavior, actual behavior, and compatibility impact.
- Whether an agent or maintainer can reproduce locally.

Security issues that describe a vulnerability must move to `SECURITY.md` private reporting. Public issue comments should not include exploit steps or sensitive details.

## Pull Request Lifecycle

1. PR opens or updates.
2. QA agent maps changed files to risk surfaces and recommends validation commands.
3. Quality gate agent checks whether required evidence is present.
4. PR review agent reviews behavior, compatibility, security, tests, docs, and release risk.
5. Maintainers decide which findings are blocking, advisory, deferred, or accepted risk.
6. Merge happens only after required checks pass and human review approves the risk.

PR agents should inspect:

- The PR diff and changed paths.
- Existing tests and docs near the changed code.
- `.github/workflows/`, `Makefile`, and `scripts/test-all.sh` for available gates.
- `docs/STABILITY_POLICY.md`, `docs/COMPATIBILITY_MATRIX.md`, and `docs/QUALITY_GATES.md`.
- Public APIs, IAM behavior, release assets, and generated artifacts touched by the PR.

Agent review output should separate:

- Blocking findings: correctness, security, compatibility, migration, or required evidence gaps.
- Advisory findings: cleanup, maintainability, optional test expansion, or docs polish.
- Questions: decisions that need maintainer or contributor input.
- Evidence: commands run, reports read, and files inspected.

## Quality Gate Lifecycle

Quality gates are documented in `docs/QUALITY_GATES.md`. Agents do not invent new required gates during review; they map changed files to existing required, optional, deep, release, and security gates.

Default PR validation should favor fast checks:

```bash
make test-unit
make test-frontend
make smoke
make validate-helm
```

Deep or release-sensitive changes may require:

```bash
make test-integration
make test-e2e
make worker-conformance
make cqrs-e2e-smoke
make test-perf
make test-security
make smoke-release
make release-dry-run
```

Agents must record which checks were run, skipped, or recommended but not run. A skipped gate needs a reason and a maintainer decision if the change is release-sensitive.

## Automation Layers

ArtificialFlow separates agentic SDLC work into three layers:

| Layer | Examples | Safety default |
| :--- | :--- | :--- |
| Documentation and prompts | `docs/AGENTIC_SDLC.md`, `docs/AGENTIC_QA.md`, `docs/QUALITY_GATES.md`, `.agents/*.md`, `.agents/agentic-sdlc.yml`. | Source-controlled guidance only. |
| Repository automation | Issue templates, PR template, labels, CODEOWNERS, branch protection guidance, required checks, GitHub Actions summaries. | Minimal permissions and no automatic merge. |
| Agent execution automation | Future provider-backed agents on GitHub events, using `AGENT_API_KEY` or a GitHub App only after maintainer approval. | Read-only advisory mode unless explicitly configured. |

Current safe scaffolds:

- `.github/workflows/agentic-issue-triage.yml`
- `.github/workflows/agentic-qa-planner.yml`
- `.github/workflows/agentic-quality-gate.yml`
- `scripts/agentic/collect_quality_evidence.sh`
- `scripts/agentic/summarize_test_reports.py`
- `.agents/agentic-sdlc.yml`

These scaffolds collect summaries and artifacts. They do not call an external agent provider, post comments, add labels, create commits, or approve PRs by default.

## Agent Execution Event Model

Future agent execution can be wired to these events after maintainers choose a provider and configure secrets:

| Event | Agent | Default action | Write approval point |
| :--- | :--- | :--- | :--- |
| Issue opened/edited | Issue triage agent | Draft labels, missing-info checklist, and routing summary. | Maintainer enables comments/labels through a protected workflow or GitHub App. |
| PR opened/synchronized | QA agent and quality gate agent | Changed-path QA plan and recommended commands. | Maintainer approves any generated PR comment or patch branch. |
| CI or Security failure | CI failure investigator and security triage agent | Failure summary, likely root cause, reproduction commands. | Maintainer approves reruns, comments, labels, or fixes. |
| Security alert/dependency update | Security triage agent | Private or maintainer-only risk summary. | Maintainer approves public disclosure, upgrade, or exception. |
| Release candidate tag | Release readiness agent | Release gate checklist and artifact review. | Protected `release` environment approves publishing. |
| Scheduled community maintenance | Community maintainer agent | Stale thread, docs gap, and good-first-issue report. | Maintainer approves comments, closures, or labels. |

Required optional secrets for real agent execution:

- `AGENT_API_KEY` or provider-specific equivalent.
- `AGENT_GITHUB_APP_ID` and `AGENT_GITHUB_APP_PRIVATE_KEY` if write actions use a GitHub App.
- Release secrets stay separate: `DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN`, `NPM_TOKEN`.

Do not expose these secrets to forked PR code. Prefer GitHub protected environments such as `agent-apply` and `release` for privileged jobs.

## Community Growth Workflow

Community agents help maintainers reduce toil by:

- Finding stale issues that need reproduction, owner input, or closure.
- Suggesting `good first issue` and `help wanted` candidates.
- Identifying docs gaps from repeated questions.
- Drafting contributor-friendly next steps and reproduction checklists.
- Summarizing discussions and open questions without making roadmap promises.

Maintainers should review community agent output for tone, accuracy, and commitment risk. Agents should not promise dates, assign unpaid work, or close conduct-sensitive discussions.

## Security And Dependency Triage

Security and dependency agents should inspect:

- `.github/dependabot.yml`
- `.github/workflows/security.yml`
- `SECURITY.md`
- `docs/DEPENDENCY_POLICY.md`
- Lockfile, module, Dockerfile, GitHub Actions, and Helm changes.

Decision rules:

- Real vulnerabilities stay private until maintainers approve disclosure.
- Dependency updates that touch runtime, IAM, crypto, parser, Docker, Helm, or GitHub Actions need security and compatibility review.
- Public comments may summarize impact at a high level but must not include secrets, exploit payloads, or private report details.
- Advisory-only scanner output should still be tracked if it affects release confidence.

## Release Readiness Workflow

Release agents use `docs/RELEASE_CHECKLIST.md` as the source of truth and cross-check:

- CI and Security workflow status.
- `make smoke`
- `make smoke-release`
- `make validate-helm`
- `make release-dry-run`
- Node SDK tests, package validation, npm pack dry run, and SBOM/provenance notes.
- Docker image build, scan, signing, and published-image smoke expectations.
- Changelog, known limitations, compatibility notes, and upgrade guidance.

Agents may prepare a readiness report. Maintainers approve tags, published artifacts, release notes, and known-risk acceptance.

## Human Approval And Escalation

Escalate to maintainers when:

- The agent is uncertain whether a finding is blocking.
- A PR changes compatibility-sensitive surfaces.
- A workflow or script would require secrets or write permissions.
- Security findings mention active exploitation, private data, credentials, or auth bypass.
- A gate fails in a way that could be infrastructure flake or product regression.
- A release readiness check requires accepting known limitations.

Agent automation should default to read-only summaries. Writes require an explicit maintainer-controlled path such as a protected environment, trusted label, manual `workflow_dispatch`, or GitHub App installation with scoped permissions.

## Unsafe Changes To Avoid

Agents must not:

- Force-push, auto-merge, or publish releases.
- Rewrite contributor branches.
- Loosen CI or security gates to make a PR pass.
- Run forked PR code with repository secrets.
- Commit credentials, generated tokens, private vulnerability details, or local `.env` values.
- Broaden a scoped fix into unrelated refactors.
- Claim tests passed without command output or report evidence.
- Modify `.agents/thermo-nuclear-review-history.md` unless the current workflow explicitly requires a ledger update.

## Maintainer Review Of Agent Output

Maintainers should verify:

- The agent read the right files and event context.
- Findings include concrete evidence and a command or reproduction path.
- Labels and severity match `docs/LABELS.md`.
- Required gates match `docs/QUALITY_GATES.md`.
- Security-sensitive details are not public.
- Any proposed writes are small, auditable, and tied to an approved issue, PR, or release task.

When in doubt, keep the agent comment advisory and ask for human review before changing repository state.
