#!/usr/bin/env bash
set -euo pipefail
umask 077

: "${SOURCE_KEYS_COMMAND:?SOURCE_KEYS_COMMAND must emit tenant<TAB>stable-key rows}"
: "${PROJECTION_KEYS_COMMAND:?PROJECTION_KEYS_COMMAND must emit tenant<TAB>stable-key rows}"

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

bash -o pipefail -c "${SOURCE_KEYS_COMMAND}" | LC_ALL=C sort > "${tmp}/source.tsv"
bash -o pipefail -c "${PROJECTION_KEYS_COMMAND}" | LC_ALL=C sort > "${tmp}/projection.tsv"

python3 - "${tmp}/source.tsv" "${tmp}/projection.tsv" <<'PY'
import collections
import json
import sys

def load(path):
    rows = []
    with open(path, encoding="utf-8") as handle:
        for number, raw in enumerate(handle, 1):
            parts = raw.rstrip("\n").split("\t")
            if len(parts) != 2 or not all(parts):
                raise SystemExit(f"{path}:{number}: expected non-empty tenant<TAB>stable-key")
            rows.append(tuple(parts))
    duplicates = [row for row, count in collections.Counter(rows).items() if count > 1]
    if duplicates:
        raise SystemExit(f"{path}: duplicate tenant/key pairs: {duplicates[:5]}")
    return set(rows)

source = load(sys.argv[1])
projection = load(sys.argv[2])
missing = sorted(source - projection)
extra = sorted(projection - source)
source_tenants = sorted({tenant for tenant, _ in source})
projection_tenants = sorted({tenant for tenant, _ in projection})
result = {
    "formatVersion": 1,
    "sourceCount": len(source),
    "projectionCount": len(projection),
    "sourceTenants": source_tenants,
    "projectionTenants": projection_tenants,
    "missingKeys": missing[:100],
    "extraKeys": extra[:100],
}
print(json.dumps(result, sort_keys=True))
if missing or extra or source_tenants != projection_tenants:
    raise SystemExit("projection parity or tenant isolation check failed")
PY
