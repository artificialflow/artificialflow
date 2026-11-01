#!/usr/bin/env bash
set -euo pipefail

if [[ -n "${COMPOSE_CMD:-}" ]]; then
  read -r -a COMPOSE_CMD_ARGS <<< "${COMPOSE_CMD}"
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE_CMD_ARGS=(docker-compose)
elif command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  COMPOSE_CMD_ARGS=(docker compose)
else
  echo "docker compose is required; install docker compose or set COMPOSE_CMD" >&2
  exit 127
fi
WAIT_TIMEOUT_SEC="${WAIT_TIMEOUT_SEC:-300}"
QUERY_PAGE_SIZE="${QUERY_PAGE_SIZE:-200}"
QUERY_MAX_PAGES="${QUERY_MAX_PAGES:-50}"
POSTGRES_SERVICE="${POSTGRES_SERVICE:-postgres}"
POSTGRES_USER="${POSTGRES_USER:-artificialflow}"
POSTGRES_DB="${POSTGRES_DB:-workflow_db}"
CONNECT_URL="${CONNECT_URL:-http://localhost:8083}"
CONNECTOR_NAME="${CONNECTOR_NAME:-artificialflow-postgres-connector}"
QUERY_URL="${QUERY_URL:-http://localhost:8081}"
SYNC_HEALTH_URL="${SYNC_HEALTH_URL:-http://localhost:8092/health}"
KAFKA_SERVICE="${KAFKA_SERVICE:-kafka}"
KAFKA_BOOTSTRAP="${KAFKA_BOOTSTRAP:-localhost:9092}"
SYNC_GROUP_ID="${SYNC_GROUP_ID:-artificialflow-sync-worker-v8}"
SYNC_READY_TOPIC="${SYNC_READY_TOPIC:-artificialflow.public.process}"
SYNC_READY_TOPICS="${SYNC_READY_TOPICS:-workflow.events.v1,artificialflow.public.process,artificialflow.public.process_instance,artificialflow.public.element_instance,artificialflow.public.variable,artificialflow.public.job,artificialflow.public.incident,artificialflow.public.timer,artificialflow.public.message_subscription}"
SYNC_CAPTURED_TABLES="${SYNC_CAPTURED_TABLES:-process,process_instance,element_instance,variable,job,incident,timer,message_subscription}"
QUERY_AUTH_MODE="${QUERY_AUTH_MODE:-auto}"
QUERY_BEARER_TOKEN="${QUERY_BEARER_TOKEN:-}"
OIDC_TOKEN_URL="${OIDC_TOKEN_URL:-}"
OIDC_CLIENT_ID="${OIDC_CLIENT_ID:-}"
OIDC_CLIENT_SECRET="${OIDC_CLIENT_SECRET:-}"
OIDC_USERNAME="${OIDC_USERNAME:-}"
OIDC_PASSWORD="${OIDC_PASSWORD:-}"
OIDC_SCOPE="${OIDC_SCOPE:-openid profile email}"
OIDC_GRANT_TYPE="${OIDC_GRANT_TYPE:-password}"
DEBUG_ON_FAILURE="${DEBUG_ON_FAILURE:-true}"
CLEANUP="${CLEANUP:-${CI:-false}}"
AUTH_ENFORCE_AUDIENCE="${AUTH_ENFORCE_AUDIENCE:-false}"

export AUTH_ENFORCE_AUDIENCE

compose() {
  "${COMPOSE_CMD_ARGS[@]}" --profile full-cqrs "$@"
}

# apache/kafka ships admin tools as /opt/kafka/bin/*.sh. The short names from
# older distributions are not on PATH, so the container can be healthy while
# `kafka-topics` still fails.
kafka_admin() {
  local tool="$1"
  shift
  compose exec -T "${KAFKA_SERVICE}" "/opt/kafka/bin/${tool}.sh" "$@"
}

wait_for_http_200() {
  local url="$1"
  local started
  started="$(date +%s)"

  until curl -fsS "${url}" >/dev/null 2>&1; do
    local now
    now="$(date +%s)"
    if (( now - started > WAIT_TIMEOUT_SEC )); then
      echo "Timed out waiting for ${url}"
      return 1
    fi
    sleep 2
  done
}

