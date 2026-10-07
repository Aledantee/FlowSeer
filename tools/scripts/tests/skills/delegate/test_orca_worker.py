import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest

from lib import repo


SCRIPT = repo.root(Path(__file__).parent) / ".agents/skills/delegate/scripts/orca-worker.sh"


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
    if args[args.index("--terminal") + 1] == "":
        print("esc to interrupt")
    elif failure == "terminal-read":
        sys.exit(1)
    elif os.environ.get("ORCA_STUB_DIALOG") and Path(os.environ["ORCA_STUB_DIALOG"]).exists():
        print(Path(os.environ["ORCA_STUB_DIALOG"]).read_text())
    elif os.environ.get("ORCA_STUB_SCREENS") and (Path(os.environ["ORCA_STUB_SCREENS"]) / args[args.index("--terminal") + 1]).exists():
        term_handle = args[args.index("--terminal") + 1]
        clock_val = int(Path(os.environ["ORCA_STUB_CLOCK"]).read_text() or "0")
        if (
            os.environ.get("ORCA_STUB_CHILD_SCREEN_AFTER")
            and clock_val >= int(os.environ.get("ORCA_STUB_CHILD_SCREEN_AFTER_CLOCK", "0"))
        ):
            print(os.environ["ORCA_STUB_CHILD_SCREEN_AFTER"])
        else:
            print((Path(os.environ["ORCA_STUB_SCREENS"]) / term_handle).read_text())
    elif (
        os.environ.get("ORCA_STUB_SCREEN_AFTER")
        and int(Path(os.environ["ORCA_STUB_CLOCK"]).read_text() or "0")
        >= int(os.environ.get("ORCA_STUB_SCREEN_AFTER_CLOCK", "0"))
    ):
        print(os.environ["ORCA_STUB_SCREEN_AFTER"])
    elif os.environ.get("ORCA_STUB_SCREEN"):
        print(os.environ["ORCA_STUB_SCREEN"])
    else:
        print(pointer_file.read_text() if pointer_file.exists() else "")
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
    if failure == "terminal-no-handle":
        print(json.dumps({{"result": {{"terminal": {{"handle": ""}}}}}}))
        sys.exit(0)
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
    if failure == "terminal-wait-exited":
        print(json.dumps({{"result": {{"wait": {{"status": "exited"}}}}}}))
    elif failure in ("terminal-wait", "terminal-exited"):
        sys.exit(1)
    elif failure == "terminal-wait-unsatisfied":
        # Orca prints the result, then exits 1, for an unsatisfied wait.
        print(json.dumps({{"result": {{"wait": {{"status": "timeout", "satisfied": False}}}}}}))
        sys.exit(1)
    else:
        print(json.dumps({{"result": {{"wait": {{"status": "idle"}}}}}}))
elif len(args) >= 2 and args[0] == "terminal" and args[1] == "show":
    if failure == "terminal-show":
        sys.exit(1)
    status = {{"terminal-wait": "running", "terminal-exited": "exited"}}.get(failure, "idle")
    print(json.dumps({{"result": {{"terminal": {{"status": status}}}}}}))
elif len(args) >= 2 and args[0] == "worktree" and args[1] == "show":
    # ORCA_STUB_CHILDREN_FROM: the query, counting from 1, that first reports the children.
    if failure == "worktree-show":
        sys.exit(1)
    shows = Path(str(log_file) + ".shows")
    count = int(shows.read_text()) + 1 if shows.exists() else 1
    shows.write_text(str(count))
    children = [c for c in os.environ.get("ORCA_STUB_CHILDREN", "").split(",") if c]
    if count < int(os.environ.get("ORCA_STUB_CHILDREN_FROM", "1")):
        children = []
    print(json.dumps({{"result": {{"worktree": {{"childWorktreeIds": children}}}}}}))
elif len(args) >= 2 and args[0] == "worktree" and args[1] == "create":
    base = args[args.index("--base-branch") + 1]
    subprocess.run(["git", "worktree", "add", "-b", "wt1", str(child), base], check=True, capture_output=True)
    print(json.dumps({{"result": {{"worktree": {{"id": "wt1", "path": str(child), "branch": "refs/heads/wt1"}}}}}}))
