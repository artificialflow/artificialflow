#!/usr/bin/env bash
# Exhaustive all-functionality tester for ArtificialFlow.
#
# Live deployment checks run by default. Use --skip-* flags to produce explicit
# skip entries when an environment cannot run a heavy layer.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPORTS_DIR="${REPORTS_DIR:-${ROOT}/reports}"
RUN_DIR="${REPORTS_DIR}/all-functionality"
EVENTS_FILE="${RUN_DIR}/events.jsonl"
STARTED_AT="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
WAIT_TIMEOUT_SEC="${WAIT_TIMEOUT_SEC:-420}"
CONTINUE_ON_FAILURE=true
FAIL_FAST=false
RESET_VOLUMES=false
SKIP_DEPLOYMENTS=false
SKIP_UI=false
SKIP_SDK_LIVE=false
SKIP_PERF=false
SKIP_SECURITY=false
SKIP_HELM_LIVE=false
ALLOW_HELM_LIVE=false
ALLOW_EXTERNAL_IAM_LIVE="${ALLOW_EXTERNAL_IAM_LIVE:-false}"
MAX_EXCERPT_BYTES=12000
DOCKER_AVAILABLE=true
LAST_EXIT_CODE=0
LAST_STATUS=pass
UI_ALREADY_RUN=false
PERF_ALREADY_RUN=false

mkdir -p "${RUN_DIR}"
: > "${EVENTS_FILE}"

usage() {
  cat <<'EOF'
Usage: scripts/test-all-functionality.sh [flags]

Live/deep checks run by default. Flags:
  --skip-deployments       Skip live Docker Compose deployment matrix.
  --skip-ui                Skip Playwright browser/UI layer.
  --skip-sdk-live          Skip live Node.js SDK smoke.
  --skip-perf              Skip k6 performance layer.
  --skip-security          Skip security scanners.
  --skip-helm-live         Skip live Helm install checks.
  --allow-helm-live        Allow live Helm install checks when kubectl/helm are available.
  --allow-external-iam-live
                           Allow external-IAM live checks when a real provider is configured.
  --continue-on-failure    Continue after failures (default).
  --fail-fast              Stop at first required failure.
  --reset-volumes          Use docker compose down -v during cleanup.
  --reports-dir DIR        Report directory (default: reports).
  --wait-timeout-sec N     Health wait timeout (default: 420).
  --max-excerpt-bytes N    Failure excerpt size (default: 12000).
  -h, --help               Show this help.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-deployments) SKIP_DEPLOYMENTS=true ;;
    --skip-ui) SKIP_UI=true ;;
    --skip-sdk-live) SKIP_SDK_LIVE=true ;;
    --skip-perf) SKIP_PERF=true ;;
    --skip-security) SKIP_SECURITY=true ;;
    --skip-helm-live) SKIP_HELM_LIVE=true ;;
    --allow-helm-live) ALLOW_HELM_LIVE=true; SKIP_HELM_LIVE=false ;;
    --allow-external-iam-live) ALLOW_EXTERNAL_IAM_LIVE=true ;;
    --continue-on-failure) CONTINUE_ON_FAILURE=true ;;
    --fail-fast) FAIL_FAST=true; CONTINUE_ON_FAILURE=false ;;
    --reset-volumes) RESET_VOLUMES=true ;;
    --reports-dir)
      shift
      REPORTS_DIR="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
      RUN_DIR="${REPORTS_DIR}/all-functionality"
      EVENTS_FILE="${RUN_DIR}/events.jsonl"
      mkdir -p "${RUN_DIR}"
      : > "${EVENTS_FILE}"
      ;;
    --wait-timeout-sec)
      shift
      WAIT_TIMEOUT_SEC="$1"
      ;;
    --max-excerpt-bytes)
      shift
      MAX_EXCERPT_BYTES="$1"
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown flag: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

log() {
  echo "[all-functionality] $*"
}

