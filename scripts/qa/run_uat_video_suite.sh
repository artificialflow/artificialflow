#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEPLOYMENT="${1:-external-keycloak}"
RUN_ID="${UAT_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
REPORT_DIR="${UAT_REPORT_DIR:-${ROOT_DIR}/reports/uat-video-suite/${RUN_ID}}"
PLAYWRIGHT_DIR="${ROOT_DIR}/tests/e2e/playwright"
FRONTEND_URL="${FRONTEND_URL:-http://localhost:9100}"
UAT_COMPOSE_BUILD="${UAT_COMPOSE_BUILD:-false}"
RESTORE_STACK_ON_EXIT=false

cleanup_on_exit() {
  local status=$?
  if [[ "${RESTORE_STACK_ON_EXIT}" == "true" ]]; then
    restore_compose_stack || true
  fi
  exit "${status}"
}

trap cleanup_on_exit EXIT

usage() {
  cat <<'USAGE'
Usage: scripts/qa/run_uat_video_suite.sh [external-keycloak]

Environment:
  UAT_RUN_ID       Optional deterministic run id.
  UAT_REPORT_DIR   Optional output directory. Defaults to reports/uat-video-suite/<run-id>.
  UAT_CASE_FILTER  Optional comma-separated case IDs.
  UAT_KEEP_EVIDENCE Preserve created workflows, instances, users, roles, and clients after the run.
  UAT_USERNAME     IAM test username, default admin.
  UAT_PASSWORD     IAM test password, default admin.
USAGE
}

case "${DEPLOYMENT}" in
  external-keycloak) ;;
  -h|--help) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac

mkdir -p "${REPORT_DIR}"

wait_for_http_200() {
  local url="$1"
  local deadline=$((SECONDS + 300))
  until curl -fsS "${url}" >/dev/null 2>&1; do
    if (( SECONDS > deadline )); then
      echo "Timed out waiting for ${url}" >&2
      return 1
    fi
    sleep 2
  done
}

ensure_playwright() {
  cd "${PLAYWRIGHT_DIR}"
  npm install --silent
  npx playwright install chromium >/dev/null
}

run_playwright() {
  local mode="$1"
  echo "[uat] Running ${mode} UAT video suite"
  cd "${PLAYWRIGHT_DIR}"
  UAT_DEPLOYMENT="${mode}" \
  UAT_RUN_ID="${RUN_ID}" \
  UAT_REPORT_DIR="${REPORT_DIR}" \
  UAT_KEEP_EVIDENCE="${UAT_KEEP_EVIDENCE:-false}" \
  FRONTEND_URL="${FRONTEND_URL}" \
  npx playwright test specs/uat-functional-video.spec.ts --project=chromium --workers=1 --retries=0 --reporter=list
}

start_external_keycloak() {
  echo "[uat] Starting stack with generated local Keycloak"
  cd "${ROOT_DIR}"
  prepare_keycloak_override
  local build_args=()
  if [[ "${UAT_COMPOSE_BUILD}" == "true" ]]; then
    build_args=(--build)
  fi
  docker compose --env-file /dev/null -f docker-compose.yml -f "${REPORT_DIR}/keycloak/docker-compose.keycloak.override.yml" up -d ${build_args[@]+"${build_args[@]}"}
  wait_for_http_200 "http://localhost:9181/realms/artificialflow/.well-known/openid-configuration"
  wait_for_http_200 "http://localhost:8080/health"
  wait_for_http_200 "http://localhost:8081/health"
  wait_for_http_200 "http://localhost:8092/health"
  wait_for_http_200 "${FRONTEND_URL}"
  bash scripts/init_connector.sh
  docker compose --env-file /dev/null -f docker-compose.yml -f "${REPORT_DIR}/keycloak/docker-compose.keycloak.override.yml" restart sync-worker
  wait_for_http_200 "http://localhost:8092/health"
}

restore_compose_stack() {
  echo "[uat] Stopping Keycloak overlay"
  cd "${ROOT_DIR}"
  if [[ -f "${REPORT_DIR}/keycloak/docker-compose.keycloak.override.yml" ]]; then
    docker compose --env-file /dev/null -f docker-compose.yml -f "${REPORT_DIR}/keycloak/docker-compose.keycloak.override.yml" down --remove-orphans || true
  fi
}

