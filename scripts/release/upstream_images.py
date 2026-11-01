#!/usr/bin/env python3
"""Pin checks for community images that this repository does not build."""

from __future__ import annotations

import json
import sys
from dataclasses import dataclass
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PINS = ROOT / "release" / "upstream-images.tsv"


@dataclass(frozen=True)
class Pin:
    name: str
    repository: str
    tag: str
    locked: bool

    @property
    def reference(self) -> str:
        return f"{self.repository}:{self.tag}"


def load_pins(path: Path = PINS) -> list[Pin]:
    pins: list[Pin] = []
    seen: set[str] = set()
    for line_number, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split("\t")
        if len(parts) != 4:
            raise SystemExit(f"{path}:{line_number}: expected name, repository, tag, locked")
        name, repository, tag, locked = parts
        if locked not in {"yes", "no"}:
            raise SystemExit(f"{path}:{line_number}: locked must be yes or no")
        if name in seen:
            raise SystemExit(f"{path}:{line_number}: duplicate image {name}")
        if not repository or not tag or " " in tag:
            raise SystemExit(f"{path}:{line_number}: repository and tag are required")
        seen.add(name)
        pins.append(Pin(name, repository, tag, locked == "yes"))
    if not pins:
        raise SystemExit(f"{path}: no community images are pinned")
    return pins


def short_repository(repository: str) -> str:
    if repository.startswith("docker.io/library/"):
        return repository.removeprefix("docker.io/library/")
    if repository.startswith("docker.io/"):
        return repository.removeprefix("docker.io/")
    return repository


def _require(text: str, needle: str, source: str) -> None:
    if needle not in text:
        raise SystemExit(f"{source} does not use the community pin {needle}")


def assert_sources_match(root: Path = ROOT, pins: list[Pin] | None = None) -> None:
    """Fail when Compose, Helm, or a script drifts from the community pin file."""
    pins = pins if pins is not None else load_pins(root / "release" / "upstream-images.tsv")
    by_name = {pin.name: pin for pin in pins}
    required = {
        "gateway",
        "postgresql",
        "kafka",
        "debezium-connect",
        "elasticsearch",
        "compose-gateway",
    }
    missing = required.difference(by_name)
    if missing:
        raise SystemExit(f"community pin file is missing {sorted(missing)}")

    values = (root / "charts/artificialflow/values.yaml").read_text(encoding="utf-8")
    compose = (root / "docker-compose.yml").read_text(encoding="utf-8")
    migrations = (root / "scripts/test_schema_migrations.sh").read_text(encoding="utf-8")
    gateway_check = (root / "scripts/validate_gateway_security.sh").read_text(encoding="utf-8")

    gateway = by_name["gateway"]
    postgres = by_name["postgresql"]
    kafka = by_name["kafka"]
    connect = by_name["debezium-connect"]
    search = by_name["elasticsearch"]
    compose_gateway = by_name["compose-gateway"]

    _require(values, short_repository(gateway.repository), "charts/artificialflow/values.yaml")
    _require(values, f'tag: "{gateway.tag}"', "charts/artificialflow/values.yaml")
    _require(
        values,
        f"image: {{repository: {short_repository(postgres.repository)}, tag: {postgres.tag}}}",
        "charts/artificialflow/values.yaml",
    )
    _require(
        values,
        f'image: {{repository: {short_repository(search.repository)}, tag: "{search.tag}"}}',
        "charts/artificialflow/values.yaml",
    )
    _require(
        values,
        f'image: {{repository: {short_repository(kafka.repository)}, tag: "{kafka.tag}"}}',
        "charts/artificialflow/values.yaml",
    )
    _require(
        values,
        f'image: {{repository: {short_repository(connect.repository)}, tag: "{connect.tag}"}}',
        "charts/artificialflow/values.yaml",
    )
    _require(compose, f"image: {short_repository(postgres.repository)}:{postgres.tag}", "docker-compose.yml")
    _require(compose, f"image: {short_repository(search.repository)}:{search.tag}", "docker-compose.yml")
    _require(compose, f"image: {short_repository(kafka.repository)}:{kafka.tag}", "docker-compose.yml")
    _require(compose, f"image: {short_repository(connect.repository)}:{connect.tag}", "docker-compose.yml")
    _require(
        compose,
        f"image: {short_repository(compose_gateway.repository)}:{compose_gateway.tag}",
        "docker-compose.yml",
    )
    _require(migrations, f"{short_repository(postgres.repository)}:{postgres.tag}", "scripts/test_schema_migrations.sh")
    _require(
        gateway_check,
        f"{short_repository(compose_gateway.repository)}:{compose_gateway.tag}",
        "scripts/validate_gateway_security.sh",
    )


def lock_pairs(lock_path: Path, pins: list[Pin] | None = None) -> list[tuple[str, str, str, str]]:
    """Return locked community images as name, repository, tag, lock digest."""
    pins = pins if pins is not None else load_pins()
    locked = {pin.name: pin for pin in pins if pin.locked}
    data = json.loads(lock_path.read_text(encoding="utf-8"))
    dependencies = data.get("dependencies")
    if not isinstance(dependencies, list):
        raise SystemExit("candidate lock is missing dependencies")
    found: dict[str, str] = {}
    for image in dependencies:
        name = image.get("name")
        repository = image.get("repository")
        digest = image.get("digest")
        if name not in locked:
            raise SystemExit(f"candidate lock has an unexpected dependency {name}")
        pin = locked[name]
        if repository != pin.repository:
            raise SystemExit(
                f"{name} must be upgraded to {pin.reference}; lock has {repository}"
            )
        if not isinstance(digest, str) or not digest.startswith("sha256:"):
            raise SystemExit(f"{name} lock digest is invalid")
        found[name] = digest
    missing = sorted(set(locked).difference(found))
    if missing:
        raise SystemExit(f"candidate lock is missing community images: {missing}")
    return [
        (pin.name, pin.repository, pin.tag, found[pin.name])
        for pin in pins
        if pin.locked
    ]


def scan_plan(lock_path: Path, pins: list[Pin] | None = None) -> list[tuple[str, str]]:
    """Return blocking application scans, then recorded community scans."""
    pins = pins if pins is not None else load_pins()
    data = json.loads(lock_path.read_text(encoding="utf-8"))
    plan: list[tuple[str, str]] = []
    for image in data.get("images") or []:
        plan.append(("blocking", f"{image['repository']}@{image['digest']}"))
    digests = {name: digest for name, _repository, _tag, digest in lock_pairs(lock_path, pins)}
    for pin in pins:
        if pin.locked:
            plan.append(("report", f"{pin.repository}@{digests[pin.name]}"))
        else:
            plan.append(("report", pin.reference))
    return plan


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        raise SystemExit("usage: upstream_images.py check-sources|lock-lines|scan-plan [lock]")
    command = argv[1]
    if command == "check-sources":
        assert_sources_match()
        print("community image pins match Compose, Helm, and the gateway check")
        return 0
    if command not in {"lock-lines", "scan-plan"}:
        raise SystemExit(f"unknown command {command}")
    if len(argv) != 3:
        raise SystemExit(f"usage: upstream_images.py {command} LOCK")
    lock = Path(argv[2])
    if command == "lock-lines":
        for name, repository, tag, digest in lock_pairs(lock):
            print(f"{name}\t{repository}\t{tag}\t{digest}")
        return 0
    for mode, reference in scan_plan(lock):
        print(f"{mode}\t{reference}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