record_event() {
  local id="$1" name="$2" surface="$3" status="$4" required="$5" command="$6" exit_code="$7" duration_ms="$8" artifact="$9" failure_excerpt="${10:-}" skip_reason="${11:-}" deployment_model="${12:-}"
  python3 - "${EVENTS_FILE}" "$id" "$name" "$surface" "$status" "$required" "$command" "$exit_code" "$duration_ms" "$artifact" "$failure_excerpt" "$skip_reason" "$deployment_model" <<'PY'
import json
import sys
from datetime import datetime, timezone

events_path = sys.argv[1]
id_, name, surface, status, required, command, exit_code, duration_ms, artifact, failure_excerpt, skip_reason, deployment_model = sys.argv[2:]
event = {
    "id": id_,
    "name": name,
    "surface": surface,
    "status": status,
    "required": required == "true",
    "command": command,
    "exit_code": None if exit_code == "" else int(exit_code),
    "duration_ms": int(duration_ms or 0),
    "artifacts": [] if artifact == "" else [artifact],
    "failure_excerpt": failure_excerpt,
    "skip_reason": skip_reason,
    "deployment_model": deployment_model or None,
    "ended_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
}
with open(events_path, "a", encoding="utf-8") as handle:
    handle.write(json.dumps(event) + "\n")
PY
}

failure_excerpt() {
  local file="$1"
  if [[ -f "${file}" ]]; then
    python3 - "${file}" "${MAX_EXCERPT_BYTES}" <<'PY'
import sys
from pathlib import Path
path = Path(sys.argv[1])
limit = int(sys.argv[2])
data = path.read_text(encoding="utf-8", errors="replace")
if len(data) <= limit:
    print(data)
else:
    half = max(limit // 2, 1)
    print(data[:half])
    print("\n... output truncated ...\n")
    print(data[-half:])
PY
  fi
}

is_environmental_block() {
  local excerpt="$1"
  [[ "${excerpt}" == *"Blocked by sandbox network policy"* ]] && return 0
  [[ "${excerpt}" == *"permission denied while trying to connect to the docker API"* ]] && return 0
  [[ "${excerpt}" == *"Cannot connect to the Docker daemon"* ]] && return 0
  [[ "${excerpt}" == *"vuln.go.dev"* && "${excerpt}" == *"Forbidden"* ]] && return 0
  [[ "${excerpt}" == *"Binary is private but AWS credentials not found"* ]] && return 0
  [[ "${excerpt}" == *"Received 403 Forbidden"* && "${excerpt}" == *"node-precompiled-binaries.grpc.io"* ]] && return 0
  return 1
}

run_case() {
  local id="$1" name="$2" surface="$3" required="$4" command="$5" deployment_model="${6:-}"
  local artifact="${RUN_DIR}/${id}.log"
  mkdir -p "$(dirname "${artifact}")"
  log "RUN ${id}: ${command}"
  local start end duration status excerpt
  start="$(date +%s)"
  set +e
  (cd "${ROOT}" && bash -o pipefail -c "${command}") > "${artifact}" 2>&1
  local exit_code=$?
  set -e
  LAST_EXIT_CODE="${exit_code}"
  end="$(date +%s)"
  duration=$(( (end - start) * 1000 ))
  if [[ "${exit_code}" -eq 0 ]]; then
    status="pass"
    excerpt=""
  else
    excerpt="$(failure_excerpt "${artifact}")"
    if is_environmental_block "${excerpt}"; then
      status="warn"
    else
      status="fail"
    fi
  fi
  LAST_STATUS="${status}"
  record_event "${id}" "${name}" "${surface}" "${status}" "${required}" "${command}" "${exit_code}" "${duration}" "${artifact}" "${excerpt}" "" "${deployment_model}"
  if [[ "${status}" == "pass" ]]; then
    log "PASS ${id}"
  elif [[ "${status}" == "warn" ]]; then
    log "WARN ${id}; see ${artifact}"
  else
    log "FAIL ${id}; see ${artifact}"
  fi
  if [[ "${status}" == "fail" && "${required}" == "true" && "${FAIL_FAST}" == "true" ]]; then
    write_report || true
    exit "${exit_code}"
  fi
  if [[ "${status}" == "fail" && "${required}" == "true" && "${CONTINUE_ON_FAILURE}" != "true" ]]; then
    write_report || true
    exit "${exit_code}"
  fi
  return 0
}

skip_case() {
  local id="$1" name="$2" surface="$3" required="$4" reason="$5" deployment_model="${6:-}"
  log "SKIP ${id}: ${reason}"
  record_event "${id}" "${name}" "${surface}" "skip" "${required}" "" "" "0" "" "" "${reason}" "${deployment_model}"
}

warn_case() {
  local id="$1" name="$2" surface="$3" reason="$4" deployment_model="${5:-}"
  log "WARN ${id}: ${reason}"
  record_event "${id}" "${name}" "${surface}" "warn" "false" "" "" "0" "" "${reason}" "" "${deployment_model}"
}

external_iam_live_configured() {
  [[ -n "${AUTH_ISSUER_PUBLIC_URL:-}" && "${AUTH_ISSUER_PUBLIC_URL}" != "https://login.example.com" ]]
}

wait_http() {
  local url="$1" label="$2" start now
  start="$(date +%s)"
  log "Waiting for ${label} at ${url}"
  until curl -fsS --max-time 5 "${url}" >/dev/null 2>&1; do
    now="$(date +%s)"
    if (( now - start > WAIT_TIMEOUT_SEC )); then
      return 1
    fi
    sleep 3
  done
}

compose_down() {
  local files="$1" profile="${2:-}" label="$3"
  local volume_flag=""
  if [[ "${RESET_VOLUMES}" == "true" ]]; then
    volume_flag="-v"
  fi
  local profile_args=""
  if [[ -n "${profile}" ]]; then
    profile_args="--profile ${profile}"
  fi
  run_case "teardown-${label}" "Teardown ${label}" "deployment" "false" "docker compose ${files} ${profile_args} down ${volume_flag} --remove-orphans" "${label}" || true
}

collect_compose_diagnostics() {
  local files="$1" profile="${2:-}" label="$3"
  local diag_dir="${RUN_DIR}/diagnostics/${label}"
  mkdir -p "${diag_dir}"
  local profile_args=""
  if [[ -n "${profile}" ]]; then
    profile_args="--profile ${profile}"
  fi
  (cd "${ROOT}" && docker compose ${files} ${profile_args} ps > "${diag_dir}/compose-ps.txt" 2>&1 || true)
  (cd "${ROOT}" && docker compose ${files} ${profile_args} logs --tail=200 > "${diag_dir}/compose-logs.txt" 2>&1 || true)
}

write_report() {
  local flags_json
  flags_json="$(python3 - <<PY
import json
def b(value):
    return value == "true"
print(json.dumps({
  "skip_deployments": b("${SKIP_DEPLOYMENTS}"),
  "skip_ui": b("${SKIP_UI}"),
  "skip_sdk_live": b("${SKIP_SDK_LIVE}"),
  "skip_perf": b("${SKIP_PERF}"),
  "skip_security": b("${SKIP_SECURITY}"),
  "skip_helm_live": b("${SKIP_HELM_LIVE}"),
  "allow_helm_live": b("${ALLOW_HELM_LIVE}"),
  "allow_external_iam_live": b("${ALLOW_EXTERNAL_IAM_LIVE}"),
  "continue_on_failure": b("${CONTINUE_ON_FAILURE}"),
  "fail_fast": b("${FAIL_FAST}"),
  "reset_volumes": b("${RESET_VOLUMES}"),
  "wait_timeout_sec": "${WAIT_TIMEOUT_SEC}"
}))
PY
)"
  (cd "${ROOT}" && python3 scripts/qa/all_functionality_report.py \
    --repo "${ROOT}" \
    --reports-dir "${REPORTS_DIR}" \
    --events "${EVENTS_FILE}" \
    --bpmn-report "${REPORTS_DIR}/bpmn-matrix-report.json" \
    --output-json "${REPORTS_DIR}/all-functionality-report.json" \
    --output-md "${REPORTS_DIR}/all-functionality-report.md" \
    --started-at "${STARTED_AT}" \
    --flags-json "${flags_json}") || true
}

