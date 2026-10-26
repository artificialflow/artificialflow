#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
for tool in docker go psql pg_dump pg_restore python3; do
  command -v "${tool}" >/dev/null 2>&1 || {
    if [[ "${SKIP_IF_NO_DOCKER:-false}" == "true" ]]; then
      echo "recovery test skipped: ${tool} is unavailable"
      exit 0
    fi
    echo "recovery test requires ${tool}" >&2
    exit 1
  }
done
docker info >/dev/null 2>&1 || {
  if [[ "${SKIP_IF_NO_DOCKER:-false}" == "true" ]]; then
    echo "recovery test skipped: Docker daemon is unavailable"
    exit 0
  fi
  echo "recovery test requires a running Docker daemon" >&2
  exit 1
}

name="artificialflow-recovery-${RANDOM}-$$"
tmp="$(mktemp -d)"
trap 'docker rm -f "${name}" >/dev/null 2>&1 || true; rm -rf "${tmp}"' EXIT
client_major="$(pg_dump --version | awk '{for (i=1; i<=NF; i++) if ($i ~ /^[0-9]+([.][0-9]+)*$/) {print $i; exit}}' | cut -d. -f1)"
[[ -n "${client_major}" ]] || { echo "could not determine pg_dump major version" >&2; exit 1; }
postgres_image="${RECOVERY_POSTGRES_IMAGE:-postgres:${client_major}-alpine}"
docker run --rm -d --name "${name}" -e POSTGRES_PASSWORD=recovery \
  -e POSTGRES_DB=source -p 127.0.0.1::5432 "${postgres_image}" >/dev/null
port="$(docker port "${name}" 5432/tcp | awk -F: '{print $NF}')"
source_dsn="postgres://postgres:recovery@127.0.0.1:${port}/source?sslmode=disable"
admin_dsn="postgres://postgres:recovery@127.0.0.1:${port}/postgres?sslmode=disable"
restore_dsn="postgres://postgres:recovery@127.0.0.1:${port}/restored?sslmode=disable"

for _ in $(seq 1 60); do
  psql "${source_dsn}" -XAt -c "SELECT 1" >/dev/null 2>&1 && break
  sleep 1
done
psql "${source_dsn}" -XAt -v ON_ERROR_STOP=1 -c "SELECT 1" >/dev/null

(cd "${root}" && PG_DSN="${source_dsn}" go run ./backend/services/workflow-command/cmd/migrate)
psql "${source_dsn}" -X -v ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE recovery_fixture (
  tenant_id text NOT NULL,
  stable_key text NOT NULL,
  payload jsonb NOT NULL,
  PRIMARY KEY (tenant_id, stable_key)
);
INSERT INTO recovery_fixture VALUES
  ('tenant-a', 'instance-1', '{"state":"ACTIVE"}'),
  ('tenant-a', 'instance-2', '{"state":"COMPLETED"}'),
  ('tenant-b', 'instance-1', '{"state":"ACTIVE"}');
SQL

PG_DSN="${source_dsn}" FINGERPRINT_TABLES="recovery_fixture" \
  bash "${root}/scripts/recovery/dataset_fingerprint.sh" > "${tmp}/before.json"
PG_DSN="${source_dsn}" BACKUP_FILE="${tmp}/source.dump" \
  bash "${root}/scripts/recovery/postgres_backup.sh"
psql "${admin_dsn}" -X -v ON_ERROR_STOP=1 -c "CREATE DATABASE restored;" >/dev/null
RESTORE_PG_DSN="${restore_dsn}" BACKUP_FILE="${tmp}/source.dump" \
  ALLOW_DESTRUCTIVE_RESTORE=true bash "${root}/scripts/recovery/postgres_restore.sh"
PG_DSN="${restore_dsn}" FINGERPRINT_TABLES="recovery_fixture" \
  bash "${root}/scripts/recovery/dataset_fingerprint.sh" > "${tmp}/after.json"
cmp "${tmp}/before.json" "${tmp}/after.json"
(cd "${root}" && PG_DSN="${restore_dsn}" go run ./backend/services/workflow-command/cmd/migrate validate)

python3 - "${tmp}/after.json" <<'PY'
import json, sys
data=json.load(open(sys.argv[1], encoding="utf-8"))
assert data["tables"]["recovery_fixture"]["count"] == 3
assert data["tenants"]["recovery_fixture"] == {"tenant-a": 2, "tenant-b": 1}
assert len(data["schema"]) >= 2
PY

psql "${restore_dsn}" -X -v ON_ERROR_STOP=1 -c \
  "CREATE TABLE projection_fixture AS SELECT tenant_id, stable_key FROM recovery_fixture;" >/dev/null
source_cmd="psql '${restore_dsn}' -XAt -F \$'\\t' -c 'SELECT tenant_id, stable_key FROM recovery_fixture'"
projection_cmd="psql '${restore_dsn}' -XAt -F \$'\\t' -c 'SELECT tenant_id, stable_key FROM projection_fixture'"
SOURCE_KEYS_COMMAND="${source_cmd}" PROJECTION_KEYS_COMMAND="${projection_cmd}" \
  bash "${root}/scripts/recovery/projection_parity.sh" > "${tmp}/projection-parity.json"

psql "${restore_dsn}" -X -v ON_ERROR_STOP=1 -c \
  "DELETE FROM projection_fixture WHERE tenant_id='tenant-b';" >/dev/null
if SOURCE_KEYS_COMMAND="${source_cmd}" PROJECTION_KEYS_COMMAND="${projection_cmd}" \
  bash "${root}/scripts/recovery/projection_parity.sh" >/dev/null 2>&1; then
  echo "projection parity test did not detect a missing tenant" >&2
  exit 1
fi

echo "disposable recovery test passed"
