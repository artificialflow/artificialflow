#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

command -v docker >/dev/null
command -v go >/dev/null

name="artificialflow-schema-migration-test-$$"
port="${SCHEMA_MIGRATION_TEST_PORT:-55439}"
password="migration-test-password"
binary="/tmp/artificialflow-workflow-migrate-$$"
dump_file="/tmp/artificialflow-schema-migration-$$.dump"
before_file="/tmp/artificialflow-schema-before-$$.txt"
after_file="/tmp/artificialflow-schema-after-$$.txt"
restore_file="/tmp/artificialflow-schema-restore-$$.txt"

cleanup() {
  docker rm -f "${name}" >/dev/null 2>&1 || true
  rm -f "${binary}" "${dump_file}" "${before_file}" "${after_file}" "${restore_file}" \
    "/tmp/artificialflow-migrator-one-$$.log" "/tmp/artificialflow-migrator-two-$$.log" \
    "/tmp/artificialflow-migration-lock-$$.log"
}
trap cleanup EXIT

go build -o "${binary}" ./backend/services/workflow-command/cmd/migrate

docker run -d --name "${name}" \
  -e POSTGRES_USER=artificialflow \
  -e POSTGRES_PASSWORD="${password}" \
  -e POSTGRES_DB=workflow_db \
  -p "127.0.0.1:${port}:5432" \
  postgres:15.19-alpine >/dev/null

for _ in $(seq 1 30); do
  if docker exec "${name}" pg_isready -U artificialflow -d workflow_db >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker exec "${name}" pg_isready -U artificialflow -d workflow_db >/dev/null

dsn() {
  printf 'host=127.0.0.1 user=artificialflow password=%s dbname=%s port=%s sslmode=disable TimeZone=UTC' \
    "${password}" "$1" "${port}"
}

run_migrate() {
  local database="$1"
  shift
  PG_DSN="$(dsn "${database}")" "${binary}" "$@"
}

expect_failure() {
  local expected="$1"
  shift
  local output
  if output="$("$@" 2>&1)"; then
    echo "expected command to fail: $*" >&2
    exit 1
  fi
  if [[ "${output}" != *"${expected}"* ]]; then
    echo "failure did not contain ${expected}: ${output}" >&2
    exit 1
  fi
}

snapshot() {
  local database="$1"
  local output="$2"
  docker exec -i "${name}" psql -U artificialflow -d "${database}" -v ON_ERROR_STOP=1 -At >"${output}" <<'SQL'
SELECT table_name || '|' || row_count || '|' || key_state || '|' || payload_hash
FROM (
  SELECT 'decision_definition' AS table_name, count(*)::text AS row_count,
    coalesce(string_agg(key::text || ':' || decision_id || ':' || version::text, ',' ORDER BY key), '') AS key_state,
    coalesce(md5(string_agg(encode(resource, 'hex'), ',' ORDER BY key)), '') AS payload_hash
  FROM decision_definition
  UNION ALL
  SELECT 'idempotency_record', count(*)::text,
    coalesce(string_agg(key || ':' || operation, ',' ORDER BY key, operation), ''), ''
  FROM idempotency_record
  UNION ALL
  SELECT 'job', count(*)::text,
    coalesce(string_agg(key::text || ':' || state || ':' || worker || ':' ||
      coalesce(lock_expiration_time::text, ''), ',' ORDER BY key), ''), ''
  FROM job
  UNION ALL
  SELECT 'message_subscription', count(*)::text,
    coalesce(string_agg(key::text || ':' || state || ':' || correlation_key, ',' ORDER BY key), ''), ''
  FROM message_subscription
  UNION ALL
  SELECT 'outbox_message', count(*)::text,
    coalesce(string_agg(id || ':' || status || ':' || attempts::text, ',' ORDER BY id), ''),
    coalesce(md5(string_agg(id || ':' || encode(payload, 'hex'), ',' ORDER BY id)), '')
  FROM outbox_message
  UNION ALL
  SELECT 'process', count(*)::text,
    coalesce(string_agg(key::text || ':' || bpmn_process_id || ':' || version::text, ',' ORDER BY key), ''),
    coalesce(md5(string_agg(encode(resource, 'hex'), ',' ORDER BY key)), '')
  FROM process
  UNION ALL
  SELECT 'process_instance', count(*)::text,
    coalesce(string_agg(key::text || ':' || state, ',' ORDER BY key), ''), ''
  FROM process_instance
  UNION ALL
  SELECT 'timer', count(*)::text,
    coalesce(string_agg(key::text || ':' || state || ':' || due_date::text, ',' ORDER BY key), ''), ''
  FROM timer
  UNION ALL
  SELECT 'variable', count(*)::text,
    coalesce(string_agg(scope_key::text || ':' || process_instance_key::text, ',' ORDER BY scope_key), ''),
    coalesce(md5(string_agg(value::text, ',' ORDER BY scope_key)), '')
  FROM variable
) evidence
ORDER BY table_name;
SQL
}

