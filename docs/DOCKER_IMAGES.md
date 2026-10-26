# Docker Images

ArtificialFlow publishes five images under the `artificialflow` Docker Hub
namespace. Each release is also pushed under the deprecated `azizaltaleb`
namespace. Both names resolve to the same multi-platform manifest digest.

## Image repositories

| Compose service | Image | Description |
| :--- | :--- | :--- |
| `app` | `artificialflow/workflow-command` | Command API, worker and user-task APIs, identity endpoints. Applies migrations at startup. Also contains the `workflow-migrate` binary. |
| `workflow-runtime` | `artificialflow/workflow-runtime` | Timers, SLA checks, background execution, and the outbox relay. |
| `workflow-query` | `artificialflow/workflow-query` | CQRS query API backed by Elasticsearch. |
| `sync-worker` | `artificialflow/sync-worker` | Kafka and Debezium consumer that writes the search projections. |
| `frontend` | `artificialflow/frontend` | React console and modeler served by NGINX. |

The application images run as the non-root user `artificialflow` (UID and GID
`10001`).

Compose and the Helm chart also use third-party images: `postgres:15-alpine`,
`docker.elastic.co/elasticsearch/elasticsearch:8.11.1`,
`confluentinc/cp-kafka:7.6.1`, `debezium/connect:2.6`, and `nginx:alpine` for
the gateway.

## Tags

A release `vX.Y.Z` publishes three tags for each image:

| Tag | Meaning |
| :--- | :--- |
| `v1.2.0` | Exact release tag |
| `1.2.0` | Same image without the `v` prefix. Compose and Helm default to this form. |
| `1.2` | Moves to the latest patch of the `1.2` line. Not published for pre-releases. |

No `latest` tag is published. Production deployments should pin an exact tag
or an image digest.

The deprecated aliases (`azizaltaleb/workflow-command`,
`azizaltaleb/workflow-runtime`, `azizaltaleb/workflow-query`,
`azizaltaleb/sync-worker`, `azizaltaleb/frontend`) are not separate builds.
The release workflow copies the canonical digest to both names, checks that
the digests match, scans the canonical digest, and signs both references. New
deployments should use `artificialflow/*`.

## Compose usage

`docker-compose.yml` names every application image as
`${ARTIFICIALFLOW_IMAGE_REGISTRY:-artificialflow}/<image>:${ARTIFICIALFLOW_IMAGE_TAG:-1.2.0}`.

- `make up` builds these images from the checked-out source.
- `make up-release` pulls them and starts without building.

Run `make up-release` from a checkout of the matching release tag. On a
development branch, use `make up`, because the published images may not match
the configuration in the branch.

```bash
git clone --depth 1 --branch v1.2.0 https://github.com/artificialflow/artificialflow.git
cd artificialflow
make up-release
```

Use a staging registry or a fork:

```bash
ARTIFICIALFLOW_IMAGE_REGISTRY=registry.example.com/artificialflow \
ARTIFICIALFLOW_IMAGE_TAG=1.2.0 make up-release
```

`make smoke` (also available as `make smoke-release`) validates the rendered
Compose config and the Kafka wiring without starting containers.

## Helm usage

The chart pins each application image under `images.<component>`
(`command`, `query`, `runtime`, `syncWorker`, `frontend`) with `repository`
and `tag` (`1.2.0`, matching `appVersion`). Each entry also accepts `digest`
and `pullPolicy`. See [deployment.md](deployment.md#3-helm).

## Build metadata

Release images include these OCI labels:

- `org.opencontainers.image.title`
- `org.opencontainers.image.description`
- `org.opencontainers.image.source`
- `org.opencontainers.image.licenses`
- `org.opencontainers.image.version`
- `org.opencontainers.image.revision`

The release workflow builds `linux/amd64` and `linux/arm64` images, requests
SBOM and provenance attestations, scans the published images, and signs them
with Cosign.

## Docker Hub release credentials

`DOCKERHUB_USERNAME` is the Docker Hub user that authenticates the workflow. It
is not the `artificialflow` organization name unless the account has that
name. The user needs push permission to all five repositories in both the
`artificialflow` and `azizaltaleb` namespaces.

`DOCKERHUB_TOKEN` is a Docker Hub personal access token for that user with
write access to those repositories. Do not store an account password. Store
both values as GitHub Actions secrets, use a dedicated release identity, and
rotate the token after the alias window ends. Verify all references and
matching digests before announcing a release.

## Security notes

- Compose defaults are for development and evaluation.
- Replace default credentials before production use.
- Use TLS, strict OIDC audience validation, secret management, backups, and
  monitoring.
- Verify image signatures and review the SBOM and provenance before
  production rollouts.
