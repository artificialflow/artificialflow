#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

go test -race -count=1 \
  ./backend/libs/metrics \
  ./backend/libs/worker \
  ./backend/services/sync-worker/internal/application \
  ./backend/services/sync-worker/internal/infrastructure/messaging \
  ./backend/services/workflow-command/internal/application \
  ./backend/services/workflow-command/internal/infrastructure/persistence
