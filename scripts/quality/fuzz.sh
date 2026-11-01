#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
FUZZ_TIME="${FUZZ_TIME:-5s}"
cd "${ROOT}"

go test ./backend/services/workflow-command/internal/domain/bpmn \
  -run '^$' -fuzz '^FuzzParse$' -fuzztime "${FUZZ_TIME}"
go test ./backend/libs/auth \
  -run '^$' -fuzz '^FuzzExtractBearerToken$' -fuzztime "${FUZZ_TIME}"
go test ./backend/libs/httpproblem \
  -run '^$' -fuzz '^FuzzIsVersionedPath$' -fuzztime "${FUZZ_TIME}"
