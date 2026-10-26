#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${PG_DSN:-}" ]]; then
  echo "PG_DSN is required for PostgreSQL runtime concurrency tests" >&2
  exit 2
fi

REPEAT="${RUNTIME_CONCURRENCY_REPEAT:-10}"
if ! [[ "${REPEAT}" =~ ^[1-9][0-9]*$ ]]; then
  echo "RUNTIME_CONCURRENCY_REPEAT must be a positive integer" >&2
  exit 2
fi

go test \
  ./backend/services/workflow-command/internal/infrastructure/persistence \
  -run 'TestPostgres(ThreeOwnerRuntimeClaimsAndDelayedFence|ConcurrentJobAndRuntimeWorkClaims)$' \
  -count="${REPEAT}" \
  -timeout=10m