prepare_keycloak_override() {
  mkdir -p "${REPORT_DIR}/keycloak"
  cat >"${REPORT_DIR}/keycloak/keycloak-realm.json" <<'JSON'
{
  "realm": "artificialflow",
  "enabled": true,
  "displayName": "ArtificialFlow External IAM",
  "roles": {
    "realm": [
      { "name": "artificialflowadmin" },
      { "name": "artificialflowbusiness" },
      { "name": "artificialflowclient" }
    ]
  },
  "clients": [
    {
      "clientId": "artificialflow-frontend",
      "name": "ArtificialFlow Frontend",
      "enabled": true,
      "publicClient": true,
      "standardFlowEnabled": true,
      "directAccessGrantsEnabled": true,
      "redirectUris": ["http://localhost:9100/*"],
      "webOrigins": ["http://localhost:9100"],
      "attributes": { "pkce.code.challenge.method": "S256" }
    }
  ],
  "users": [
    {
      "username": "admin",
      "email": "admin@artificialflow.io",
      "firstName": "ArtificialFlow",
      "lastName": "Admin",
      "enabled": true,
      "emailVerified": true,
      "credentials": [{ "type": "password", "value": "admin", "temporary": false }],
      "realmRoles": ["artificialflowadmin"]
    },
    {
      "username": "modeler",
      "email": "modeler@artificialflow.io",
      "firstName": "ArtificialFlow",
      "lastName": "Modeler",
      "enabled": true,
      "emailVerified": true,
      "credentials": [{ "type": "password", "value": "UatPass123!", "temporary": false }],
      "realmRoles": ["artificialflowbusiness"]
    },
    {
      "username": "sdk-client",
      "email": "sdk-client@artificialflow.io",
      "firstName": "ArtificialFlow",
      "lastName": "SDK Client",
      "enabled": true,
      "emailVerified": true,
      "credentials": [{ "type": "password", "value": "UatPass123!", "temporary": false }],
      "realmRoles": ["artificialflowclient"]
    },
    {
      "username": "accountant",
      "email": "accountant@artificialflow.io",
      "firstName": "UAT",
      "lastName": "Accountant",
      "enabled": true,
      "emailVerified": true,
      "credentials": [{ "type": "password", "value": "UatPass123!", "temporary": false }],
      "realmRoles": []
    },
    {
      "username": "reviewer",
      "email": "reviewer@artificialflow.io",
      "firstName": "UAT",
      "lastName": "Reviewer",
      "enabled": true,
      "emailVerified": true,
      "credentials": [{ "type": "password", "value": "UatPass123!", "temporary": false }],
      "realmRoles": []
    }
  ]
}
JSON
  cat >"${REPORT_DIR}/keycloak/docker-compose.keycloak.override.yml" <<YAML
services:
  keycloak:
    image: quay.io/keycloak/keycloak:26.4.5
    container_name: workflow-keycloak
    command:
      - start-dev
      - --import-realm
      - --hostname=http://localhost:9181
      - --hostname-strict=false
      - --hostname-backchannel-dynamic=true
    environment:
      KC_BOOTSTRAP_ADMIN_USERNAME: admin
      KC_BOOTSTRAP_ADMIN_PASSWORD: admin
      KC_HTTP_ENABLED: "true"
      KC_HEALTH_ENABLED: "true"
      KC_PROXY_HEADERS: xforwarded
    ports:
      - "9181:8080"
    volumes:
      - ${REPORT_DIR}/keycloak/keycloak-realm.json:/opt/keycloak/data/import/artificialflow-realm.json:ro

  app:
    environment:
      IAM_DEPLOYMENT_MODE: "true"
      IAM_PROVIDER_NAME: Keycloak
      AUTH_ISSUER_INTERNAL_URL: http://keycloak:8080/realms/artificialflow
      AUTH_ISSUER_PUBLIC_URL: http://localhost:9181/realms/artificialflow
      AUTH_CLIENT_ID: artificialflow-frontend
      AUTH_TOKEN_MODE: jwt
      AUTH_CLAIM_ROLES_PATH: roles,realm_access.roles,groups
      AUTH_ENFORCE_AUDIENCE: "false"
      AUTH_ALLOW_INSECURE_ISSUER: "true"
      FRONTEND_AUTH_OIDC_AUTHORITY: http://localhost:9181/realms/artificialflow
      FRONTEND_AUTH_OIDC_CLIENT_ID: artificialflow-frontend
    depends_on:
      keycloak:
        condition: service_started

  workflow-query:
    environment:
      IAM_DEPLOYMENT_MODE: "true"
      IAM_PROVIDER_NAME: Keycloak
      AUTH_ISSUER_INTERNAL_URL: http://keycloak:8080/realms/artificialflow
      AUTH_ISSUER_PUBLIC_URL: http://localhost:9181/realms/artificialflow
      AUTH_CLIENT_ID: artificialflow-frontend
      AUTH_TOKEN_MODE: jwt
      AUTH_CLAIM_ROLES_PATH: roles,realm_access.roles,groups
      AUTH_ENFORCE_AUDIENCE: "false"
      AUTH_ALLOW_INSECURE_ISSUER: "true"
      FRONTEND_AUTH_OIDC_AUTHORITY: http://localhost:9181/realms/artificialflow
      FRONTEND_AUTH_OIDC_CLIENT_ID: artificialflow-frontend
    depends_on:
      keycloak:
        condition: service_started

  frontend:
    environment:
      IAM_DEPLOYMENT_MODE: "true"
      FRONTEND_AUTH_OIDC_AUTHORITY: http://localhost:9181/realms/artificialflow
      FRONTEND_AUTH_OIDC_CLIENT_ID: artificialflow-frontend
    depends_on:
      keycloak:
        condition: service_started
YAML
}

ensure_playwright

case "${DEPLOYMENT}" in
  external-keycloak)
    RESTORE_STACK_ON_EXIT=true
    start_external_keycloak
    run_playwright external-keycloak
    restore_compose_stack
    RESTORE_STACK_ON_EXIT=false
    ;;
esac

echo "[uat] Report directory: ${REPORT_DIR}"
echo "[uat] Manifest: ${REPORT_DIR}/manifest.json"
echo "[uat] Summary: ${REPORT_DIR}/summary.md"
