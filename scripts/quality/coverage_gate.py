#!/usr/bin/env python3
"""Enforce statement coverage floors from a Go coverprofile."""

from __future__ import annotations

import json
import sys
from collections import defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CONFIG = json.loads((Path(__file__).with_name("coverage_thresholds.json")).read_text())
MODULE_PREFIX = "github.com/artificialflow/artificialflow/"


def percentage(covered: int, total: int) -> float:
    return 100.0 * covered / total if total else 0.0


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: coverage_gate.py <coverprofile>", file=sys.stderr)
        return 2
    profile = Path(sys.argv[1])
    totals: dict[str, list[int]] = defaultdict(lambda: [0, 0])
    aggregate = [0, 0]
    excluded = tuple(CONFIG["excluded_prefixes"])

    for line in profile.read_text().splitlines():
        if not line or line.startswith("mode:"):
            continue
        location, statements, count = line.rsplit(" ", 2)
        source = location.split(":", 1)[0]
        if source.startswith(excluded):
            continue
        package = source.rsplit("/", 1)[0]
        if package.startswith(MODULE_PREFIX):
            package = package[len(MODULE_PREFIX) :]
        statement_count = int(statements)
        covered = statement_count if int(count) > 0 else 0
        totals[package][0] += covered
        totals[package][1] += statement_count
        aggregate[0] += covered
        aggregate[1] += statement_count

    failures: list[str] = []
    measured: dict[str, float] = {}
    for package, floor in CONFIG["packages"].items():
        covered, total = totals[package]
        value = percentage(covered, total)
        measured[package] = value
        print(f"{package}: {value:.1f}% (floor {floor:.1f}%)")
        if total == 0 or value + 1e-9 < floor:
            failures.append(f"{package}: {value:.1f}% < {floor:.1f}%")

    aggregate_value = percentage(*aggregate)
    aggregate_floor = float(CONFIG["aggregate_min"])
    print(
        f"aggregate (generated protobuf excluded): {aggregate_value:.1f}% "
        f"(floor {aggregate_floor:.1f}%)"
    )
    if aggregate_value + 1e-9 < aggregate_floor:
        failures.append(
            f"aggregate: {aggregate_value:.1f}% < {aggregate_floor:.1f}%"
        )

    report = ROOT / "reports" / "quality-coverage.json"
    report.parent.mkdir(parents=True, exist_ok=True)
    report.write_text(
        json.dumps(
            {
                "aggregate": aggregate_value,
                "aggregate_floor": aggregate_floor,
                "packages": measured,
                "package_floors": CONFIG["packages"],
                "generated_protobuf_excluded": True,
            },
            indent=2,
        )
        + "\n"
    )

    if failures:
        print("coverage gate failed:", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
