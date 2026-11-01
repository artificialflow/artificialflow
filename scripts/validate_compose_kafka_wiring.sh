#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -eq 0 ]]; then
  echo "usage: $0 <docker compose args, e.g. -f docker-compose.yml>" >&2
  exit 2
fi

tmp_json="$(mktemp)"
trap 'rm -f "${tmp_json}"' EXIT

docker compose "$@" config --format json > "${tmp_json}"
python3 - "${tmp_json}" "$*" <<'PY'
import json
import sys

json_path = sys.argv[1]
compose_args = sys.argv[2]
with open(json_path, "r", encoding="utf-8") as handle:
    data = json.load(handle)
services = data.get("services", {})
required = {
    "KAFKA_BROKERS": "kafka:29092",
    "KAFKA_TOPIC_EVENTS": "workflow.events.v1",
}


def normalize_environment(raw):
    if raw is None:
        return {}
    if isinstance(raw, dict):
        return {str(key): "" if value is None else str(value) for key, value in raw.items()}
    if isinstance(raw, list):
        env = {}
        for item in raw:
            if not isinstance(item, str):
                continue
            key, sep, value = item.partition("=")
            env[key] = value if sep else ""
        return env
    return {}


errors = []
for service_name in ("app", "workflow-runtime"):
    service = services.get(service_name)
    if service is None:
        errors.append(f"{service_name}: service missing")
        continue
    env = normalize_environment(service.get("environment"))
    for key, expected in required.items():
        actual = env.get(key)
        if actual != expected:
            errors.append(f"{service_name}: {key}={actual!r}, expected {expected!r}")

if errors:
    print(f"compose Kafka wiring invalid for {compose_args}:", file=sys.stderr)
    for error in errors:
        print(f"  - {error}", file=sys.stderr)
    sys.exit(1)
PY
