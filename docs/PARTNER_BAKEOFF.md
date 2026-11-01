# Design-partner bake-off scorecard

Scorecard for Phase C of the 1.0 readiness plan. Pass = partner completes the path on ArtificialFlow **or** explicitly prefers MIT + simpler self-hosted ops after equal functional success.

| Scenario | ArtificialFlow | Result (fill) |
| :--- | :--- | :--- |
| Install to first process | Compose/Helm + gateway | **PASS** (local) — `make up` healthy; release images via `ARTIFICIALFLOW_IMAGE_TAG=v1.0.0-rc.1` |
| Deploy their BPMN (or linted import) | [EXTERNAL_BPMN_IMPORT.md](EXTERNAL_BPMN_IMPORT.md) + matrix | **PASS** (local) — mega BPMN UAT deploy + Tier-2 escalation deploy |
| One external worker | Node/Go worker | **PASS** (local) — `make worker-conformance` + mega UAT job activate/complete |
| One human task | User tasks API (`GET /tasks`, claim/complete) | **PASS** (local) — mega UAT task claim/complete |
| Their IdP | [docs/iam/](iam/) recipe | **PASS** (docs) — external OIDC with Keycloak/Entra/Auth0 recipes shipped |
| Day-2: runtime restart / lag | Lease drill + CQRS banner | **PASS** (code+docs) — HA/lease docs + CQRS banner on admin UI |
| Mega Supported BPMN UAT | `uat-mega-bpmn.spec.ts` | **PASS** (local) — 4/4 on RC images 2026-07-30 |

## ArtificialFlow runbook

```bash
make up   # or release images after v0.4.0 / v1.0.0-rc.1 publish
node scripts/mint_bakeoff_token.mjs
export ARTIFICIALFLOW_TOKEN="$(cat .local/bakeoff.token)"
export ARTIFICIALFLOW_END_USER=approver
node examples/golden-demo/run-bakeoff.mjs
make test-release-suite
```

## Evidence (maintainer self-run, 2026-07-29 → 2026-07-30)

- Partner: **internal maintainer self-run** (no external design partner scheduled yet; scorecard filled from Phase T gates on tip `b32906a` / tags `v0.4.0` + `v1.0.0-rc.1`).
- Release suite / DAST / mega UAT / dry-run: `reports/release-suite-local.md`
- RC re-gate (2026-07-30): `worker-conformance` + mega UAT 4/4 + `smoke-release-profiles` against `v1.0.0-rc.1` images
- Issues closed with evidence: #35–#40 (screenshots #34 checklist remains open for PNG refresh)
- Verdict: **PASS for GA gate under maintainer self-run**; re-validate with a named external partner when available.

Record dates, partner name (private), and links to evidence under `reports/`.
