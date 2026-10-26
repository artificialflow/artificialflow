# Capacity Numbers

Published capacity evidence for ArtificialFlow. Numbers are indicative and must be re-measured on target hardware before capacity planning.

## How to reproduce

```bash
# Stack must be healthy (Compose or Helm) with auth configured for the k6 scripts.
export COMMAND_URL=http://localhost:8080
export QUERY_URL=http://localhost:8081
export AUTH_TOKEN=<bearer>                 # e.g. $(cat .local/bakeoff.token) after mint_bakeoff_token.mjs
export WORKFLOW_ID=<definition-key-for-cqrs-lag>

make test-perf
```

Artifacts: k6 JSON under `reports/k6.json`.

Extract percentiles (example):

```bash
# After make test-perf, inspect reports/k6.json http_req_duration and cqrs_lag_ms trends.
python3 - <<'PY'
import json,sys
from pathlib import Path
paths=list(Path('reports').rglob('k6.json'))
if not paths:
    print('no k6.json under reports/'); sys.exit(1)
print('using', paths[-1])
# k6 --out json writes NDJSON event stream; summarize http_req_duration points if present
vals=[]; lag=[]
for line in paths[-1].read_text().splitlines():
    try: o=json.loads(line)
    except: continue
    if o.get('type')=='Point' and o.get('metric')=='http_req_duration':
        vals.append(o['data']['value'])
    if o.get('type')=='Point' and o.get('metric')=='cqrs_lag_ms':
        lag.append(o['data']['value'])
def pct(a,p):
    if not a: return None
    a=sorted(a); i=int(round((p/100)*(len(a)-1))); return a[i]
print('http_req_duration ms', { 'n': len(vals), 'p50': pct(vals,50), 'p95': pct(vals,95), 'p99': pct(vals,99)})
print('cqrs_lag_ms', { 'n': len(lag), 'p50': pct(lag,50), 'p95': pct(lag,95), 'p99': pct(lag,99)})
PY
```

## Snapshot (2026-07-27, local Docker Desktop)

Environment:

- `ARTIFICIALFLOW_IMAGE_TAG=v0.3.0` via `make up-release`
- Host: macOS Docker Desktop (single-node Compose; not a production HA cluster)
- Auth: short-lived OIDC access token (mint with `scripts/mint_bakeoff_token.mjs`, OAuth2 client_credentials)
- k6: `make test-perf` (~2 min; thresholds `http_req_duration p95<2000`, `http_req_failed<5%` — both passed)

| Metric | Environment | p50 | p95 | p99 | Notes |
| :--- | :--- | :--- | :--- | :--- | :--- |
| Instance start latency | local Compose release | 81 ms | 107 ms | 119 ms | `POST /instances` samples under `workflow_throughput` + `worker_activate_jobs` |
| Job activate latency | local Compose release | 26 ms | 54 ms | 65 ms | `POST /jobs/activate` (throughput scenario) |
| Query list instances | local Compose release | 26 ms | 44 ms | 52 ms | `query_read_load` `list_instances` |
| CQRS write→query lag | local Compose release | 1.0 s | 15.2 s | 15.2 s | `cqrs_lag` trend (`n=6`); one sample hit the 15s wait ceiling |
| Runtime failover RTO | local Compose release | — | — | **~5.6 s** | `USE_RELEASE=1 ./scripts/runtime-lease-failover-compose.sh` (TTL 15s + tick 5s budget) |
| Overall HTTP duration | local Compose release | 13 ms | 80 ms | 99 ms | All scenarios combined (`n=24523`) |

Scenario HTTP duration (tagged):

| Scenario | p50 | p95 | p99 |
| :--- | ---: | ---: | ---: |
| `workflow_throughput` | 58 ms | 97 ms | 112 ms |
| `query_read_load` | 11 ms | 37 ms | 47 ms |
| `worker_activate_jobs` | 53 ms | 95 ms | 107 ms |
| `cqrs_lag` (HTTP only) | 4 ms | 31 ms | 33 ms |

Checks: **98.5%** succeeded. Residual failures were mostly contended job completes under parallel workers (expected under synthetic load), plus one CQRS lag sample that exceeded the 15s wait.

## SLO targets (1.0 aspirational)

| Signal | Target |
| :--- | :--- |
| Outbox pending age | p99 &lt; 30s under nominal load |
| CQRS projection lag | p99 &lt; 15s under nominal load |
| Runtime lease failover | &lt; `RUNTIME_LEASE_TTL` + `RUNTIME_TICK_INTERVAL` |

Update this file when release validation publishes new measurements.
