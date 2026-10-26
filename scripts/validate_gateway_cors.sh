#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG="${ROOT}/gateway/nginx.conf"

required_headers=(
  "Authorization"
  "Content-Type"
  "X-Correlation-ID"
  "Idempotency-Key"
  "X-Workflow-Worker-Protocol-Version"
)

allow_headers_line="$(
  awk '/Access-Control-Allow-Headers/ { print; found = 1 } END { if (!found) exit 1 }' "${CONFIG}"
)" || {
  echo "gateway CORS configuration does not declare Access-Control-Allow-Headers" >&2
  exit 1
}

for header in "${required_headers[@]}"; do
  if [[ "${allow_headers_line}" != *"${header}"* ]]; then
    echo "gateway CORS allowlist is missing ${header}" >&2
    exit 1
  fi
done

echo "Gateway CORS integration-header validation passed"
