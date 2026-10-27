#!/usr/bin/env bash
# Stop writers, prove backup/restore and projection rebuild, then bring traffic back.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
namespace="${KIND_NAMESPACE:-artificialflow-release}"
cd "${root}"

scale() {
  local replicas="$1"
  kubectl -n "${namespace}" scale sts/artificialflow-command --replicas="${replicas}"
  kubectl -n "${namespace}" scale deploy/artificialflow-runtime --replicas="${replicas}"
  kubectl -n "${namespace}" scale deploy/artificialflow-sync-worker --replicas="${replicas}"
  kubectl -n "${namespace}" scale deploy/artificialflow-query --replicas="${replicas}"
  kubectl -n "${namespace}" scale deploy/artificialflow-connect --replicas="${replicas}"
}

command_replicas="$(kubectl -n "${namespace}" get sts/artificialflow-command -o jsonpath='{.spec.replicas}')"
runtime_replicas="$(kubectl -n "${namespace}" get deploy/artificialflow-runtime -o jsonpath='{.spec.replicas}')"
sync_replicas="$(kubectl -n "${namespace}" get deploy/artificialflow-sync-worker -o jsonpath='{.spec.replicas}')"
query_replicas="$(kubectl -n "${namespace}" get deploy/artificialflow-query -o jsonpath='{.spec.replicas}')"
connect_replicas="$(kubectl -n "${namespace}" get deploy/artificialflow-connect -o jsonpath='{.spec.replicas}')"
pg_forward=""
search_forward=""

restore_writers() {
  kubectl -n "${namespace}" scale sts/artificialflow-command --replicas="${command_replicas}" || true
  kubectl -n "${namespace}" scale deploy/artificialflow-runtime --replicas="${runtime_replicas}" || true
  kubectl -n "${namespace}" scale deploy/artificialflow-sync-worker --replicas="${sync_replicas}" || true
  kubectl -n "${namespace}" scale deploy/artificialflow-query --replicas="${query_replicas}" || true
  kubectl -n "${namespace}" scale deploy/artificialflow-connect --replicas="${connect_replicas}" || true
  if [[ -n "${pg_forward}" ]]; then kill "${pg_forward}" >/dev/null 2>&1 || true; fi
  if [[ -n "${search_forward}" ]]; then kill "${search_forward}" >/dev/null 2>&1 || true; fi
}
trap restore_writers EXIT

scale 0
for component in command runtime sync-worker query connect; do
  kubectl -n "${namespace}" wait --for=delete pod -l "app.kubernetes.io/component=${component}" --timeout=180s
done

password="$(kubectl -n "${namespace}" get secret artificialflow-env -o jsonpath='{.data.POSTGRES_PASSWORD}' | base64 -d)"
export PG_DSN="host=127.0.0.1 user=artificialflow password=${password} dbname=workflow_db port=5432 sslmode=disable"
export RESTORE_PG_DSN="${PG_DSN}"
export ES_ADDR="http://127.0.0.1:9200"
export BACKUP_FILE="${root}/reports/release-backup.dump"
export BACKUP_ARTIFACT="${BACKUP_FILE}"
export ALLOW_DESTRUCTIVE_RESTORE=true
export DATASET_FINGERPRINT_COMMAND="bash scripts/recovery/dataset_fingerprint.sh"
export BACKUP_COMMAND="bash scripts/recovery/postgres_backup.sh"
export RESTORE_COMMAND="bash scripts/recovery/postgres_restore.sh"
export PROJECTION_REBUILD_COMMAND="bash scripts/recovery/projection_rebuild.sh"
export PROJECTION_PARITY_COMMAND="bash scripts/recovery/projection_parity_http.sh"

kubectl -n "${namespace}" port-forward svc/artificialflow-postgresql 5432:5432 >/tmp/evidence-postgres-forward.log 2>&1 &
pg_forward=$!
kubectl -n "${namespace}" port-forward svc/artificialflow-elasticsearch 9200:9200 >/tmp/evidence-search-forward.log 2>&1 &
search_forward=$!
for _ in $(seq 1 30); do
  if (echo >/dev/tcp/127.0.0.1/5432) >/dev/null 2>&1 \
    && (echo >/dev/tcp/127.0.0.1/9200) >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
(echo >/dev/tcp/127.0.0.1/5432) >/dev/null 2>&1 || { echo "postgres port-forward is not ready" >&2; exit 1; }
(echo >/dev/tcp/127.0.0.1/9200) >/dev/null 2>&1 || { echo "elasticsearch port-forward is not ready" >&2; exit 1; }

MODE=backup-restore bash scripts/release/data_recovery_gate.sh
MODE=projection-rebuild bash scripts/release/data_recovery_gate.sh

restore_writers
trap - EXIT
pg_forward=""
search_forward=""
kubectl -n "${namespace}" rollout status sts/artificialflow-command --timeout=10m
kubectl -n "${namespace}" rollout status deploy/artificialflow-runtime --timeout=10m
kubectl -n "${namespace}" rollout status deploy/artificialflow-query --timeout=10m
kubectl -n "${namespace}" rollout status deploy/artificialflow-sync-worker --timeout=10m
kubectl -n "${namespace}" rollout status deploy/artificialflow-connect --timeout=10m
bash scripts/release/expose_evidence_endpoints.sh
