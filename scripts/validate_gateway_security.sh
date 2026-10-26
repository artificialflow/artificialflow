#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG="${ROOT}/gateway/nginx.conf"

require() {
  local pattern="$1"
  if ! rg -q -- "${pattern}" "${CONFIG}"; then
    echo "gateway security validation missing: ${pattern}" >&2
    exit 1
  fi
}

require 'map \$http_origin \$cors_origin'
require 'add_header Vary "Origin" always'
require 'X-Content-Type-Options'
require 'Content-Security-Policy'
require 'client_max_body_size'
require 'proxy_connect_timeout'
require 'limit_req zone='
require 'X-Forwarded-For \$remote_addr'

if rg -q "Access-Control-Allow-Origin['\" ]+\\*" "${CONFIG}"; then
  echo "wildcard gateway CORS is forbidden" >&2
  exit 1
fi
if rg -q "Strict-Transport-Security" "${CONFIG}"; then
  echo "development HTTP gateway must not emit HSTS" >&2
  exit 1
fi

require "connect-src 'self' \\\$\\{OIDC_CSP_ORIGIN\\}"

if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  render_gateway() {
    docker run --rm \
      --add-host frontend:127.0.0.1 \
      --add-host app:127.0.0.1 \
      --add-host workflow-query:127.0.0.1 \
      -e NGINX_ENTRYPOINT_QUIET_LOGS=1 \
      -e NGINX_ENVSUBST_FILTER='^OIDC_CSP_ORIGIN$' \
      "$@" \
      -v "${CONFIG}:/etc/nginx/templates/default.conf.template:ro" \
      -v "${ROOT}/gateway/oidc-csp.envsh:/docker-entrypoint.d/15-oidc-csp.envsh:ro" \
      nginx:alpine sh -c '/docker-entrypoint.sh nginx -t >/dev/null 2>&1 && grep -o "connect-src[^;]*" /etc/nginx/conf.d/default.conf'
  }
  csp="$(render_gateway -e IAM_DEPLOYMENT_MODE=true -e OIDC_AUTHORITY=https://login.example.com:8443/realms/artificialflow)"
  [[ "${csp}" == "connect-src 'self' https://login.example.com:8443" ]] || {
    echo "IAM_DEPLOYMENT_MODE=true CSP does not allow the OIDC origin: ${csp}" >&2
    exit 1
  }
  csp="$(render_gateway -e IAM_DEPLOYMENT_MODE=false -e OIDC_AUTHORITY=https://login.example.com)"
  [[ "${csp}" == "connect-src 'self' " ]] || {
    echo "IAM_DEPLOYMENT_MODE=false CSP must only allow 'self': ${csp}" >&2
    exit 1
  }
  if render_gateway -e IAM_DEPLOYMENT_MODE=true -e "OIDC_AUTHORITY=https://evil.example.com; script-src *" >/dev/null 2>&1; then
    echo "gateway accepted a malformed OIDC_AUTHORITY" >&2
    exit 1
  fi
else
  echo "docker daemon unavailable; nginx -t not run" >&2
  exit 2
fi

echo "gateway security validation passed"
