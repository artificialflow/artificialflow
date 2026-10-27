#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
lock="${CANDIDATE_IMAGE_LOCK:-${root}/reports/candidate-images.json}"
started_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
log="${root}/reports/security-release-gate.txt"
mkdir -p "${root}/reports"

for tool in docker gitleaks govulncheck gosec trivy cosign npm python3; do
  command -v "${tool}" >/dev/null || { echo "required security tool missing: ${tool}" >&2; exit 1; }
done
[[ -f "${lock}" ]] || { echo "candidate image lock required: ${lock}" >&2; exit 1; }
gitleaks version | grep -F "8.30.1"
trivy --version | grep -F "Version: 0.65.0"
govulncheck -version | grep -F "v1.1.4"
gosec -version | grep -F "Version: 2.22.8"

{
  gitleaks detect --source "${root}" --redact --exit-code 1
  (
    cd "${root}"
    govulncheck ./...
    gosec -severity medium -exclude=G102,G304,G703,G704 \
      -exclude-generated -exclude-dir=vendor ./backend/...
    npm --prefix frontend audit --audit-level=high --omit=dev
    npm --prefix clients/nodejs-sdk audit --audit-level=high --omit=dev
    npm --prefix tests/e2e/playwright audit --audit-level=high --omit=dev
  )
  python3 "${root}/scripts/release/upstream_images.py" check-sources
  python3 - "${lock}" <<'PY'
import json, sys
data=json.load(open(sys.argv[1], encoding="utf-8"))
for image in data["images"]:
    print(f"{image['repository']}@{image['digest']}")
PY
} 2>&1 | tee "${log}"

# Community images are scanned for the record. A finding blocks only when the
# lock is not the tag in release/upstream-images.tsv, which means an upgrade
# is still required. Application images keep a zero HIGH/CRITICAL gate.
python3 "${root}/scripts/release/upstream_images.py" lock-lines "${lock}" |
while IFS=$'\t' read -r name repository tag digest; do
  published="$(docker buildx imagetools inspect "${repository}:${tag}" --format '{{printf "%s" .Manifest.Digest}}')"
  if [[ "${published}" != "${digest}" ]]; then
    echo "${name} must be upgraded to ${repository}:${tag} before release; lock is ${digest}, published tag is ${published}" >&2
    exit 1
  fi
  echo "community image ${name} matches ${repository}:${tag}" | tee -a "${log}"
done

python3 "${root}/scripts/release/upstream_images.py" scan-plan "${lock}" |
while IFS=$'\t' read -r mode reference; do
  if [[ "${mode}" == "blocking" ]]; then
    exit_code=1
  elif [[ "${mode}" == "report" ]]; then
    exit_code=0
  else
    echo "unknown scan mode ${mode}" >&2
    exit 1
  fi
  echo "trivy ${mode} ${reference}" | tee -a "${log}"
  trivy image --timeout 20m --exit-code "${exit_code}" --severity HIGH,CRITICAL --ignore-unfixed "${reference}" \
    2>&1 | tee -a "${log}"
done

identity="https://github.com/${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}/.github/workflows/release-images.yml@${GITHUB_REF:?GITHUB_REF is required}"
issuer="https://token.actions.githubusercontent.com"
python3 - "${lock}" <<'PY' | while IFS= read -r reference; do
import json, sys
for image in json.load(open(sys.argv[1], encoding="utf-8"))["images"]:
    print(f"{image['repository']}@{image['digest']}")
PY
  cosign verify --certificate-identity "${identity}" \
    --certificate-oidc-issuer "${issuer}" "${reference}" 2>&1 | tee -a "${log}"
  test "$(docker buildx imagetools inspect "${reference}" --format '{{json .SBOM}}')" != "null"
  test "$(docker buildx imagetools inspect "${reference}" --format '{{json .Provenance}}')" != "null"
done

bash "${root}/scripts/release/record_evidence.sh" REL-011 pass "${started_at}" \
  "${lock}" "${log}"
