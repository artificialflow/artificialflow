#!/usr/bin/env bash
# Collect changed-path quality evidence for agentic QA and quality gate review.
#
# Usage:
#   scripts/agentic/collect_quality_evidence.sh [base-ref] [head-ref]
#
# Outputs:
#   reports/agentic/changed-files.txt
#   reports/agentic/recommended-commands.txt
#   reports/agentic/quality-evidence.md
#   reports/agentic/quality-evidence.json

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
REPORT_DIR="${ROOT}/reports/agentic"
BASE_REF="${1:-${BASE_REF:-}}"
HEAD_REF="${2:-${HEAD_REF:-HEAD}}"

mkdir -p "${REPORT_DIR}"

cd "${ROOT}"

if [[ -z "${BASE_REF}" ]]; then
  if [[ -n "${GITHUB_BASE_REF:-}" ]]; then
    BASE_REF="origin/${GITHUB_BASE_REF}"
  elif git rev-parse --verify origin/main >/dev/null 2>&1; then
    BASE_REF="origin/main"
  elif git rev-parse --verify HEAD~1 >/dev/null 2>&1; then
    BASE_REF="HEAD~1"
  else
    BASE_REF=""
  fi
fi

CHANGED_FILE="${REPORT_DIR}/changed-files.txt"
: > "${CHANGED_FILE}"

if [[ -n "${BASE_REF}" ]] && git rev-parse --verify "${BASE_REF}" >/dev/null 2>&1; then
  git diff --name-only "${BASE_REF}...${HEAD_REF}" > "${CHANGED_FILE}"
else
  git diff --name-only "${HEAD_REF}" > "${CHANGED_FILE}" || true
fi

git ls-files --others --exclude-standard >> "${CHANGED_FILE}" || true
sort -u "${CHANGED_FILE}" -o "${CHANGED_FILE}"

COMMAND_FILE="${REPORT_DIR}/recommended-commands.txt"
: > "${COMMAND_FILE}"

add_command() {
  local command="$1"
  if ! grep -Fxq "${command}" "${COMMAND_FILE}" 2>/dev/null; then
    echo "${command}" >> "${COMMAND_FILE}"
  fi
}

add_risk() {
  local risk="$1"
  echo "${risk}" >> "${REPORT_DIR}/risks.tmp"
}

: > "${REPORT_DIR}/risks.tmp"

if [[ ! -s "${CHANGED_FILE}" ]]; then
  add_risk "No changed files detected."
fi

