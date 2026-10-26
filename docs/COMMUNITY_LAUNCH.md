# Community Launch Checklist

Use this checklist for repository settings and community setup that cannot be fully enforced from source files alone.

## GitHub Repository Settings

- Enable branch protection on the default branch.
- Require pull request reviews before merge.
- Require the CI and Security workflows before merge.
- Enable Dependabot alerts and security updates.
- Enable secret scanning and private vulnerability reporting.
- Enable CodeQL/code scanning for public repositories.
- Enable Discussions when maintainers are ready to support community questions.
- Review `CODEOWNERS`; it currently routes all ownership to `@azizAltaleb` and can be expanded with area teams later.
- Create or sync labels from `docs/LABELS.md` and `.github/labels.yml`.
- Treat agentic workflows as advisory unless maintainers explicitly configure provider secrets and write permissions.

## Repository Topics

Add these topics to improve discovery:

- `bpmn`
- `workflow-engine`
- `workflow-automation`
- `golang`
- `typescript`
- `react`
- `docker`
- `oidc`
- `sdk`
- `dotnet`
- `java`

## Starter Issues

Create initial issues after the public repository is ready.

| Label | Suggested issue |
| :--- | :--- |
| `good first issue` | Improve README screenshots and quickstart screenshots. |
| `documentation` | Expand production Helm examples for managed dependencies. |
| `sdk` | Add more Node.js SDK examples for worker processing. |
| `runtime` | Add BPMN compatibility examples for advanced gateway behavior. |
| `iam` | Add external IAM provider setup recipes. |
| `frontend` | Improve modeler validation and inline BPMN lint feedback. |
| `help wanted` | Add browser E2E coverage for the external OIDC quickstart (Keycloak). |

## Release Readiness

Before announcing a public release, confirm:

- CI and Security pass on the release commit.
- `make release-dry-run` passes locally or in a controlled CI runner.
- Agentic QA and quality gate summaries have no unresolved maintainer decisions.
- Docker Hub repositories exist and repository descriptions are populated.
- npm scope/package ownership is confirmed.
- Release notes include known limitations and upgrade guidance.
