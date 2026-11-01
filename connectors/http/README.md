# HTTP outbound connector

Job type: `io.artificialflow.connector.http`

## Variables / job custom headers (instance context)

| Field | Required | Description |
| :--- | :--- | :--- |
| `url` | yes | Absolute HTTPS/HTTP URL |
| `method` | no | Default `POST` |
| `headers` | no | Object of string headers (or JSON object string); count/size bounded |
| `body` | no | JSON-serializable body (or JSON string) |
| `timeoutMs` | no | Default `10000` |
| `failOnNon2xx` | no | Default `true` — fail the job on non-2xx responses |

These variables may be set at process start, or modeled as `artificialflow:property` name/value pairs on the service/send task (modeler connector form). At job creation the engine copies known connector keys into instance context when not already set.

## Output variables

| Field | Description |
| :--- | :--- |
| `httpStatus` | Response status code |
| `httpBody` | Response body string (truncated) |
| `httpOk` | `true` when 2xx |

## Security

| Variable | Default | Notes |
| :--- | :--- | :--- |
| `HTTP_CONNECTOR_ALLOWED_HOSTS` | _(empty)_ | Comma-separated hostnames; **required** unless allow-any is set |
| `HTTP_CONNECTOR_ALLOW_ANY_HOST` | `false` | Set `true` only for local/dev evaluation |
| `HTTP_CONNECTOR_FAIL_ON_NON_2XX` | `true` | Default when instance var `failOnNon2xx` is unset |

Redirects use a custom `CheckRedirect` that re-validates the allowlist host. Empty allowlist without `HTTP_CONNECTOR_ALLOW_ANY_HOST=true` fails at startup with a clear error.

## Run

```bash
export ARTIFICIALFLOW_BASE_URL=http://localhost:9100/api
export ARTIFICIALFLOW_TOKEN=...   # only with IAM_DEPLOYMENT_MODE=true
export HTTP_CONNECTOR_ALLOWED_HOSTS=api.example.com
go run ./connectors/http

# Local/dev only (not for production):
# HTTP_CONNECTOR_ALLOW_ANY_HOST=true go run ./connectors/http
```

## Docker

```bash
docker build -f connectors/http/Dockerfile -t artificialflow-http-connector .
```