preflight() {
  run_case "preflight-go" "Go toolchain available" "preflight" "true" "go version"
  run_case "preflight-node" "Node.js available" "preflight" "true" "node --version && npm --version"
  run_case "preflight-docker" "Docker available" "preflight" "true" "docker --version && docker compose version"
  run_case "preflight-docker-api" "Docker API reachable" "preflight" "false" "docker info"
  if [[ "${LAST_EXIT_CODE}" -ne 0 ]]; then
    DOCKER_AVAILABLE=false
  fi
  if command -v helm >/dev/null 2>&1; then
    run_case "preflight-helm" "Helm available" "preflight" "false" "helm version --short"
  else
    skip_case "preflight-helm" "Helm available" "preflight" "false" "helm is not installed"
  fi
  if command -v kubectl >/dev/null 2>&1; then
    run_case "preflight-kubectl" "kubectl available" "preflight" "false" "kubectl version --client=true"
  else
    skip_case "preflight-kubectl" "kubectl available" "preflight" "false" "kubectl is not installed"
  fi
}

config_and_contracts() {
  run_case "config-smoke" "Compose smoke checks" "deployment" "true" "make smoke"
  run_case "config-smoke-release" "Release compose smoke checks" "deployment" "true" "make smoke-release"
  run_case "helm-render-validation" "Helm render and lint validation" "deployment" "true" "make validate-helm"
  run_case "go-unit" "Go unit and contract tests" "backend" "true" "make test-unit"
  run_case "frontend-unit-build" "Frontend lint, tests, and build" "frontend" "true" "npm --prefix frontend ci && npm --prefix frontend run lint && npm --prefix frontend test && npm --prefix frontend run build"
  run_case "sdk-contract" "Node.js SDK contract and package validation" "sdk" "true" "npm --prefix clients/nodejs-sdk ci && npm --prefix clients/nodejs-sdk test && npm --prefix clients/nodejs-sdk run validate:package && (cd clients/nodejs-sdk && npm pack --dry-run)"
  run_case "sdk-dotnet" ".NET SDK build and tests" "sdk" "true" "make sdk-dotnet"
  run_case "sdk-java" "Java SDK build and tests" "sdk" "true" "make sdk-java"
}

