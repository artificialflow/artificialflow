#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
namespace="${KIND_NAMESPACE:-artificialflow-release}"
scenario="${CHAOS_SCENARIO:-}"
assert_command="${CHAOS_ASSERT_COMMAND:-}"
started_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
disruption_started_epoch="$(date +%s)"
mkdir -p "${root}/reports"
disrupted_resource=""
original_replicas=""
restore_resource() {
  if [[ -n "${disrupted_resource}" && -n "${original_replicas}" ]]; then
    kubectl -n "${namespace}" scale "${disrupted_resource}" \
      --replicas="${original_replicas}" >/dev/null 2>&1 || true
  fi
}
trap restore_resource EXIT INT TERM

[[ -n "${assert_command}" ]] || {
  echo "CHAOS_ASSERT_COMMAND is required and must prove recovery and data integrity" >&2
  exit 1
}

case "${scenario}" in
  pod-kill)
    gate=REL-004
    max_recovery_seconds=300
    selector="${POD_KILL_SELECTOR:-app.kubernetes.io/component=runtime}"
    victim="$(kubectl -n "${namespace}" get pod -l "${selector}" -o jsonpath='{.items[0].metadata.name}')"
    [[ -n "${victim}" ]] || { echo "no pod matched ${selector}" >&2; exit 1; }
    kubectl -n "${namespace}" delete pod "${victim}" --wait=false
    ;;
  kafka)
    gate=REL-005
    max_recovery_seconds=600
    resource="${KAFKA_RESOURCE:-statefulset/artificialflow-kafka}"
    replicas="$(kubectl -n "${namespace}" get "${resource}" -o jsonpath='{.spec.replicas}')"
    disrupted_resource="${resource}"
    original_replicas="${replicas}"
    kubectl -n "${namespace}" scale "${resource}" --replicas=0
    sleep "${CHAOS_OUTAGE_SECONDS:-30}"
    kubectl -n "${namespace}" scale "${resource}" --replicas="${replicas}"
    disrupted_resource=""
    ;;
  sync-outage)
    gate=REL-006
    max_recovery_seconds=600
    resource="${SYNC_RESOURCE:-deployment/artificialflow-sync-worker}"
    replicas="$(kubectl -n "${namespace}" get "${resource}" -o jsonpath='{.spec.replicas}')"
    disrupted_resource="${resource}"
    original_replicas="${replicas}"
    kubectl -n "${namespace}" scale "${resource}" --replicas=0
    sleep "${CHAOS_OUTAGE_SECONDS:-60}"
    kubectl -n "${namespace}" scale "${resource}" --replicas="${replicas}"
    disrupted_resource=""
    ;;
  database)
    gate=REL-013
    max_recovery_seconds=600
    resource="${DATABASE_RESOURCE:-statefulset/artificialflow-postgresql}"
    replicas="$(kubectl -n "${namespace}" get "${resource}" -o jsonpath='{.spec.replicas}')"
    disrupted_resource="${resource}"
    original_replicas="${replicas}"
    kubectl -n "${namespace}" scale "${resource}" --replicas=0
    sleep "${CHAOS_OUTAGE_SECONDS:-30}"
    kubectl -n "${namespace}" scale "${resource}" --replicas="${replicas}"
    disrupted_resource=""
    ;;
  *)
    echo "CHAOS_SCENARIO must be pod-kill, kafka, sync-outage, or database" >&2
    exit 2
    ;;
esac

log="${root}/reports/${gate}-chaos.txt"
{
  if [[ -n "${resource:-}" ]]; then
    kubectl -n "${namespace}" rollout status "${resource}" \
      --timeout="${CHAOS_RECOVERY_TIMEOUT:-10m}"
  fi
  kubectl -n "${namespace}" wait --for=condition=Available deployment --all \
    --timeout="${CHAOS_RECOVERY_TIMEOUT:-10m}"
  # The caller supplies a release-specific assertion (functional transaction
  # plus parity) so infrastructure recovery alone can never produce a pass.
  bash -o pipefail -c "${assert_command}"
  recovery_seconds="$(( $(date +%s) - disruption_started_epoch ))"
  [[ "${recovery_seconds}" -le "${max_recovery_seconds}" ]] || {
    echo "recovery exceeded ${max_recovery_seconds}s: ${recovery_seconds}s" >&2
    exit 1
  }
  echo "recovery_seconds=${recovery_seconds} threshold_seconds=${max_recovery_seconds}"
  kubectl -n "${namespace}" get pods -o wide
} 2>&1 | tee "${log}"

if [[ "${SKIP_RECORD_EVIDENCE:-}" != "1" ]]; then
  bash "${root}/scripts/release/record_evidence.sh" "${gate}" pass "${started_at}" "${log}"
fi