query_status_no_auth() {
  local url="$1"
  curl -sS -o /dev/null -w "%{http_code}" "${url}" || true
}

query_status() {
  local url="$1"
  if [[ -n "${QUERY_BEARER_TOKEN}" ]]; then
    curl -sS -o /dev/null -w "%{http_code}" -H "Authorization: Bearer ${QUERY_BEARER_TOKEN}" "${url}" || true
    return
  fi
  query_status_no_auth "${url}"
}

workflow_visible_in_query() {
  local payload="$1"
  local workflow_id="$2"

  python3 - "$payload" "$workflow_id" <<'PY'
import json
import sys

raw = sys.argv[1]
needle = str(sys.argv[2])

try:
    body = json.loads(raw)
except Exception:
    raise SystemExit(1)

for item in body.get("workflows", []):
    if str(item.get("id", "")) == needle:
        raise SystemExit(0)

raise SystemExit(1)
PY
}

workflow_total_pages() {
  local payload="$1"
  local page_size="$2"

  python3 - "$payload" "$page_size" <<'PY'
import json
import math
import sys

raw = sys.argv[1]

try:
    page_size = max(1, int(sys.argv[2]))
except Exception:
    page_size = 200

try:
    body = json.loads(raw)
except Exception:
    print(1)
    raise SystemExit(0)

total = body.get("total", 0)
try:
    total_int = int(total)
except Exception:
    total_int = 0

print(max(1, math.ceil(total_int / page_size)))
PY
}

obtain_query_token() {
  if [[ -z "${OIDC_TOKEN_URL}" ]]; then
    echo "OIDC_TOKEN_URL is required for automatic token bootstrap; set QUERY_BEARER_TOKEN or configure OIDC_TOKEN_URL" >&2
    return 1
  fi
  if [[ -z "${OIDC_CLIENT_ID}" ]]; then
    echo "OIDC_CLIENT_ID is required for automatic token bootstrap" >&2
    return 1
  fi

  if [[ "${OIDC_GRANT_TYPE}" == "password" ]]; then
    if [[ -z "${OIDC_USERNAME}" || -z "${OIDC_PASSWORD}" ]]; then
      echo "OIDC_USERNAME and OIDC_PASSWORD are required when OIDC_GRANT_TYPE=password" >&2
      return 1
    fi
  fi

  local started
  started="$(date +%s)"

  while true; do
    local response token now
    local curl_args
    curl_args=(
      -sS
      -X POST
      "${OIDC_TOKEN_URL}"
      -H "Content-Type: application/x-www-form-urlencoded"
      --data-urlencode "grant_type=${OIDC_GRANT_TYPE}"
      --data-urlencode "client_id=${OIDC_CLIENT_ID}"
    )

    if [[ -n "${OIDC_CLIENT_SECRET}" ]]; then
      curl_args+=(--data-urlencode "client_secret=${OIDC_CLIENT_SECRET}")
    fi
    if [[ -n "${OIDC_SCOPE}" ]]; then
      curl_args+=(--data-urlencode "scope=${OIDC_SCOPE}")
    fi

    case "${OIDC_GRANT_TYPE}" in
      password)
        curl_args+=(
          --data-urlencode "username=${OIDC_USERNAME}"
          --data-urlencode "password=${OIDC_PASSWORD}"
        )
        ;;
      client_credentials)
        ;;
      *)
        echo "Unsupported OIDC_GRANT_TYPE=${OIDC_GRANT_TYPE}; expected password|client_credentials" >&2
        return 1
        ;;
    esac

    response="$(curl "${curl_args[@]}" || true)"

    token="$(python3 - "$response" <<'PY'
import json
import sys

raw = sys.argv[1]
try:
    body = json.loads(raw)
except Exception:
    print("")
    raise SystemExit(0)

print(body.get("access_token", ""))
PY
)"

    if [[ -n "${token}" ]]; then
      printf "%s" "${token}"
      return 0
    fi

    now="$(date +%s)"
    if (( now - started > WAIT_TIMEOUT_SEC )); then
      echo "Timed out acquiring OIDC token from ${OIDC_TOKEN_URL}" >&2
      return 1
    fi

    sleep 2
  done
}