run_ui_playwright() {
  if [[ "${UI_ALREADY_RUN}" == "true" ]]; then
    return
  fi

  if [[ "${SKIP_UI}" == "true" ]]; then
    skip_case "ui-playwright" "Playwright browser and UI tests" "frontend" "false" "--skip-ui was requested"
  else
    run_case "ui-playwright" "Playwright browser and UI tests" "frontend" "false" "bash -o pipefail -c 'cd tests/e2e/playwright && npm install --silent && npx playwright install --with-deps chromium && npx playwright test specs/workflow.spec.ts --reporter=json,line 2>&1 | tee ../../../reports/playwright-functional.txt'"
  fi

  UI_ALREADY_RUN=true
}

run_perf_k6() {
  local deployment_model="${1:-}"
  if [[ "${PERF_ALREADY_RUN}" == "true" ]]; then
    return
  fi

  if [[ "${SKIP_PERF}" == "true" ]]; then
    skip_case "performance-k6" "k6 performance tests" "performance" "false" "--skip-perf was requested" "${deployment_model}"
  elif curl -fsS --max-time 5 "http://localhost:8080/health" >/dev/null 2>&1 && curl -fsS --max-time 5 "http://localhost:8081/health" >/dev/null 2>&1; then
    run_case "performance-k6" "k6 performance tests" "performance" "false" "make test-perf" "${deployment_model}"
  else
    skip_case "performance-k6" "k6 performance tests" "performance" "false" "No healthy workflow-command and workflow-query services are listening on http://localhost:8080 and http://localhost:8081; performance tests require a live stack." "${deployment_model}"
  fi

  PERF_ALREADY_RUN=true
}

deployment_source_disabled() {
  local files="-f docker-compose.yml" profile="" label="source-disabled"
  export IAM_DEPLOYMENT_MODE=false
  compose_down "${files}" "${profile}" "${label}"
  run_case "deploy-${label}" "Start source deployment (IAM disabled)" "deployment" "true" "make up" "${label}"
  if [[ "${LAST_EXIT_CODE}" -ne 0 ]]; then
    collect_compose_diagnostics "${files}" "${profile}" "${label}"
    skip_case "health-${label}" "Base full CQRS health checks" "deployment" "true" "Deployment startup failed." "${label}"
    skip_case "integration-${label}" "HTTP integration lifecycle" "command-api" "true" "Deployment startup failed." "${label}"
    skip_case "e2e-${label}" "CQRS, worker, and parity E2E" "cqrs" "true" "Deployment startup failed." "${label}"
    skip_case "worker-${label}" "Worker protocol conformance" "worker-api" "true" "Deployment startup failed." "${label}"
    skip_case "cqrs-smoke-${label}" "CQRS write to query smoke" "cqrs" "true" "Deployment startup failed." "${label}"
    skip_case "cqrs-parity-${label}" "CQRS parity check" "cqrs" "false" "Deployment startup failed." "${label}"
    compose_down "${files}" "${profile}" "${label}"
    return
  fi
  if wait_http "http://localhost:8080/health" "workflow-command" && wait_http "http://localhost:8081/health" "workflow-query" && wait_http "http://localhost:8092/health" "sync-worker"; then
    record_event "health-${label}" "Base full CQRS health checks" "deployment" "pass" "true" "curl health endpoints" "0" "0" "" "" "" "${label}"
  else
    collect_compose_diagnostics "${files}" "${profile}" "${label}"
    record_event "health-${label}" "Base full CQRS health checks" "deployment" "fail" "true" "curl health endpoints" "1" "0" "${RUN_DIR}/diagnostics/${label}/compose-logs.txt" "Timed out waiting for health endpoints." "" "${label}"
  fi
  run_case "integration-${label}" "HTTP integration lifecycle" "command-api" "true" "QUERY_AUTH_MODE=off make test-integration" "${label}"
  run_case "e2e-${label}" "CQRS, worker, and parity E2E" "cqrs" "true" "QUERY_AUTH_MODE=off make test-e2e" "${label}"
  run_case "worker-${label}" "Worker protocol conformance" "worker-api" "true" "QUERY_AUTH_MODE=off make worker-conformance" "${label}"
  run_case "cqrs-smoke-${label}" "CQRS write to query smoke" "cqrs" "true" "QUERY_AUTH_MODE=off make cqrs-e2e-smoke" "${label}"
  run_case "cqrs-parity-${label}" "CQRS parity check" "cqrs" "false" "make cqrs-parity-check" "${label}"
  run_ui_playwright
  run_perf_k6 "${label}"
  compose_down "${files}" "${profile}" "${label}"
  unset IAM_DEPLOYMENT_MODE
}

