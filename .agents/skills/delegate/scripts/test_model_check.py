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
        self.write_records("591787e1-4bf0-4dc0-abd8-ecada0cdb716/subagents/agent-1.jsonl", [records[1]])

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
        self.write_records("591787e1-4bf0-4dc0-abd8-ecada0cdb716/subagents/agent-1.jsonl", [
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

    def test_resolves_session_directory_from_home_when_claude_config_dir_is_unset(self):
        env = dict(self.env)
        env.pop("CLAUDE_CONFIG_DIR", None)
        home = self.root / "home"
        session_dir = home / ".claude" / "projects" / re.sub(r"[^a-zA-Z0-9]", "-", str(self.lane))
        session_dir.mkdir(parents=True)
        path = session_dir / "session.jsonl"
        path.write_text(json.dumps(self.record("2026-09-30T10:05:00Z", "claude-opus-5-5")) + "\n")

        result = subprocess.run(
            [sys.executable, str(SCRIPT), str(self.lane), "claude-opus-5-5", "2026-09-30T10:00:00Z"],
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "")

    def test_skips_files_whose_mtime_is_before_since(self):
        old_file = self.session_dir / "old_session.jsonl"
        self.write_records("old_session.jsonl", [
            self.record("2026-09-30T10:05:00Z", "claude-opus-4-8"),
        ])
        old_mtime = 1790762400.0
        os.utime(old_file, (old_mtime - 100, old_mtime - 100))

        self.write_records("session.jsonl", [
            self.record("2026-09-30T10:05:00Z", "claude-opus-5-5"),
        ])

        result = self.run_check()

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "")

    def test_ignores_non_jsonl_files_and_files_under_session_uuid_directories(self):
        (self.session_dir / ".DS_Store").write_text("not json")
        tool_results = self.session_dir / "591787e1-4bf0-4dc0-abd8-ecada0cdb716" / "tool-results"
        tool_results.mkdir(parents=True)
        (tool_results / "output.txt").write_text("plain text")

        self.write_records("session.jsonl", [
            self.record("2026-09-30T10:05:00Z", "claude-opus-5-5"),
        ])

        result = self.run_check()

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "")


if __name__ == "__main__":
    unittest.main()
