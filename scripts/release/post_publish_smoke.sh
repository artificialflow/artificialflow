#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
image_lock="${CANDIDATE_IMAGE_LOCK:-${root}/reports/candidate-images.json}"
package_lock="${CANDIDATE_PACKAGE_LOCK:-${root}/reports/candidate-packages.json}"
tag="${RELEASE_TAG:-}"
smoke_command="${POST_PUBLISH_SMOKE_COMMAND:-}"
started_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
log="${root}/reports/post-publish-smoke.txt"

[[ "${tag}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]] || {
  echo "RELEASE_TAG must be a v-prefixed semantic version" >&2
  exit 1
}
[[ -f "${image_lock}" && -f "${package_lock}" ]] || {
  echo "candidate image and package locks are required" >&2
  exit 1
}
[[ -n "${smoke_command}" ]] || {
  echo "POST_PUBLISH_SMOKE_COMMAND is required" >&2
  exit 1
}

{
  python3 - "${image_lock}" <<'PY'
import json, sys
for image in json.load(open(sys.argv[1], encoding="utf-8"))["images"]:
    print(f"{image['name']}\t{image['repository']}\t{image['digest']}")
PY
} | while IFS=$'\t' read -r name repository digest; do
  published="$(docker buildx imagetools inspect "${repository}:${tag}" --format '{{printf "%s" .Manifest.Digest}}')"
  [[ "${published}" == "${digest}" ]] || {
    echo "${name}: published digest ${published} does not match tested ${digest}" >&2
    exit 1
  }
  echo "${repository}:${tag}@${published}"
done | tee "${log}"

python3 - "${package_lock}" "${tag#v}" <<'PY' | tee -a "${log}"
import base64, hashlib, json, subprocess, sys
data=json.load(open(sys.argv[1], encoding="utf-8"))
version=sys.argv[2]
for package in data["packages"]:
    if package["version"] != version:
        raise SystemExit(f"package lock version mismatch: {package['version']} != {version}")
    metadata=json.loads(subprocess.check_output(
        ["npm", "view", f"{package['name']}@{version}", "dist.integrity", "dist.tarball", "--json"],
        text=True,
    ))
    integrity=metadata["dist.integrity"]
    if integrity != package["integrity"]:
        raise SystemExit(f"published npm integrity mismatch for {package['name']}")
    print(f"{package['name']}@{version} {integrity}")
PY

# Run a functional transaction against the public references only after their
# immutable identities have been matched to the tested candidate locks.
bash -o pipefail -c "${smoke_command}" 2>&1 | tee -a "${log}"

bash "${root}/scripts/release/record_evidence.sh" REL-012 pass "${started_at}" \
  "${image_lock}" "${package_lock}" "${log}"
