#!/usr/bin/env bash
# test-security.sh — govulncheck + gosec + trivy image scan
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPORTS="${ROOT}/reports"
mkdir -p "${REPORTS}"

OVERALL=0
GOVULNCHECK_VERSION="${GOVULNCHECK_VERSION:-v1.1.4}"
GOSEC_VERSION="${GOSEC_VERSION:-v2.22.8}"
TRIVY_VERSION="${TRIVY_VERSION:-0.65.0}"
# Scanners must be built with the repo toolchain or they cannot load newer packages.
REPO_GO_VERSION="$(cd "${ROOT}" && go env GOVERSION)"
export PATH="$(go env GOPATH)/bin:${PATH}"

{
  echo "# Security Test Results"
  echo ""
  echo "Generated: $(date '+%Y-%m-%d %H:%M:%S')"
  echo ""
} > "${REPORTS}/security.md"

# ── 1. govulncheck ─────────────────────────────────────────────────────────

echo "[security] Running govulncheck..."
GOTOOLCHAIN="${REPO_GO_VERSION}" go install "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}"

{
  echo "## govulncheck (CVE scan — Go deps)"
  echo '```'
} >> "${REPORTS}/security.md"

if govulncheck ./... 2>&1 | tee "${REPORTS}/govulncheck.txt"; then
  VULN_EXIT=0
else
  VULN_EXIT=$?
  OVERALL=1
fi

cat "${REPORTS}/govulncheck.txt" >> "${REPORTS}/security.md"
{
  echo '```'
  if [[ $VULN_EXIT -eq 0 ]]; then
    echo "**Status: ✅ No vulnerabilities found**"
  else
    echo "**Status: ❌ Scan failed or vulnerabilities found — release blocked**"
  fi
  echo ""
} >> "${REPORTS}/security.md"

# ── 2. gosec ───────────────────────────────────────────────────────────────

echo "[security] Running gosec (SAST)..."
GOTOOLCHAIN="${REPO_GO_VERSION}" go install "github.com/securego/gosec/v2/cmd/gosec@${GOSEC_VERSION}"

{
  echo "## gosec (Go SAST)"
  echo '```'
} >> "${REPORTS}/security.md"

if gosec -no-fail -fmt text -out "${REPORTS}/gosec.txt" \
  -exclude-dir=vendor \
  -exclude-generated \
  ./backend/...; then
  GOSEC_EXIT=0
  grep -q "^Summary:" "${REPORTS}/gosec.txt" 2>/dev/null || GOSEC_EXIT=1
else
  GOSEC_EXIT=$?
fi

cat "${REPORTS}/gosec.txt" >> "${REPORTS}/security.md" 2>/dev/null || true
HIGH_ISSUES=$(grep -c "Severity: HIGH" "${REPORTS}/gosec.txt" 2>/dev/null || true)
MED_ISSUES=$(grep -c "Severity: MEDIUM" "${REPORTS}/gosec.txt" 2>/dev/null || true)
HIGH_ISSUES=${HIGH_ISSUES:-0}
MED_ISSUES=${MED_ISSUES:-0}

{
  echo '```'
  echo "**Findings: HIGH=${HIGH_ISSUES}, MEDIUM=${MED_ISSUES}**"
  if [[ "${GOSEC_EXIT}" -ne 0 || "${HIGH_ISSUES}" -gt 0 || "${MED_ISSUES}" -gt 0 ]]; then
    echo "**Status: ❌ Scanner failure or medium/high severity issues found**"
    OVERALL=1
  else
    echo "**Status: ✅ No high/medium issues**"
  fi
  echo ""
} >> "${REPORTS}/security.md"

# ── 3. trivy — container image scan ───────────────────────────────────────

echo "[security] Running trivy container scan..."

{
  echo "## trivy (Container Image Scan)"
  echo ""
} >> "${REPORTS}/security.md"

IMAGES=()
while IFS= read -r img; do
  [[ -n "${img}" ]] && IMAGES+=("${img}")
done < <(cd "${ROOT}" && docker compose config --format json | python3 -c '
import json, sys
services = json.load(sys.stdin)["services"].values()
print("\n".join(sorted({s["image"] for s in services if "build" in s and "image" in s})))
')
if [[ ${#IMAGES[@]} -eq 0 ]]; then
  echo "  could not resolve locally built images from docker compose config"
  OVERALL=1
fi
TRIVY_CMD=()

if command -v trivy &>/dev/null; then
  TRIVY_CMD=(trivy)
elif command -v docker &>/dev/null; then
  TRIVY_CMD=(docker run --rm -v /var/run/docker.sock:/var/run/docker.sock "aquasec/trivy:${TRIVY_VERSION}")
fi

if [[ ${#TRIVY_CMD[@]} -eq 0 ]]; then
  echo "  trivy unavailable — release blocked"
  echo "**trivy: ❌ Required scanner unavailable**" >> "${REPORTS}/security.md"
  OVERALL=1
else
  for img in ${IMAGES[@]+"${IMAGES[@]}"}; do
    echo "  Scanning ${img}..."
    {
      echo "### ${img}"
      echo '```'
    } >> "${REPORTS}/security.md"
    if ! docker image inspect "${img}" >/dev/null 2>&1; then
      echo "required image is missing: ${img}" | tee -a "${REPORTS}/security.md"
      OVERALL=1
      continue
    fi
    if ! "${TRIVY_CMD[@]}" image --exit-code 1 --severity HIGH,CRITICAL \
      --format table "${img}" 2>&1 | \
      tee -a "${REPORTS}/trivy-${img//[\/:]/_}.txt" >> "${REPORTS}/security.md"; then
      OVERALL=1
    fi
    {
      echo '```'
      echo ""
    } >> "${REPORTS}/security.md"
  done
fi

# ── final status ───────────────────────────────────────────────────────────

{
  echo "---"
  echo "_Security scan complete._"
} >> "${REPORTS}/security.md"

echo "[security] Done. Report: ${REPORTS}/security.md"
exit $OVERALL
