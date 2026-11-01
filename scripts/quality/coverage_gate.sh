#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
REPORTS="${ROOT}/reports"
PROFILE="${REPORTS}/quality-coverage.out"

mkdir -p "${REPORTS}"
cd "${ROOT}"

go test ./backend/... ./connectors/... \
  -covermode=atomic \
  -coverprofile="${PROFILE}" \
  -count=1
python3 scripts/quality/coverage_gate.py "${PROFILE}"
