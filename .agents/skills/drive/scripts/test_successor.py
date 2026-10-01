import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("successor.sh")


class SuccessorTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        subprocess.run(
            ["git", "init", "-b", "main"],
            cwd=self.repo,
            check=True,
            capture_output=True,
        )
        subprocess.run(
            ["git", "config", "user.name", "Test User"],
            cwd=self.repo,
            check=True,
            capture_output=True,
        )
        subprocess.run(
            ["git", "config", "user.email", "test@example.com"],
            cwd=self.repo,
            check=True,
            capture_output=True,
        )
        (self.repo / "README.md").write_text("initial")
        self.parent = self.repo / "docs/plans/parent-plan.md"
        self.parent.parent.mkdir(parents=True)
        self.parent.write_text("---\nstatus: implemented\n---\n")
        subprocess.run(
            ["git", "add", "."], cwd=self.repo, check=True, capture_output=True
        )
        subprocess.run(
            ["git", "commit", "-m", "initial commit"],
            cwd=self.repo,
            check=True,
            capture_output=True,
        )

        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.log = self.root / "orca.log"
        self.env = {
            **os.environ,
            "PATH": f"{self.bin}:{os.environ.get('PATH', '')}",
            "ORCA_STUB_LOG": str(self.log),
            "ORCA_STUB_CHILDREN": "",
            "ORCA_STUB_SCREEN": "esc to interrupt",
        }

        orca = self.bin / "orca"
        orca.write_text(
            "#!" + sys.executable + "\n"
            + r'''
import json
import os
import sys

with open(os.environ["ORCA_STUB_LOG"], "a") as log:
    log.write(" ".join(sys.argv[1:]) + "\n")

args = sys.argv[1:]
if args and args[0] == "status":
    mode = os.environ.get("ORCA_STUB_STATUS", "")
    if mode == "fail":
        raise SystemExit(1)
    print(json.dumps({"result": {"runtime": {"reachable": mode != "unreachable"}}}))
elif args[:2] == ["worktree", "show"]:
    if os.environ.get("ORCA_STUB_FAIL") == "worktree-show":
        raise SystemExit(1)
    if os.environ.get("ORCA_STUB_NO_CHILD_FIELD") == "1":
        print(json.dumps({"result": {"worktree": {}}}))
        raise SystemExit(0)
    children = [item for item in os.environ.get("ORCA_STUB_CHILDREN", "").split(",") if item]
    print(json.dumps({"result": {"worktree": {"childWorktreeIds": children}}}))
elif args[:2] == ["terminal", "create"]:
    if os.environ.get("ORCA_STUB_FAIL") == "terminal-create":
        raise SystemExit(1)
    handle = "" if os.environ.get("ORCA_STUB_NO_HANDLE") == "1" else "term-1"
    print(json.dumps({"result": {"terminal": {"handle": handle}}}))
elif args[:2] == ["terminal", "read"]:
    with open(os.environ["ORCA_STUB_LOG"]) as log:
        reads = sum(1 for line in log if line.startswith("terminal read"))
    if reads >= int(os.environ.get("ORCA_STUB_HINT_AFTER", "1")):
        print(os.environ.get("ORCA_STUB_SCREEN", ""))
    else:
        print("echoed command")
elif args[:2] == ["terminal", "close"]:
    if os.environ.get("ORCA_STUB_CLOSE_FAIL") == "1":
        raise SystemExit(1)
    print(json.dumps({"result": {"status": "ok"}}))
else:
    print(json.dumps({"result": {}}))
'''
        )
        orca.chmod(orca.stat().st_mode | stat.S_IEXEC)
        sleep = self.bin / "sleep"
        sleep.write_text("#!/bin/sh\nexit 0\n")
        sleep.chmod(sleep.stat().st_mode | stat.S_IEXEC)

    def invoke(self, model="claude-opus-5-5", effort=None, parent=None):
        args = [str(SCRIPT), parent or "docs/plans/parent-plan.md", "--model", model]
        if effort is not None:
            args.extend(["--effort", effort])
        return subprocess.run(
            args,
            cwd=self.repo,
            env=self.env,
            capture_output=True,
            text=True,
            check=False,
        )

    def calls(self):
        return self.log.read_text() if self.log.exists() else ""

    def count_calls(self, prefix):
        return sum(1 for call in self.calls().splitlines() if call.startswith(prefix))

    def commit_all(self, message):
        subprocess.run(
            ["git", "add", "."], cwd=self.repo, check=True, capture_output=True
        )
        subprocess.run(
            ["git", "commit", "-m", message],
            cwd=self.repo,
            check=True,
            capture_output=True,
        )

    def assert_no_create(self, result):
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertNotIn("terminal create", self.calls())

    def test_rejects_non_claude_model_before_creating_terminal(self):
        result = self.invoke(model="gpt-6-sol")
        self.assert_no_create(result)
        self.assertIn("model", result.stderr)
        self.assertIn("gpt-6-sol", result.stderr)

    def test_rejects_non_lowercase_effort_before_creating_terminal(self):
        result = self.invoke(effort="High1")
        self.assert_no_create(result)
        self.assertIn("effort", result.stderr)
        self.assertIn("High1", result.stderr)

    def test_rejects_missing_parent_before_creating_terminal(self):
        result = self.invoke(parent="docs/plans/missing-plan.md")
        self.assert_no_create(result)
        self.assertIn("parent", result.stderr)
        self.assertIn("docs/plans/missing-plan.md", result.stderr)

    def test_rejects_existing_file_outside_plan_name_pattern(self):
        for parent in ("docs/plans/notes.md", "README.md"):
            with self.subTest(parent=parent):
                (self.repo / parent).write_text("not a plan")
                self.commit_all(f"add {parent}")
                result = self.invoke(parent=parent)
                self.assert_no_create(result)
                self.assertIn("parent", result.stderr)
                self.assertIn(parent, result.stderr)

    def test_rejects_untracked_file_before_creating_terminal(self):
        (self.repo / "untracked.txt").write_text("dirty")
        result = self.invoke()
        self.assert_no_create(result)
        self.assertIn("untracked.txt", result.stderr)

    def test_rejects_non_empty_child_worktrees_before_creating_terminal(self):
        self.env["ORCA_STUB_CHILDREN"] = "child-1"
        result = self.invoke()
        self.assert_no_create(result)
        self.assertIn("child-1", result.stderr)

    def test_rejects_failed_child_query_before_creating_terminal(self):
        self.env["ORCA_STUB_FAIL"] = "worktree-show"
        result = self.invoke()
        self.assert_no_create(result)
        self.assertIn("childWorktreeIds", result.stderr)

    def test_rejects_child_answer_without_child_field_before_creating_terminal(self):
        self.env["ORCA_STUB_NO_CHILD_FIELD"] = "1"
        result = self.invoke()
        self.assert_no_create(result)
        self.assertIn("childWorktreeIds", result.stderr)

    def test_rejects_unreachable_runtime_before_creating_terminal(self):
        for mode in ("unreachable", "fail"):
            with self.subTest(mode=mode):
                self.env["ORCA_STUB_STATUS"] = mode
                result = self.invoke()
                self.assert_no_create(result)
                self.assertIn("not reachable", result.stderr)

    def test_refuses_in_the_documented_order(self):
        untracked = self.repo / "untracked.txt"
        untracked.write_text("dirty")
        self.env["ORCA_STUB_STATUS"] = "unreachable"
        self.env["ORCA_STUB_CHILDREN"] = "child-1"
        missing = "docs/plans/missing-plan.md"

        def refusal(wanted, **kwargs):
            with self.subTest(wanted=wanted):
                result = self.invoke(**kwargs)
                self.assert_no_create(result)
                self.assertIn(wanted, result.stderr)

        refusal("model", model="gpt-6-sol", effort="High1", parent=missing)
        refusal("effort", effort="High1", parent=missing)
        refusal("parent must", parent=missing)
        refusal("untracked.txt")
        untracked.unlink()
        refusal("not reachable")
        self.env["ORCA_STUB_STATUS"] = ""
        refusal("child-1")

    def test_starts_successor_after_screen_shows_working_hint(self):
        result = self.invoke(effort="high")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("term-1", result.stdout)
        creates = [call for call in self.calls().splitlines() if "terminal create" in call]
        self.assertEqual(len(creates), 1)
        command = creates[0]
        self.assertIn("--worktree active", command)
        self.assertIn("--title drive", command)
        self.assertIn("--model claude-opus-5-5", command)
        self.assertIn("switchModelsOnFlag", command)
        self.assertIn(".claude/skills/drive/SKILL.md", command)
        self.assertIn("--effort high", command)
        self.assertTrue(
            command.endswith(
                ' "Read .claude/skills/drive/SKILL.md and drive docs/plans/parent-plan.md." --json'
            ),
            command,
        )

    def test_waits_for_working_hint_on_a_later_read(self):
        self.env["ORCA_STUB_HINT_AFTER"] = "3"
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("term-1", result.stdout)
        self.assertEqual(self.count_calls("terminal read"), 3)
        self.assertNotIn("terminal close", self.calls())

    def test_failed_terminal_create_reports_successor_may_be_running(self):
        self.env["ORCA_STUB_FAIL"] = "terminal-create"
        result = self.invoke()
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn("successor may be running", result.stderr)
        self.assertEqual(self.count_calls("terminal read"), 0)

    def test_closes_refused_successor_when_screen_has_no_hint(self):
        self.env["ORCA_STUB_SCREEN"] = "Read .claude/skills/drive/SKILL.md and drive docs/plans/parent-plan.md."
        result = self.invoke()
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertEqual(self.count_calls("terminal read"), 20)
        self.assertIn("terminal close", self.calls())
        self.assertNotIn("successor may be running", result.stderr)

    def test_failed_close_reports_successor_may_be_running(self):
        self.env["ORCA_STUB_SCREEN"] = "echoed command"
        self.env["ORCA_STUB_CLOSE_FAIL"] = "1"
        result = self.invoke()
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn("successor may be running", result.stderr)
        self.assertIn("term-1", result.stderr)

    def test_empty_handle_reports_successor_may_be_running(self):
        self.env["ORCA_STUB_NO_HANDLE"] = "1"
        result = self.invoke()
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn("successor may be running", result.stderr)
        self.assertNotIn("terminal close", self.calls())


if __name__ == "__main__":
    unittest.main()
