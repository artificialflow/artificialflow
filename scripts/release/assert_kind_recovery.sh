#!/usr/bin/env bash
set -euo pipefail

namespace="${KIND_NAMESPACE:-artificialflow-release}"
gateway="$(kubectl -n "${namespace}" get pod \
  -l app.kubernetes.io/component=gateway \
  -o jsonpath='{.items[0].metadata.name}')"
[[ -n "${gateway}" ]] || { echo "gateway pod not found" >&2; exit 1; }

kubectl -n "${namespace}" exec "${gateway}" -- \
  wget -qO- -T 10 http://artificialflow-command:8080/readyz >/dev/null
kubectl -n "${namespace}" exec "${gateway}" -- \
  wget -qO- -T 10 http://artificialflow-query:8081/readyz >/dev/null

bpmn_b64="$(base64 < examples/golden-demo/order-approval.bpmn | tr -d '\n')"
kubectl -n "${namespace}" exec "${gateway}" -- sh -c \
  "printf '%s' '${bpmn_b64}' | base64 -d > /tmp/recovery-assert.bpmn"
kubectl -n "${namespace}" exec "${gateway}" -- \
  wget -qO- -T 20 \
    --header 'Content-Type: application/xml' \
    --post-file /tmp/recovery-assert.bpmn \
    http://artificialflow-command:8080/workflows >/dev/null

instance_json="$(kubectl -n "${namespace}" exec "${gateway}" -- \
  wget -qO- -T 20 \
    --header 'Content-Type: application/json' \
    --post-data "{\"workflow_id\":\"golden_order_approval\",\"context\":{\"evidenceRun\":\"$(date +%s)\"}}" \
    http://artificialflow-command:8080/instances)"
instance_id="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <<<"${instance_json}")"
[[ -n "${instance_id}" ]] || { echo "functional instance id missing" >&2; exit 1; }

kubectl -n "${namespace}" exec "${gateway}" -- \
  wget -qO- -T 10 "http://artificialflow-command:8080/instances/${instance_id}" >/dev/null
projected=false
for _ in {1..30}; do
  if kubectl -n "${namespace}" exec "${gateway}" -- \
    wget -qO- -T 5 "http://artificialflow-query:8081/instances/${instance_id}" >/dev/null 2>&1; then
    projected=true
    break
  fi
  sleep 2
done
[[ "${projected}" == "true" ]] || {
  echo "instance ${instance_id} did not reach the query projection" >&2
  exit 1
}

db_pod="$(kubectl -n "${namespace}" get pod -l app=postgresql -o jsonpath='{.items[0].metadata.name}')"
integrity="$(kubectl -n "${namespace}" exec "${db_pod}" -- \
  psql -U artificialflow -d workflow_db -Atc "
    SELECT
      (SELECT count(*) FROM outbox_message WHERE tenant_id IS NULL OR btrim(tenant_id) = ''),
      (SELECT count(*) FROM (
        SELECT tenant_id, id FROM outbox_message
        GROUP BY tenant_id, id HAVING count(*) > 1
      ) duplicates);")"
[[ "${integrity}" == "0|0" ]] || {
  echo "outbox tenant/deduplication integrity failed: ${integrity}" >&2
  exit 1
}

echo "functional_instance=${instance_id} projection=visible outbox_integrity=pass"
