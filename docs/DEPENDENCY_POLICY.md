# Dependency Policy

## Goals

- Keep dependencies current enough for security support.
- Prefer maintained libraries with clear licenses.
- Avoid adding runtime dependencies without a clear owner and validation path.

## Review Requirements

New dependencies should be reviewed for:

- License compatibility with MIT distribution.
- Maintenance activity.
- Known vulnerabilities.
- Transitive dependency risk.
- Runtime footprint and image impact.

## Automation

Dependabot tracks:

- Go modules.
- Frontend npm packages.
- Node.js SDK npm packages.
- GitHub Actions.
- Docker base images.

Security workflows scan Go, npm, and filesystem dependencies.

## Community images

Postgres, Kafka, Elasticsearch, Debezium Connect, and nginx are published by
their own projects. `release/upstream-images.tsv` records the current stable
tag this stack can run. The release scan records HIGH and CRITICAL findings
in those images. A finding blocks the release only when the lock is not that
tag, because that means the pin still needs an upgrade. Application images
keep a zero HIGH and CRITICAL gate.

Postgres stays on the latest 15 release so existing data directories open.
Elasticsearch stays on the latest 8.19 release because the application uses
the 8.x client. Debezium scripting stays off; no connector uses it. The
gateway does not enable brotli or HTTP/2. Kafka producers leave compression
unset, so they do not select lz4. The remaining findings are inside the
published images and cannot be removed without replacing those images.