configure_query_auth() {
  local probe_url="${QUERY_URL}/workflows?page=1&pageSize=1"
  local probe_status

  case "${QUERY_AUTH_MODE}" in
    off)
      probe_status="$(query_status_no_auth "${probe_url}")"
      if [[ "${probe_status}" != "200" ]]; then
        echo "Query auth mode is OFF but anonymous query probe returned status=${probe_status}; set QUERY_AUTH_MODE=auto|required (or provide QUERY_BEARER_TOKEN)" >&2
        return 1
      fi

      echo "Query auth mode is OFF; query API is reachable anonymously"
      QUERY_BEARER_TOKEN=""
      return 0
      ;;
    required)
      ;;
    auto)
      probe_status="$(query_status_no_auth "${probe_url}")"
      if [[ "${probe_status}" == "200" ]]; then
        echo "Query API allows anonymous workflow search; auth token not required"
        QUERY_BEARER_TOKEN=""
        return 0
      fi
      if [[ "${probe_status}" != "401" && "${probe_status}" != "403" ]]; then
        echo "Unexpected query auth probe status=${probe_status} for ${probe_url}" >&2
        return 1
      fi
      ;;
    *)
      echo "Unsupported QUERY_AUTH_MODE=${QUERY_AUTH_MODE}; expected auto|required|off" >&2
      return 1
      ;;
  esac

  if [[ -z "${QUERY_BEARER_TOKEN}" ]]; then
    echo "Query API requires auth; acquiring OIDC token"
    QUERY_BEARER_TOKEN="$(obtain_query_token)"
  fi

  probe_status="$(query_status "${probe_url}")"
  if [[ "${probe_status}" != "200" ]]; then
    echo "Authenticated query probe failed with status=${probe_status}" >&2
    return 1
  fi
}

connector_running() {
  local payload="$1"

  python3 - "$payload" <<'PY'
import json
import sys

raw = sys.argv[1]
try:
    body = json.loads(raw)
except Exception:
    raise SystemExit(1)

connector_state = str(body.get("connector", {}).get("state", "")).upper()
if connector_state != "RUNNING":
    raise SystemExit(1)

tasks = body.get("tasks", [])
if tasks and any(str(task.get("state", "")).upper() != "RUNNING" for task in tasks):
    raise SystemExit(1)

raise SystemExit(0)
PY
}

wait_for_connector_running() {
  local status_url="${CONNECT_URL}/connectors/${CONNECTOR_NAME}/status"
  local started
  started="$(date +%s)"

  while true; do
    local payload now
    payload="$(curl -sS "${status_url}" || true)"
    if [[ -n "${payload}" ]] && connector_running "${payload}"; then
      return 0
    fi

    now="$(date +%s)"
    if (( now - started > WAIT_TIMEOUT_SEC )); then
      echo "Timed out waiting for connector ${CONNECTOR_NAME} to become RUNNING" >&2
      echo "Last connector status payload: ${payload:-<empty>}" >&2
      return 1
    fi

    sleep 2
  done
}

wait_for_kafka_cli() {
  local started
  started="$(date +%s)"

  while true; do
    if kafka_admin kafka-topics --bootstrap-server "${KAFKA_BOOTSTRAP}" --list >/dev/null 2>&1; then
      return 0
    fi

    local now
    now="$(date +%s)"
    if (( now - started > WAIT_TIMEOUT_SEC )); then
      echo "Timed out waiting for Kafka CLI readiness via service ${KAFKA_SERVICE}" >&2
      return 1
    fi

    sleep 2
  done
}

trim_space() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf "%s" "${value}"
}

ensure_sync_ready_topics() {
  local topic
  local topics
  IFS=',' read -r -a topics <<< "${SYNC_READY_TOPICS}"

  for topic in "${topics[@]}"; do
    topic="$(trim_space "${topic}")"
    if [[ -z "${topic}" ]]; then
      continue
    fi

    kafka_admin kafka-topics \
      --bootstrap-server "${KAFKA_BOOTSTRAP}" \
      --create \
      --if-not-exists \
      --topic "${topic}" \
      --partitions 1 \
      --replication-factor 1 >/dev/null
  done
}

