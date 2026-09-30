import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("model_check.py")
FIXTURE = Path(__file__).with_name("testdata") / "fallback-records.jsonl"


class ModelCheckTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config = self.root / "claude config"
        self.lane = self.root / "lane work" / "p2_fix"
        self.lane.mkdir(parents=True)
        self.session_dir = self.config / "projects" / re.sub(r"[^a-zA-Z0-9]", "-", str(self.lane))
        self.session_dir.mkdir(parents=True)
        self.env = {
            **os.environ,
            "CLAUDE_CONFIG_DIR": str(self.config),
            "HOME": str(self.root / "home"),
        }

    def record(self, timestamp, model, record_type="assistant"):
        if record_type == "assistant":
            return {"type": "assistant", "timestamp": timestamp, "message": {"model": model}}
        return {"type": record_type, "timestamp": timestamp, "message": {"model": model}}

    def write_records(self, relative, records):
        path = self.session_dir / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("".join(json.dumps(record) + "\n" for record in records))

    def run_check(self, expected="claude-opus-5-5", since="2026-09-30T10:00:00Z"):
        return subprocess.run(
            [sys.executable, str(SCRIPT), str(self.lane), expected, since],
            env=self.env,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_rejects_another_model_in_a_top_level_session_file(self):
        self.write_records("session.jsonl", [
            self.record("2026-09-30T10:05:00Z", "claude-opus-4-8"),
        ])

        result = self.run_check()

        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("claude-opus-4-8", result.stdout)
        self.assertIn("claude-opus-5-5", result.stdout)

    def test_rejects_a_fallback_record_in_a_subagent_file(self):
        records = [json.loads(line) for line in FIXTURE.read_text().splitlines()]
        self.write_records("session.jsonl", [records[0]])
        self.write_records("subagents/agent.jsonl", [records[1]])

        result = self.run_check(
            expected="claude-fable-5-1",
            since="2026-09-19T11:38:00Z",
        )

        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("model_refusal_fallback", result.stdout)
        self.assertIn("claude-fable-5-1", result.stdout)
        self.assertIn("claude-opus-4-8", result.stdout)

    def test_accepts_prior_records_dated_model_suffix_synthetic_and_subagent_models(self):
        self.write_records("session.jsonl", [
            self.record("2026-09-30T09:00:00Z", "claude-opus-4-8"),
            self.record("2026-09-30T10:05:00Z", "claude-haiku-4-5-20251001"),
            self.record("2026-09-30T10:06:00Z", "<synthetic>"),
        ])
        self.write_records("subagents/agent.jsonl", [
            self.record("2026-09-30T10:07:00Z", "claude-opus-4-8"),
        ])

        result = self.run_check(
            expected="claude-haiku-4-5",
            since="2026-09-30T10:00:00Z",
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "")

    def test_rejects_when_no_qualifying_session_file_exists(self):
        self.write_records("session.jsonl", [
            self.record("2026-09-30T09:00:00Z", "claude-opus-4-8"),
        ])

        result = self.run_check()

        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("no qualifying session file", result.stdout)
        self.assertIn(str(self.session_dir), result.stdout)


if __name__ == "__main__":
    unittest.main()
