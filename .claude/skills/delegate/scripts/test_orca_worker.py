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
import subprocess
import sys

log_file = os.environ.get("ORCA_STUB_LOG")
if log_file:
    with open(log_file, "a") as f:
        f.write(" ".join(sys.argv[1:]) + "\\n")

args = sys.argv[1:]
failure = os.environ.get("ORCA_STUB_FAIL")
child = Path(os.environ["ORCA_STUB_CHILD"])
pointer_file = Path(os.environ["ORCA_STUB_POINTER_FILE"])
if args and args[0] == "status":
    print(json.dumps({{"result": {{"runtime": {{"reachable": True}}}}}}))
elif len(args) >= 2 and args[0] == "terminal" and args[1] == "read":
    print("Read .orca-brief.md" if pointer_file.exists() else "")
elif len(args) >= 2 and args[0] == "terminal" and args[1] == "close":
    if failure == "terminal-close" or os.environ.get("ORCA_STUB_CLOSE_FAIL") == "1":
        print("injected close failure", file=sys.stderr)
        sys.exit(1)
    print(json.dumps({{"result": {{"status": "ok"}}}}))
elif len(args) >= 2 and args[0] == "worktree" and args[1] == "rm":
    runlog = Path(os.environ["FLOWSEER_RUNLOG"])
    Path(os.environ["ORCA_STUB_AT_RM"]).write_text(runlog.read_text() if runlog.exists() else "")
    if os.environ.get("ORCA_STUB_RM_FAIL") == "1":
        print("injected worktree removal failure", file=sys.stderr)
        sys.exit(1)
    subprocess.run(["git", "worktree", "remove", "--force", str(child)], check=False, capture_output=True)
    subprocess.run(["git", "branch", "-D", "wt1"], check=False, capture_output=True)
    print(json.dumps({{"result": {{"status": "ok"}}}}))
elif len(args) >= 2 and args[0] == "terminal" and args[1] == "create":
    if failure == "terminal-create":
        print("injected terminal creation failure", file=sys.stderr)
        sys.exit(1)
    if failure == "advance-base":
        subprocess.run(["git", "commit", "--allow-empty", "-m", "advance base"], check=True, capture_output=True)
    if failure == "brief-copy":
        (child / ".orca-brief.md" / "brief.md").mkdir(parents=True)
    if failure == "exclude-write":
        exclude = Path.cwd() / ".git" / "info" / "exclude"
        exclude.unlink()
        exclude.mkdir()
    print(json.dumps({{"result": {{"terminal": {{"handle": "term-1"}}}}}}))
elif len(args) >= 2 and args[0] == "terminal" and args[1] == "wait":
    if failure in ("terminal-wait", "terminal-exited"):
        sys.exit(1)
    print(json.dumps({{"result": {{"wait": {{"status": "idle"}}}}}}))
elif len(args) >= 2 and args[0] == "terminal" and args[1] == "show":
    if failure == "terminal-show":
        sys.exit(1)
    status = {{"terminal-wait": "running", "terminal-exited": "exited"}}.get(failure, "idle")
    print(json.dumps({{"result": {{"terminal": {{"status": status}}}}}}))
elif len(args) >= 2 and args[0] == "worktree" and args[1] == "create":
    base = args[args.index("--base-branch") + 1]
    subprocess.run(["git", "worktree", "add", "-b", "wt1", str(child), base], check=True, capture_output=True)
    print(json.dumps({{"result": {{"worktree": {{"id": "wt1", "path": str(child), "branch": "refs/heads/wt1"}}}}}}))
elif len(args) >= 2 and args[0] == "terminal" and args[1] == "send":
    if failure == "pointer":
        print("injected pointer failure", file=sys.stderr)
        sys.exit(1)
    pointer_file.write_text("sent")
    if failure == "state-mkdir":
        Path(os.environ["ORCA_STUB_STATE_DIR"]).write_text("occupied")
    print(json.dumps({{"result": {{"status": "ok"}}}}))
