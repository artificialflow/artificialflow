#!/usr/bin/env python3
"""Validate release gate policy and generate a fail-closed evidence report."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import stat
import subprocess
import sys
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
DEFAULT_MANIFEST = ROOT / "release" / "evidence-gates.json"
WAIVERS = ROOT / "release" / "waivers.json"
INVENTORY = ROOT / "release" / "artifact-inventory.json"
REQUIRED_GATE_FIELDS = {
    "id",
    "title",
    "command",
    "job",
    "requiredEnvironment",
    "fixture",
    "artifact",
    "timeoutSeconds",
    "retries",
    "threshold",
    "blocking",
    "retentionDays",
    "maxAgeHours",
}
BAD_STATUSES = {"missing", "stale", "skipped", "warn", "warning", "flaky", "sha-mismatch"}
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
ID_RE = re.compile(r"^REL-[0-9]{3}$")


def load_json(path: Path) -> dict[str, Any]:
    with path.open(encoding="utf-8") as handle:
        value = json.load(handle)
    if not isinstance(value, dict):
        raise ValueError(f"{path}: expected a JSON object")
    return value


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def parse_time(value: str) -> dt.datetime:
    parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=dt.timezone.utc)
    return parsed.astimezone(dt.timezone.utc)


def git_sha() -> str:
    return subprocess.check_output(
        ["git", "-C", str(ROOT), "rev-parse", "HEAD"], text=True
    ).strip()


def candidate_identity() -> dict[str, Any]:
    """Hash the exact tracked and untracked, non-ignored working-tree bytes."""
    commit = git_sha()
    paths = subprocess.check_output(
        [
            "git", "-C", str(ROOT), "ls-files", "-z", "--cached", "--others",
            "--exclude-standard",
        ]
    ).split(b"\0")
    digest = hashlib.sha256()
    file_count = 0
    for raw_path in sorted(path for path in paths if path):
        relative = raw_path.decode("utf-8", errors="surrogateescape")
        path = ROOT / relative
        digest.update(len(raw_path).to_bytes(8, "big"))
        digest.update(raw_path)
        if not path.exists() and not path.is_symlink():
            digest.update(b"deleted\0")
            file_count += 1
            continue
        mode = stat.S_IMODE(path.lstat().st_mode)
        digest.update(f"{mode:o}\0".encode())
        if path.is_symlink():
            content = os.readlink(path).encode("utf-8", errors="surrogateescape")
        elif path.is_file():
            content = path.read_bytes()
        else:
            content = b"<non-file>"
        digest.update(len(content).to_bytes(8, "big"))
        digest.update(content)
        file_count += 1
    dirty = bool(
        subprocess.check_output(
            ["git", "-C", str(ROOT), "status", "--porcelain=v1", "-z"]
        )
    )
    return {
        "commitSha": commit,
        "candidateTreeSha256": digest.hexdigest(),
        "dirty": dirty,
        "fileCount": file_count,
    }


def validate_manifest(manifest: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    if manifest.get("schemaVersion") != 1:
        errors.append("schemaVersion must be 1")
    gates = manifest.get("gates")
    if not isinstance(gates, list) or not gates:
        return errors + ["gates must be a non-empty array"]
    seen: set[str] = set()
    for index, gate in enumerate(gates):
        label = gate.get("id", f"index {index}") if isinstance(gate, dict) else f"index {index}"
        if not isinstance(gate, dict):
            errors.append(f"{label}: gate must be an object")
            continue
        missing = REQUIRED_GATE_FIELDS - gate.keys()
        if missing:
            errors.append(f"{label}: missing fields {', '.join(sorted(missing))}")
        gate_id = gate.get("id", "")
        if not ID_RE.fullmatch(gate_id):
            errors.append(f"{label}: id must match REL-NNN")
        if gate_id in seen:
            errors.append(f"{label}: duplicate immutable gate id")
        seen.add(gate_id)
        if not isinstance(gate.get("blocking"), bool):
            errors.append(f"{label}: blocking must be boolean")
        for field in ("timeoutSeconds", "retentionDays", "maxAgeHours"):
            if not isinstance(gate.get(field), int) or gate.get(field, 0) <= 0:
                errors.append(f"{label}: {field} must be a positive integer")
        if not isinstance(gate.get("retries"), int) or gate.get("retries", -1) < 0:
            errors.append(f"{label}: retries must be a non-negative integer")
        fixture = gate.get("fixture", {})
        if isinstance(fixture, dict) and fixture.get("path"):
            fixture_path = ROOT / fixture["path"]
            if not fixture_path.is_file():
                errors.append(f"{label}: fixture path does not exist: {fixture['path']}")
            expected = fixture.get("sha256")
            if expected and (not SHA256_RE.fullmatch(expected) or sha256(fixture_path) != expected):
                errors.append(f"{label}: fixture SHA-256 mismatch")
    return errors


def validate_waivers(valid_gate_ids: set[str]) -> list[str]:
    errors: list[str] = []
    data = load_json(WAIVERS)
    waivers = data.get("waivers")
    if data.get("schemaVersion") != 1 or not isinstance(waivers, list):
        return ["waivers.json must use schemaVersion 1 and a waivers array"]
    required = {
        "id", "gateId", "owner", "reason", "compensatingControl", "approvedBy",
        "createdAt", "expiresAt", "scope",
    }
    seen: set[str] = set()
    for waiver in waivers:
        waiver_id = waiver.get("id", "<missing>")
        missing = required - waiver.keys()
        if missing:
            errors.append(f"{waiver_id}: missing waiver fields {', '.join(sorted(missing))}")
            continue
        if waiver_id in seen:
            errors.append(f"{waiver_id}: duplicate waiver id")
        seen.add(waiver_id)
        if waiver["gateId"] not in valid_gate_ids:
            errors.append(f"{waiver_id}: unknown gateId {waiver['gateId']}")
        if waiver["owner"] == waiver["approvedBy"]:
            errors.append(f"{waiver_id}: owner may not approve their own waiver")
        try:
            created, expires = parse_time(waiver["createdAt"]), parse_time(waiver["expiresAt"])
            if expires <= created or expires - created > dt.timedelta(days=30):
                errors.append(f"{waiver_id}: expiry must be after creation and within 30 days")
            if expires <= dt.datetime.now(dt.timezone.utc):
                errors.append(f"{waiver_id}: waiver is expired")
        except (TypeError, ValueError):
            errors.append(f"{waiver_id}: waiver timestamps must be RFC3339")
        scope = waiver.get("scope", {})
        if not re.fullmatch(r"[0-9a-f]{40}", str(scope.get("commitSha", ""))):
            errors.append(f"{waiver_id}: scope.commitSha must be an exact 40-hex SHA")
        if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?", str(scope.get("releaseTag", ""))):
            errors.append(f"{waiver_id}: scope.releaseTag must be a v-prefixed semantic version")
    return errors


def validate_inventory() -> list[str]:
    errors: list[str] = []
    data = load_json(INVENTORY)
    if data.get("schemaVersion") != 1:
        errors.append("artifact inventory schemaVersion must be 1")
    images = data.get("containerImages", [])
    expected = {"workflow-command", "workflow-runtime", "workflow-query", "sync-worker", "frontend"}
    names = {image.get("name") for image in images}
    if names != expected:
        errors.append(f"artifact inventory image set mismatch: {sorted(names)}")
    for image in images:
        if not all(image.get(field) for field in ("dockerfile", "sbom", "provenance", "signature")):
            errors.append(f"{image.get('name')}: SBOM, provenance, signature, and Dockerfile are required")
        elif not (ROOT / image["dockerfile"]).is_file():
            errors.append(f"{image.get('name')}: Dockerfile does not exist")
    for artifact in data.get("helmCharts", []) + data.get("sdks", []):
        artifact_path = artifact.get("path")
        if not artifact_path or not (ROOT / artifact_path).exists():
            errors.append(f"inventory path does not exist: {artifact_path}")
    notes = [item for item in data.get("releaseMetadata", []) if item.get("name") == "release-notes"]
    if len(notes) != 1 or not (ROOT / notes[0].get("source", "")).is_file():
        errors.append("release notes source must identify one existing file")
    return errors


def inspect_evidence(
    gate: dict[str, Any],
    evidence_dir: Path,
    expected_sha: str,
    now: dt.datetime,
    expected_tree_sha256: str | None = None,
) -> tuple[str, list[str], dict[str, Any] | None]:
    gate_id = gate["id"]
    path = evidence_dir / f"{gate_id}.json"
    problems: list[str] = []
    if not path.is_file():
        return "MISSING", ["required evidence file is missing"], None
    try:
        evidence = load_json(path)
    except (OSError, ValueError, json.JSONDecodeError) as error:
        return "INVALID", [f"cannot parse evidence: {error}"], None

    status = str(evidence.get("status", "missing")).lower()
    if evidence.get("schemaVersion") != 1:
        problems.append("schemaVersion must be 1")
    if status != "pass":
        problems.append(f"status is {status!r}; required status is 'pass'")
    if status in BAD_STATUSES:
        problems.append(f"fail-closed status detected: {status}")
    if evidence.get("gateId") != gate_id:
        problems.append("gateId does not match manifest")
    if evidence.get("commitSha") != expected_sha:
        problems.append(
            f"commit SHA mismatch: evidence={evidence.get('commitSha')} expected={expected_sha}"
        )
    if expected_tree_sha256 and evidence.get("candidateTreeSha256") != expected_tree_sha256:
        problems.append(
            "candidate tree mismatch: "
            f"evidence={evidence.get('candidateTreeSha256')} expected={expected_tree_sha256}"
        )
    if evidence.get("attempts") != 1:
        problems.append("required evidence is flaky or did not record exactly one attempt")
    try:
        started = parse_time(str(evidence["startedAt"]))
        completed = parse_time(str(evidence["completedAt"]))
        if completed < started:
            problems.append("completedAt precedes startedAt")
        age = now - completed
        if age.total_seconds() < -300:
            problems.append("completion timestamp is in the future")
        if age > dt.timedelta(hours=gate["maxAgeHours"]):
            problems.append(
                f"evidence is stale ({age.total_seconds() / 3600:.1f}h > {gate['maxAgeHours']}h)"
            )
    except (KeyError, TypeError, ValueError):
        problems.append("completedAt is missing or invalid")

    artifacts = evidence.get("artifacts")
    if not isinstance(artifacts, list) or not artifacts:
        problems.append("at least one hashed evidence artifact is required")
        artifacts = []
    for artifact in artifacts:
        if not isinstance(artifact, dict):
            problems.append("artifact entry must be an object")
            continue
        artifact_path = ROOT / str(artifact.get("path", ""))
        expected = str(artifact.get("sha256", ""))
        if not artifact_path.is_file():
            problems.append(f"artifact is missing: {artifact.get('path')}")
        elif not SHA256_RE.fullmatch(expected) or sha256(artifact_path) != expected:
            problems.append(f"artifact SHA-256 mismatch: {artifact.get('path')}")

    fixture = gate.get("fixture", {})
    if fixture.get("path") and evidence.get("fixtureSha256") != fixture.get("sha256"):
        problems.append("fixture SHA-256 does not match the manifest")
    return ("FAIL" if problems else "PASS"), problems, evidence


def report(args: argparse.Namespace) -> int:
    manifest = load_json(args.manifest)
    manifest_errors = validate_manifest(manifest)
    manifest_errors.extend(validate_waivers({gate.get("id", "") for gate in manifest.get("gates", [])}))
    manifest_errors.extend(validate_inventory())
    if manifest_errors:
        for error in manifest_errors:
            print(f"manifest: {error}", file=sys.stderr)
        return 1
    identity = candidate_identity()
    expected_sha = args.sha or os.environ.get("GITHUB_SHA") or identity["commitSha"]
    expected_tree_sha256 = args.tree_sha or identity["candidateTreeSha256"]
    now = dt.datetime.now(dt.timezone.utc)
    rows: list[tuple[str, str, str]] = []
    failed = False
    selected_gates = [
        gate
        for gate in manifest["gates"]
        if args.phase == "all" or gate.get("phase", "pre-publish") == args.phase
    ]
    for gate in selected_gates:
        status, problems, _ = inspect_evidence(
            gate, args.evidence_dir, expected_sha, now, expected_tree_sha256
        )
        detail = "; ".join(problems) if problems else "fresh, single-attempt, SHA-bound evidence"
        rows.append((gate["id"], status, detail))
        if gate["blocking"] and status != "PASS":
            failed = True

    output = [
        "# Release Evidence Report",
        "",
        f"- Manifest: `{manifest['manifestId']}`",
        f"- Commit: `{expected_sha}`",
        f"- Candidate tree SHA-256: `{expected_tree_sha256}`",
        f"- Uncommitted candidate: `{'yes' if identity['dirty'] else 'no'}`",
        f"- Generated: `{now.isoformat().replace('+00:00', 'Z')}`",
        f"- Decision: **{'BLOCK' if failed else 'PASS'}**",
        "",
        "| Gate | Result | Detail |",
        "|---|---|---|",
    ]
    output.extend(f"| {gate_id} | {status} | {detail.replace('|', '/')} |" for gate_id, status, detail in rows)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text("\n".join(output) + "\n", encoding="utf-8")
    print(f"release evidence decision: {'BLOCK' if failed else 'PASS'} ({args.output})")
    return 1 if failed else 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", type=Path, default=DEFAULT_MANIFEST)
    subparsers = parser.add_subparsers(dest="command", required=True)
    subparsers.add_parser("validate-manifest")
    subparsers.add_parser("candidate-identity")
    report_parser = subparsers.add_parser("report")
    report_parser.add_argument("--evidence-dir", type=Path, default=ROOT / "reports" / "evidence")
    report_parser.add_argument("--output", type=Path, default=ROOT / "reports" / "release-evidence.md")
    report_parser.add_argument("--sha")
    report_parser.add_argument("--tree-sha")
    report_parser.add_argument(
        "--phase", choices=("pre-publish", "post-publish", "all"), default="all"
    )
    args = parser.parse_args()
    manifest = load_json(args.manifest)
    if args.command == "validate-manifest":
        errors = validate_manifest(manifest)
        errors.extend(validate_waivers({gate.get("id", "") for gate in manifest.get("gates", [])}))
        errors.extend(validate_inventory())
        if errors:
            for error in errors:
                print(error, file=sys.stderr)
            return 1
        print(f"release evidence manifest valid ({len(manifest['gates'])} immutable gates)")
        return 0
    if args.command == "candidate-identity":
        print(json.dumps(candidate_identity(), sort_keys=True))
        return 0
    return report(args)


if __name__ == "__main__":
    raise SystemExit(main())
