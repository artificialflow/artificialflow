#!/usr/bin/env python3
"""Ratchet maintainability debt without demanding a one-shot rewrite."""

from __future__ import annotations

import hashlib
import json
import re
import sys
from collections import defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CONFIG = json.loads((Path(__file__).with_name("maintainability.json")).read_text())
SOURCE_ROOTS = ("backend", "connectors", "frontend/src", "clients/nodejs-sdk/src")
SOURCE_SUFFIXES = {".go", ".ts", ".tsx", ".js", ".mjs"}
DEBT_PATTERN = re.compile(r"\b(TODO|FIXME|HACK)\b")
DEBT_REFERENCE = re.compile(r"\bDEBT\(([A-Z]+-\d+)\)")
GO_FUNCTION = re.compile(r"^func\s+(?:\([^)]*\)\s*)?([A-Za-z0-9_]+)\s*\(")
TS_FUNCTION = re.compile(
    r"^\s*(?:export\s+)?(?:async\s+)?(?:function\s+([A-Za-z0-9_]+)"
    r"|(?:public\s+|private\s+|protected\s+)?(?:async\s+)?([A-Za-z0-9_]+)"
    r"\s*\([^;]*\)\s*(?::[^=]+)?\{)"
)


def relative(path: Path) -> str:
    return path.relative_to(ROOT).as_posix()


def generated(path: Path) -> bool:
    name = "/" + relative(path)
    return any(marker in name for marker in CONFIG["generated_path_markers"])


def source_files() -> list[Path]:
    files: list[Path] = []
    for root_name in SOURCE_ROOTS:
        root = ROOT / root_name
        if not root.exists():
            continue
        files.extend(
            path
            for path in root.rglob("*")
            if path.is_file() and path.suffix in SOURCE_SUFFIXES and not generated(path)
        )
    return sorted(files)


def function_spans(path: Path, lines: list[str]) -> list[tuple[str, int]]:
    pattern = GO_FUNCTION if path.suffix == ".go" else TS_FUNCTION
    spans: list[tuple[str, int]] = []
    index = 0
    while index < len(lines):
        match = pattern.match(lines[index])
        if not match:
            index += 1
            continue
        name = next(group for group in match.groups() if group)
        depth = 0
        started = False
        end = index
        while end < len(lines):
            depth += lines[end].count("{") - lines[end].count("}")
            started = started or "{" in lines[end]
            if started and depth <= 0:
                break
            end += 1
        spans.append((name, end - index + 1))
        index = max(index + 1, end + 1)
    return spans


def normalized_duplicate_blocks(files: list[Path]) -> int:
    block_size = int(CONFIG["duplicate_block_lines"])
    excluded = tuple(CONFIG["duplicate_exclusions"])
    occurrences: dict[str, set[str]] = defaultdict(set)
    for path in files:
        rel = relative(path)
        if rel.endswith("_test.go") or ".test." in rel or rel.startswith(excluded):
            continue
        normalized = [
            re.sub(r"\s+", " ", line.strip())
            for line in path.read_text(errors="ignore").splitlines()
            if line.strip() and not line.lstrip().startswith(("//", "/*", "*"))
        ]
        for index in range(0, len(normalized) - block_size + 1):
            block = "\n".join(normalized[index : index + block_size])
            digest = hashlib.sha256(block.encode()).hexdigest()
            occurrences[digest].add(rel)
    return sum(1 for paths in occurrences.values() if len(paths) > 1)


def registered_debt() -> set[str]:
    register = ROOT / "docs" / "ENGINEERING_DEBT.md"
    if not register.exists():
        return set()
    return set(re.findall(r"\bDEBT-[0-9]+\b", register.read_text(errors="ignore")))


def main() -> int:
    files = source_files()
    failures: list[str] = []
    debt_ids = registered_debt()

    for path in files:
        rel = relative(path)
        lines = path.read_text(errors="ignore").splitlines()
        is_test = rel.endswith("_test.go") or ".test." in rel
        default_limit = CONFIG["max_test_file_lines"] if is_test else CONFIG["max_file_lines"]
        limit = CONFIG["file_allowlist"].get(rel, default_limit)
        if len(lines) > limit:
            failures.append(f"{rel}: {len(lines)} lines exceeds ratchet {limit}")

        if not is_test:
            for name, size in function_spans(path, lines):
                key = f"{rel}:{name}"
                function_limit = CONFIG["function_allowlist"].get(
                    key, CONFIG["max_function_lines"]
                )
                if size > function_limit:
                    failures.append(
                        f"{key}: {size} lines exceeds ratchet {function_limit}"
                    )

        for line_number, line in enumerate(lines, 1):
            if not DEBT_PATTERN.search(line):
                continue
            reference = DEBT_REFERENCE.search(line)
            if reference is None:
                failures.append(
                    f"{rel}:{line_number}: actionable debt needs DEBT(<id>) reference"
                )
            elif reference.group(1) not in debt_ids:
                failures.append(
                    f"{rel}:{line_number}: {reference.group(1)} missing from debt register"
                )

    duplicate_blocks = normalized_duplicate_blocks(files)
    if duplicate_blocks > CONFIG["max_duplicate_blocks"]:
        failures.append(
            f"duplicate blocks: {duplicate_blocks} exceeds ratchet "
            f"{CONFIG['max_duplicate_blocks']}"
        )

    if failures:
        print("maintainability gate failed:", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return 1

    print(
        f"maintainability gate passed: {len(files)} files, "
        f"{duplicate_blocks} cross-file duplicate blocks"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
