#!/usr/bin/env bash
set -euo pipefail

: "${PG_DSN:?PG_DSN is required}"
: "${ES_ADDR:?ES_ADDR is required}"
prefix="${ES_INDEX_PREFIX:-artificialflow}"
tables=(process process_instance element_instance variable job incident timer message_subscription)
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

for table in "${tables[@]}"; do
  id_column="key"
  [[ "${table}" == "variable" ]] && id_column="scope_key"
  index="${prefix}-${table}"
  pg_keys="${tmp}/${table}-pg.tsv"
  es_keys="${tmp}/${table}-es.tsv"
  es_response="${tmp}/${table}-search.json"

  psql "${PG_DSN}" -XAt -v ON_ERROR_STOP=1 \
    -c "SELECT tenant_id || E'\t' || \"${id_column}\"::text
        FROM public.\"${table}\" ORDER BY tenant_id, \"${id_column}\";" > "${pg_keys}"
  : > "${es_keys}"
  curl -fsS -H 'Content-Type: application/json' \
    -d "{\"size\":1000,\"_source\":[\"tenant_id\",\"${id_column}\"]}" \
    "${ES_ADDR}/${index}/_search?scroll=2m" > "${es_response}"
  scroll_id=""
  while true; do
    metadata="$(python3 - "${es_response}" "${es_keys}" "${id_column}" <<'PY'
import json, sys
payload = json.load(open(sys.argv[1], encoding="utf-8"))
hits = payload.get("hits", {}).get("hits", [])
id_column = sys.argv[3]
with open(sys.argv[2], "a", encoding="utf-8") as output:
  for hit in hits:
    source = hit.get("_source", {})
    output.write(f"{source.get('tenant_id', 'default')}\t{source[id_column]}\n")
print(f"{len(hits)} {payload.get('_scroll_id', '')}")
PY
)"
    page_count="${metadata%% *}"
    scroll_id="${metadata#* }"
    [[ "${page_count}" -eq 1000 ]] || break
    scroll_payload="$(python3 - "${scroll_id}" <<'PY'
import json, sys
print(json.dumps({"scroll": "2m", "scroll_id": sys.argv[1]}))
PY
)"
    curl -fsS -H 'Content-Type: application/json' \
      -d "${scroll_payload}" "${ES_ADDR}/_search/scroll" > "${es_response}"
  done
  if [[ -n "${scroll_id}" ]]; then
    clear_payload="$(python3 - "${scroll_id}" <<'PY'
import json, sys
print(json.dumps({"scroll_id": [sys.argv[1]]}))
PY
)"
    curl -fsS -X DELETE -H 'Content-Type: application/json' \
      -d "${clear_payload}" "${ES_ADDR}/_search/scroll" >/dev/null || true
  fi

  LC_ALL=C sort -u "${pg_keys}" -o "${pg_keys}"
  LC_ALL=C sort -u "${es_keys}" -o "${es_keys}"
  if ! diff -u "${pg_keys}" "${es_keys}"; then
    echo "projection parity failed for ${table}" >&2
    exit 1
  fi
  echo "${table}: $(wc -l < "${pg_keys}" | tr -d ' ') keys match"
done
