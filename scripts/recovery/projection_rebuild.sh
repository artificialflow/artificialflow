#!/usr/bin/env bash
set -euo pipefail
umask 077

: "${PG_DSN:?PG_DSN is required}"
: "${ES_ADDR:?ES_ADDR is required}"
prefix="${ES_INDEX_PREFIX:-artificialflow}"
tables=(process process_instance element_instance variable job incident timer message_subscription)
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

for table in "${tables[@]}"; do
  id_column="key"
  [[ "${table}" == "variable" ]] && id_column="scope_key"
  document_expression="to_jsonb(t)"
  if [[ "${table}" == "process" ]]; then
    document_expression="jsonb_set(to_jsonb(t), '{resource}', COALESCE(to_jsonb(encode(t.resource, 'base64')), 'null'::jsonb))"
  elif [[ "${table}" == "variable" ]]; then
    document_expression="jsonb_set(to_jsonb(t), '{value}', to_jsonb(t.value::text))"
  fi
  index="${prefix}-${table}"
  bulk="${tmp}/${table}.ndjson"

  psql "${PG_DSN}" -XAt -v ON_ERROR_STOP=1 \
    -c "SELECT
      json_build_object('index', json_build_object(
        '_index', '${index}',
        '_id', to_jsonb(t)->>'${id_column}'
      ))::text || E'\n' || (${document_expression})::text
    FROM public.\"${table}\" AS t
    ORDER BY (to_jsonb(t)->>'${id_column}');" > "${bulk}"

  status="$(curl -sS -o "${tmp}/${table}-delete.json" -w '%{http_code}' \
    -X DELETE "${ES_ADDR}/${index}")"
  [[ "${status}" == "200" || "${status}" == "404" ]] || {
    echo "failed to reset ${index}: HTTP ${status}" >&2
    exit 1
  }
  if [[ -s "${bulk}" ]]; then
    chunk_prefix="${tmp}/${table}-bulk-"
    # Keep each request bounded for production-size datasets and retry
    # transient search-node backpressure. Two NDJSON lines form one document.
    split -l 2000 "${bulk}" "${chunk_prefix}"
    chunk_number=0
    for chunk in "${chunk_prefix}"*; do
      chunk_number=$((chunk_number + 1))
      response="${tmp}/${table}-bulk-${chunk_number}.json"
      curl -fsS --retry 5 --retry-all-errors --retry-delay 2 \
        -H 'Content-Type: application/x-ndjson' \
        --data-binary "@${chunk}" "${ES_ADDR}/_bulk?refresh=wait_for" \
        > "${response}"
      python3 - "${response}" "${index}" "${chunk_number}" <<'PY'
import json, sys
payload = json.load(open(sys.argv[1], encoding="utf-8"))
if payload.get("errors"):
    failures = [
        item for entry in payload.get("items", [])
        for item in entry.values() if item.get("status", 500) >= 300
    ]
    raise SystemExit(
        f"{sys.argv[2]} bulk rebuild chunk {sys.argv[3]} failed: {failures[:3]}"
    )
PY
    done
  else
    curl -fsS -X PUT "${ES_ADDR}/${index}" > "${tmp}/${table}-create.json"
  fi
  echo "rebuilt ${index}"
done
