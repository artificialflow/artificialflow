#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
lock="${CANDIDATE_IMAGE_LOCK:-${root}/reports/candidate-images.json}"
started_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
log="${root}/reports/candidate-images-verify.txt"
mkdir -p "${root}/reports"

[[ -f "${lock}" ]] || { echo "candidate image lock is required: ${lock}" >&2; exit 1; }
command -v docker >/dev/null || { echo "docker with buildx is required" >&2; exit 1; }
identity="$(python3 "${root}/scripts/release/evidence.py" candidate-identity)"

python3 - "${lock}" "${identity}" <<'PY' |
import json, re, sys
data=json.load(open(sys.argv[1], encoding="utf-8"))
identity=json.loads(sys.argv[2])
if data.get("commitSha") != identity["commitSha"]:
    raise SystemExit("candidate lock commit does not match the checked-out candidate")
if data.get("candidateTreeSha256") != identity["candidateTreeSha256"]:
    raise SystemExit("candidate lock tree does not match the exact working-tree bytes")
expected={"workflow-command","workflow-runtime","workflow-query","sync-worker","frontend"}
images=data.get("images", [])
names={image.get("name") for image in images}
if names != expected:
    raise SystemExit(f"candidate lock image inventory mismatch: {sorted(names)}")
dependencies=data.get("dependencies", [])
expected_dependencies={"gateway","postgresql","kafka","debezium-connect","elasticsearch"}
dependency_names={image.get("name") for image in dependencies}
if dependency_names != expected_dependencies:
    raise SystemExit(f"candidate lock dependency inventory mismatch: {sorted(dependency_names)}")
repositories=set()
for image in images + dependencies:
    digest=image.get("digest","")
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        raise SystemExit(f"invalid digest for {image.get('name')}: {digest}")
    repository=image.get("repository","")
    if not repository or repository in repositories:
        raise SystemExit(f"missing or duplicate repository for {image.get('name')}: {repository}")
    repositories.add(repository)
    print(f"{image['name']}\t{image['repository']}\t{digest}")
PY
while IFS=$'\t' read -r name repository digest; do
  reference="${repository}@${digest}"
  raw="$(docker buildx imagetools inspect "${reference}" --raw)"
  actual_digest="$(docker buildx imagetools inspect "${reference}" --format '{{printf "%s" .Manifest.Digest}}')"
  [[ "${actual_digest}" == "${digest}" ]] || {
    echo "${name}: digest mismatch ${actual_digest} != ${digest}" >&2
    exit 1
  }
  platforms="$(python3 -c 'import json,sys; d=json.load(sys.stdin); print("\n".join(sorted({"{}/{}".format(m.get("platform",{}).get("os"),m.get("platform",{}).get("architecture")) for m in d.get("manifests",[])})))' <<<"${raw}")"
  grep -qx "linux/amd64" <<<"${platforms}" || { echo "${name}: linux/amd64 missing" >&2; exit 1; }
  grep -qx "linux/arm64" <<<"${platforms}" || { echo "${name}: linux/arm64 missing" >&2; exit 1; }
  printf '%s %s %s\n' "${name}" "${digest}" "$(tr '\n' ',' <<<"${platforms}")"
done | tee "${log}"

bash "${root}/scripts/release/record_evidence.sh" REL-002 pass "${started_at}" \
  "${lock}" "${log}"