else:
    print(json.dumps({{"result": {{}}}}))
"""
        stub_orca.write_text(stub_script)
        stub_orca.chmod(stub_orca.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)

        stub_python = self.bin_dir / "python3"
        stub_python.write_text(f"""#!{sys.executable}
import os
import sys

if os.environ.get("ORCA_STUB_FAIL") == "state-write" and len(sys.argv) > 2 and sys.argv[1] == "-c" and "json.dump(dict(zip" in sys.argv[2]:
    marker = os.environ.get("ORCA_STUB_STATE_WRITE_MARKER")
    if not marker or not os.path.exists(marker):
        if marker:
            open(marker, "w").close()
        print("injected state write failure", file=sys.stderr)
        sys.exit(1)
os.execv(sys.executable, [sys.executable, *sys.argv[1:]])
""")
        stub_python.chmod(stub_python.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)

        stub_sleep = self.bin_dir / "sleep"
        stub_sleep.write_text("#!/bin/sh\nexit 0\n")
        stub_sleep.chmod(stub_sleep.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)

        self.runlog = self.root / "runs.jsonl"
        self.state_dir = self.repo / ".git" / "orca-workers"
        self.state_dir.mkdir(parents=True, exist_ok=True)

        self.env = {
            **os.environ,
            "PATH": f"{self.bin_dir}:{os.environ.get('PATH', '')}",
            "FLOWSEER_RUNLOG": str(self.runlog),
            "ORCA_STUB_LOG": str(self.orca_log),
            "ORCA_STUB_CHILD": str(self.root / "child-l1"),
            "ORCA_STUB_POINTER_FILE": str(self.root / "pointer-sent"),
            "ORCA_STUB_STATE_DIR": str(self.state_dir),
            "ORCA_STUB_AT_RM": str(self.root / "runlog-at-rm.jsonl"),
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

    def start(self, cli="codex", model="gpt-6-sol"):
        brief = self.repo / "brief.md"
        brief.write_text("task description")
        return self.command(
            "start", "--lane", "l1", "--cli", cli, "--model", model,
            "--role", "execute", "--brief", str(brief),
        )

    def assert_rolled_back_run(self, result, message):
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(message, result.stderr)
        events = [json.loads(line) for line in self.runlog.read_text().splitlines()]
        self.assertEqual([event["event"] for event in events], ["start", "end"])
        self.assertEqual(events[1]["run"], events[0]["run"])
        self.assertEqual(events[1]["head"], events[0]["base"])
        at_rm = [json.loads(line) for line in (self.root / "runlog-at-rm.jsonl").read_text().splitlines()]
        self.assertEqual([event["event"] for event in at_rm], ["start", "end"])
        self.assertIn("terminal close --terminal term-1", self.orca_calls())
        self.assertIn("worktree rm --worktree id:wt1", self.orca_calls())
        self.assertFalse((self.root / "child-l1").exists())
        self.assertFalse((self.state_dir / "l1.json").exists())

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

    def test_start_logs_child_head_before_base_advances(self):
        initial_head = subprocess.run(
            ["git", "rev-parse", "HEAD"], cwd=self.repo, check=True, capture_output=True, text=True,
        ).stdout.strip()
        self.env["ORCA_STUB_FAIL"] = "advance-base"
        result = self.start()
        self.assertEqual(result.returncode, 0, result.stderr)
        event = json.loads(self.runlog.read_text().splitlines()[0])
        child_head = subprocess.run(
            ["git", "rev-parse", "HEAD"], cwd=self.root / "child-l1", check=True,
            capture_output=True, text=True,
        ).stdout.strip()
        parent_head = subprocess.run(
            ["git", "rev-parse", "HEAD"], cwd=self.repo, check=True,
            capture_output=True, text=True,
        ).stdout.strip()
        self.assertEqual(event["base"], child_head)
        self.assertEqual(child_head, initial_head)
        self.assertNotEqual(parent_head, initial_head)
        self.assertTrue((self.state_dir / "l1.json").exists())

    def test_start_pointer_failure_closes_run_before_removing_worktree(self):
        self.env["ORCA_STUB_FAIL"] = "pointer"
        result = self.start()
        self.assert_rolled_back_run(result, "pointer not on")
        calls = self.orca_calls()
        self.assertLess(calls.index("terminal send"), calls.index("worktree rm"))

    def test_start_brief_copy_failure_closes_run(self):
        self.env["ORCA_STUB_FAIL"] = "brief-copy"
        result = self.start()
        self.assert_rolled_back_run(result, "cannot write the brief")

    def test_start_exclude_write_failure_closes_run(self):
        self.env["ORCA_STUB_FAIL"] = "exclude-write"
        result = self.start()
        self.assert_rolled_back_run(result, "cannot write git exclude file")

    def test_start_terminal_checks_roll_back_before_logging(self):
        for failure, message in (("terminal-show", "terminal show failed"),
                                 ("terminal-exited", "agy exited at startup")):
            with self.subTest(failure=failure):
                self.env["ORCA_STUB_FAIL"] = failure
                result = self.start(cli="agy", model="gemini-4-pro")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)
                self.assertFalse(self.runlog.exists())
                self.assertFalse((self.root / "child-l1").exists())
                self.assertFalse((self.state_dir / "l1.json").exists())

    def test_start_survives_failed_wait_on_running_terminal(self):
        self.env["ORCA_STUB_FAIL"] = "terminal-wait"
        result = self.start(cli="agy", model="gemini-4-pro")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("terminal wait --terminal term-1", self.orca_calls())
        self.assertIn("terminal send --terminal term-1", self.orca_calls())
        self.assertEqual(json.loads(result.stdout)["name"], "l1")
        events = [json.loads(line) for line in self.runlog.read_text().splitlines()]
        self.assertEqual([event["event"] for event in events], ["start"])
        self.assertTrue((self.root / "child-l1").exists())
        self.assertTrue((self.state_dir / "l1.json").exists())
        self.assertNotIn("worktree rm", self.orca_calls())

    def test_start_state_directory_failure_closes_run(self):
        self.state_dir.rmdir()
        self.env["ORCA_STUB_FAIL"] = "state-mkdir"
        result = self.start()
        self.assert_rolled_back_run(result, "cannot create lane state directory")

    def test_start_state_write_failure_closes_run(self):
        self.env["ORCA_STUB_FAIL"] = "state-write"
        result = self.start()
        self.assert_rolled_back_run(result, "cannot write lane state")
        self.assertIn("injected state write failure", result.stderr)

    def test_start_keeps_lane_when_worktree_removal_fails(self):
        self.env["ORCA_STUB_FAIL"] = "pointer"
        self.env["ORCA_STUB_RM_FAIL"] = "1"
        result = self.start()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("lane l1 is still live and must be removed by hand", result.stderr)
        state_file = self.state_dir / "l1.json"
        self.assertTrue(state_file.exists())
        state = json.loads(state_file.read_text())
        self.assertEqual(state["terminal"], "term-1")
        self.assertEqual(state["worktree"], "wt1")
        self.assertEqual(state["path"], str(self.root / "child-l1"))
        self.assertEqual(state["branch"], "wt1")
        self.assertTrue(state["run"])
        self.assertTrue((self.root / "child-l1").exists())
        self.assertIn("l1 codex", self.command("status").stdout)

    def test_start_keeps_pre_run_lane_when_worktree_removal_fails(self):
        self.env["ORCA_STUB_FAIL"] = "terminal-show"
        self.env["ORCA_STUB_RM_FAIL"] = "1"
        result = self.start()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("lane l1 is still live and must be removed by hand", result.stderr)
        state = json.loads((self.state_dir / "l1.json").read_text())
        self.assertEqual(state["terminal"], "term-1")
        self.assertEqual(state["worktree"], "wt1")
        self.assertEqual(state["run"], "")
        self.assertTrue((self.root / "child-l1").exists())
        self.assertIn("l1 codex", self.command("status").stdout)

    def test_start_keeps_lane_without_terminal_when_worktree_removal_fails(self):
        self.env["ORCA_STUB_FAIL"] = "terminal-create"
        self.env["ORCA_STUB_RM_FAIL"] = "1"
        result = self.start()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("lane l1 is still live and must be removed by hand", result.stderr)
        state = json.loads((self.state_dir / "l1.json").read_text())
        self.assertEqual(state["terminal"], "")
        self.assertEqual(state["worktree"], "wt1")
        self.assertEqual(state["run"], "")
        self.assertTrue((self.root / "child-l1").exists())
        self.assertIn("l1 codex", self.command("status").stdout)

    def test_start_restores_state_after_write_and_removal_fail(self):
        self.env["ORCA_STUB_FAIL"] = "state-write"
        self.env["ORCA_STUB_RM_FAIL"] = "1"
        self.env["ORCA_STUB_STATE_WRITE_MARKER"] = str(self.root / "state-write-attempted")
        result = self.start()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("lane l1 is still live and must be removed by hand", result.stderr)
        state = json.loads((self.state_dir / "l1.json").read_text())
        self.assertEqual(state["terminal"], "term-1")
        self.assertEqual(state["worktree"], "wt1")
        self.assertTrue(state["run"])
        self.assertTrue((self.root / "child-l1").exists())

    def test_start_keeps_worktree_when_terminal_close_fails(self):
        self.env["ORCA_STUB_FAIL"] = "pointer"
        self.env["ORCA_STUB_CLOSE_FAIL"] = "1"
        result = self.start()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("terminal close failed", result.stderr)
        self.assertIn("lane l1 is still live and must be removed by hand", result.stderr)
        state = json.loads((self.state_dir / "l1.json").read_text())
        self.assertEqual(state["terminal"], "term-1")
        self.assertEqual(state["worktree"], "wt1")
        self.assertTrue(state["run"])
        self.assertTrue((self.root / "child-l1").exists())
        self.assertNotIn("worktree rm", self.orca_calls())

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

    def graded_lane(self):
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
        return state_file, child_path, branch_head

    def test_stop_after_grade_writes_end_and_removes_lane(self):
        state_file, _, branch_head = self.graded_lane()
        result = self.command("stop", "l1")
        self.assertEqual(result.returncode, 0, result.stderr)
        events = [json.loads(line) for line in self.runlog.read_text().splitlines()]
        self.assertEqual(len(events), 2)
        end_event = events[1]
        self.assertEqual(end_event["event"], "end")
        self.assertEqual(end_event["run"], "run-l1")
        self.assertEqual(end_event["head"], branch_head)
        at_rm = [json.loads(line) for line in (self.root / "runlog-at-rm.jsonl").read_text().splitlines()]
        self.assertEqual([event["event"] for event in at_rm], ["grade", "end"])
        self.assertFalse(state_file.exists())
        calls = self.orca_calls()
        self.assertIn("terminal close --terminal term-1", calls)
        self.assertIn("worktree rm --worktree id:wt-1", calls)
        self.assertLess(calls.index("terminal close"), calls.index("worktree rm"))

    def test_stop_close_failure_logs_no_end_and_keeps_lane(self):
        state_file, child_path, _ = self.graded_lane()
        self.env["ORCA_STUB_FAIL"] = "terminal-close"
        result = self.command("stop", "l1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("terminal close failed", result.stderr)
        events = [json.loads(line) for line in self.runlog.read_text().splitlines()]
        self.assertEqual([event["event"] for event in events], ["grade"])
        self.assertTrue(state_file.exists())
        self.assertTrue(child_path.exists())
        self.assertNotIn("worktree rm", self.orca_calls())


if __name__ == "__main__":
    unittest.main()
