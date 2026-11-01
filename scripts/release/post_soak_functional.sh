#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
soak_evidence="${root}/reports/evidence/REL-007.json"
lock="${CANDIDATE_IMAGE_LOCK:-${root}/reports/candidate-images.json}"
suite_command="${FUNCTIONAL_SUITE_COMMAND:-}"
started_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

[[ -n "${suite_command}" ]] || {
  echo "FUNCTIONAL_SUITE_COMMAND is required for the deployed candidate environment" >&2
  exit 1
}
[[ -f "${soak_evidence}" && -f "${lock}" ]] || {
  echo "unchanged-digest soak evidence and candidate lock are required" >&2
  exit 1
}

python3 - "${soak_evidence}" "${lock}" <<'PY'
import hashlib, json, pathlib, sys
evidence=json.load(open(sys.argv[1], encoding="utf-8"))
if evidence.get("status") != "pass" or evidence.get("attempts") != 1:
    raise SystemExit("REL-007 must be a clean single-attempt pass")
lock=pathlib.Path(sys.argv[2])
actual=hashlib.sha256(lock.read_bytes()).hexdigest()
recorded=[a.get("sha256") for a in evidence.get("artifacts", [])
          if pathlib.Path(a.get("path","")).name == lock.name]
if recorded != [actual]:
    raise SystemExit("candidate digest lock differs from the soak")
PY

logs=()
for run in 1 2; do
  log="${root}/reports/post-soak-functional-${run}.txt"
  logs+=("${log}")
  bash -o pipefail -c "${suite_command}" 2>&1 | tee "${log}"
  if grep -Eiq '(^|[^a-z])(skip(ped)?|warn(ing)?|flaky)([^a-z]|$)' "${log}"; then
    echo "functional suite ${run} contained skipped, warning, or flaky output" >&2
    exit 1
  fi
done

bash "${root}/scripts/release/record_evidence.sh" REL-010 pass "${started_at}" \
  "${lock}" "${logs[@]}"
