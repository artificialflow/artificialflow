#!/usr/bin/env bash
set -euo pipefail

: "${RESTORE_PG_DSN:?RESTORE_PG_DSN is required}"
: "${BACKUP_FILE:?BACKUP_FILE is required}"
[[ "${ALLOW_DESTRUCTIVE_RESTORE:-false}" == "true" ]] || {
  echo "refusing destructive restore; set ALLOW_DESTRUCTIVE_RESTORE=true for an isolated target" >&2
  exit 1
}
[[ -s "${BACKUP_FILE}" ]] || { echo "backup is missing or empty: ${BACKUP_FILE}" >&2; exit 1; }

command -v pg_restore >/dev/null 2>&1 || { echo "pg_restore is required" >&2; exit 1; }
command -v psql >/dev/null 2>&1 || { echo "psql is required" >&2; exit 1; }

if [[ -f "${BACKUP_FILE}.sha256" ]]; then
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$(dirname "${BACKUP_FILE}")" && sha256sum --check "$(basename "${BACKUP_FILE}.sha256")")
  elif command -v shasum >/dev/null 2>&1; then
    expected="$(awk '{print $1}' "${BACKUP_FILE}.sha256")"
    actual="$(shasum -a 256 "${BACKUP_FILE}" | awk '{print $1}')"
    [[ "${actual}" == "${expected}" ]] || { echo "backup checksum mismatch" >&2; exit 1; }
  else
    echo "checksum sidecar exists but no SHA-256 tool is available" >&2
    exit 1
  fi
fi

pg_restore --dbname="${RESTORE_PG_DSN}" --clean --if-exists --no-owner \
  --no-privileges --exit-on-error "${BACKUP_FILE}"
psql "${RESTORE_PG_DSN}" -X -v ON_ERROR_STOP=1 -c "ANALYZE;" >/dev/null
echo "restore completed; run schema validation and dataset/projection parity before traffic"