while IFS= read -r path; do
  [[ -z "${path}" ]] && continue

  case "${path}" in
    backend/services/workflow-command/internal/domain/bpmn/*|backend/services/workflow-command/tests/*)
      add_risk "BPMN parser/runtime behavior may be affected by ${path}."
      add_command "make test-bpmn-matrix"
      add_command "make test-unit"
      ;;
    backend/*)
      add_risk "Backend behavior may be affected by ${path}."
      add_command "make test-unit"
      ;;
  esac

  case "${path}" in
    backend/services/workflow-query/*|backend/services/sync-worker/*|scripts/cqrs_*|docs/RUNBOOK_CQRS_SYNC.md)
      add_risk "CQRS/query/sync behavior may be affected by ${path}."
      add_command "make cqrs-e2e-smoke"
      add_command "make cqrs-parity-check"
      ;;
    backend/libs/worker/*|docs/worker-api.md|backend/services/workflow-command/internal/interfaces/http/*)
      add_risk "Worker API compatibility may be affected by ${path}."
      add_command "make worker-conformance"
      ;;
    backend/libs/auth/*|docs/iam.md|SECURITY.md)
      add_risk "IAM or security behavior may be affected by ${path}."
      add_command "make test-security"
      ;;
    frontend/*)
      add_risk "Frontend behavior may be affected by ${path}."
      add_command "make test-frontend"
      add_command "npm --prefix frontend run lint"
      add_command "npm --prefix frontend run build"
      ;;
    clients/nodejs-sdk/*)
      add_risk "Node.js SDK compatibility may be affected by ${path}."
      add_command "npm --prefix clients/nodejs-sdk test"
      add_command "npm --prefix clients/nodejs-sdk run validate:package"
      add_command "cd clients/nodejs-sdk && npm pack --dry-run"
      ;;
    docker-compose*.yml|charts/*|backend/Dockerfile*|frontend/Dockerfile|scripts/validate_helm.sh|scripts/validate_compose_kafka_wiring.sh)
      add_risk "Deployment assets may be affected by ${path}."
      add_command "make smoke"
      add_command "make smoke-release"
      add_command "make validate-helm"
      ;;
    .github/workflows/security.yml|.github/dependabot.yml|package-lock.json|frontend/package-lock.json|clients/nodejs-sdk/package-lock.json|go.mod|go.sum)
      add_risk "Dependency or security automation may be affected by ${path}."
      add_command "make test-security"
      ;;
    .github/workflows/release-*|scripts/release_dry_run.sh|CHANGELOG.md|docs/RELEASE_CHECKLIST.md)
      add_risk "Release readiness may be affected by ${path}."
      add_command "make release-dry-run"
      add_command "make smoke-release"
      ;;
    docs/*|README.md|CONTRIBUTING.md|CODE_OF_CONDUCT.md)
      add_risk "Documentation or community workflow may be affected by ${path}."
      ;;
  esac
done < "${CHANGED_FILE}"

sort -u "${REPORT_DIR}/risks.tmp" -o "${REPORT_DIR}/risks.txt"
rm -f "${REPORT_DIR}/risks.tmp"

SUMMARY_MD="${REPORT_DIR}/quality-evidence.md"
{
  echo "# Agentic Quality Evidence"
  echo ""
  echo "Base ref: \`${BASE_REF:-unavailable}\`"
  echo "Head ref: \`${HEAD_REF}\`"
  echo ""
  echo "## Changed Files"
  echo ""
  if [[ -s "${CHANGED_FILE}" ]]; then
    sed 's/^/- `/' "${CHANGED_FILE}" | sed 's/$/`/'
  else
    echo "- No changed files detected."
  fi
  echo ""
  echo "## Risk Notes"
  echo ""
  if [[ -s "${REPORT_DIR}/risks.txt" ]]; then
    sed 's/^/- /' "${REPORT_DIR}/risks.txt"
  else
    echo "- No risk notes generated."
  fi
  echo ""
  echo "## Recommended Commands"
  echo ""
  if [[ -s "${COMMAND_FILE}" ]]; then
    sed 's/^/- `/' "${COMMAND_FILE}" | sed 's/$/`/'
  else
    echo "- No commands recommended beyond docs/template review."
  fi
  echo ""
  echo "## Policy References"
  echo ""
  echo "- \`docs/AGENTIC_SDLC.md\`"
  echo "- \`docs/AGENTIC_QA.md\`"
  echo "- \`docs/QUALITY_GATES.md\`"
} > "${SUMMARY_MD}"

python3 - "${CHANGED_FILE}" "${COMMAND_FILE}" "${REPORT_DIR}/risks.txt" "${REPORT_DIR}/quality-evidence.json" <<'PY'
import json
import sys

changed_path, commands_path, risks_path, out_path = sys.argv[1:]

def read_lines(path):
    try:
        with open(path, encoding="utf-8") as handle:
            return [line.strip() for line in handle if line.strip()]
    except FileNotFoundError:
        return []

data = {
    "changed_files": read_lines(changed_path),
    "recommended_commands": read_lines(commands_path),
    "risk_notes": read_lines(risks_path),
    "policy_refs": [
        "docs/AGENTIC_SDLC.md",
        "docs/AGENTIC_QA.md",
        "docs/QUALITY_GATES.md",
    ],
}

with open(out_path, "w", encoding="utf-8") as handle:
    json.dump(data, handle, indent=2)
    handle.write("\n")
PY

echo "[agentic] Wrote ${SUMMARY_MD}"