elif len(args) >= 2 and args[0] == "terminal" and args[1] == "send":
    if failure == "pointer":
        print("injected pointer failure", file=sys.stderr)
        sys.exit(1)
    text = args[args.index("--text") + 1]
    dialog = Path(os.environ.get("ORCA_STUB_DIALOG") or "/nonexistent")
    if dialog.exists():
        # A digit selects an option; Enter on an empty text confirms it.
        selected = Path(str(dialog) + ".selected")
        if text and "--enter" not in args:
            selected.write_text(text)
        elif not text and "--enter" in args and selected.exists():
            dialog.unlink()
        print(json.dumps({{"result": {{"status": "ok"}}}}))
        sys.exit(0)
    pointer_file.write_text(text)
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
        stub_sleep.write_text(f"""#!{sys.executable}
import os
import sys

clock = os.environ["ORCA_STUB_CLOCK"]
with open(clock, "r+") as stream:
    elapsed = int(stream.read() or "0")
    stream.seek(0)
    stream.write(str(elapsed + int(sys.argv[1])))
    stream.truncate()
""")
        stub_sleep.chmod(stub_sleep.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)

        stub_date = self.bin_dir / "date"
        stub_date.write_text(f"""#!{sys.executable}
import os
import sys

if sys.argv[1:] == ["+%s"]:
    print(open(os.environ["ORCA_STUB_CLOCK"]).read().strip() or "0")
else:
    os.execv(os.environ["ORCA_REAL_DATE"], [os.environ["ORCA_REAL_DATE"], *sys.argv[1:]])
""")
        stub_date.chmod(stub_date.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)

        self.runlog = self.root / "runs.jsonl"
        self.clock = self.root / "clock"
        self.clock.write_text("0")
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
            "ORCA_STUB_CLOCK": str(self.clock),
            "ORCA_REAL_DATE": shutil.which("date"),
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

    def assert_wait(self, result, lines, clock):
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), lines)
        self.assertEqual(self.clock.read_text().strip(), str(clock))

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

    def assert_retained_lane(self, result, terminal, has_run):
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("lane l1 is still live and must be removed by hand", result.stderr)
        state_file = self.state_dir / "l1.json"
        self.assertTrue(state_file.exists())
        state = json.loads(state_file.read_text())
        self.assertEqual(state["name"], "l1")
        self.assertEqual(state["cli"], "codex")
        self.assertEqual(state["terminal"], terminal)
        self.assertEqual(state["worktree"], "wt1")
        self.assertEqual(state["path"], str(self.root / "child-l1"))
        self.assertEqual(state["branch"], "wt1")
        self.assertEqual(bool(state["run"]), has_run)
        self.assertTrue((self.root / "child-l1").exists())

        status = self.command("status")
        self.assertEqual(status.returncode, 0, status.stderr)
        self.assertIn("l1 codex ", status.stdout)
        self.assertIn(state["path"], status.stdout)
        if not terminal:
            self.assertIn(f"l1 codex unavailable-terminal {state['path']}", status.stdout)
            self.assertNotIn("terminal read --terminal  --screen", self.orca_calls())

    def codex_dialog(self, text):
        dialog = self.root / "dialog"
        dialog.write_text(text)
        self.env["ORCA_STUB_DIALOG"] = str(dialog)
        return dialog

    def test_start_launches_codex_with_the_update_check_off(self):
        result = self.start()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("-c check_for_update_on_startup=false", self.orca_calls())

    def uv_start_wrapper(self, fail=False):
        wrapper = self.bin_dir / "uv"
        wrapper.write_text(f"""#!{sys.executable}
import os
import sys

if sys.argv[1:3] == ["run", "--quiet"] and sys.argv[4:7] == ["delegate", "runlog", "start"]:
    print("launcher notice", file=sys.stderr)
    if {fail!r}:
        print("launcher output")
        sys.exit(1)
os.execv({shutil.which('uv')!r}, [{shutil.which('uv')!r}, *sys.argv[1:]])
""")
        wrapper.chmod(wrapper.stat().st_mode | stat.S_IEXEC)

    def test_start_stores_only_stdout_when_launcher_writes_stderr(self):
        self.uv_start_wrapper()
        result = self.start()
        self.assertEqual(result.returncode, 0, result.stderr)
        event = json.loads(self.runlog.read_text().splitlines()[0])
        state = json.loads((self.state_dir / "l1.json").read_text())
        self.assertEqual(state["run"], event["run"])
        self.assertEqual(json.loads(result.stdout)["run"], event["run"])

    def test_start_failure_keeps_launcher_error_visible_and_undoes_lane(self):
        self.uv_start_wrapper(fail=True)
        result = self.start()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("launcher notice", result.stderr.splitlines())
        failure_lines = [line for line in result.stderr.splitlines() if "run log start failed:" in line]
        self.assertEqual(len(failure_lines), 1)
        self.assertIn("run log start failed: launcher output", failure_lines[0])
        self.assertNotIn("launcher notice", failure_lines[0])
        self.assertIn("terminal close --terminal term-1", self.orca_calls())
        self.assertIn("worktree rm --worktree id:wt1", self.orca_calls())
        self.assertFalse((self.root / "child-l1").exists())
        self.assertFalse((self.state_dir / "l1.json").exists())
        self.assertFalse(self.runlog.exists())

    def test_start_trusts_codex_hooks_by_the_option_number_on_screen(self):
        dialog = self.codex_dialog(
            "  Hooks need review\n"
            "› 1. Review hooks\n"
            "  2. Continue without trusting (hooks won't run)\n"
            "  3. Trust all and continue\n"
            "  enter confirm · esc skip\n"
        )
        result = self.start()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(dialog.exists())
        calls = self.orca_calls()
        self.assertIn("terminal send --terminal term-1 --text 3 --json", calls)
        self.assertLess(calls.index("--text 3 --json"), calls.index("--text  --enter --json"))
        self.assertLess(calls.index("--text  --enter --json"), calls.index("--text Read .orca-brief.md"))

    def test_start_rolls_back_codex_dialogs_it_cannot_answer_before_logging(self):
        for screen, message in (
            ("  Hooks need review\n› 1. Review hooks\n  2. Quit\n", 'has no "Trust all and continue" option'),
            ("  Update available!\n› 1. Update now\n  2. Skip\n  3. Skip until next version\n", "showed its update offer"),
            ("Update ran successfully! Please restart Codex.\n$ ", "Codex updated itself"),
        ):
            with self.subTest(message=message):
                self.codex_dialog(screen)
                result = self.start()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)
                self.assertNotIn("--text Read .orca-brief.md", self.orca_calls())
                self.assertFalse(self.runlog.exists())
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

    def test_start_waits_once_more_when_the_startup_wait_is_unsatisfied(self):
        self.env["ORCA_STUB_FAIL"] = "terminal-wait-unsatisfied"
        result = self.start(cli="claude", model="claude-opus-5-5")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.orca_calls()
        self.assertEqual(calls.count("terminal wait --terminal term-1"), 2)
        self.assertLess(calls.index("--timeout-ms 180000"), calls.index("--text Read .orca-brief.md"))

    def test_start_waits_once_when_the_startup_wait_is_satisfied(self):
        result = self.start(cli="claude", model="claude-opus-5-5")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.orca_calls().count("terminal wait --terminal term-1"), 1)

    def test_start_refuses_an_empty_brief_before_worktree_create(self):
        brief = self.repo / "brief.md"
        brief.write_text("")
        result = self.command(
            "start", "--lane", "l1", "--cli", "codex", "--model", "gpt-6-sol",
            "--role", "execute", "--brief", str(brief),
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("is empty", result.stderr)
        self.assertNotIn("worktree create", self.orca_calls())

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
        self.assert_retained_lane(result, "term-1", True)

    def test_start_keeps_pre_run_lane_when_worktree_removal_fails(self):
        self.env["ORCA_STUB_FAIL"] = "terminal-show"
        self.env["ORCA_STUB_RM_FAIL"] = "1"
        result = self.start()
        self.assert_retained_lane(result, "term-1", False)

    def test_start_keeps_lane_without_terminal_when_worktree_removal_fails(self):
        self.env["ORCA_STUB_FAIL"] = "terminal-create"
        self.env["ORCA_STUB_RM_FAIL"] = "1"
        result = self.start()
        self.assert_retained_lane(result, "", False)
        stopped = self.command("stop", "l1")
        self.assertIn("lane l1 has no run", stopped.stderr)
        self.assertNotIn("terminal read --terminal  --screen", self.orca_calls())

    def test_start_keeps_lane_when_terminal_has_no_handle(self):
        self.env["ORCA_STUB_FAIL"] = "terminal-no-handle"
        self.env["ORCA_STUB_RM_FAIL"] = "1"
        result = self.start()
        self.assertIn("terminal create returned no handle", result.stderr)
        self.assert_retained_lane(result, "", False)

    def test_start_keeps_lane_when_brief_copy_and_removal_fail(self):
        self.env["ORCA_STUB_FAIL"] = "brief-copy"
        self.env["ORCA_STUB_RM_FAIL"] = "1"
        result = self.start()
        self.assertIn("cannot write the brief", result.stderr)
        self.assert_retained_lane(result, "term-1", True)

    def test_start_keeps_lane_when_exclude_write_and_removal_fail(self):
        self.env["ORCA_STUB_FAIL"] = "exclude-write"
        self.env["ORCA_STUB_RM_FAIL"] = "1"
        result = self.start()
        self.assertIn("cannot write git exclude file", result.stderr)
        self.assert_retained_lane(result, "term-1", True)

    def test_start_restores_state_after_write_and_removal_fail(self):
        self.env["ORCA_STUB_FAIL"] = "state-write"
        self.env["ORCA_STUB_RM_FAIL"] = "1"
        self.env["ORCA_STUB_STATE_WRITE_MARKER"] = str(self.root / "state-write-attempted")
        result = self.start()
        self.assert_retained_lane(result, "term-1", True)

    def test_start_keeps_worktree_when_terminal_close_fails(self):
        self.env["ORCA_STUB_FAIL"] = "pointer"
        self.env["ORCA_STUB_CLOSE_FAIL"] = "1"
        result = self.start()
        self.assertIn("terminal close failed", result.stderr)
        self.assert_retained_lane(result, "term-1", True)
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

    def claude_lane_without_session(self):
        lane_path = self.root / "claude-lane"
        lane_path.mkdir(exist_ok=True)
        self.config = self.root / "claude-config"
        self.env["CLAUDE_CONFIG_DIR"] = str(self.config)
        state_file = self.state_dir / "l1.json"
        state_file.write_text(json.dumps({
            "name": "l1", "cli": "claude", "terminal": "term-1",
            "worktree": "wt-1", "path": str(lane_path), "branch": "main",
            "run": "run-l1",
        }))
        self.runlog.write_text(json.dumps({
            "v": 1, "event": "start", "run": "run-l1",
            "at": "2026-09-30T10:00:00Z", "lane": "l1", "cli": "claude",
            "model": "claude-opus-5-5", "role": "execute", "worktree": "wt-1",
            "branch": "main", "base": "base-sha",
        }) + "\n")

    def test_grade_refuses_accepted_and_amended_when_model_check_fails(self):
        for outcome in ("accepted", "amended"):
            with self.subTest(outcome=outcome):
                self.claude_lane_without_session()
                result = self.command("grade", "l1", "--outcome", outcome, "--verify", "pass")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("no qualifying session file", result.stderr)
                events = [json.loads(line) for line in self.runlog.read_text().splitlines()]
                self.assertEqual([event["event"] for event in events], ["start"])

    def test_grade_writes_rejected_and_blocked_when_model_check_fails(self):
        for outcome in ("rejected", "blocked"):
            with self.subTest(outcome=outcome):
                self.claude_lane_without_session()
                result = self.command("grade", "l1", "--outcome", outcome, "--verify", "none")
                self.assertEqual(result.returncode, 0, result.stderr)
                events = [json.loads(line) for line in self.runlog.read_text().splitlines()]
                self.assertEqual([event["event"] for event in events], ["start", "grade"])
                self.assertEqual(events[-1]["outcome"], outcome)

    def test_check_names_a_claude_lane_whose_run_has_no_start_event(self):
        self.claude_lane_with_session()
        self.runlog.write_text(self.runlog.read_text().replace("run-l1", "run-other"))

        result = self.command("check", "l1")

        self.assertEqual(result.returncode, 1)
        self.assertIn("lane l1 has no start model and time", result.stderr)

    def test_check_reports_non_claude_lanes_as_not_checked(self):
        child_path = self.root / "child-l1"
        child_path.mkdir()
        (self.state_dir / "l1.json").write_text(json.dumps({
            "name": "l1", "cli": "codex", "terminal": "term-1",
            "worktree": "wt-1", "path": str(child_path), "branch": "main",
            "run": "run-l1",
        }))

        result = self.command("check", "l1")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "not checked: codex")

    def claude_lane_with_session(self):
        lane_path = self.root / "claude-lane"
        lane_path.mkdir(exist_ok=True)
        self.env.pop("CLAUDE_CONFIG_DIR", None)
        home = self.root / "home"
        outer = Path(os.environ["HOME"])
        # uv defaults both directories under HOME, which this lane replaces.
        self.env["UV_CACHE_DIR"] = os.environ.get("UV_CACHE_DIR", str(outer / ".cache/uv"))
        self.env["UV_PYTHON_INSTALL_DIR"] = os.environ.get(
            "UV_PYTHON_INSTALL_DIR", str(outer / ".local/share/uv/python"))
        self.env["HOME"] = str(home)
        session_dir = home / ".claude" / "projects" / re.sub(r"[^a-zA-Z0-9]", "-", str(lane_path))
        session_dir.mkdir(parents=True, exist_ok=True)
        (session_dir / "session.jsonl").write_text(json.dumps({
            "type": "assistant",
            "timestamp": "2026-09-30T10:05:00Z",
            "message": {"model": "claude-opus-5-5"},
        }) + "\n")
        state_file = self.state_dir / "l1.json"
        state_file.write_text(json.dumps({
            "name": "l1", "cli": "claude", "terminal": "term-1",
            "worktree": "wt-1", "path": str(lane_path), "branch": "main",
            "run": "run-l1",
        }))
        self.runlog.write_text(json.dumps({
            "v": 1, "event": "start", "run": "run-l1",
            "at": "2026-09-30T10:00:00Z", "lane": "l1", "cli": "claude",
            "model": "claude-opus-5-5", "role": "execute", "worktree": "wt-1",
            "branch": "main", "base": "base-sha",
        }) + "\n")

    def test_check_passes_for_claude_lane_with_valid_session_using_home(self):
        self.claude_lane_with_session()
        result = self.command("check", "l1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "")

    def test_grade_accepted_passes_for_claude_lane_with_valid_session_using_home(self):
        self.claude_lane_with_session()
        result = self.command("grade", "l1", "--outcome", "accepted", "--verify", "pass")
        self.assertEqual(result.returncode, 0, result.stderr)
        events = [json.loads(line) for line in self.runlog.read_text().splitlines()]
        self.assertEqual([event["event"] for event in events], ["start", "grade"])
        self.assertEqual(events[-1]["outcome"], "accepted")

    def test_grade_does_not_duplicate_orca_worker_prefix_when_model_check_fails(self):
        self.claude_lane_without_session()
        self.runlog.write_text(json.dumps({
            "v": 1, "event": "start", "run": "run-l1",
            "lane": "l1", "cli": "claude", "role": "execute",
            "worktree": "wt-1", "branch": "main", "base": "base-sha",
        }) + "\n")
        result = self.command("grade", "l1", "--outcome", "accepted", "--verify", "pass")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("orca-worker: orca-worker:", result.stderr)


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

    def unmerged_graded_lane(self):
        state_file, child_path, _ = self.graded_lane()
        subprocess.run(["git", "checkout", "branch-l1"], cwd=self.repo, check=True, capture_output=True)
        (self.repo / "later.txt").write_text("work after the merge")
        subprocess.run(["git", "add", "."], cwd=self.repo, check=True, capture_output=True)
        subprocess.run(["git", "commit", "-m", "unmerged work"], cwd=self.repo, check=True, capture_output=True)
        subprocess.run(["git", "checkout", "main"], cwd=self.repo, check=True, capture_output=True)
        return state_file, child_path

    def test_stop_refuses_an_unmerged_lane_and_removes_nothing(self):
        state_file, child_path = self.unmerged_graded_lane()
        result = self.command("stop", "l1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("parked/l1", result.stderr)
        self.assertTrue(state_file.exists())
        self.assertTrue(child_path.exists())
        self.assertNotIn("terminal close", self.orca_calls())
        self.assertNotIn("worktree rm", self.orca_calls())

    def test_stop_removes_an_unmerged_lane_whose_commits_are_parked(self):
        state_file, _ = self.unmerged_graded_lane()
        subprocess.run(["git", "branch", "parked/l1", "branch-l1"], cwd=self.repo, check=True, capture_output=True)
        result = self.command("stop", "l1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(state_file.exists())
        self.assertIn("worktree rm --worktree id:wt-1", self.orca_calls())

    def test_stop_refuses_a_lane_whose_parked_branch_lacks_its_tip(self):
        state_file, _ = self.unmerged_graded_lane()
        subprocess.run(["git", "branch", "parked/l1", "main"], cwd=self.repo, check=True, capture_output=True)
        result = self.command("stop", "l1")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(state_file.exists())
        self.assertNotIn("worktree rm", self.orca_calls())

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

    def live_lane(self):
        child_path = self.root / "child-l1"
        child_path.mkdir()
        (self.state_dir / "l1.json").write_text(json.dumps({
            "name": "l1", "cli": "agy", "terminal": "term-1",
            "worktree": "wt-1", "path": str(child_path), "branch": "branch-l1",
            "run": "run-l1",
        }))
        return child_path

    def child_lane(self, child_id="wt-c1", name="c1", terminal="term-c1", screen="> child done"):
        self.child_lanes((child_id, name, terminal, screen))

    def child_lanes(self, *children):
        screens = self.root / "screens"
        screens.mkdir(exist_ok=True)
        self.env["ORCA_STUB_SCREENS"] = str(screens)
        self.env["ORCA_STUB_CHILDREN"] = ",".join(child_id for child_id, _, _, _ in children)
        for child_id, name, terminal, screen in children:
            (screens / terminal).write_text(screen)
            (self.state_dir / f"{name}.json").write_text(json.dumps({
                "name": name, "cli": "codex", "terminal": terminal,
                "worktree": child_id, "path": str(self.root / f"child-{name}"),
                "branch": f"branch-{name}", "run": f"run-{name}",
            }))

    def child_without_state(self, child_id="wt-c1", screen="esc to interrupt"):
        screens = self.root / "screens"
        screens.mkdir(exist_ok=True)
        (screens / "term-c1").write_text(screen)
        self.env["ORCA_STUB_SCREENS"] = str(screens)
        self.env["ORCA_STUB_CHILDREN"] = child_id

    def test_wait_reads_agy_cancel_hint_as_working_and_reports_a_frozen_screen_stalled(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "Thinking... (esc to cancel)"
        result = self.command("wait", "l1", "--stall", "5", "--max", "20")
        self.assert_wait(result, ["stalled", "Thinking... (esc to cancel)"], 15)

    def test_wait_reports_idle_for_a_settled_screen(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "> done"
        result = self.command("wait", "l1", "--stall", "5", "--max", "10")
        self.assert_wait(result, ["idle", "> done"], 5)

    def test_wait_reports_limited_for_a_settled_screen_that_names_a_usage_limit(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "You've hit your usage limit. Try again at 3pm."
        result = self.command("wait", "l1", "--stall", "5", "--max", "10")
        self.assert_wait(result, ["limited", "You've hit your usage limit. Try again at 3pm."], 5)

    def test_wait_reports_idle_for_a_finished_report_that_names_a_quota(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "Report: Lite quota 2K credits; no Orca quota request"
        result = self.command("wait", "l1", "--stall", "5", "--max", "10")
        self.assert_wait(result, ["idle", "Report: Lite quota 2K credits; no Orca quota request"], 5)

    def test_wait_reports_limited_for_an_exhausted_quota(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "Quota exceeded for this model."
        result = self.command("wait", "l1", "--stall", "5", "--max", "10")
        self.assert_wait(result, ["limited", "Quota exceeded for this model."], 5)

    def test_wait_reports_limited_when_blank_rows_follow_the_limit_line(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "usage limit reached, resets at 3pm" + "\n" * 40 + ">"
        result = self.command("wait", "l1", "--stall", "5", "--max", "10")
        self.assert_wait(result, ["limited", "usage limit reached, resets at 3pm", ">"], 5)

    def test_wait_until_reports_done_on_a_screen_that_names_a_usage_limit(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "usage limit reached earlier; report written"
        result = self.command("wait", "l1", "--until", "true", "--stall", "5", "--max", "10")
        self.assert_wait(result, ["done", "usage limit reached earlier; report written"], 5)

    def test_keys_refuses_text_the_terminal_would_truncate(self):
        self.live_lane()
        result = self.command("keys", "l1", "x" * 201)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("use tell", result.stderr)
        self.assertNotIn("terminal send", self.orca_calls())

    def test_tell_delivers_the_file_and_points_the_terminal_at_it(self):
        child_path = self.live_lane()
        note = self.root / "note.md"
        note.write_text("x" * 600)
        result = self.command("tell", "l1", str(note))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((child_path / ".orca-note.md").read_text(), "x" * 600)
        self.assertIn("Read .orca-note.md", self.orca_calls())
        exclude = (self.repo / ".git" / "info" / "exclude").read_text().splitlines()
        self.assertIn("/.orca-note.md", exclude)

    def test_wait_reports_a_closed_terminal_as_exited(self):
        self.live_lane()
        self.env["ORCA_STUB_FAIL"] = "terminal-read"
        result = self.command("wait", "l1", "--stall", "5", "--max", "10")
        self.assert_wait(result, ["exited"], 0)

    def test_wait_reports_an_exited_wait_with_the_screen(self):
        self.live_lane()
        self.env["ORCA_STUB_FAIL"] = "terminal-wait-exited"
        self.env["ORCA_STUB_SCREEN"] = "worker exited unexpectedly"
        result = self.command("wait", "l1", "--stall", "5", "--max", "10")
        self.assert_wait(result, ["exited", "worker exited unexpectedly"], 0)

    def test_wait_times_out_when_an_idle_lane_has_a_working_child(self):
        self.live_lane()
        self.child_lane(screen="esc to interrupt")
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command("wait", "l1", "--stall", "5", "--max", "20")
        self.assert_wait(result, ["timeout", "> lane done"], 20)

    def test_wait_keeps_waiting_when_one_of_multiple_children_works(self):
        self.live_lane()
        self.child_lanes(
            ("wt-c1", "c1", "term-c1", "> child one done"),
            ("wt-c2", "c2", "term-c2", "esc to interrupt"),
        )
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command("wait", "l1", "--stall", "5", "--max", "20")
        self.assert_wait(result, ["timeout", "> lane done"], 20)

    def test_wait_keeps_waiting_for_a_joined_child_after_an_earlier_lane_is_kept(self):
        self.live_lane()
        self.child_lane(child_id="wt-c1", name="b", terminal="term-b", screen="esc to interrupt")
        (self.state_dir / "a.json").write_text(json.dumps({
            "name": "a", "cli": "codex", "terminal": "", "kept": True,
            "worktree": "wt-c1", "path": str(self.root / "child-b"),
            "branch": "branch-b", "run": "run-a",
        }))
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command("wait", "l1", "--stall", "5", "--max", "20")
        self.assert_wait(result, ["timeout", "> lane done"], 20)
        self.assertIn("terminal read --terminal term-b --screen", self.orca_calls())

    def test_wait_reports_idle_children_for_a_quiet_child(self):
        self.live_lane()
        self.child_lane()
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command("wait", "l1", "--stall", "5", "--max", "20")
        self.assert_wait(result, ["idle-children c1", "> lane done"], 15)

    def test_wait_reports_all_quiet_children_by_lane_name(self):
        self.live_lane()
        self.child_lanes(
            ("wt-c1", "c1", "term-c1", "> child one done"),
            ("wt-c2", "c2", "term-c2", "> child two done"),
        )
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command("wait", "l1", "--stall", "5", "--max", "20")
        self.assert_wait(result, ["idle-children c1 c2", "> lane done"], 15)

    def test_wait_restarts_the_stall_clock_when_the_lane_screen_changes(self):
        self.live_lane()
        self.child_lane()
        self.env["ORCA_STUB_SCREEN"] = "> lane before"
        self.env["ORCA_STUB_SCREEN_AFTER"] = "> lane after"
        self.env["ORCA_STUB_SCREEN_AFTER_CLOCK"] = "5"
        result = self.command("wait", "l1", "--stall", "10", "--max", "30")
        self.assert_wait(result, ["idle-children c1", "> lane after"], 25)

    def test_wait_reads_each_child_id_as_one_entry_and_holds_for_a_working_child(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        quiet_lists = (
            (("plain-id", "plain", "term-plain", "> plain done"),),
            (("worktree with space", "space", "term-space", "> space done"),),
            (("01234567-89ab-cdef-0123-456789abcdef::/absolute/path", "shape", "term-shape", "> shape done"),),
            (
                ("wt-one", "one", "term-one", "> one done"),
                ("wt two", "two", "term-two", "> two done"),
            ),
            (
                ("wt-three", "three", "term-three", "> three done"),
                ("wt four", "four", "term-four", "> four done"),
                ("fedcba98-7654-3210-fedc-ba9876543210::/absolute/path", "shape-three", "term-shape-three", "> shape three done"),
            ),
        )

        expected_clock = 0
        for children in quiet_lists:
            with self.subTest(child_ids=[child_id for child_id, _, _, _ in children]):
                self.child_lanes(*children)
                self.orca_log.write_text("")
                result = self.command("wait", "l1", "--stall", "5", "--max", "20")
                names = [name for _, name, _, _ in children]
                expected_clock += 15
                self.assert_wait(result, [f"idle-children {' '.join(names)}", "> lane done"], expected_clock)
                reads = {
                    line.split("--terminal ", 1)[1].split(" --screen", 1)[0]
                    for line in self.orca_calls().splitlines()
                    if line.startswith("terminal read")
                }
                self.assertEqual(reads, {"term-1", *(terminal for _, _, terminal, _ in children)})
                outcome = result.stdout.splitlines()[0].split()[1:]
                for name in names:
                    self.assertEqual(outcome.count(name), 1)

        working_children = (
            ("wt quiet", "quiet", "term-quiet", "> quiet done"),
            ("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee::/absolute/working-path", "working", "term-working", "esc to interrupt"),
        )
        self.child_lanes(*working_children)
        self.orca_log.write_text("")
        result = self.command("wait", "l1", "--stall", "5", "--max", "20")
        expected_clock += 20
        self.assert_wait(result, ["timeout", "> lane done"], expected_clock)
        reads = {
            line.split("--terminal ", 1)[1].split(" --screen", 1)[0]
            for line in self.orca_calls().splitlines()
            if line.startswith("terminal read")
        }
        self.assertEqual(reads, {"term-1", "term-quiet", "term-working"})

    def test_wait_names_a_child_without_state_and_does_not_read_an_empty_handle(self):
        self.live_lane()
        self.child_without_state()
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command("wait", "l1", "--stall", "5", "--max", "20")
        self.assert_wait(result, ["idle-children wt-c1", "> lane done"], 15)
        self.assertNotIn("terminal read --terminal  --screen", self.orca_calls())

    def test_wait_names_an_unreadable_child_query(self):
        self.live_lane()
        self.env["ORCA_STUB_FAIL"] = "worktree-show"
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command("wait", "l1", "--stall", "5", "--max", "20")
        self.assert_wait(result, ["idle-children unreadable", "> lane done"], 15)

    def test_wait_times_out_when_a_frozen_lane_has_a_working_child(self):
        self.live_lane()
        self.child_lane(screen="esc to interrupt")
        self.env["ORCA_STUB_SCREEN"] = "esc to interrupt"
        result = self.command("wait", "l1", "--stall", "5", "--max", "20")
        self.assert_wait(result, ["timeout", "esc to interrupt"], 20)

    def test_wait_times_out_before_stall_for_a_quiet_child(self):
        self.live_lane()
        self.child_lane()
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command("wait", "l1", "--stall", "15", "--max", "10")
        self.assert_wait(result, ["timeout", "> lane done"], 10)

    def test_wait_times_out_before_stall_for_a_frozen_lane_without_children(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "esc to interrupt"
        result = self.command("wait", "l1", "--stall", "15", "--max", "10")
        self.assert_wait(result, ["timeout", "esc to interrupt"], 10)

    def test_wait_until_reports_idle_when_the_command_fails(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command(
            "wait", "l1", "--until", "test -f DONE", "--stall", "5", "--max", "10",
        )
        self.assert_wait(result, ["idle", "> lane done"], 5)

    def test_wait_until_reports_stalled_before_running_a_command(self):
        lane_path = self.live_lane()
        (lane_path / "DONE").write_text("")
        self.env["ORCA_STUB_SCREEN"] = "esc to interrupt"
        result = self.command(
            "wait", "l1", "--until", "test -f DONE", "--stall", "5", "--max", "20",
        )
        self.assert_wait(result, ["stalled", "esc to interrupt"], 15)

    def test_wait_until_does_not_run_a_command_while_the_lane_is_working(self):
        lane_path = self.live_lane()
        marker = lane_path / "until-ran"
        self.env["ORCA_STUB_SCREEN"] = "esc to interrupt"
        result = self.command(
            "wait", "l1", "--until", "touch until-ran", "--stall", "5", "--max", "20",
        )
        self.assert_wait(result, ["stalled", "esc to interrupt"], 15)
        self.assertFalse(marker.exists())

    def test_wait_until_reports_done_for_an_idle_lane(self):
        lane_path = self.live_lane()
        (lane_path / "DONE").write_text("")
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command(
            "wait", "l1", "--until", "test -f DONE", "--stall", "5", "--max", "10",
        )
        self.assert_wait(result, ["done", "> lane done"], 5)

    def test_wait_until_reports_done_for_a_quiet_child_without_stall_delay(self):
        lane_path = self.live_lane()
        (lane_path / "DONE").write_text("")
        self.child_lane()
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command(
            "wait", "l1", "--until", "test -f DONE", "--stall", "0", "--max", "10",
        )
        self.assert_wait(result, ["done", "> lane done"], 5)

    def test_wait_until_reports_idle_children_when_the_command_fails(self):
        self.live_lane()
        self.child_lane()
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command(
            "wait", "l1", "--until", "test -f DONE", "--stall", "5", "--max", "20",
        )
        self.assert_wait(result, ["idle-children c1", "> lane done"], 15)

    def test_wait_until_times_out_while_a_child_works(self):
        lane_path = self.live_lane()
        (lane_path / "DONE").write_text("")
        self.child_lane(screen="esc to interrupt")
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command(
            "wait", "l1", "--until", "test -f DONE", "--stall", "5", "--max", "20",
        )
        self.assert_wait(result, ["timeout", "> lane done"], 20)

    def test_wait_until_times_out_when_one_of_multiple_children_works(self):
        lane_path = self.live_lane()
        (lane_path / "DONE").write_text("")
        self.child_lanes(
            ("wt-c1", "c1", "term-c1", "> child one done"),
            ("wt-c2", "c2", "term-c2", "esc to interrupt"),
        )
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command(
            "wait", "l1", "--until", "test -f DONE", "--stall", "5", "--max", "20",
        )
        self.assert_wait(result, ["timeout", "> lane done"], 20)

    def test_wait_times_out_and_shows_the_screen_for_a_working_lane(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "esc to interrupt"
        result = self.command("wait", "l1", "--stall", "0", "--max", "1")
        self.assert_wait(result, ["timeout", "esc to interrupt"], 1)

    def test_wait_with_stall_zero_checks_the_decision_before_a_non_multiple_ceiling(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "esc to interrupt"
        result = self.command("wait", "l1", "--stall", "0", "--max", "7")
        self.assert_wait(result, ["timeout", "esc to interrupt"], 7)

    def test_wait_with_stall_zero_times_out_for_a_quiet_child(self):
        self.live_lane()
        self.child_lane()
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command("wait", "l1", "--stall", "0", "--max", "10")
        self.assert_wait(result, ["timeout", "> lane done"], 10)

    def test_wait_holds_for_a_child_working_until_clock_fifteen(self):
        self.live_lane()
        self.child_lane(screen="esc to interrupt")
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        self.env["ORCA_STUB_CHILD_SCREEN_AFTER"] = "> child done"
        self.env["ORCA_STUB_CHILD_SCREEN_AFTER_CLOCK"] = "15"
        result = self.command("wait", "l1", "--stall", "15", "--max", "50")
        self.assert_wait(result, ["idle-children c1", "> lane done"], 35)

    def test_wait_reports_idle_after_a_lane_screen_change_without_children(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "> before"
        self.env["ORCA_STUB_SCREEN_AFTER"] = "> after"
        self.env["ORCA_STUB_SCREEN_AFTER_CLOCK"] = "5"
        result = self.command("wait", "l1", "--stall", "5", "--max", "20")
        self.assert_wait(result, ["idle", "> after"], 15)

    def test_wait_times_out_for_an_idle_lane_at_deadline(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "> done"
        result = self.command("wait", "l1", "--stall", "5", "--max", "5")
        self.assert_wait(result, ["timeout", "> done"], 5)

    def test_wait_restarts_stall_when_lane_screen_changes_inside_one_pass(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "Thinking... (esc to cancel)"
        self.env["ORCA_STUB_SCREEN_AFTER"] = "> done"
        self.env["ORCA_STUB_SCREEN_AFTER_CLOCK"] = "15"
        result = self.command("wait", "l1", "--stall", "10", "--max", "30")
        self.assert_wait(result, ["idle", "> done"], 25)

    def test_wait_rejects_the_removed_timeout_flag(self):
        self.live_lane()
        self.env["ORCA_STUB_SCREEN"] = "> lane done"
        result = self.command("wait", "l1", "--timeout", "1000")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("replaced by --max", result.stderr)
        self.assertEqual(self.clock.read_text().strip(), "0")

    def test_wait_rejects_a_zero_maximum(self):
        self.live_lane()
        result = self.command("wait", "l1", "--max", "0")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("positive integer", result.stderr)
        self.assertEqual(self.clock.read_text().strip(), "0")

    def test_start_records_the_line_command_for_codex_with_the_poll_cap(self):
        expected = self.command("line", "--cli", "codex", "--model", "gpt-6-sol").stdout.strip()
        result = self.start()
        self.assertEqual(result.returncode, 0, result.stderr)
        terminal_create = next(call for call in self.orca_calls().splitlines() if "terminal create" in call)
        command = terminal_create.split("--command ", 1)[1].rsplit(" --json", 1)[0]
        self.assertEqual(command, expected)
        self.assertIn("-c background_terminal_max_timeout=3600000", command)

    def test_tell_fails_when_the_pointer_count_does_not_grow(self):
        self.live_lane()
        note = self.root / "note.md"
        note.write_text("text")
        self.env["ORCA_STUB_SCREEN"] = "Read .orca-note.md in this directory"
        result = self.command("tell", "l1", str(note))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("message not on", result.stderr)

    def test_stop_stalled_removes_a_lane_still_showing_the_hint(self):
        state_file, child_path, _ = self.graded_lane()
        (child_path / ".orca-note.md").write_text("note")
        self.env["ORCA_STUB_SCREEN"] = "esc to cancel"
        refused = self.command("stop", "l1")
        self.assertNotEqual(refused.returncode, 0)
        self.assertIn("still working", refused.stderr)
        result = self.command("stop", "l1", "--stalled")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(state_file.exists())
        self.assertFalse((child_path / ".orca-note.md").exists())

    def test_stop_refuses_a_lane_with_child_worktrees_and_removes_nothing(self):
        state_file, child_path, _ = self.graded_lane()
        (child_path / ".orca-brief.md").write_text("brief")
        self.env["ORCA_STUB_CHILDREN"] = "repo::/lanes/grandchild,repo::/lanes/second-child"
        result = self.command("stop", "l1")
        self.assertTrue((child_path / ".orca-brief.md").exists())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("child worktrees", result.stderr)
        self.assertIn("repo::/lanes/grandchild repo::/lanes/second-child", result.stderr)
        self.assertTrue(state_file.exists())
        self.assertTrue(child_path.exists())
        calls = self.orca_calls()
        self.assertNotIn("terminal close", calls)
        self.assertNotIn("worktree rm", calls)

    def test_stop_rechecks_children_after_a_stalled_terminal_closes(self):
        state_file, child_path, _ = self.graded_lane()
        self.env["ORCA_STUB_SCREEN"] = "esc to cancel"
        self.env["ORCA_STUB_CHILDREN"] = "repo::/lanes/late,repo::/lanes/second-late"
        self.env["ORCA_STUB_CHILDREN_FROM"] = "2"
        result = self.command("stop", "l1", "--stalled")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("started child worktrees before its terminal closed", result.stderr)
        self.assertIn("repo::/lanes/late repo::/lanes/second-late", result.stderr)
        self.assertTrue(state_file.exists())
        self.assertTrue(child_path.exists())
        calls = self.orca_calls()
        self.assertIn("terminal close", calls)
        self.assertNotIn("worktree rm", calls)

    def test_start_rollback_keeps_a_lane_with_child_worktrees(self):
        self.env["ORCA_STUB_FAIL"] = "pointer"
        self.env["ORCA_STUB_CHILDREN"] = "repo::/lanes/grandchild,repo::/lanes/second-child"
        result = self.start()
        self.assert_retained_lane(result, "term-1", True)
        self.assertIn("it has child worktrees: repo::/lanes/grandchild repo::/lanes/second-child", result.stderr)
        self.assertNotIn("worktree rm", self.orca_calls())

    def git(self, *args, cwd=None):
        return subprocess.run(
            ["git", *args], cwd=cwd or self.repo, check=True, capture_output=True, text=True,
        ).stdout.strip()

    def kept_lane(self):
        """Starts l1 through the script, grades it, and stops it with --keep-worktree."""
        result = self.start()
        self.assertEqual(result.returncode, 0, result.stderr)
        child = self.root / "child-l1"
        (child / "work.txt").write_text("lane output")
        self.git("add", ".", cwd=child)
        self.git("commit", "-m", "work on lane", cwd=child)
        grade = self.command("grade", "l1", "--outcome", "rejected", "--verify", "none")
        self.assertEqual(grade.returncode, 0, grade.stderr)
        kept = self.command("stop", "l1", "--keep-worktree")
        self.assertEqual(kept.returncode, 0, kept.stderr)
        return child

    def join(self, lane="l2", target="l1", *extra):
        return self.command(
            "start", "--lane", lane, "--cli", "codex", "--model", "gpt-6-sol",
            "--role", "execute", "--brief", str(self.repo / "brief.md"), "--join", target, *extra,
        )

    def runlog_events(self):
        return [json.loads(line) for line in self.runlog.read_text().splitlines()]

    def test_join_reuses_the_path_and_branch_and_logs_the_worktree_head(self):
        child = self.kept_lane()
        head = self.git("rev-parse", "HEAD", cwd=child)
        first = json.loads((self.state_dir / "l1.json").read_text())
        result = self.join()
        self.assertEqual(result.returncode, 0, result.stderr)
        line = json.loads(result.stdout)
        self.assertEqual((line["name"], line["path"], line["branch"]), ("l2", str(child), "wt1"))
        self.assertNotEqual(line["run"], first["run"])
        self.assertEqual(self.orca_calls().count("worktree create"), 1)
        self.assertEqual(self.orca_calls().count("terminal create"), 2)
        start = [e for e in self.runlog_events() if e["event"] == "start" and e["lane"] == "l2"][0]
        self.assertEqual((start["base"], start["worktree"], start["branch"]), (head, str(child), "wt1"))
        state = json.loads((self.state_dir / "l2.json").read_text())
        self.assertEqual((state["worktree"], state["terminal"]), ("wt1", "term-1"))

    def test_join_refuses_and_creates_no_terminal(self):
        child = self.kept_lane()
        creates = self.orca_calls().count("terminal create")

        def dirty_join():
            (child / "stray.txt").write_text("x")
            return self.join()

        cases = {
            "unknown lane": (lambda: self.join(target="nope"), "no lane named nope"),
            "base": (lambda: self.join("l2", "l1", "--base", "main"), "--base"),
            "dirty worktree": (dirty_join, "dirty"),
        }
        for name, (run, message) in cases.items():
            with self.subTest(name):
                result = run()
                self.assertEqual(result.returncode, 1, result.stderr)
                self.assertIn(message, result.stderr)
                self.assertEqual(self.orca_calls().count("terminal create"), creates)
                self.assertFalse((self.state_dir / "l2.json").exists())

    def test_join_refuses_a_worktree_with_a_live_terminal(self):
        self.start()
        creates = self.orca_calls().count("terminal create")
        result = self.join()
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("live terminal", result.stderr)
        self.assertEqual(self.orca_calls().count("terminal create"), creates)
        self.assertFalse((self.state_dir / "l2.json").exists())

    def test_stop_keep_worktree_leaves_an_unmerged_lane_and_status_prints_it_kept(self):
        child = self.kept_lane()
        self.assertTrue(child.exists())
        self.assertNotIn("worktree rm", self.orca_calls())
        self.assertIn("terminal close --terminal term-1", self.orca_calls())
        self.assertIn(str(child), self.git("worktree", "list"))
        state = json.loads((self.state_dir / "l1.json").read_text())
        self.assertEqual((state["terminal"], state["kept"]), ("", True))
        self.assertEqual([e["event"] for e in self.runlog_events()], ["start", "grade", "end"])
        status = self.command("status")
        self.assertIn(f"l1 codex kept {child}", status.stdout)
        self.assertNotIn("unavailable-terminal", status.stdout)

    def test_stop_on_a_kept_lane_closes_no_terminal_and_logs_no_second_end(self):
        child = self.kept_lane()
        self.git("merge", "wt1")
        self.orca_log.unlink()
        result = self.command("stop", "l1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("terminal close", self.orca_calls())
        self.assertIn("worktree rm --worktree id:wt1", self.orca_calls())
        self.assertEqual([e["event"] for e in self.runlog_events()].count("end"), 1)
        self.assertFalse(child.exists())
        self.assertFalse((self.state_dir / "l1.json").exists())

    def test_stop_kept_lane_refuses_while_joined_lane_has_a_live_terminal(self):
        child = self.kept_lane()
        joined = self.join()
        self.assertEqual(joined.returncode, 0, joined.stderr)
        graded = self.command("grade", "l2", "--outcome", "accepted", "--verify", "pass")
        self.assertEqual(graded.returncode, 0, graded.stderr)
        self.git("merge", "wt1")
        self.orca_log.unlink()
        events = self.runlog_events()

        result = self.command("stop", "l1")

        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("lane l2 still has a live terminal", result.stderr)
        self.assertTrue(child.exists())
        self.assertTrue((self.state_dir / "l1.json").exists())
        self.assertTrue((self.state_dir / "l2.json").exists())
        self.assertNotIn("terminal close", self.orca_calls())
        self.assertNotIn("worktree rm", self.orca_calls())
        self.assertEqual(self.runlog_events(), events)

    def test_stop_refuses_to_keep_an_already_kept_lane(self):
        child = self.kept_lane()
        self.orca_log.unlink()
        events = self.runlog_events()

        result = self.command("stop", "l1", "--keep-worktree")

        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("lane l1 is already kept", result.stderr)
        self.assertTrue(child.exists())
        state = json.loads((self.state_dir / "l1.json").read_text())
        self.assertEqual((state["terminal"], state["kept"]), ("", True))
        self.assertNotIn("terminal close", self.orca_calls())
        self.assertNotIn("worktree rm", self.orca_calls())
        self.assertEqual(self.runlog_events(), events)

    def test_failed_joined_start_closes_its_terminal_and_leaves_the_worktree(self):
        child = self.kept_lane()
        self.env["ORCA_STUB_FAIL"] = "pointer"
        result = self.join()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("pointer not on", result.stderr)
        self.assertEqual(self.orca_calls().count("terminal close"), 2)
        self.assertNotIn("worktree rm", self.orca_calls())
        self.assertTrue(child.exists())
        self.assertTrue((self.state_dir / "l1.json").exists())
        self.assertFalse((self.state_dir / "l2.json").exists())
        self.assertEqual([e["event"] for e in self.runlog_events()][-2:], ["start", "end"])

    def test_final_stop_removes_the_worktree_and_every_lane_state(self):
        child = self.kept_lane()
        self.assertEqual(self.join().returncode, 0)
        self.git("merge", "wt1")
        graded = self.command("grade", "l2", "--outcome", "accepted", "--verify", "pass")
        self.assertEqual(graded.returncode, 0, graded.stderr)
        result = self.command("stop", "l2")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.orca_calls().count("worktree rm"), 1)
        self.assertFalse(child.exists())
        self.assertEqual(list(self.state_dir.glob("*.json")), [])


if __name__ == "__main__":
    unittest.main()
