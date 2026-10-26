#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cluster="${KIND_CLUSTER_NAME:-artificialflow-release}"
namespace="${KIND_NAMESPACE:-artificialflow-release}"
lock="${CANDIDATE_IMAGE_LOCK:-${root}/reports/candidate-images.json}"
values="${KIND_VALUES_FILE:-${root}/charts/artificialflow/values-kind-smoke.yaml}"
kind_node_image="${KIND_NODE_IMAGE:-kindest/node:v1.33.1@sha256:050072256b9a903bd914c0b2866828150cb229cea0efe5892e2b644d5dd3b34f}"
worker_count="${KIND_WORKER_COUNT:-0}"
calico_version="${CALICO_VERSION:-v3.30.2}"
calico_sha256="${CALICO_SHA256:-76f763d9d6ae4d3f13552a67cf805883831e5a6a40f1a0cbf3693798d90a0854}"
started_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
report="${root}/reports/kind-helm-smoke.txt"
generated_values="$(mktemp)"
cni_manifest="$(mktemp)"
pods_manifest="$(mktemp)"
cluster_config="$(mktemp)"
cleanup() {
  rm -f "${generated_values}" "${cni_manifest}" "${pods_manifest}" "${cluster_config}"
  if [[ "${KEEP_KIND_CLUSTER:-false}" != "true" ]]; then kind delete cluster --name "${cluster}" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT

for tool in docker kind kubectl helm python3; do
  command -v "${tool}" >/dev/null || { echo "required tool is missing: ${tool}" >&2; exit 1; }
done
[[ -f "${lock}" ]] || { echo "candidate image lock required: ${lock}" >&2; exit 1; }
[[ -f "${values}" ]] || { echo "Helm values file required: ${values}" >&2; exit 1; }
[[ "${worker_count}" =~ ^[0-9]+$ ]] || {
  echo "KIND_WORKER_COUNT must be a non-negative integer" >&2
  exit 1
}

bash "${root}/scripts/release/verify_candidate_images.sh"
python3 - "${lock}" "${generated_values}" <<'PY'
import json, sys
data=json.load(open(sys.argv[1], encoding="utf-8"))
keys={"workflow-command":"command","workflow-runtime":"runtime","workflow-query":"query",
      "sync-worker":"syncWorker","frontend":"frontend"}
with open(sys.argv[2], "w", encoding="utf-8") as out:
    out.write("images:\n")
    for image in data["images"]:
        out.write(f"  {keys[image['name']]}:\n")
        out.write(f"    repository: {image['repository']}\n")
        out.write(f"    digest: {image['digest']}\n")
        out.write("    pullPolicy: IfNotPresent\n")
    dependencies={image["name"]: image for image in data["dependencies"]}
    gateway=dependencies["gateway"]
    out.write("  gateway:\n")
    out.write(f"    repository: {gateway['repository']}\n")
    out.write(f"    digest: {gateway['digest']}\n")
    out.write("    pullPolicy: IfNotPresent\n")
    def write_bundled(section, name):
        image=dependencies[name]
        out.write(f"{section}\n")
        out.write("    image:\n")
        out.write(f"      repository: {image['repository']}\n")
        out.write(f"      digest: {image['digest']}\n")
        out.write("      pullPolicy: IfNotPresent\n")
    write_bundled("postgresql:\n  bundled:", "postgresql")
    write_bundled("search:\n  bundled:", "elasticsearch")
    write_bundled("kafka:\n  bundled:", "kafka")
    write_bundled("connect:\n  bundled:", "debezium-connect")
PY

cat <<EOF > "${cluster_config}"
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  disableDefaultCNI: true
EOF
if [[ -n "${KIND_REGISTRY_MIRROR:-}" ]]; then
cat <<EOF >> "${cluster_config}"
containerdConfigPatches:
  - |-
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."localhost:5001"]
      endpoint = ["${KIND_REGISTRY_MIRROR}"]
EOF
fi
cat <<EOF >> "${cluster_config}"
nodes:
  - role: control-plane
EOF
for ((worker = 0; worker < worker_count; worker++)); do
  echo "  - role: worker" >> "${cluster_config}"
done

kind create cluster --name "${cluster}" --image "${kind_node_image}" \
  --config="${cluster_config}"

if [[ -n "${KIND_REGISTRY_MIRROR:-}" ]]; then
  if [[ -n "${KIND_REGISTRY_CONTAINER:-}" ]]; then
    mirror_host="$(python3 - "${KIND_REGISTRY_MIRROR}" <<'PY'
import sys
from urllib.parse import urlparse

host = urlparse(sys.argv[1]).hostname
if not host:
    raise SystemExit("KIND_REGISTRY_MIRROR must include a hostname")
