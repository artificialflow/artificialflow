#!/usr/bin/env bash
set -euo pipefail
umask 077

: "${PG_DSN:?PG_DSN is required}"
: "${BACKUP_FILE:?BACKUP_FILE is required}"

command -v pg_dump >/dev/null 2>&1 || { echo "pg_dump is required" >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1 || {
  echo "sha256sum or shasum is required" >&2
  exit 1
}

mkdir -p "$(dirname "${BACKUP_FILE}")"
tmp="${BACKUP_FILE}.partial.$$"
trap 'rm -f "${tmp}"' EXIT

pg_dump --dbname="${PG_DSN}" --format=custom --compress=9 --no-owner \
  --no-privileges --file="${tmp}"
pg_restore --list "${tmp}" >/dev/null
mv "${tmp}" "${BACKUP_FILE}"

if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "${BACKUP_FILE}" > "${BACKUP_FILE}.sha256"
else
  shasum -a 256 "${BACKUP_FILE}" > "${BACKUP_FILE}.sha256"
fi

echo "backup created: ${BACKUP_FILE}"
