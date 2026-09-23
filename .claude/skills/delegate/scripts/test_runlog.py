import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import runlog


SCRIPT = Path(__file__).with_name("runlog.py")


class RunlogTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.log = Path(self.temp.name) / "runs.jsonl"
        self.env = {**os.environ, "FLOWSEER_RUNLOG": str(self.log)}

    def command(self, *args):
        return subprocess.run(
            [sys.executable, str(SCRIPT), *args],
            env=self.env, capture_output=True, text=True, check=False,
        )

    def test_start_writes_lane_and_prints_run(self):
        result = self.command(
            "start", "--lane", "l1", "--cli", "codex", "--model", "gpt-6-sol",
            "--role", "execute", "--worktree", "/w/l1", "--branch", "u/l1",
            "--base", "abc123",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        events = [json.loads(line) for line in self.log.read_text().splitlines()]
        self.assertEqual(len(events), 1)
        event = events[0]
        self.assertEqual(result.stdout.strip(), event["run"])
        self.assertEqual(event["event"], "start")
        self.assertEqual(event["v"], 1)
        self.assertEqual(
            {key: event[key] for key in ("lane", "cli", "model", "role", "worktree", "branch", "base")},
            {"lane": "l1", "cli": "codex", "model": "gpt-6-sol", "role": "execute",
             "worktree": "/w/l1", "branch": "u/l1", "base": "abc123"},
        )

    def test_invalid_role_writes_nothing(self):
        result = self.command(
            "start", "--lane", "l1", "--cli", "codex", "--model", "gpt-6-sol",
            "--role", "Execute", "--worktree", "/w/l1", "--branch", "u/l1",
            "--base", "abc123",
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.log.exists())

    def test_parallel_review_writes_complete_lines(self):
        processes = [subprocess.Popen(
            [sys.executable, str(SCRIPT), "review", "--model", "claude-opus-5-5",
             "--role", "review-unit", "--agent", f"a{index}",
             "--findings", "4", "--held", "3", "--unverified", "1"],
            env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        ) for index in range(20)]
        for process in processes:
            _, stderr = process.communicate(timeout=10)
            self.assertEqual(process.returncode, 0, stderr)
        events = [json.loads(line) for line in self.log.read_text().splitlines()]
        self.assertEqual(len(events), 20)
        self.assertEqual({event["agent"] for event in events}, {f"a{i}" for i in range(20)})

    def test_review_counts_are_validated(self):
        result = self.command(
            "review", "--model", "claude-opus-5-5", "--role", "review-unit",
            "--agent", "a1", "--findings", "4", "--held", "3", "--unverified", "1",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        event = json.loads(self.log.read_text().splitlines()[0])
        self.assertEqual(
            (event["model"], event["role"], event["agent"],
             event["findings"], event["held"], event["unverified"]),
            ("claude-opus-5-5", "review-unit", "a1", 4, 3, 1),
        )
        invalid = self.command(
            "review", "--model", "claude-opus-5-5", "--role", "review-unit",
            "--agent", "a2", "--findings", "4", "--held", "5", "--unverified", "0",
        )
        self.assertNotEqual(invalid.returncode, 0)
        self.assertEqual(len(self.log.read_text().splitlines()), 1)

    def test_read_skips_truncated_line(self):
        self.log.write_text('{"v":1,"event":"grade","run":"r","at":"2026-09-23T00:00:00Z"}\n{"v":')
        result = runlog.read(self.log)
        self.assertEqual(len(list(result)), 1)
        self.assertEqual(result.skipped, 1)


if __name__ == "__main__":
    unittest.main()