echo "unmigrated validation rejection"
expect_failure "schema is not initialized" run_migrate workflow_db validate

echo "concurrent fresh-install migration serialization"
docker exec "${name}" psql -U artificialflow -d workflow_db -v ON_ERROR_STOP=1 \
  -c "SELECT pg_advisory_lock(X'4152464C4F575341'::bit(64)::bigint); SELECT pg_sleep(5); SELECT pg_advisory_unlock(X'4152464C4F575341'::bit(64)::bigint);" \
  >"/tmp/artificialflow-migration-lock-$$.log" 2>&1 &
lock_pid=$!
for _ in $(seq 1 50); do
  held="$(docker exec "${name}" psql -U artificialflow -d workflow_db -Atc \
    "SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND granted")"
  [[ "${held}" -ge 1 ]] && break
  sleep 0.1
done

PG_DSN="$(dsn workflow_db)" "${binary}" >"/tmp/artificialflow-migrator-one-$$.log" 2>&1 &
migrator_one=$!
PG_DSN="$(dsn workflow_db)" "${binary}" >"/tmp/artificialflow-migrator-two-$$.log" 2>&1 &
migrator_two=$!

sleep 2
ledger="$(docker exec "${name}" psql -U artificialflow -d workflow_db -Atc \
  "SELECT to_regclass('artificialflow_schema_migrations') IS NULL")"
if ! kill -0 "${migrator_one}" 2>/dev/null || ! kill -0 "${migrator_two}" 2>/dev/null || [[ "${ledger}" != "t" ]]; then
  echo "concurrent migrators did not wait for the advisory lock" >&2
  exit 1
fi
wait "${lock_pid}"
wait "${migrator_one}"
wait "${migrator_two}"
ledger_count="$(docker exec "${name}" psql -U artificialflow -d workflow_db -Atc \
  "SELECT count(*) FROM artificialflow_schema_migrations")"
expected_count="$(find backend/services/workflow-command/internal/infrastructure/migration/sql -name '*.sql' | wc -l | tr -d ' ')"
[[ "${ledger_count}" == "${expected_count}" ]]
run_migrate workflow_db validate
echo "three-owner PostgreSQL runtime fencing"
PG_DSN="$(dsn workflow_db)" go test \
  ./backend/services/workflow-command/internal/infrastructure/persistence \
  -run '^TestPostgresThreeOwnerRuntimeClaimsAndDelayedFence$' -count=1

echo "GORM model and migrated schema drift"
PG_DSN="$(dsn workflow_db)" go test \
  ./backend/services/workflow-command/internal/infrastructure/migration \
  -run '^Test(RequiredSchema|MigratedSchema)MatchesModels$' -count=1

