# Gate 0 — GitHub issue triage for v0.4 release train

Validated against working tree on `release/v0.4.0-train` (2026-07-28).

| Issue | Status | Evidence / action |
| :--- | :--- | :--- |
| #38 IAM recipes | **Done** | `docs/iam/KEYCLOAK.md`, `ENTRA.md`, `AUTH0.md` + links from `docs/iam.md`. Close with evidence when PR lands. |
| #40 IAM quickstart E2E (external OIDC with `IAM_DEPLOYMENT_MODE=true`) | **Partial → covered** | `tests/e2e/playwright/specs/uat-mega-bpmn.spec.ts` + existing UAT video suite. Close when mega UAT green on RC. |
| #39 Modeler lint UX | **Done in train** | Inline deploy/validation banner in `frontend/src/pages/Modeler.tsx`. |
| #37 Gateway examples | **Done in train** | `examples/bpmn/gateways/`. |
| #36 Node worker examples | **Done in train** | `clients/nodejs-sdk/examples/worker-processing.js`. |
| #35 Helm managed deps | **Done in train** | `charts/artificialflow/examples/values-managed-deps.yaml`. |
| #34 Screenshots | **Docs ready** | Capture checklist in `docs/SCREENSHOTS.md`; refresh PNGs after UI screenshot pass. |

`gh` auth on the authoring machine was invalid (`gh auth login` required). Maintainer should comment/close issues with PR/commit links after merge.

PR description should use `Closes #38`, `Closes #39`, `Closes #37`, `Closes #36`, `Closes #35` when those paths land; `Closes #40` after mega UAT evidence; `#34` when screenshots committed.
