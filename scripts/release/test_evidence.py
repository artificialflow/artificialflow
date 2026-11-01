#!/usr/bin/env python3
import datetime as dt
import hashlib
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).with_name("evidence.py")
SPEC = importlib.util.spec_from_file_location("release_evidence", MODULE_PATH)
assert SPEC and SPEC.loader
evidence = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(evidence)


class EvidenceFailClosedTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.directory = Path(self.temp.name)
        self.now = dt.datetime.now(dt.timezone.utc)
        self.sha = "a" * 40
        self.gate = {
            "id": "REL-001",
            "maxAgeHours": 24,
            "fixture": {"kind": "git-tree", "identity": "GITHUB_SHA"},
        }

    def tearDown(self):
        self.temp.cleanup()

    def write(self, **changes):
        artifact = self.directory / "artifact.txt"
        artifact.write_text("tested bytes", encoding="utf-8")
        payload = {
            "schemaVersion": 1,
            "gateId": "REL-001",
            "status": "pass",
            "commitSha": self.sha,
            "attempts": 1,
            "startedAt": (self.now - dt.timedelta(seconds=1)).isoformat(),
            "completedAt": self.now.isoformat(),
            "artifacts": [{
                "path": str(artifact),
                "sha256": hashlib.sha256(artifact.read_bytes()).hexdigest(),
            }],
        }
        payload.update(changes)
        (self.directory / "REL-001.json").write_text(json.dumps(payload), encoding="utf-8")

    def inspect(self):
        return evidence.inspect_evidence(self.gate, self.directory, self.sha, self.now)[0]

    def test_clean_pass(self):
        self.write()
        self.assertEqual(self.inspect(), "PASS")

    def test_missing_blocks(self):
        self.assertEqual(self.inspect(), "MISSING")

    def test_skipped_warn_and_flaky_statuses_block(self):
        for status in ("skipped", "warn", "flaky"):
            with self.subTest(status=status):
                self.write(status=status)
                self.assertEqual(self.inspect(), "FAIL")

    def test_retry_is_flaky_and_blocks(self):
        self.write(attempts=2)
        self.assertEqual(self.inspect(), "FAIL")

    def test_stale_blocks(self):
        self.write(completedAt=(self.now - dt.timedelta(hours=25)).isoformat())
        self.assertEqual(self.inspect(), "FAIL")

    def test_commit_sha_mismatch_blocks(self):
        self.write(commitSha="b" * 40)
        self.assertEqual(self.inspect(), "FAIL")

    def test_artifact_sha_mismatch_blocks(self):
        artifact = self.directory / "artifact.txt"
        artifact.write_text("tested bytes", encoding="utf-8")
        self.write(artifacts=[{"path": str(artifact), "sha256": "0" * 64}])
        self.assertEqual(self.inspect(), "FAIL")

    def test_empty_artifacts_block(self):
        self.write(artifacts=[])
        self.assertEqual(self.inspect(), "FAIL")

    def test_invalid_schema_blocks(self):
        self.write(schemaVersion=2)
        self.assertEqual(self.inspect(), "FAIL")

    def test_candidate_tree_mismatch_blocks(self):
        self.write(candidateTreeSha256="b" * 64)
        status = evidence.inspect_evidence(
            self.gate, self.directory, self.sha, self.now, "a" * 64
        )[0]
        self.assertEqual(status, "FAIL")


if __name__ == "__main__":
    unittest.main()