print(host)
PY
)"
    docker inspect "${KIND_REGISTRY_CONTAINER}" >/dev/null
    if ! docker inspect "${KIND_REGISTRY_CONTAINER}" \
      --format '{{json .NetworkSettings.Networks}}' | rg -q '"kind"'; then
      docker network connect --alias "${mirror_host}" kind "${KIND_REGISTRY_CONTAINER}"
    fi
    docker exec "${cluster}-control-plane" getent hosts "${mirror_host}" >/dev/null || {
      echo "Kind node cannot resolve registry mirror ${mirror_host}" >&2
      exit 1
    }
  fi
fi

curl -fsSL "https://raw.githubusercontent.com/projectcalico/calico/${calico_version}/manifests/calico.yaml" \
  -o "${cni_manifest}"
echo "${calico_sha256}  ${cni_manifest}" | shasum -a 256 -c -
kubectl apply -f "${cni_manifest}"
kubectl -n kube-system rollout status daemonset/calico-node --timeout=10m
kubectl create namespace "${namespace}"
if [[ -n "${KIND_BOOTSTRAP_COMMAND:-}" ]]; then
  KIND_NAMESPACE="${namespace}" bash -o pipefail -c "${KIND_BOOTSTRAP_COMMAND}"
fi

# Prove that the CNI enforces a default-deny policy before testing the chart.
kubectl -n "${namespace}" create deployment policy-server --image=registry.k8s.io/e2e-test-images/agnhost:2.53 -- /agnhost netexec --http-port=8080
kubectl -n "${namespace}" expose deployment policy-server --port=8080
kubectl -n "${namespace}" rollout status deployment/policy-server --timeout=5m
kubectl -n "${namespace}" run policy-client --image=curlimages/curl:8.14.1 --restart=Never --command -- sleep 600
kubectl -n "${namespace}" wait --for=condition=Ready pod/policy-client --timeout=3m
kubectl -n "${namespace}" exec policy-client -- curl -fsS --max-time 10 http://policy-server:8080/hostname >/dev/null
cat <<EOF | kubectl -n "${namespace}" apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny-ingress
spec:
  podSelector: {}
  policyTypes: [Ingress]
EOF
policy_enforced=false
for _ in {1..12}; do
  if ! kubectl -n "${namespace}" exec policy-client -- \
    curl -fsS --max-time 2 http://policy-server:8080/hostname >/dev/null 2>&1; then
    policy_enforced=true
    break
  fi
  sleep 2
done
[[ "${policy_enforced}" == "true" ]] || {
  echo "NetworkPolicy was not enforced by the CNI" >&2
  exit 1
}
kubectl -n "${namespace}" delete deployment/policy-server service/policy-server pod/policy-client networkpolicy/default-deny-ingress

{
  echo "candidate lock: $(shasum -a 256 "${lock}" | awk '{print $1}')"
  echo "kind node: ${kind_node_image}"
  echo "calico: ${calico_version}"
  helm upgrade --install artificialflow "${root}/charts/artificialflow" \
    --namespace "${namespace}" -f "${values}" -f "${generated_values}" \
    --set-string global.podAnnotations.releaseEvidenceRun=install --wait --timeout 20m
  helm upgrade artificialflow "${root}/charts/artificialflow" \
    --namespace "${namespace}" -f "${values}" -f "${generated_values}" \
    --set-string global.podAnnotations.releaseEvidenceRun=upgrade --wait --timeout 20m
  helm rollback artificialflow 1 --namespace "${namespace}" --wait --timeout 20m
  kubectl -n "${namespace}" wait --for=condition=Available deployment --all --timeout=10m
  kubectl -n "${namespace}" get pods -o json > "${pods_manifest}"
  python3 - "${lock}" "${pods_manifest}" <<'PY'
import json, re, sys
lock=json.load(open(sys.argv[1], encoding="utf-8"))
pods=json.load(open(sys.argv[2], encoding="utf-8"))
expected={
    image["name"]: f"{image['repository']}@{image['digest']}"
    for image in lock["images"] + lock["dependencies"]
}
seen=set()
for pod in pods["items"]:
    for container in pod["spec"].get("initContainers", []) + pod["spec"].get("containers", []):
        image=container["image"]
        if "@sha256:" not in image:
            raise SystemExit(f"mutable deployed image reference: {image}")
        if image not in expected.values():
            raise SystemExit(f"deployed image is absent from candidate lock: {image}")
        seen.add(image)
required={expected[name] for name in (
    "workflow-command", "workflow-runtime", "workflow-query", "sync-worker",
    "frontend", "gateway",
)}
missing=required-seen
if missing:
    raise SystemExit(f"locked release images were not deployed: {sorted(missing)}")
print(f"verified {len(seen)} deployed digest references against candidate lock")
PY
  kubectl -n "${namespace}" get pods -o wide
} 2>&1 | tee "${report}"

bash "${root}/scripts/release/record_evidence.sh" REL-003 pass "${started_at}" \
  "${lock}" "${report}"