wait_for_postgres_captured_tables() {
  local table
  local tables
  local expected=0
  local table_list=""
  local started
  IFS=',' read -r -a tables <<< "${SYNC_CAPTURED_TABLES}"

  for table in "${tables[@]}"; do
    table="$(trim_space "${table}")"
    if [[ -z "${table}" ]]; then
      continue
    fi

    table="${table//\'/\'\'}"
    if [[ -n "${table_list}" ]]; then
      table_list="${table_list},"
    fi
    table_list="${table_list}'${table}'"
    ((expected += 1))
  done

  if (( expected == 0 )); then
    return 0
  fi

  started="$(date +%s)"
  while true; do
    local count
    count="$(compose exec -T "${POSTGRES_SERVICE}" psql \
      -U "${POSTGRES_USER}" \
      -d "${POSTGRES_DB}" \
      -tAc "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name IN (${table_list});" 2>/dev/null | tr -d '[:space:]' || true)"

    if [[ "${count}" == "${expected}" ]]; then
      return 0
    fi

    local now
    now="$(date +%s)"
    if (( now - started > WAIT_TIMEOUT_SEC )); then
      echo "Timed out waiting for Postgres captured tables (${count:-0}/${expected})" >&2
      compose exec -T "${POSTGRES_SERVICE}" psql \
        -U "${POSTGRES_USER}" \
        -d "${POSTGRES_DB}" \
        -c "SELECT table_schema, table_name FROM information_schema.tables WHERE table_schema = 'public' ORDER BY table_name;" >&2 || true
      return 1
    fi

    sleep 2
  done
}

sync_consumer_group_ready() {
  local output
  output="$(kafka_admin kafka-consumer-groups \
    --bootstrap-server "${KAFKA_BOOTSTRAP}" \
    --describe \
    --group "${SYNC_GROUP_ID}" \
    --state 2>/dev/null || true)"

  if printf "%s\n" "${output}" | grep -F "${SYNC_GROUP_ID}" | grep -Ei 'stable|STABLE' >/dev/null; then
    return 0
  fi

  if compose logs --no-color "${KAFKA_SERVICE}" 2>/dev/null | grep -F "Assignment received from leader" | grep -F "group ${SYNC_GROUP_ID}" >/dev/null; then
    return 0
  fi

  return 1
}

wait_for_sync_consumer_ready() {
  local started
  started="$(date +%s)"

  while true; do
    if sync_consumer_group_ready; then
      return 0
    fi

    local now
    now="$(date +%s)"
    if (( now - started > WAIT_TIMEOUT_SEC )); then
      echo "Timed out waiting for sync-worker consumer group ${SYNC_GROUP_ID} readiness" >&2
      kafka_admin kafka-consumer-groups \
        --bootstrap-server "${KAFKA_BOOTSTRAP}" \
        --describe \
        --group "${SYNC_GROUP_ID}" \
        --state >&2 || true
      return 1
    fi

    sleep 2
  done
}

query_workflows_page() {
  local page="$1"
  local page_size="$2"
  local url="${QUERY_URL}/workflows?page=${page}&pageSize=${page_size}"
  local response

  if [[ -n "${QUERY_BEARER_TOKEN}" ]]; then
    response="$(curl -sS -H "Authorization: Bearer ${QUERY_BEARER_TOKEN}" -w $'\n%{http_code}' "${url}" || true)"
  else
    response="$(curl -sS -w $'\n%{http_code}' "${url}" || true)"
  fi

  if [[ -z "${response}" ]]; then
    return 1
  fi

  local status body
  status="${response##*$'\n'}"
  body="${response%$'\n'*}"

  if [[ "${status}" != "200" ]]; then
    echo "Query request failed for page=${page} status=${status}" >&2
    return 1
  fi

  printf "%s" "${body}"
}

workflow_visible_across_pages() {
  local workflow_id="$1"
  local page=1
  local total_pages=1

  while (( page <= total_pages && page <= QUERY_MAX_PAGES )); do
    local payload
    payload="$(query_workflows_page "${page}" "${QUERY_PAGE_SIZE}" || true)"
    if [[ -z "${payload}" ]]; then
      return 1
    fi

    if workflow_visible_in_query "${payload}" "${workflow_id}"; then
      return 0
    fi

    total_pages="$(workflow_total_pages "${payload}" "${QUERY_PAGE_SIZE}")"
    if ! [[ "${total_pages}" =~ ^[0-9]+$ ]]; then
      total_pages=1
    fi
    if (( total_pages > QUERY_MAX_PAGES )); then
      total_pages="${QUERY_MAX_PAGES}"
    fi
    ((page += 1))
  done

  return 1
}

