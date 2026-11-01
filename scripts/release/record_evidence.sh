#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 3 ]]; then
  echo "usage: $0 GATE_ID STATUS STARTED_AT [artifact ...]" >&2
  exit 2
fi

gate_id="$1"
status="$2"
started_at="$3"
shift 3

case "${status}" in
  pass|fail|skipped|warn|flaky) ;;
  *) echo "invalid evidence status: ${status}" >&2; exit 2 ;;
esac

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
mkdir -p "${root}/reports/evidence"
commit_sha="${GITHUB_SHA:-$(git -C "${root}" rev-parse HEAD)}"
completed_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
attempts="${EVIDENCE_ATTEMPTS:-1}"
fixture_sha="${EVIDENCE_FIXTURE_SHA256:-}"
candidate_identity="$(python3 "${root}/scripts/release/evidence.py" candidate-identity)"

python3 - "${root}" "${gate_id}" "${status}" "${started_at}" "${completed_at}" \
  "${commit_sha}" "${attempts}" "${fixture_sha}" "${candidate_identity}" "$@" <<'PY'
import hashlib
import json
import pathlib
import subprocess
import sys

root = pathlib.Path(sys.argv[1])
identity = json.loads(sys.argv[9])
if identity["commitSha"] != sys.argv[6]:
    raise SystemExit(
        f"candidate commit mismatch: {identity['commitSha']} != {sys.argv[6]}"
    )
artifact_paths = sys.argv[10:]
artifacts = []
for value in artifact_paths:
    path = pathlib.Path(value)
    if not path.is_absolute():
        path = root / path
    if not path.is_file():
        raise SystemExit(f"evidence artifact does not exist: {value}")
    artifacts.append({
        "path": str(path.relative_to(root)),
        "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
    })

manifest = json.loads((root / "release/evidence-gates.json").read_text(encoding="utf-8"))
gate = next((item for item in manifest["gates"] if item["id"] == sys.argv[2]), None)
if gate is None:
    raise SystemExit(f"gate is not present in evidence manifest: {sys.argv[2]}")

tool_commands = {
    "git": ["git", "--version"],
    "bash": ["bash", "--version"],
    "python3": ["python3", "--version"],
    "node": ["node", "--version"],
    "npm": ["npm", "--version"],
    "go": ["go", "version"],
    "docker": ["docker", "--version"],
    "kubectl": ["kubectl", "version", "--client"],
    "helm": ["helm", "version", "--short"],
    "kind": ["kind", "version"],
    "k6": ["k6", "version"],
    "gitleaks": ["gitleaks", "version"],
    "govulncheck": ["govulncheck", "-version"],
    "gosec": ["gosec", "-version"],
    "trivy": ["trivy", "--version"],
    "cosign": ["cosign", "version"],
}
tool_versions = {}
for name, command in tool_commands.items():
    try:
        output = subprocess.check_output(
            command, stderr=subprocess.STDOUT, text=True, timeout=10
        ).strip()
    except (FileNotFoundError, subprocess.CalledProcessError, subprocess.TimeoutExpired):
        continue
    tool_versions[name] = output[:1000]

payload = {
    "schemaVersion": 1,
    "gateId": sys.argv[2],
    "status": sys.argv[3],
    "startedAt": sys.argv[4],
    "completedAt": sys.argv[5],
    "commitSha": sys.argv[6],
    "candidateTreeSha256": identity["candidateTreeSha256"],
    "uncommittedCandidate": identity["dirty"],
    "candidateFileCount": identity["fileCount"],
    "attempts": int(sys.argv[7]),
    "fixtureSha256": sys.argv[8] or None,
    "command": gate["command"],
    "toolVersions": tool_versions,
    "artifacts": artifacts,
}
output = root / "reports" / "evidence" / f"{sys.argv[2]}.json"
output.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")
print(output)
PY