deployment_compose_model() {
  local label="$1" files="$2" up_command="$3" down_command="$4"
  run_case "deploy-${label}" "Start ${label} deployment" "deployment" "true" "${up_command}" "${label}"
  if [[ "${LAST_EXIT_CODE}" -ne 0 ]]; then
    collect_compose_diagnostics "${files}" "" "${label}"
    skip_case "health-${label}" "${label} health checks" "deployment" "true" "Deployment startup failed." "${label}"
    skip_case "auth-worker-${label}" "Authenticated worker/API role matrix" "iam" "false" "Deployment startup failed." "${label}"
    run_case "teardown-${label}" "Teardown ${label}" "deployment" "false" "${down_command}" "${label}"
    return
  fi
  local health_status="pass"
  if ! wait_http "http://localhost:8080/health" "${label} workflow-command"; then
    health_status="fail"
  fi
  if ! wait_http "http://localhost:8081/health" "${label} workflow-query"; then
    health_status="fail"
  fi
  if [[ "${health_status}" == "pass" ]]; then
    record_event "health-${label}" "${label} health checks" "deployment" "pass" "true" "curl command/query health endpoints" "0" "0" "" "" "" "${label}"
  else
    collect_compose_diagnostics "${files}" "" "${label}"
    record_event "health-${label}" "${label} health checks" "deployment" "fail" "true" "curl command/query health endpoints" "1" "0" "${RUN_DIR}/diagnostics/${label}/compose-logs.txt" "Timed out waiting for command/query health endpoints." "" "${label}"
  fi
  if [[ -n "${QUERY_BEARER_TOKEN:-}" ]]; then
    WORKER_AUTH_BEARER_TOKEN="${QUERY_BEARER_TOKEN}"
    export WORKER_AUTH_BEARER_TOKEN
    run_case "worker-${label}" "Worker conformance with provided token" "worker-api" "false" "make worker-conformance" "${label}"
    unset WORKER_AUTH_BEARER_TOKEN
  else
    warn_case "auth-worker-${label}" "Authenticated worker/API role matrix" "iam" "No QUERY_BEARER_TOKEN provided for authenticated live checks." "${label}"
  fi
  run_case "teardown-${label}" "Teardown ${label}" "deployment" "false" "${down_command}" "${label}"
}

