#!/usr/bin/env python3
import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).with_name("upstream_images.py")
SPEC = importlib.util.spec_from_file_location("upstream_images", MODULE_PATH)
assert SPEC and SPEC.loader
upstream = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = upstream
SPEC.loader.exec_module(upstream)


def pin(name, repository, tag, locked=True):
    return upstream.Pin(name, repository, tag, locked)


class UpstreamImageTests(unittest.TestCase):
    def test_repository_sources_match_the_pin_file(self):
        upstream.assert_sources_match()

    def test_lock_rejects_an_older_community_repository(self):
        pins = [pin("kafka", "docker.io/apache/kafka", "4.4.0")]
        with tempfile.TemporaryDirectory() as directory:
            lock = Path(directory) / "lock.json"
            lock.write_text(json.dumps({
                "dependencies": [{
                    "name": "kafka",
                    "repository": "docker.io/confluentinc/cp-kafka",
                    "digest": "sha256:" + "a" * 64,
                }],
            }), encoding="utf-8")
            with self.assertRaises(SystemExit) as caught:
                upstream.lock_pairs(lock, pins)
        self.assertIn("must be upgraded", str(caught.exception))

    def test_scan_plan_blocks_only_application_images(self):
        pins = [
            pin("kafka", "docker.io/apache/kafka", "4.4.0"),
            pin("compose-gateway", "docker.io/library/nginx", "1.31.7-alpine", locked=False),
        ]
        digest = "sha256:" + "b" * 64
        with tempfile.TemporaryDirectory() as directory:
            lock = Path(directory) / "lock.json"
            lock.write_text(json.dumps({
                "images": [{
                    "name": "frontend",
                    "repository": "docker.io/artificialflow/frontend",
                    "digest": digest,
                }],
                "dependencies": [{
                    "name": "kafka",
                    "repository": "docker.io/apache/kafka",
                    "digest": digest,
                }],
            }), encoding="utf-8")
            plan = upstream.scan_plan(lock, pins)
        self.assertEqual(plan, [
            ("blocking", f"docker.io/artificialflow/frontend@{digest}"),
            ("report", f"docker.io/apache/kafka@{digest}"),
            ("report", "docker.io/library/nginx:1.31.7-alpine"),
        ])


if __name__ == "__main__":
    unittest.main()
