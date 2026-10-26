# Release train: v0.4.0 → 1.0.0

Branch: `release/v0.4.0-train` (product + UX + BPMN + Phase T harness).

## Cut v0.4.0

```bash
node scripts/validate_release_version.mjs
make test-release-suite
# stack up:
make up
make test-all && make test-all-functionality
make test-dast
ARTIFICIALFLOW_TOKEN=... make test-uat-mega-bpmn
make release-dry-run
git tag -s v0.4.0 -m "ArtificialFlow v0.4.0"
git push origin v0.4.0
# trigger Release workflow / publish images
ARTIFICIALFLOW_IMAGE_TAG=v0.4.0 make smoke-release
# refresh docs/CAPACITY.md from make test-perf
```

Close triaged GitHub issues with evidence ([ISSUE_TRIAGE_v0.4.md](ISSUE_TRIAGE_v0.4.md)).

## Freeze 1.0 RC

BPMN full coverage (Escalation, Conditional, Cancel/transaction, Event sub-process, Message/Signal start, Compensation, Manual wait, visual Pool/Lane/Data) lands on `release/1.0` before GA. Matrix: [BPMN_SUPPORT_MATRIX.md](BPMN_SUPPORT_MATRIX.md).

```bash
git checkout release/1.0
make test-bpmn-matrix
# freeze surfaces per docs/STABILITY_POLICY.md
git tag -s v1.0.0-rc.1 -m "ArtificialFlow v1.0.0-rc.1"
# re-run full Phase T on RC images
ARTIFICIALFLOW_IMAGE_TAG=v1.0.0-rc.1 make smoke-release
ARTIFICIALFLOW_IMAGE_TAG=v1.0.0-rc.1 make test-release-suite
```

## Partner bake-off

Fill [PARTNER_BAKEOFF.md](PARTNER_BAKEOFF.md).

## GA v1.0.0

Tag only after RC Phase T green + partner pass:

```bash
git tag -s v1.0.0 -m "ArtificialFlow v1.0.0"
git push origin v1.0.0
```

Messaging: MIT, your IdP, honest [BPMN_SUPPORT_MATRIX.md](BPMN_SUPPORT_MATRIX.md) — no Optimize / fake full-BPMN claims.