dump_failure_diagnostics() {
  if [[ "${DEBUG_ON_FAILURE}" != "true" ]]; then
    return
  fi

  echo "Smoke failed; collecting CQRS diagnostics..."

  echo "--- compose ps ---"
  compose ps || true

  echo "--- sync-worker health ---"
  curl -sS "${SYNC_HEALTH_URL}" || true
  echo

  echo "--- query health ---"
  curl -sS "${QUERY_URL}/health" || true
  echo

  echo "--- query workflows probe status ---"
  echo "status=$(query_status "${QUERY_URL}/workflows?page=1&pageSize=1")"

  echo "--- connect connector status (${CONNECTOR_NAME}) ---"
  curl -sS "${CONNECT_URL}/connectors/${CONNECTOR_NAME}/status" || true
  echo

  for service in app sync-worker workflow-query connect kafka postgres; do
    echo "--- logs: ${service} (tail=120) ---"
    compose logs --tail=120 "${service}" || true
  done
}

cleanup_stack() {
  local exit_code="$1"

  trap - EXIT

  if [[ "${exit_code}" -ne 0 ]]; then
    dump_failure_diagnostics
  fi

  if [[ "${CLEANUP}" == "true" ]]; then
    compose down --remove-orphans || true
  fi

  exit "${exit_code}"
}
trap 'cleanup_stack $?' EXIT

echo "Starting CQRS dependency stack..."
compose up -d --build postgres elasticsearch kafka connect app workflow-query

echo "Waiting for Kafka CLI readiness..."
wait_for_kafka_cli

echo "Ensuring sync-worker Kafka topics exist..."
ensure_sync_ready_topics

echo "Waiting for query health endpoint..."
wait_for_http_200 "${QUERY_URL}/health"

echo "Waiting for Postgres captured tables..."
wait_for_postgres_captured_tables

echo "Ensuring Debezium connector exists..."
CONNECT_URL="${CONNECT_URL}" bash ./scripts/init_connector.sh

echo "Waiting for connector ${CONNECTOR_NAME} to report RUNNING..."
wait_for_connector_running

echo "Starting sync-worker..."
compose up -d --build sync-worker

echo "Waiting for sync-worker health endpoint..."
wait_for_http_200 "${SYNC_HEALTH_URL}"

echo "Waiting for sync-worker consumer group readiness..."
wait_for_sync_consumer_ready

echo "Configuring query auth mode..."
configure_query_auth

TEST_KEY="$(date +%s%N | cut -b1-18)"
DEPLOYMENT_KEY="$((TEST_KEY + 1))"
BPMN_ID="cqrs-e2e-${TEST_KEY}"
RESOURCE_NAME="${BPMN_ID}.bpmn"
RESOURCE_CHECKSUM="${BPMN_ID}-checksum"

echo "Inserting synthetic process row into Postgres (key=${TEST_KEY})..."
compose exec -T "${POSTGRES_SERVICE}" psql \
  -U "${POSTGRES_USER}" \
  -d "${POSTGRES_DB}" \
  -v ON_ERROR_STOP=1 \
  -c "INSERT INTO process (key, bpmn_process_id, version, resource_name, deployment_key, resource, resource_checksum, tenant_id, created_at) VALUES (${TEST_KEY}, '${BPMN_ID}', 1, '${RESOURCE_NAME}', ${DEPLOYMENT_KEY}, E'\\\\x', '${RESOURCE_CHECKSUM}', 'default', NOW()) ON CONFLICT (key) DO NOTHING;"

echo "Waiting for workflow visibility in query read model..."
started="$(date +%s)"
until workflow_visible_across_pages "${TEST_KEY}"; do

  now="$(date +%s)"
  if (( now - started > WAIT_TIMEOUT_SEC )); then
    echo "Timed out waiting for workflow id=${TEST_KEY} in query model"
    exit 1
  fi
  sleep 3
done

echo "CQRS E2E smoke passed: workflow id=${TEST_KEY} is visible in query read model"
