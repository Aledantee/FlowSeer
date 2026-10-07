import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest

from lib import repo

SKILLS = repo.root(Path(__file__).parent) / ".agents/skills"
SCRIPT = SKILLS / "drive/scripts/successor.sh"


class SuccessorTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.home = self.root / "home"
        self.machine_registry = self.home / ".claude/models/registry.yaml"
        self.machine_registry.parent.mkdir(parents=True)
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
        self.project_registry = self.repo / ".claude/models/registry.yaml"
        self.project_registry.parent.mkdir(parents=True)
        self.write_registries()
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
            "HOME": str(self.home),
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

    def registry_text(self, fit="claude-opus-5-5, gpt-6-sol, gemini-3.8-flash"):
        return (
            "pools:\n"
            "  claude: {cli: claude}\n"
            "  codex: {cli: codex}\n"
            "  google: {cli: agy}\n"
            "models:\n"
            "  claude-opus-5-5: {pool: claude}\n"
            "  claude-opus-5: {pool: claude}\n"
            "  gpt-6-sol: {pool: codex}\n"
            "  gemini-3.8-flash: {pool: google, id_format: \"gemini-3.8-flash-<effort>\"}\n"
            "roles:\n"
            f"  plan: {{effort: xhigh, fit: [{fit}]}}\n"
        )

    def write_registries(self, machine_fit=None, project_fit=None):
        self.machine_registry.write_text(
            self.registry_text(machine_fit or "claude-opus-5-5, gpt-6-sol, gemini-3.8-flash")
        )
        self.project_registry.write_text(
            self.registry_text(project_fit or "claude-opus-5-5, gpt-6-sol, gemini-3.8-flash")
        )

    def invoke(
        self,
        model="claude-opus-5-5",
        effort=None,
        parent=None,
        cli="claude",
    ):
        args = [
            str(SCRIPT),
            parent or "docs/plans/parent-plan.md",
            "--cli",
            cli,
            "--model",
            model,
        ]
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

    def worker_line(self, cli, model, effort=None):
        worker = SKILLS / "delegate/scripts/orca-worker.sh"
        args = [str(worker), "line", "--cli", cli, "--model", model]
        if effort is not None:
            args.extend(["--effort", effort])
        result = subprocess.run(
            args,
            cwd=self.repo,
            env=self.env,
            capture_output=True,
            text=True,
            check=True,
        )
        return result.stdout.strip()

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

    def test_rejects_model_outside_plan_fit_before_creating_terminal(self):
        result = self.invoke(model="claude-opus-5")
        self.assert_no_create(result)
        self.assertIn("model", result.stderr)
        self.assertIn("claude-opus-5", result.stderr)

    def test_rejects_model_on_another_cli_before_creating_terminal(self):
        result = self.invoke(model="gpt-6-sol", cli="claude")
        self.assert_no_create(result)
        self.assertIn("cli", result.stderr)
        self.assertIn("codex", result.stderr)

    def test_accepts_fit_entry_with_effort_suffix(self):
        self.write_registries(project_fit="claude-opus-5-5@xhigh")
        self.commit_all("pin plan fit effort")
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_project_registry_replaces_machine_fit(self):
        self.write_registries(machine_fit="gpt-6-sol", project_fit="claude-opus-5-5")
        self.commit_all("narrow project plan fit")
        result = self.invoke(model="gpt-6-sol", cli="codex")
        self.assert_no_create(result)
        self.assertIn("model", result.stderr)
        self.assertIn("gpt-6-sol", result.stderr)

    def test_rejects_missing_registry_before_creating_terminal(self):
        self.machine_registry.unlink()
        self.project_registry.unlink()
        subprocess.run(
            ["git", "rm", str(self.project_registry.relative_to(self.repo))],
            cwd=self.repo,
            check=True,
            capture_output=True,
        )
        self.commit_all("remove registry")
        result = self.invoke()
        self.assert_no_create(result)
        self.assertIn("registry", result.stderr)

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

        refusal("model", model="claude-opus-5", effort="High1", parent=missing)
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
        expected = self.worker_line("claude", "claude-opus-5-5", "high")
        prompt = '"Read .claude/skills/drive/SKILL.md and drive docs/plans/parent-plan.md."'
        self.assertTrue(command.endswith(f" --command {expected} {prompt} --json"), command)

    def test_starts_codex_with_worker_line_and_positional_prompt(self):
        result = self.invoke(model="gpt-6-sol", cli="codex", effort="xhigh")
        self.assertEqual(result.returncode, 0, result.stderr)
        expected = self.worker_line("codex", "gpt-6-sol", "xhigh")
        prompt = '"Read .claude/skills/drive/SKILL.md and drive docs/plans/parent-plan.md."'
        self.assertIn(f"--command {expected} {prompt} --json", self.calls())

    def test_starts_agy_with_effort_in_model_id_and_interactive_prompt(self):
        result = self.invoke(
            model="gemini-3.8-flash", cli="agy", effort="high"
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        expected = self.worker_line("agy", "gemini-3.8-flash-high")
        prompt = '"Read .claude/skills/drive/SKILL.md and drive docs/plans/parent-plan.md."'
        self.assertIn(
            f"--command {expected} --prompt-interactive {prompt} --json", self.calls()
        )

    def test_rejects_agy_without_effort_before_creating_terminal(self):
        result = self.invoke(model="gemini-3.8-flash", cli="agy")
        self.assert_no_create(result)
        self.assertIn("effort", result.stderr)

    def test_accepts_agy_cancel_hint(self):
        self.env["ORCA_STUB_SCREEN"] = "esc to cancel"
        result = self.invoke(model="gemini-3.8-flash", cli="agy", effort="high")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("term-1", result.stdout)

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
