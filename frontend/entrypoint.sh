#!/bin/sh
set -eu

escape_js_string() {
  printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
}

api_url="${ARTIFICIALFLOW_API_URL:-${VITE_API_URL:-/api}}"
case "${IAM_DEPLOYMENT_MODE:-}" in
  true | false) iam_mode="$IAM_DEPLOYMENT_MODE" ;;
  "") iam_mode="null" ;;
  *)
    echo "unsupported IAM_DEPLOYMENT_MODE \"$IAM_DEPLOYMENT_MODE\": allowed values are true and false" >&2
    exit 1
    ;;
esac
oidc_authority="${FRONTEND_AUTH_OIDC_AUTHORITY:-}"
oidc_client_id="${FRONTEND_AUTH_OIDC_CLIENT_ID:-}"

cat > /usr/share/nginx/html/runtime-config.js <<EOF
window.__ARTIFICIALFLOW_RUNTIME_CONFIG__ = {
  apiUrl: "$(escape_js_string "$api_url")",
  iamMode: $iam_mode,
  oidcAuthority: "$(escape_js_string "$oidc_authority")",
  oidcClientId: "$(escape_js_string "$oidc_client_id")"
};
EOF

exec "$@"