echo "partial legacy schema adoption rejection"
docker exec "${name}" createdb -U artificialflow workflow_partial
docker exec "${name}" psql -U artificialflow -d workflow_partial -v ON_ERROR_STOP=1 \
  -c "CREATE TABLE process (key text PRIMARY KEY, bpmn_process_id text)" >/dev/null
expect_failure "refusing legacy schema adoption" run_migrate workflow_partial

echo "unknown legacy schema adoption rejection"
docker exec "${name}" createdb -U artificialflow workflow_unknown
docker exec "${name}" psql -U artificialflow -d workflow_unknown -v ON_ERROR_STOP=1 \
  -c "CREATE TABLE workflow_instances (id bigint PRIMARY KEY)" >/dev/null
expect_failure "unknown existing schema tables" run_migrate workflow_unknown

echo "rich v1.1.0 adoption and data invariants"
docker exec "${name}" dropdb -U artificialflow --force workflow_db
docker exec "${name}" createdb -U artificialflow workflow_db
docker exec -i "${name}" psql -U artificialflow -d workflow_db -v ON_ERROR_STOP=1 \
  <backend/services/workflow-command/internal/infrastructure/migration/sql/0001_v1_1_0_baseline.sql >/dev/null
docker exec -i "${name}" psql -U artificialflow -d workflow_db -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
INSERT INTO process VALUES
  (101, 'migration-evidence', 3, 'evidence.bpmn', 201, decode('3c62706d6e2f3e', 'hex'), 'process-sha', 'tenant-a', '2026-01-01T00:00:00Z');
INSERT INTO process_instance VALUES
  (1001, 'instance-active', 101, 3, 0, 0, 'ACTIVE', '2026-01-01T00:01:00Z', NULL);
INSERT INTO element_instance VALUES
  (2001, 'element-active', 1001, 101, 'service-task', 'SERVICE_TASK', 1001, 'ACTIVATED', '2026-01-01T00:02:00Z', NULL);
INSERT INTO variable VALUES
  (1001, 1001, 'context', '{"amount":42,"customer":"stable"}', '2026-01-01T00:03:00Z', '2026-01-01T00:03:00Z');
INSERT INTO job VALUES
  (3001, 'job-locked', 'payment', 1001, 2001, 101, 'service-task', 'worker-a', 3, 'ACTIVATED',
   '', '', '', '2030-01-01T00:00:00Z', '2026-01-02T00:00:00Z', NULL, '2026-01-01T00:04:00Z', '2026-01-01T00:05:00Z');
INSERT INTO incident VALUES
  (3501, 'incident-open', 1001, 2001, 3001, 'JOB_ERROR', 'preserved', 'CREATED', '2026-01-01T00:06:00Z', NULL);
INSERT INTO timer VALUES
  (4001, 'timer-open', 2001, 1001, 'boundary-timer', '2030-02-01T00:00:00Z', 'CREATED', 2, '2026-01-01T00:07:00Z');
INSERT INTO message_subscription VALUES
  (5001, 'subscription-open', 2001, 1001, 'message-catch', 'payment-received', 'order-42', 'OPEN', '2026-01-01T00:08:00Z');
INSERT INTO idempotency_record VALUES
  ('idem-1', 'complete-job', '2026-01-01T00:09:00Z');
INSERT INTO outbox_message VALUES
  ('outbox-pending', 'JobCreated', decode('70656e64696e67', 'hex'), 'PENDING', 0, '', '2026-01-01T00:10:00Z', NULL, '2026-01-01T00:10:00Z', NULL),
  ('outbox-processing', 'JobActivated', decode('70726f63657373696e67', 'hex'), 'PROCESSING', 1, '', '2026-01-01T00:11:00Z', '2026-01-01T00:11:00Z', '2026-01-01T00:11:00Z', NULL),
  ('outbox-published', 'JobCompleted', decode('7075626c6973686564', 'hex'), 'PUBLISHED', 1, '', NULL, NULL, '2026-01-01T00:12:00Z', '2026-01-01T00:13:00Z');
