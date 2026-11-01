#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
mode="${MODE:-}"
started_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
mkdir -p "${root}/reports"

require_command() {
  local name="$1"
  [[ -n "${!name:-}" ]] || { echo "${name} is required" >&2; exit 1; }
}

case "${mode}" in
  backup-restore)
    gate=REL-008
    require_command DATASET_FINGERPRINT_COMMAND
    require_command BACKUP_COMMAND
    require_command RESTORE_COMMAND
    before="${root}/reports/backup-restore-before.json"
    after="${root}/reports/backup-restore-after.json"
    backup="${BACKUP_ARTIFACT:-${root}/reports/release-backup.dump}"
    bash -o pipefail -c "${DATASET_FINGERPRINT_COMMAND}" > "${before}"
    bash -o pipefail -c "${BACKUP_COMMAND}"
    [[ -s "${backup}" ]] || { echo "backup artifact missing or empty: ${backup}" >&2; exit 1; }
    bash -o pipefail -c "${RESTORE_COMMAND}"
    bash -o pipefail -c "${DATASET_FINGERPRINT_COMMAND}" > "${after}"
    python3 - "${before}" "${after}" <<'PY'
import json, sys
before=json.load(open(sys.argv[1], encoding="utf-8"))
after=json.load(open(sys.argv[2], encoding="utf-8"))
required={"schema", "tables", "tenants"}
if not isinstance(before, dict) or not required.issubset(before):
    raise SystemExit("dataset fingerprint must include schema, tables, and per-table tenant counts")
if not before["schema"] or not before["tables"] or not before["tenants"]:
    raise SystemExit("dataset fingerprint cannot be empty")
if before != after:
    raise SystemExit("restored dataset row counts/checksums do not match the source")
PY
    artifacts=("${before}" "${after}" "${backup}")
    ;;
  projection-rebuild)
    gate=REL-009
    require_command PROJECTION_REBUILD_COMMAND
    require_command PROJECTION_PARITY_COMMAND
    rebuild_log="${root}/reports/projection-rebuild.txt"
    parity_log="${root}/reports/projection-parity.txt"
    bash -o pipefail -c "${PROJECTION_REBUILD_COMMAND}" 2>&1 | tee "${rebuild_log}"
    # This command must compare both counts and stable keys against the source
    # of truth; a process-exit check alone is intentionally insufficient.
    INCLUDE_KEY_DIFF=true bash -o pipefail -c "${PROJECTION_PARITY_COMMAND}" \
      2>&1 | tee "${parity_log}"
    artifacts=("${rebuild_log}" "${parity_log}")
    ;;
  *)
    echo "MODE must be backup-restore or projection-rebuild" >&2
    exit 2
    ;;
esac

if [[ "${SKIP_RECORD_EVIDENCE:-}" != "1" ]]; then
  bash "${root}/scripts/release/record_evidence.sh" "${gate}" pass "${started_at}" \
    "${artifacts[@]}"
fi
