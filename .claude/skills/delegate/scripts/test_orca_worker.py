import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("orca-worker.sh")


class OrcaWorkerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

        self.repo = self.root / "repo"
        self.repo.mkdir()
        subprocess.run(["git", "init", "-b", "main"], cwd=self.repo, check=True, capture_output=True)
        subprocess.run(["git", "config", "user.name", "Test User"], cwd=self.repo, check=True, capture_output=True)
        subprocess.run(["git", "config", "user.email", "test@example.com"], cwd=self.repo, check=True, capture_output=True)
        (self.repo / "README.md").write_text("initial")
        subprocess.run(["git", "add", "."], cwd=self.repo, check=True, capture_output=True)
        subprocess.run(["git", "commit", "-m", "initial commit"], cwd=self.repo, check=True, capture_output=True)

        self.bin_dir = self.root / "bin"
        self.bin_dir.mkdir()
        self.orca_log = self.root / "orca_calls.log"

        stub_orca = self.bin_dir / "orca"
        stub_script = f"""#!{sys.executable}
import json
import os
from pathlib import Path
import sys

log_file = os.environ.get("ORCA_STUB_LOG")
if log_file:
    with open(log_file, "a") as f:
        f.write(" ".join(sys.argv[1:]) + "\\n")

args = sys.argv[1:]
if args and args[0] == "status":
    print(json.dumps({{"result": {{"runtime": {{"reachable": True}}}}}}))
elif len(args) >= 2 and args[0] == "terminal" and args[1] == "read":
    print("")
elif len(args) >= 2 and args[0] == "terminal" and args[1] == "close":
    print(json.dumps({{"result": {{"status": "ok"}}}}))
elif len(args) >= 2 and args[0] == "worktree" and args[1] == "rm":
    print(json.dumps({{"result": {{"status": "ok"}}}}))
elif len(args) >= 2 and args[0] == "terminal" and args[1] == "show":
    print(json.dumps({{"result": {{"terminal": {{"status": "idle"}}}}}}))
elif len(args) >= 2 and args[0] == "worktree" and args[1] == "create":
    print(json.dumps({{"result": {{"worktree": {{"id": "wt1", "path": str(Path.cwd()), "branch": "refs/heads/wt1"}}}}}}))
else:
    print(json.dumps({{"result": {{}}}}))
"""
        stub_orca.write_text(stub_script)
        stub_orca.chmod(stub_orca.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)

        self.runlog = self.root / "runs.jsonl"
        self.state_dir = self.repo / ".git" / "orca-workers"
        self.state_dir.mkdir(parents=True, exist_ok=True)

        self.env = {
            **os.environ,
            "PATH": f"{self.bin_dir}:{os.environ.get('PATH', '')}",
            "FLOWSEER_RUNLOG": str(self.runlog),
            "ORCA_STUB_LOG": str(self.orca_log),
        }

    def command(self, *args, cwd=None):
        return subprocess.run(
            [str(SCRIPT), *args],
            cwd=cwd or self.repo,
            env=self.env,
            capture_output=True,
            text=True,
            check=False,
        )

    def orca_calls(self):
        if not self.orca_log.exists():
            return ""
        return self.orca_log.read_text()

    def test_start_requires_role_before_worktree_create(self):
        brief = self.repo / "brief.md"
        brief.write_text("task description")
        result = self.command(
            "start", "--lane", "l1", "--cli", "codex", "--model", "gpt-6-sol",
            "--brief", str(brief),
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--role is required", result.stderr)
        self.assertNotIn("worktree create", self.orca_calls())
        self.assertFalse((self.state_dir / "l1.json").exists())

    def test_stop_refuses_ungraded_lane_and_removes_nothing(self):
        child_path = self.root / "child-l1"
        child_path.mkdir()
        state_file = self.state_dir / "l1.json"
        state_file.write_text(json.dumps({
            "name": "l1", "cli": "codex", "terminal": "term-1",
            "worktree": "wt-1", "path": str(child_path), "branch": "branch-l1",
            "run": "run-l1",
        }))
        result = self.command("stop", "l1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("grade", result.stderr.lower())
        self.assertTrue(state_file.exists())
        self.assertTrue(child_path.exists())
        self.assertNotIn("worktree rm", self.orca_calls())
        self.assertNotIn("terminal close", self.orca_calls())

    def test_grade_appends_events_and_retains_latest(self):
        state_file = self.state_dir / "l1.json"
        state_file.write_text(json.dumps({
            "name": "l1", "cli": "codex", "terminal": "term-1",
            "worktree": "wt-1", "path": str(self.repo), "branch": "main",
            "run": "run-l1",
        }))
        first = self.command("grade", "l1", "--outcome", "accepted", "--verify", "pass")
        self.assertEqual(first.returncode, 0, first.stderr)
        events = [json.loads(line) for line in self.runlog.read_text().splitlines()]
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["event"], "grade")
        self.assertEqual(events[0]["run"], "run-l1")
        self.assertEqual(events[0]["outcome"], "accepted")
        self.assertEqual(events[0]["verify"], "pass")

        second = self.command(
            "grade", "l1", "--outcome", "amended", "--verify", "fail",
            "--note", "adjusted test",
        )
        self.assertEqual(second.returncode, 0, second.stderr)
        events = [json.loads(line) for line in self.runlog.read_text().splitlines()]
        self.assertEqual(len(events), 2)
        self.assertEqual(events[1]["event"], "grade")
        self.assertEqual(events[1]["run"], "run-l1")
        self.assertEqual(events[1]["outcome"], "amended")
        self.assertEqual(events[1]["verify"], "fail")
        self.assertEqual(events[1]["note"], "adjusted test")

    def test_stop_after_grade_writes_end_and_removes_lane(self):
        subprocess.run(["git", "checkout", "-b", "branch-l1"], cwd=self.repo, check=True, capture_output=True)
        (self.repo / "work.txt").write_text("lane output")
        subprocess.run(["git", "add", "."], cwd=self.repo, check=True, capture_output=True)
        subprocess.run(["git", "commit", "-m", "work on lane"], cwd=self.repo, check=True, capture_output=True)
        branch_head = subprocess.run(
            ["git", "rev-parse", "HEAD"], cwd=self.repo, check=True, capture_output=True, text=True,
        ).stdout.strip()
        subprocess.run(["git", "checkout", "main"], cwd=self.repo, check=True, capture_output=True)
        subprocess.run(["git", "merge", "branch-l1"], cwd=self.repo, check=True, capture_output=True)

        child_path = self.root / "child-l1"
        child_path.mkdir()
        subprocess.run(["git", "init"], cwd=child_path, check=True, capture_output=True)

        state_file = self.state_dir / "l1.json"
        state_file.write_text(json.dumps({
            "name": "l1", "cli": "codex", "terminal": "term-1",
            "worktree": "wt-1", "path": str(child_path), "branch": "branch-l1",
            "run": "run-l1",
        }))
        self.runlog.write_text(
            json.dumps({"v": 1, "event": "grade", "run": "run-l1",
                        "at": "2026-09-23T12:00:00Z", "outcome": "accepted", "verify": "pass"}) + "\n"
        )

        result = self.command("stop", "l1")
        self.assertEqual(result.returncode, 0, result.stderr)
        events = [json.loads(line) for line in self.runlog.read_text().splitlines()]
        self.assertEqual(len(events), 2)
        end_event = events[1]
        self.assertEqual(end_event["event"], "end")
        self.assertEqual(end_event["run"], "run-l1")
        self.assertEqual(end_event["head"], branch_head)
        self.assertFalse(state_file.exists())
        self.assertIn("terminal close --terminal term-1", self.orca_calls())
        self.assertIn("worktree rm --worktree id:wt-1", self.orca_calls())


if __name__ == "__main__":
    unittest.main()
