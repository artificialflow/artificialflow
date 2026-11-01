## Summary

- **What changed**:
- **Why**:

## Validation

- **Backend**:
- **Frontend**:
- **SDK**:
- **Docker/Helm**:
- **Agentic QA / quality gates**:

## Risk

- **Compatibility impact**:
- **Security impact**:
- **Operational impact**:

## Checklist

- [ ] Tests or validation steps are included.
- [ ] Documentation is updated for user-facing behavior.
- [ ] Security-sensitive changes were reviewed.
- [ ] Worker/API compatibility impact is documented.
- [ ] Changed files were mapped to the relevant gates in `docs/QUALITY_GATES.md`.
- [ ] Skipped heavy checks are listed with a reason.
- [ ] PR does not expose secrets, private vulnerability details, or local credentials.

## Suggested Gate Mapping

Use this section when relevant:

- Backend/runtime: `make test-unit`, plus `make test-bpmn-matrix` for BPMN behavior.
- Frontend: `make test-frontend`, plus build/lint evidence from CI.
- SDK: `npm --prefix clients/nodejs-sdk test`, package validation, and `npm pack --dry-run`.
- Deployment: `make smoke`, `make smoke-release`, `make validate-helm`.
- Worker API: `make worker-conformance`.
- CQRS/query/sync: `make cqrs-e2e-smoke` and `make cqrs-parity-check` when infrastructure behavior changes.
- Security-sensitive: `make test-security` and Security workflow review.
- Release-sensitive: `make release-dry-run`.
