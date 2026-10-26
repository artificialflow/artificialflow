#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
profile="${root}/tests/performance/profiles/release-soak-v1.json"
lock="${CANDIDATE_IMAGE_LOCK:-${root}/reports/candidate-images.json}"
expected_profile_sha="8e3554221f0023513c05ac64b005aa9dd6ddd22794d654b7c602772f910fcfd6"
summary="${root}/reports/release-soak-summary.json"
log="${root}/reports/release-soak.txt"
started_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
mkdir -p "${root}/reports"

for required in AUTH_TOKEN WORKFLOW_ID; do
  [[ -n "${!required:-}" ]] || { echo "${required} is required" >&2; exit 1; }
done
[[ -f "${lock}" ]] || { echo "candidate image lock required: ${lock}" >&2; exit 1; }
command -v k6 >/dev/null || { echo "k6 0.57.0 is required" >&2; exit 1; }
k6 version | grep -Eq 'v?0\.57\.0([[:space:]]|$)' || {
  echo "release soak requires pinned k6 0.57.0" >&2
  exit 1
}
actual_profile_sha="$(shasum -a 256 "${profile}" | awk '{print $1}')"
[[ "${actual_profile_sha}" == "${expected_profile_sha}" ]] || {
  echo "release soak profile SHA-256 mismatch" >&2
  exit 1
}
lock_sha_before="$(shasum -a 256 "${lock}" | awk '{print $1}')"

soak_pid=""
stop_soak() {
  if [[ -n "${soak_pid}" ]] && kill -0 "${soak_pid}" 2>/dev/null; then
    kill "${soak_pid}" 2>/dev/null || true
    wait "${soak_pid}" 2>/dev/null || true
  fi
}
trap 'stop_soak; exit 130' INT TERM

k6 run --quiet --summary-export "${summary}" \
  "${root}/tests/performance/k6/release-soak.js" >"${log}" 2>&1 &
soak_pid=$!
heartbeat_seconds="${SOAK_HEARTBEAT_SECONDS:-60}"
[[ "${heartbeat_seconds}" =~ ^[0-9]+$ && "${heartbeat_seconds}" -ge 10 ]] || {
  echo "SOAK_HEARTBEAT_SECONDS must be an integer of at least 10" >&2
  stop_soak
  exit 2
}
soak_started_epoch="$(date +%s)"
while kill -0 "${soak_pid}" 2>/dev/null; do
  sleep "${heartbeat_seconds}"
  if kill -0 "${soak_pid}" 2>/dev/null; then
    elapsed_seconds="$(( $(date +%s) - soak_started_epoch ))"
    python3 - "${elapsed_seconds}" "${SOAK_DURATION:-4h0m0s}" \
      "${COMMAND_URL:-http://localhost:8080}" "${QUERY_URL:-http://localhost:8081}" <<'PY'
import json, re, sys, urllib.request

elapsed = int(sys.argv[1])
duration_text, command_url, query_url = sys.argv[2:]

def duration_seconds(value):
    parts = re.findall(r"(\d+)([hms])", value)
    multipliers = {"h": 3600, "m": 60, "s": 1}
    return sum(int(amount) * multipliers[unit] for amount, unit in parts)

def fetch(url):
    try:
        with urllib.request.urlopen(url, timeout=3) as response:
            return response.status, json.load(response)
    except Exception:
        return "unavailable", {}

command_status, outbox = fetch(command_url + "/internal/metrics")
query_status, _ = fetch(query_url + "/readyz")
_, status_payload = fetch("http://127.0.0.1:6565/v1/status")
_, metrics_payload = fetch("http://127.0.0.1:6565/v1/metrics")
status = status_payload.get("data", {}).get("attributes", {})
metrics = {
    item.get("id"): item.get("attributes", {})
    for item in metrics_payload.get("data", [])
}

def sample(name, field, default=0):
    return metrics.get(name, {}).get("sample", {}).get(field, default)

breached = sorted(
    name for name, attributes in metrics.items()
    if attributes.get("tainted") is True
)
total = duration_seconds(duration_text) or 1
progress = min(100, elapsed * 100 / total)
print(
    "[soak] "
    f"elapsed={elapsed // 3600:02d}:{elapsed % 3600 // 60:02d}:{elapsed % 60:02d} "
    f"progress={progress:.1f}% health={command_status}/{query_status} "
    f"vus={status.get('vus', 0)}/{status.get('vus-max', 0)} "
    f"iterations={sample('iterations', 'count', 0):.0f} "
    f"dropped={sample('dropped_iterations', 'count', 0):.0f} "
    f"http_p95={sample('http_req_duration', 'p(95)', 0):.1f}ms "
    f"http_fail={sample('http_req_failed', 'rate', 0) * 100:.3f}% "
    f"checks={sample('checks', 'rate', 0) * 100:.3f}% "
    f"cqrs_p95={sample('cqrs_lag_ms', 'p(95)', 0):.1f}ms "
    f"outbox_p95={sample('outbox_lag_seconds', 'p(95)', 0):.1f}s "
    f"outbox_pending={outbox.get('outboxPending', 'unknown')} "
    f"breached={','.join(breached) if breached else 'none'}"
)
PY
  fi
done

set +e
wait "${soak_pid}"
soak_status=$?
set -e
soak_pid=""
trap - INT TERM
if [[ "${soak_status}" -ne 0 ]]; then
  python3 - "${log}" <<'PY' >&2
import sys
lines = open(sys.argv[1], encoding="utf-8", errors="replace").read().splitlines()
print("\n".join(lines[-40:]))
PY
  exit "${soak_status}"
fi

lock_sha_after="$(shasum -a 256 "${lock}" | awk '{print $1}')"
[[ "${lock_sha_before}" == "${lock_sha_after}" ]] || {
  echo "candidate digest lock changed during soak" >&2
  exit 1
}

python3 - "${profile}" "${summary}" <<'PY'
import json, sys
profile=json.load(open(sys.argv[1], encoding="utf-8"))
summary=json.load(open(sys.argv[2], encoding="utf-8"))
metrics=summary.get("metrics", {})
def metric_value(name, field):
    metric=metrics.get(name, {})
    return metric.get("values", {}).get(field, metric.get(field, 0))
requests=metric_value("http_reqs", "count")
checks=metric_value("checks", "passes")
minimum_requests=profile["sampling"]["minimumRequests"]
minimum_samples=profile["sampling"]["minimumSamples"]
if requests < minimum_requests:
    raise SystemExit(f"insufficient requests: {requests} < {minimum_requests}")
if checks < minimum_samples:
    raise SystemExit(f"insufficient samples: {checks} < {minimum_samples}")
for name, metric in metrics.items():
    if any(value is True for value in metric.get("thresholds", {}).values()):
        raise SystemExit(f"threshold failed: {name}")
print(f"soak validated: requests={requests} checks={checks}")
PY

profile_duration="$(python3 - "${profile}" <<'PY'
import json, sys
print(json.load(open(sys.argv[1], encoding="utf-8"))["traffic"]["duration"])
PY
)"
requested_duration="${SOAK_DURATION:-${profile_duration}}"
if [[ "${requested_duration}" != "${profile_duration}" ]]; then
  echo "validation duration ${requested_duration} passed; release evidence requires ${profile_duration}"
  exit 0
fi

EVIDENCE_FIXTURE_SHA256="${actual_profile_sha}" \
  bash "${root}/scripts/release/record_evidence.sh" REL-007 pass "${started_at}" \
  "${profile}" "${lock}" "${summary}" "${log}"
