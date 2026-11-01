# Release Checklist

Use this checklist before publishing an ArtificialFlow public release.

## 1. Repository readiness

- Confirm the release is cut from the real Git repository, not an exported workspace.
- Confirm `.git` history is clean and the default branch has branch protection.
- Enable required status checks for CI, security, CodeQL, and release dry-runs.
- Enable Dependabot alerts, secret scanning, and code scanning.
- Add repository topics: `bpmn`, `workflow-engine`, `workflow-automation`, `golang`, `typescript`, `react`, `docker`, `oidc`, `sdk`.
- Open the GitHub Actions tab and confirm workflows can start jobs. If runs fail immediately with `startup_failure` and zero jobs, resolve any account, billing, or Actions enablement banner before release validation.

## 2. Secret and dependency checks

```bash
gitleaks detect --source . --redact
npm --prefix frontend audit --audit-level=high
npm --prefix clients/nodejs-sdk audit --audit-level=high
go test ./backend/... -count=1
```

Do not publish if any real `.env`, PAT, OIDC token, private key, client secret, or local credential is present in tracked files or release artifacts.

- Confirm SDK clients use OAuth client credentials against the configured provider.
- Confirm `artificialflowclient` tokens include the ArtificialFlow audience and role claim.
- Confirm UAT reports, screenshots, traces, and logs contain no private key, access token, or authorization header.

## 3. Local validation

```bash
actionlint -color=false
node scripts/validate_legacy_branding.mjs
node scripts/validate_release_version.mjs
node scripts/validate_release_workflows.mjs
python3 scripts/release/evidence.py validate-manifest
make test-release-suite
# With a healthy stack (after make up):
make test-all
make test-all-functionality
make test-dast
make test-uat-mega-bpmn   # needs ARTIFICIALFLOW_TOKEN
make test-perf            # needs AUTH_TOKEN + WORKFLOW_ID
make smoke
make smoke-release
bash scripts/validate_compose_identities.sh
make validate-helm
npm --prefix frontend ci
npm --prefix frontend run lint
npm --prefix frontend test
npm --prefix frontend run build
npm --prefix clients/nodejs-sdk ci
npm --prefix clients/nodejs-sdk test
npm --prefix clients/nodejs-sdk run validate:package
(cd clients/nodejs-sdk && npm pack --dry-run)
bash scripts/validate_nodejs_sdk_install.sh
bash scripts/validate_generated_proto.sh
(cd clients/nodejs-sdk && npm sbom --sbom-format cyclonedx --omit dev >/tmp/artificialflow-nodejs-sdk-sbom.cdx.json)
bash scripts/validate_migration_release.sh
make test-schema-migrations
make release-dry-run
go test ./backend/... -count=1
```

`make validate-helm` always parses chart values YAML and runs `helm lint`/`helm template` when Helm is installed. To run Helm directly:

```bash
helm lint ./charts/artificialflow
helm template artificialflow ./charts/artificialflow >/tmp/artificialflow-default.yaml
helm template artificialflow ./charts/artificialflow -f ./charts/artificialflow/values-kind-smoke.yaml >/tmp/artificialflow-kind.yaml
helm template artificialflow ./charts/artificialflow -f ./charts/artificialflow/examples/values-managed-deps.yaml >/tmp/artificialflow-managed.yaml
```

The schema migration gate must prove ordered migration loading, checksum-drift
rejection, already-applied behavior, and startup schema validation. The Docker
evidence test must additionally pass against both an empty database and the
v1.1.0 baseline fixture. Review migration SQL for expand/contract compatibility
and verify the Debezium-captured singular table names are unchanged.

## 4. Docker Hub setup

Create or verify these canonical Docker Hub repositories:

- `artificialflow/workflow-command`
- `artificialflow/workflow-runtime`
- `artificialflow/workflow-query`
- `artificialflow/sync-worker`
- `artificialflow/frontend`

For the one-release compatibility window, also verify the corresponding five
repositories under `azizaltaleb`. Each canonical/legacy pair must report the
same manifest digest.

Add GitHub Actions secrets:

- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN`

Use GitHub repository settings: Settings > Secrets and variables > Actions > Repository secrets.

`DOCKERHUB_USERNAME` is the authenticating Docker Hub user, which must be able
to push to both namespaces; it is not merely the organization name.
`DOCKERHUB_TOKEN` must be a write-capable personal access token for that same
user, not an account password. Verify access to all ten repositories before
tagging to reduce partial publication risk.

## 5. npm setup

- Confirm the `@artificialflow` npm scope is available and owned by the release maintainers.
- Confirm `@artificialflow/nodejs-sdk` can be published publicly.
- Add GitHub Actions secret `NPM_TOKEN`.
- Use GitHub repository settings: Settings > Secrets and variables > Actions > Repository secrets.
- Confirm npm trusted publishing or the automation token is authorized for both packages. Both publish steps use GitHub-hosted OIDC provenance.

## 6. Release tag validation

Validate the intended release tag before publication:

```bash
node scripts/validate_release_version.mjs --tag v1.2.0
```

Before pushing it, run
`node scripts/validate_release_version.mjs --tag v1.2.0`. The package,
lockfile, chart, Compose defaults, changelog, and documented release version
must already match the tag.

Verify release workflows produce signed images, SBOM/provenance attestations,
and both npm packages. Prerelease SDK versions publish with dist-tag `next`;
stable versions publish with `latest`. The deprecated wrapper job starts only
after the canonical publish job succeeds.

Tag pushes and publish-enabled manual runs must start the top-level `Release`
workflow. A tag builds and scans deterministic candidates but does not promote
them. The dedicated production-evidence runner must complete every pre-publish
gate in `release/evidence-gates.json`; missing, stale, skipped, warning, flaky,
or SHA-mismatched required evidence blocks the run. Promotion then waits for
approval on the protected `release-production` environment. The image workflow
promotes only the already-tested digest to final tags, so a retry can reuse the
candidate and accepts an existing final tag only when its digest matches.
The npm package is packed once, locked by integrity and SHA-256, and that exact
tarball is published only after image promotion.
Direct `Release Docker Images` manual runs may build candidate references but
cannot promote them. Direct `Release Node.js SDK` manual runs are
validation-only; a publish-enabled direct run is refused because it has no
image-workflow dependency. After publication, the workflow verifies registry
digests and npm integrity against the candidate locks, executes the published
artifact smoke, and generates the final retained evidence report.

## 7. Published image smoke

After images are pushed, validate the release override:

```bash
ARTIFICIALFLOW_IMAGE_TAG=v1.2.0 make smoke-release
ARTIFICIALFLOW_IMAGE_TAG=v1.2.0 make up-release
```

Open:

- ArtificialFlow: <http://localhost:9100>

Sign in through the configured OIDC provider and run an SDK smoke test with a `client_credentials` token.

## 8. Final release

```bash
git tag -s v1.2.0 -m "ArtificialFlow v1.2.0"
git push origin v1.2.0
```

Publish GitHub release notes with:

- Changelog summary.
- Known limitations.
- Docker image references.
- npm package version.
- SBOM/provenance/signing notes.
- Upgrade and security notes.
