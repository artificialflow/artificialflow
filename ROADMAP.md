# ArtificialFlow Roadmap

This roadmap communicates the intended direction for the open-source project. It is not a contractual commitment.

## Public Launch: `v0.1.1`

- **GitHub readiness**: governance files, issue templates, security policy, dependency automation, and release notes.
- **Docker Hub readiness**: signed multi-architecture images with SBOMs for command, runtime, query, sync-worker, and frontend.
- **npm readiness**: public `@artificialflow/nodejs-sdk` package with package provenance.
- **Documentation**: quickstart, IAM guide, SDK guide, worker API guide, Helm deployment guide, and operations runbooks.

## P0 — Trust / 1.0 readiness

Ship reliability, upgrade, and honest BPMN coverage before marketing as a self-hosted alternative.

- **1.0 contract**: freeze worker, user-task, SDK, Helm, IAM, and BPMN semantics ([docs/COMPATIBILITY_MATRIX.md](docs/COMPATIBILITY_MATRIX.md), [docs/STABILITY_POLICY.md](docs/STABILITY_POLICY.md)).
- **HA evidence**: supported replica topology, runtime safety, k6 capacity numbers ([docs/HA_REFERENCE.md](docs/HA_REFERENCE.md)).
- **Day-2 ops**: upgrade guide, CQRS rebuild, outbox drain, lag SLOs ([docs/UPGRADE.md](docs/UPGRADE.md)).
- **BPMN honesty**: matrix aligned with runtime; external BPMN import guide ([docs/BPMN_SUPPORT_MATRIX.md](docs/BPMN_SUPPORT_MATRIX.md), [docs/EXTERNAL_BPMN_IMPORT.md](docs/EXTERNAL_BPMN_IMPORT.md)).
- **Golden demo**: human task + worker + timer bake-off path under `examples/golden-demo/`.

## P1 — Daily experience

- **Analytics** console features for `artificialflowanalytics`.
- **Instance ops**: failed job retry, stuck-job playbooks, clearer worker vs user-task states.
- **CQRS productization**: lag indicator, rebuild action, metrics guidance.
- **Worker DX**: Node.js, .NET, and Java SDKs and Python and Go worker clients ship today; next are lock renewal for the Python worker and published Python packages.
- **IAM recipes**: Keycloak, Entra ID, Auth0, Okta for the external OIDC integration.

## P2 — Selective engine parity

- **DMN**: real decision-table evaluation for `decisionRef` business-rule tasks.
- **Connectors**: HTTP first, then a small official set (~8), not a marketplace.
- **Versioning**: start-on-version and open-instance migrate vs drain guidance.

## Explicitly deferred

- Process-mining / Optimize-style analytics products.
- Connector marketplace.
- Throughput leaderboard competition.
- Real multi-tenancy (tenant fields exist but deployments use `default`).
- Managed cloud / support SKU until after P1 design-partner traction.
