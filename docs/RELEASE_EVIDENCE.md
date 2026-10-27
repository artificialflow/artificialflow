# Release Evidence and Waivers

`release/evidence-gates.json` is the machine-readable release policy. Gate IDs
are permanent: change a gate's policy in place, but never reuse or renumber an
ID. Each required gate emits `reports/evidence/REL-NNN.json`, and
`scripts/release/evidence.py report` binds the result to the commit, fixture,
artifacts, and completion time.

A required gate passes only with fresh, single-attempt `pass` evidence for the
expected commit. Missing, stale, skipped, warning, flaky, malformed, or
SHA-mismatched evidence blocks the report. A successful command without its
required evidence also blocks release.

Cluster and recovery gates stay valid for 168 hours: REL-003, REL-004, REL-005,
REL-006, REL-007, REL-008, REL-009, REL-010, and REL-013. Checks that rerun in
minutes stay valid for 24 hours: REL-001, REL-002, REL-011, and REL-012. The
candidate tree includes every tracked file, including documentation changed
before or after a gate run. REL-011 still blocks on ArtificialFlow image
findings, secrets, and an outdated community image pin. Findings in the
current community image tags are recorded and do not block the release.

## Candidate identity

`reports/candidate-images.json` records the five tested image repository/digest
pairs. The lock is used for architecture checks, Helm smoke, soak, post-soak
functional runs, promotion, and post-publish smoke. Candidate digests must not
change during that sequence. `reports/candidate-packages.json` similarly records
the package name, version, tarball SHA-256, and registry integrity value.

`release/artifact-inventory.json` is the authoritative inventory for images,
the Helm chart, SDKs, SBOMs, provenance, signatures, release notes, locks, and
evidence. Release evidence is retained for the period declared by each gate;
the default is 365 days.

## Waiver policy

Waivers are exceptional, reviewed release artifacts, not warning conversions.
Add an entry to `release/waivers.json` containing all of:

```json
{
  "id": "WAIVER-2026-001",
  "gateId": "REL-000",
  "owner": "named accountable person or team",
  "reason": "specific risk and why the gate cannot complete",
  "compensatingControl": "independently verifiable control and evidence link",
  "approvedBy": "protected-environment approver",
  "createdAt": "2026-08-06T00:00:00Z",
  "expiresAt": "2026-08-13T00:00:00Z",
  "scope": {"commitSha": "40-hex SHA", "releaseTag": "v1.2.0"}
}
```

The owner must be accountable for closure. The compensating control must be
testable and at least reduce the risk covered by the blocked gate. Expiry is
mandatory, may not exceed 30 days, and may not be silently extended. Approval
must come from the protected release environment and must not be the waiver
owner. A waiver never changes evidence to `pass`; reports show the blocked gate
and the separate approved exception.