INSERT INTO runtime_scheduler_lease VALUES
  ('runtime-scheduler', 'runtime-a', '2030-01-01T00:00:00Z', '2026-01-01T00:14:00Z');
INSERT INTO decision_definition VALUES
  (6001, 'risk-decision', 'Risk Decision', 4, decode('7b2272756c6573223a5b5d7d', 'hex'), '2026-01-01T00:15:00Z', '2026-01-01T00:15:00Z');
SQL

snapshot workflow_db "${before_file}"
run_migrate workflow_db
run_migrate workflow_db validate
run_migrate workflow_db
docker exec "${name}" psql -U artificialflow -d workflow_db -c "ANALYZE" >/dev/null
explain_evidence="$(docker exec -i "${name}" psql -U artificialflow -d workflow_db -v ON_ERROR_STOP=1 -At \
  <scripts/explain_runtime_hot_paths.sql)"
for expected_index in idx_timer_claim_hot idx_job_sla_claim_hot idx_message_correlation_hot idx_element_active_scan idx_outbox_pending_created; do
  [[ "${explain_evidence}" == *"${expected_index}"* ]] || {
    echo "EXPLAIN evidence did not use ${expected_index}" >&2
    exit 1
  }
done
snapshot workflow_db "${after_file}"
diff -u "${before_file}" "${after_file}"

echo "checksum tamper rejection"
checksum="$(docker exec "${name}" psql -U artificialflow -d workflow_db -Atc \
  "SELECT trim(checksum) FROM artificialflow_schema_migrations WHERE version = 1")"
docker exec "${name}" psql -U artificialflow -d workflow_db -v ON_ERROR_STOP=1 \
  -c "UPDATE artificialflow_schema_migrations SET checksum = repeat('0', 64) WHERE version = 1" >/dev/null
expect_failure "checksum drift" run_migrate workflow_db validate
docker exec "${name}" psql -U artificialflow -d workflow_db -v ON_ERROR_STOP=1 \
  -c "UPDATE artificialflow_schema_migrations SET checksum = '${checksum}' WHERE version = 1" >/dev/null

echo "expand-compatible N-1 validation"
docker exec "${name}" psql -U artificialflow -d workflow_db -v ON_ERROR_STOP=1 \
  -c "ALTER TABLE job ADD COLUMN future_result jsonb" \
  -c "INSERT INTO artificialflow_schema_migrations (version, name, checksum) VALUES ($((expected_count + 1)), 'future_expand', repeat('f', 64))" >/dev/null
run_migrate workflow_db validate
snapshot workflow_db "${after_file}"
diff -u "${before_file}" "${after_file}"

echo "pg_dump and pg_restore round trip"
docker exec "${name}" pg_dump -U artificialflow -d workflow_db -Fc >"${dump_file}"
docker exec "${name}" createdb -U artificialflow workflow_restore
docker exec -i "${name}" pg_restore -U artificialflow -d workflow_restore --no-owner --no-privileges <"${dump_file}"
run_migrate workflow_restore validate
snapshot workflow_restore "${restore_file}"
diff -u "${before_file}" "${restore_file}"

# The legacy application-visible columns and singular captured table names
# remain readable after adoption and restore.
legacy_row="$(docker exec "${name}" psql -U artificialflow -d workflow_restore -Atc \
  "SELECT process.bpmn_process_id || '|' || process_instance.state || '|' || job.worker FROM process, process_instance, job WHERE process.key = 101 AND process_instance.key = 1001 AND job.key = 3001")"
[[ "${legacy_row}" == "migration-evidence|ACTIVE|worker-a" ]]
captured_tables="$(docker exec "${name}" psql -U artificialflow -d workflow_restore -Atc \
  "SELECT to_regclass('public.process_instance') || '|' || to_regclass('public.message_subscription')")"
[[ "${captured_tables}" == "process_instance|message_subscription" ]]

echo "schema migration evidence passed"
