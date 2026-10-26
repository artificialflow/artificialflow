#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

tmp_json="$(mktemp)"
trap 'rm -f "${tmp_json}"' EXIT

validate_config() {
  local identity="$1"
  local expected_project="$2"
  local expected_registry="$3"
  local expected_volumes="$4"
  local expected_iam_mode="$5"
  shift 5

  env -u IAM_DEPLOYMENT_MODE -u CONSOLE_ADMIN_USERNAME -u CONSOLE_ADMIN_PASSWORD "$@" \
    docker compose --env-file /dev/null config --format json > "${tmp_json}"
  python3 - "${tmp_json}" "${identity}" "${expected_project}" "${expected_registry}" "${expected_volumes}" "${expected_iam_mode}" <<'PY'
import json
import sys

path, identity, expected_project, expected_registry, raw_volumes, expected_iam_mode = sys.argv[1:]
with open(path, "r", encoding="utf-8") as handle:
    config = json.load(handle)

errors = []
if config.get("name") != expected_project:
    errors.append(f"project name {config.get('name')!r}, expected {expected_project!r}")

for service_name, service in config.get("services", {}).items():
    if service.get("container_name"):
        errors.append(f"{service_name} hard-codes container_name={service['container_name']!r}")

required = ["postgres", "elasticsearch", "kafka", "connect", "app", "workflow-runtime", "workflow-query", "sync-worker", "frontend", "gateway"]
missing = [name for name in required if name not in config.get("services", {})]
if missing:
    errors.append(f"missing services: {missing}")

expected_images = {
    "app": "workflow-command",
    "workflow-runtime": "workflow-runtime",
    "workflow-query": "workflow-query",
    "sync-worker": "sync-worker",
    "frontend": "frontend",
}
for service_name, image_name in expected_images.items():
    actual = config.get("services", {}).get(service_name, {}).get("image")
    prefix = f"{expected_registry}/{image_name}:"
    if not str(actual).startswith(prefix):
        errors.append(f"{service_name} image {actual!r}, expected prefix {prefix!r}")

iam_modes = {
    name: (config.get("services", {}).get(name, {}).get("environment") or {}).get("IAM_DEPLOYMENT_MODE")
    for name in ("app", "workflow-query", "frontend")
}
if None in iam_modes.values() or len(set(iam_modes.values())) != 1:
    errors.append(f"IAM_DEPLOYMENT_MODE must be set identically on app, workflow-query and frontend: {iam_modes}")
if any(mode not in ("true", "false") for mode in iam_modes.values() if mode):
    errors.append(f"IAM_DEPLOYMENT_MODE must be true or false: {iam_modes}")
if set(iam_modes.values()) != {expected_iam_mode}:
    errors.append(f"IAM_DEPLOYMENT_MODE {iam_modes}, expected {expected_iam_mode!r}")

console_keys = ("CONSOLE_ADMIN_USERNAME", "CONSOLE_ADMIN_PASSWORD")
for service_name, service in config.get("services", {}).items():
    present = [key for key in console_keys if key in (service.get("environment") or {})]
    wanted = list(console_keys) if expected_iam_mode == "false" and service_name == "app" else []
    if present != wanted:
        errors.append(f"{service_name} console credentials {present}, expected {wanted} when IAM_DEPLOYMENT_MODE={expected_iam_mode}")

expected_volumes = {}
for item in filter(None, raw_volumes.split(",")):
    key, value = item.split("=", 1)
    expected_volumes[key] = value

actual_volumes = config.get("volumes", {})
for key, expected_name in expected_volumes.items():
    actual_name = actual_volumes.get(key, {}).get("name")
    if actual_name != expected_name:
        errors.append(f"volume {key} name {actual_name!r}, expected {expected_name!r}")

if errors:
    print(f"compose identity validation failed for {identity}:", file=sys.stderr)
    for error in errors:
        print(f"  - {error}", file=sys.stderr)
    sys.exit(1)
PY
}

canonical_env=(
  COMPOSE_PROJECT_NAME=artificialflow
  ARTIFICIALFLOW_COMPOSE_PROJECT_NAME=artificialflow
  ARTIFICIALFLOW_PGDATA_VOLUME=artificialflow-postgres-data
  ARTIFICIALFLOW_ESDATA_VOLUME=artificialflow-elasticsearch-data
  ARTIFICIALFLOW_IMAGE_REGISTRY=artificialflow
)

volumes="pgdata=artificialflow-postgres-data,esdata=artificialflow-elasticsearch-data"

validate_config "canonical compose (default, no IAM)" artificialflow artificialflow "${volumes}" false \
  "${canonical_env[@]}"
validate_config "canonical compose (IAM_DEPLOYMENT_MODE=false)" artificialflow artificialflow "${volumes}" false \
  "${canonical_env[@]}" IAM_DEPLOYMENT_MODE=false
validate_config "canonical compose (IAM_DEPLOYMENT_MODE=true)" artificialflow artificialflow "${volumes}" true \
  "${canonical_env[@]}" IAM_DEPLOYMENT_MODE=true \
  AUTH_ISSUER_INTERNAL_URL=https://login.example.com AUTH_ISSUER_PUBLIC_URL=https://login.example.com

echo "compose identity validation passed"
