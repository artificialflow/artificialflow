#!/usr/bin/env python3
"""Fail closed on mutation reports, including timeout/tool edge cases."""

from __future__ import annotations

import json
import sys
from pathlib import Path


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: mutation_gate.py <report>", file=sys.stderr)
        return 2
    report = json.loads(Path(sys.argv[1]).read_text())
    mutations = [
        mutation
        for file_result in report.get("files", [])
        for mutation in file_result.get("mutations", [])
    ]
    bad = [mutation for mutation in mutations if mutation.get("status") != "KILLED"]
    efficacy = float(report.get("test_efficacy", 0))
    coverage = float(report.get("mutations_coverage", 0))
    failures: list[str] = []
    if not mutations:
        failures.append("no mutants were evaluated")
    if bad:
        failures.extend(
            f"{mutation.get('file_name', '')}{mutation.get('type')} "
            f"line {mutation.get('line')}: {mutation.get('status')}"
            for mutation in bad
        )
    if efficacy < 0.80:
        failures.append(f"efficacy {efficacy:.2f} is below 0.80")
    if coverage < 0.80:
        failures.append(f"mutant coverage {coverage:.2f} is below 0.80")
    if failures:
        print("mutation gate failed closed:", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return 1
    print(
        f"mutation gate passed: {len(mutations)} killed, "
        f"efficacy={efficacy:.2f}, coverage={coverage:.2f}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
