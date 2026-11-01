#!/usr/bin/env bash
set -euo pipefail
umask 077

: "${PG_DSN:?PG_DSN is required}"
command -v psql >/dev/null 2>&1 || { echo "psql is required" >&2; exit 1; }

default_tables="workflow_definition workflow_instance process_instance element_instance variable job incident timer message_subscription outbox_message runtime_work_item process_instance_migration process_instance_migration_audit"
read -r -a tables <<< "${FINGERPRINT_TABLES:-${default_tables}}"
tmp="$(mktemp)"
trap 'rm -f "${tmp}"' EXIT

schema="$(psql "${PG_DSN}" -XAt -v ON_ERROR_STOP=1 -c \
  "SELECT COALESCE(jsonb_agg(jsonb_build_object('version',version,'name',name,'checksum',checksum) ORDER BY version),'[]'::jsonb) FROM artificialflow_schema_migrations;")"

for table in "${tables[@]}"; do
  [[ "${table}" =~ ^[a-z_][a-z0-9_]*$ ]] || {
    echo "invalid fingerprint table name: ${table}" >&2
    exit 1
  }
  exists="$(psql "${PG_DSN}" -XAt -v ON_ERROR_STOP=1 -c \
    "SELECT to_regclass('public.${table}') IS NOT NULL;")"
  [[ "${exists}" == "t" ]] || continue
  stats="$(psql "${PG_DSN}" -XAt -v ON_ERROR_STOP=1 -c \
    "SELECT jsonb_build_object(
       'count', count(*),
       'checksum', COALESCE(sum(hashtextextended(row_to_json(t)::text, 0))::text, '0')
     ) FROM public.\"${table}\" AS t;")"
  has_tenant="$(psql "${PG_DSN}" -XAt -v ON_ERROR_STOP=1 -c \
    "SELECT EXISTS (
       SELECT 1 FROM information_schema.columns
       WHERE table_schema='public' AND table_name='${table}' AND column_name='tenant_id'
     );")"
  tenants='{}'
  if [[ "${has_tenant}" == "t" ]]; then
    tenants="$(psql "${PG_DSN}" -XAt -v ON_ERROR_STOP=1 -c \
      "SELECT COALESCE(jsonb_object_agg(tenant_id, count), '{}'::jsonb)
       FROM (SELECT tenant_id, count(*) AS count FROM public.\"${table}\" GROUP BY tenant_id) s;")"
  fi
  printf '%s\t%s\t%s\n' "${table}" "${stats}" "${tenants}" >> "${tmp}"
done

python3 - "${schema}" "${tmp}" <<'PY'
import json
import sys

schema = json.loads(sys.argv[1])
tables = {}
tenants = {}
with open(sys.argv[2], encoding="utf-8") as handle:
    for line in handle:
        name, stats, tenant_counts = line.rstrip("\n").split("\t", 2)
        tables[name] = json.loads(stats)
        tenants[name] = json.loads(tenant_counts)
print(json.dumps({"formatVersion": 1, "schema": schema, "tables": tables, "tenants": tenants},
                 sort_keys=True, separators=(",", ":")))
PY
