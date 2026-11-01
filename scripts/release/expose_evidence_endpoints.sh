#!/usr/bin/env bash
# Publish command and query on the evidence host so soak and recovery checks
# can call stable localhost ports after Helm assigns new NodePorts.
set -euo pipefail

namespace="${KIND_NAMESPACE:-artificialflow-release}"
command_listen="${EVIDENCE_COMMAND_PORT:-18080}"
query_listen="${EVIDENCE_QUERY_PORT:-18081}"

kubectl -n "${namespace}" patch svc artificialflow-command --type merge -p '{"spec":{"type":"NodePort"}}'
kubectl -n "${namespace}" patch svc artificialflow-query --type merge -p '{"spec":{"type":"NodePort"}}'
kubectl -n "${namespace}" apply -f - <<'EOF'
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: evidence-nodeport
spec:
  podSelector:
    matchExpressions:
      - key: app.kubernetes.io/component
        operator: In
        values: [command, query]
  policyTypes: [Ingress]
  ingress:
    - ports:
        - {protocol: TCP, port: 8080}
        - {protocol: TCP, port: 8081}
EOF

command_port="$(kubectl -n "${namespace}" get svc artificialflow-command -o jsonpath='{.spec.ports[?(@.port==8080)].nodePort}')"
query_port="$(kubectl -n "${namespace}" get svc artificialflow-query -o jsonpath='{.spec.ports[?(@.port==8081)].nodePort}')"
[[ -n "${command_port}" && -n "${query_port}" ]] || {
  echo "command or query NodePort is missing" >&2
  exit 1
}

mapfile -t nodes < <(docker network inspect kind -f '{{range .Containers}}{{.Name}} {{.IPv4Address}}{{"\n"}}{{end}}' | awk '/worker/ {split($2,a,"/"); print a[1]}')
[[ "${#nodes[@]}" -ge 1 ]] || { echo "no kind workers" >&2; exit 1; }

up() {
  local name="$1" port="$2"
  shift 2
  {
    echo "upstream ${name} {"
    for ip in "$@"; do echo "  server ${ip}:${port};"; done
    echo "}"
    echo "server { listen ${port}; location / { proxy_pass http://${name}; proxy_http_version 1.1; proxy_set_header Connection \"\"; } }"
  }
}

config="$(mktemp)"
{
  up command "${command_port}" "${nodes[@]}"
  up query "${query_port}" "${nodes[@]}"
} > "${config}"

docker rm -f release-soak-lb >/dev/null 2>&1 || true
docker run -d --name release-soak-lb --network kind \
  -p "${command_listen}:${command_port}" -p "${query_listen}:${query_port}" \
  -v "${config}:/etc/nginx/conf.d/default.conf:ro" \
  nginx:1.27-alpine >/dev/null

for _ in $(seq 1 30); do
  if curl -fsS --max-time 3 "http://127.0.0.1:${command_listen}/readyz" >/dev/null \
    && curl -fsS --max-time 3 "http://127.0.0.1:${query_listen}/readyz" >/dev/null; then
    echo "command_nodeport=${command_port} query_nodeport=${query_port}"
    echo "workers=${nodes[*]}"
    exit 0
  fi
  sleep 1
done

echo "evidence load balancer is not ready" >&2
docker logs release-soak-lb | tail
exit 1
