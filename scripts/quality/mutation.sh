#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GREMLINS_VERSION="v0.6.0"
REQUIRED="${MUTATION_REQUIRED:-false}"
REPORT="${ROOT}/reports/mutation-httpproblem.json"

cd "${ROOT}"
mkdir -p reports

if command -v gremlins >/dev/null 2>&1; then
  RUNNER=(gremlins)
elif [[ "${REQUIRED}" == "true" ]]; then
  RUNNER=(go run "github.com/go-gremlins/gremlins/cmd/gremlins@${GREMLINS_VERSION}")
else
  echo "mutation gate skipped: gremlins ${GREMLINS_VERSION} is not installed (set MUTATION_REQUIRED=true to install/run fail-closed)"
  exit 0
fi

"${RUNNER[@]}" unleash ./backend/libs/httpproblem \
  --workers 1 \
  --timeout-coefficient 10 \
  --output "${REPORT}" \
  --threshold-efficacy 0.80 \
  --threshold-mcover 0.80
python3 scripts/quality/mutation_gate.py "${REPORT}"
