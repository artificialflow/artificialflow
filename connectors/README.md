# ArtificialFlow connectors

Official connectors are external workers that implement a documented job type and payload contract. Prefer a few hardened connectors over a marketplace.

The canonical descriptor registry is
`connectors/internal/common/descriptors.json`; both the Go runtime and frontend
modeler consume it directly.

## Starter set

| Connector | Job type | Status |
| :--- | :--- | :--- |
| HTTP outbound | `io.artificialflow.connector.http` | Shipped (`connectors/http`) |
| Kafka publish | `io.artificialflow.connector.kafka` | Shipped (`connectors/kafka`) |
| Email | `io.artificialflow.connector.email` | Shipped (`connectors/email`) |
| Slack/webhook | `io.artificialflow.connector.webhook` | Shipped (`connectors/webhook`) |
| S3 | `io.artificialflow.connector.s3` | Shipped (`connectors/s3`) |

The shared runtime in `connectors/internal/common` owns activation, descriptor
input validation, `secret://NAME` resolution, bounded execution retries,
per-job idempotency, and metrics hooks. Connector packages only provide their
transport adapter.

Connector execution is at-least-once. The default in-memory idempotency store
suppresses duplicate effects only within one connector process lifetime; it is
not a durable exactly-once guarantee. Production destinations should accept a
stable operation/idempotency key, or operators should provide a durable
`IdempotencyStore`, before enabling retries for non-idempotent effects.

## Common env

| Variable | Default | Notes |
| :--- | :--- | :--- |
| `ARTIFICIALFLOW_BASE_URL` | `http://localhost:9100/api` | Gateway API base |
| `ARTIFICIALFLOW_TOKEN` | _(empty)_ | Raw access token with the `artificialflowclient` role. Required only with `IAM_DEPLOYMENT_MODE=true`. |
| `WORKER_NAME` | per-connector default | Worker identity string |
| `CONNECTOR_MAX_ATTEMPTS` | `1` | Adapter attempts, bounded to `1..3` |

## Security (HTTP / webhook)

Allowlists are **required by default**:

| Variable | Notes |
| :--- | :--- |
| `HTTP_CONNECTOR_ALLOWED_HOSTS` / `WEBHOOK_CONNECTOR_ALLOWED_HOSTS` | Comma-separated hostnames |
| `HTTP_CONNECTOR_ALLOW_ANY_HOST` / `WEBHOOK_CONNECTOR_ALLOW_ANY_HOST` | Set `true` only for local/dev when no allowlist |
| `HTTP_CONNECTOR_FAIL_ON_NON_2XX` / `WEBHOOK_CONNECTOR_FAIL_ON_NON_2XX` | Default fail on non-2xx when instance var `failOnNon2xx` is unset |

Redirects re-check the allowlist. Header count/size is bounded.

## Run examples

```bash
export ARTIFICIALFLOW_TOKEN=...   # only with IAM_DEPLOYMENT_MODE=true

# HTTP — see http/README.md (allowlist required unless ALLOW_ANY_HOST=true)
HTTP_CONNECTOR_ALLOWED_HOSTS=api.example.com go run ./connectors/http
# Dev only:
# HTTP_CONNECTOR_ALLOW_ANY_HOST=true go run ./connectors/http

# Webhook / Slack incoming webhook URL
WEBHOOK_CONNECTOR_ALLOWED_HOSTS=hooks.slack.com go run ./connectors/webhook
# Instance vars: webhookUrl, payload?, webhookToken?

# Kafka publish
KAFKA_BROKERS=localhost:9092 go run ./connectors/kafka
# Instance vars: kafkaTopic, kafkaKey?, kafkaValue?|payload?

# Email (SMTP)
SMTP_HOST=smtp.example.com SMTP_PORT=587 SMTP_USERNAME=u SMTP_PASSWORD=p SMTP_FROM=noreply@example.com \
  go run ./connectors/email
# Instance vars: emailTo, emailSubject, emailBody?

# S3-compatible PUT (AWS or MinIO)
S3_ENDPOINT=https://s3.amazonaws.com S3_REGION=us-east-1 \
  S3_ACCESS_KEY=... S3_SECRET_KEY=... go run ./connectors/s3
# Instance vars: s3Bucket, s3Key, s3Body?, contentType?
```

## HTTP connector

See [http/README.md](http/README.md).
