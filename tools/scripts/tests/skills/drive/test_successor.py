import contextlib
import io
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

from skills.delegate import orca_worker
from skills.drive import successor

ORCA_STUB = r'''
import json
import os
import sys

with open(os.environ["ORCA_STUB_LOG"], "a") as log:
    log.write(" ".join(sys.argv[1:]) + "\n")

args = sys.argv[1:]
if " ".join(args[:2]) == os.environ.get("ORCA_STUB_NON_JSON"):
    print("not JSON")
    raise SystemExit(0)
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
    if os.environ.get("ORCA_STUB_NULL_HANDLE") == "1":
        handle = None
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


class SuccessorTests(unittest.TestCase):
    def run_orca(self, args):
        out = io.StringIO()
        err = io.StringIO()
        code = 0
        with mock.patch.object(sys, "argv", ["orca", *args[1:]]), contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            try:
                exec(ORCA_STUB, {})
            except SystemExit as error:
                code = error.code or 0
        return subprocess.CompletedProcess(args, code, out.getvalue(), err.getvalue())

    def run_port(self, argv):
        real_run = orca_worker.run

        def transport(args, cwd=None):
            self.argvs.append(list(args))
            if self.git_status is not None and args[:2] == ["git", "status"]:
                return self.git_status
            if args[0] == "orca" and self.orca_missing:
                raise FileNotFoundError("orca")
            return self.run_orca(args) if args[0] == "orca" else real_run(args, cwd)

        out = io.StringIO()
        err = io.StringIO()
        previous = Path.cwd()
        clock = SimpleNamespace(sleep=self.sleeps.append)
        try:
            os.chdir(self.repo)
            with mock.patch.dict(os.environ, self.env, clear=True), mock.patch.object(orca_worker, "windows", return_value=self.windows), mock.patch.object(orca_worker, "run", side_effect=transport), mock.patch.object(successor, "time", clock), contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
                code = successor.main(argv)
        finally:
            os.chdir(previous)
        return subprocess.CompletedProcess(argv, code, out.getvalue(), err.getvalue())

    def setUp(self):
        self.windows = False
        self.orca_missing = False
        self.git_status = None
        self.sleeps = []
        self.argvs = []
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

        self.log = self.root / "orca.log"
        self.env = {
            **os.environ,
            "HOME": str(self.home),
            "ORCA_STUB_LOG": str(self.log),
            "ORCA_STUB_CHILDREN": "",
            "ORCA_STUB_SCREEN": "esc to interrupt",
        }

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
            parent or "docs/plans/parent-plan.md",
            "--cli",
            cli,
            "--model",
            model,
        ]
        if effort is not None:
            args.extend(["--effort", effort])
        return self.run_port(args)

    def calls(self):
        return self.log.read_text() if self.log.exists() else ""

    def count_calls(self, prefix):
        return sum(1 for call in self.calls().splitlines() if call.startswith(prefix))

    def prompt(self, parent="docs/plans/parent-plan.md"):
        return shlex.quote(f"Read .claude/skills/drive/SKILL.md and drive {parent}.")

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
        refusal("cli claude does not match model pool cli codex", model="gpt-6-sol", effort="High1", parent=missing)
        refusal("effort", effort="High1", parent=missing)
        agy = {"model": "gemini-3.8-flash", "cli": "agy"}
        refusal("parent must", parent=missing, **agy)
        refusal("untracked.txt", **agy)
        untracked.unlink()
        refusal("not reachable", **agy)
        self.env["ORCA_STUB_STATUS"] = ""
        refusal("child-1", **agy)
        self.env["ORCA_STUB_CHILDREN"] = ""
        refusal("effort is required for model id format", **agy)

    def test_starts_successor_after_screen_shows_working_hint(self):
        result = self.invoke(effort="high")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("term-1", result.stdout)
        creates = [call for call in self.calls().splitlines() if "terminal create" in call]
        self.assertEqual(len(creates), 1)
        command = creates[0]
        self.assertIn("--worktree active", command)
        self.assertIn("--title drive", command)
        expected = 'env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_CHILD_SESSION claude --model claude-opus-5-5 --dangerously-skip-permissions --settings \'{"switchModelsOnFlag":false}\' --effort high'
        prompt = self.prompt()
        self.assertTrue(command.endswith(f" --command {expected} {prompt} --json"), command)

    def test_starts_codex_with_worker_line_and_positional_prompt(self):
        result = self.invoke(model="gpt-6-sol", cli="codex", effort="xhigh")
        self.assertEqual(result.returncode, 0, result.stderr)
        expected = 'env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_CHILD_SESSION codex -a never --sandbox danger-full-access -c check_for_update_on_startup=false -c background_terminal_max_timeout=3600000 -m gpt-6-sol -c model_reasoning_effort=xhigh'
        prompt = self.prompt()
        self.assertIn(f"--command {expected} {prompt} --json", self.calls())

    def test_starts_agy_with_effort_in_model_id_and_interactive_prompt(self):
        result = self.invoke(
            model="gemini-3.8-flash", cli="agy", effort="high"
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        expected = 'env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_CHILD_SESSION agy --model gemini-3.8-flash-high --dangerously-skip-permissions'
        prompt = self.prompt()
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
        self.assertEqual(self.sleeps, [3, 3])
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
        self.assertEqual(self.sleeps, [3] * 20)
        self.assertIn("terminal close", self.calls())
        self.assertNotIn("successor may be running", result.stderr)

    def test_failed_close_reports_successor_may_be_running(self):
        self.env["ORCA_STUB_SCREEN"] = "echoed command"
        self.env["ORCA_STUB_CLOSE_FAIL"] = "1"
        result = self.invoke()
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn("successor may be running", result.stderr)
        self.assertIn("term-1", result.stderr)

    def test_rejects_missing_argument_values_before_creating_terminal(self):
        parent = "docs/plans/parent-plan.md"
        cases = [
            ([], "parent is required"),
            ([parent, "--model", "claude-opus-5-5"], "--cli is required"),
            ([parent, "--cli", "claude"], "--model is required"),
            ([parent, "--cli"], "--cli requires a value"),
            ([parent, "--cli", "claude", "--model"], "--model requires a value"),
            ([parent, "--cli", "claude", "--model", "claude-opus-5-5", "--effort"], "--effort requires a value"),
            ([parent, "--mode", "x"], "unknown argument: --mode"),
        ]
        for argv, wanted in cases:
            with self.subTest(wanted=wanted):
                result = self.run_port(argv)
                self.assert_no_create(result)
                self.assertIn(wanted, result.stderr)
        self.assertEqual(self.calls(), "")

    def test_rejects_unknown_cli_even_when_its_model_is_in_plan_fit(self):
        text = self.registry_text(fit="omp-model").replace(
            "  google: {cli: agy}", "  google: {cli: agy}\n  omp: {cli: omp}"
        ).replace("models:\n", "models:\n  omp-model: {pool: omp}\n")
        self.machine_registry.write_text(text)
        self.project_registry.write_text(text)
        self.commit_all("add omp model to plan fit")
        result = self.invoke(model="omp-model", cli="omp")
        self.assert_no_create(result)
        self.assertIn("unknown cli: omp", result.stderr)
        self.assertEqual(self.argvs, [])

    def test_refuses_non_json_status_reply(self):
        self.env["ORCA_STUB_NON_JSON"] = "status --json"
        result = self.invoke()
        self.assert_no_create(result)
        self.assertIn("Orca runtime is not reachable", result.stderr)
        self.assertEqual(self.count_calls("worktree show"), 0)

    def test_refuses_non_json_worktree_reply(self):
        self.env["ORCA_STUB_NON_JSON"] = "worktree show"
        result = self.invoke()
        self.assert_no_create(result)
        self.assertIn("child query returned no childWorktreeIds", result.stderr)

    def test_non_json_terminal_create_reports_successor_may_be_running(self):
        self.env["ORCA_STUB_NON_JSON"] = "terminal create"
        result = self.invoke()
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn("successor may be running", result.stderr)
        self.assertEqual(self.count_calls("terminal create"), 1)
        self.assertEqual(self.count_calls("terminal read"), 0)
        self.assertEqual(self.count_calls("terminal close"), 0)

    def test_null_handle_reports_successor_may_be_running(self):
        self.env["ORCA_STUB_NULL_HANDLE"] = "1"
        result = self.invoke()
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn("successor may be running", result.stderr)
        self.assertEqual(self.count_calls("terminal create"), 1)
        self.assertEqual(self.count_calls("terminal read"), 0)
        self.assertEqual(self.count_calls("terminal close"), 0)

    def test_refuses_missing_orca_on_status(self):
        self.orca_missing = True
        result = self.invoke()
        self.assert_no_create(result)
        self.assertIn("Orca runtime is not reachable", result.stderr)
        self.assertEqual(self.argvs, [
            ["git", "status", "--porcelain"], ["orca", "status", "--json"]
        ])

    def test_rejects_empty_effort_value(self):
        result = self.invoke(effort="")
        self.assert_no_create(result)
        self.assertIn("effort must contain lowercase letters", result.stderr)

    def test_refuses_diagnostic_stderr_from_a_successful_status(self):
        self.git_status = subprocess.CompletedProcess(["git", "status"], 0, "", "warning: untrusted index\n")
        result = self.invoke()
        self.assert_no_create(result)
        self.assertIn("working tree is dirty: warning: untrusted index", result.stderr)

    def test_windows_refuses_before_any_tool_call(self):
        self.windows = True
        result = self.invoke(effort="high")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("Windows terminal launches are unsupported", result.stderr)
        self.assertEqual(self.argvs, [])
        self.assertEqual(self.calls(), "")

    def test_quotes_parent_path_and_model_id_for_the_terminal_shell(self):
        parent = "docs/plans/odd name $HOME `x`-plan.md"
        (self.repo / parent).write_text("plan")
        text = self.registry_text().replace(
            "  gemini-3.8-flash:",
            '  odd-model: {pool: google, id_format: "odd; $x-<effort>"}\n  gemini-3.8-flash:',
        ).replace("gemini-3.8-flash]", "gemini-3.8-flash, odd-model]")
        self.machine_registry.write_text(text)
        self.project_registry.write_text(text)
        self.commit_all("add odd plan")
        result = self.invoke(model="odd-model", cli="agy", effort="high", parent=parent)
        self.assertEqual(result.returncode, 0, result.stderr)
        create = next(argv for argv in self.argvs if argv[:3] == ["orca", "terminal", "create"])
        command = create[create.index("--command") + 1]
        # Double quotes would let the terminal shell expand $HOME and the backticks.
        self.assertTrue(command.endswith(f" '{'Read .claude/skills/drive/SKILL.md and drive ' + parent + '.'}'"), command)
        words = shlex.split(command)
        self.assertEqual(words[words.index("--model") + 1], "odd; $x-high")
        self.assertEqual(words[-2:], ["--prompt-interactive", f"Read .claude/skills/drive/SKILL.md and drive {parent}."])

    def test_empty_handle_reports_successor_may_be_running(self):
        self.env["ORCA_STUB_NO_HANDLE"] = "1"
        result = self.invoke()
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn("successor may be running", result.stderr)
        self.assertNotIn("terminal close", self.calls())


if __name__ == "__main__":
    unittest.main()