deployment_matrix() {
  if [[ "${SKIP_DEPLOYMENTS}" == "true" ]]; then
    skip_case "deployment-matrix" "Live Docker Compose deployment matrix" "deployment" "true" "--skip-deployments was requested"
    return
  fi
  if [[ "${DOCKER_AVAILABLE}" != "true" ]]; then
    skip_case "deployment-matrix" "Live Docker Compose deployment matrix" "deployment" "true" "Docker preflight failed; live deployment checks cannot run."
    return
  fi
  deployment_source_disabled
  if [[ "${ALLOW_EXTERNAL_IAM_LIVE}" == "true" ]] && external_iam_live_configured; then
    deployment_compose_model "source-external" "-f docker-compose.yml" "make up" "make down"
  elif [[ "${ALLOW_EXTERNAL_IAM_LIVE}" == "true" ]]; then
    warn_case "external-iam-live" "External OIDC live deployment" "deployment" "External OIDC live checks require AUTH_ISSUER_PUBLIC_URL to point at a reachable provider." "source-external"
  else
    warn_case "external-iam-live" "External OIDC live deployment" "deployment" "External OIDC live checks require a reachable provider and are disabled by default; set IAM_DEPLOYMENT_MODE=true and AUTH_ISSUER_* then pass --allow-external-iam-live." "source-external"
  fi

  if [[ -n "${ARTIFICIALFLOW_IMAGE_TAG:-}" ]]; then
    IAM_DEPLOYMENT_MODE=false deployment_compose_model "release-no-iam" "-f docker-compose.yml" "IAM_DEPLOYMENT_MODE=false make up-release" "make down"
  else
    warn_case "release-live-deployments" "Release image live deployments" "deployment" "ARTIFICIALFLOW_IMAGE_TAG is not set; release-image live checks not run."
  fi

  if [[ "${ALLOW_HELM_LIVE}" == "true" && "${SKIP_HELM_LIVE}" != "true" ]]; then
    if command -v helm >/dev/null 2>&1 && command -v kubectl >/dev/null 2>&1 && kubectl cluster-info >/dev/null 2>&1; then
      run_case "helm-live-default-rendered" "Helm live default dry-run server validation" "deployment" "false" "helm upgrade --install artificialflow ./charts/artificialflow -n artificialflow --create-namespace --dry-run=server" "helm-default"
      run_case "helm-live-kind-rendered" "Helm live kind-smoke dry-run server validation" "deployment" "false" "helm upgrade --install artificialflow ./charts/artificialflow -n artificialflow --create-namespace -f ./charts/artificialflow/values-kind-smoke.yaml --dry-run=server" "helm-kind"
    else
      warn_case "helm-live" "Helm live deployment checks" "deployment" "helm, kubectl, or Kubernetes cluster is unavailable."
    fi
  else
    warn_case "helm-live" "Helm live deployment checks" "deployment" "Helm live install is disabled by default; pass --allow-helm-live to enable."
  fi
}

bpmn_matrix() {
  run_case "bpmn-make-matrix" "Existing BPMN matrix target" "bpmn" "true" "make test-bpmn-matrix"
  run_case "bpmn-exhaustive-catalog" "BPMN scenario catalog runner" "bpmn" "true" "python3 scripts/qa/run_bpmn_matrix.py --reports-dir '${REPORTS_DIR}' --output-json '${REPORTS_DIR}/bpmn-matrix-report.json' --output-md '${REPORTS_DIR}/bpmn-matrix-report.md'"
}

ui_sdk_perf_security() {
  run_ui_playwright

  if [[ "${SKIP_SDK_LIVE}" == "true" ]]; then
    skip_case "sdk-live" "Live Node.js SDK smoke" "sdk" "false" "--skip-sdk-live was requested"
  elif [[ -n "${ARTIFICIALFLOW_TOKEN:-${ARTIFICIALFLOW_TOKEN:-}}" ]]; then
    run_case "sdk-live" "Live Node.js SDK smoke" "sdk" "false" "cd clients/nodejs-sdk && ARTIFICIALFLOW_BASE_URL='${ARTIFICIALFLOW_BASE_URL:-${ARTIFICIALFLOW_BASE_URL:-http://localhost:9100/api}}' node examples/sdk-smoke-test.js"
  else
    warn_case "sdk-live" "Live Node.js SDK smoke" "sdk" "ARTIFICIALFLOW_TOKEN is not set."
  fi

  run_perf_k6

  if [[ "${SKIP_SECURITY}" == "true" ]]; then
    skip_case "security-scan" "Security scanners" "security" "false" "--skip-security was requested"
  else
    run_case "security-scan" "Security scanners" "security" "true" "make test-security"
  fi
}

main() {
  log "Reports directory: ${REPORTS_DIR}"
  preflight
  config_and_contracts
  deployment_matrix
  bpmn_matrix
  ui_sdk_perf_security
  write_report
  log "Report: ${REPORTS_DIR}/all-functionality-report.md"
  if python3 - "${REPORTS_DIR}/all-functionality-report.json" <<'PY'
import json, sys
report = json.load(open(sys.argv[1]))
raise SystemExit(0 if report["overall"]["status"] != "fail" else 1)
PY
  then
    log "All-functionality harness completed without required failures."
  else
    log "All-functionality harness completed with required failures."
    exit 1
  fi
}

main "$@"
