import json
import os
import re
import shutil
import unittest
from pathlib import Path

import run
from tests.hooks.support import BASH, HOOK_GROUP, HOOKS_DIR, REPO, HookCase, decision_of, registered, run_command

CLAUDE_CONFIG = REPO / ".claude" / "settings.json"
CODEX_CONFIG = REPO / ".codex" / "hooks.json"

EDIT_CLAUDE = "Edit|Write|MultiEdit|NotebookEdit"
EDIT_CODEX = "Edit|Write"
STOP_TIMEOUT = 30
PROJECT_DIR = "${CLAUDE_PROJECT_DIR}"

CLAUDE_EVENTS = ["PostToolUse", "PreToolUse", "SessionStart", "Stop", "WorktreeCreate"]
CODEX_EVENTS = ["PostToolUse", "PreToolUse", "SessionStart", "Stop"]

# The hooks each event runs, in order, for the runtime's matcher. None is an entry with no matcher.
CLAUDE_MAP = {
    ("SessionStart", None): ["worktree-guard"],
    ("WorktreeCreate", None): ["create-worktree"],
    ("PreToolUse", EDIT_CLAUDE): ["worktree-guard", "pre-tool-policy"],
    ("PreToolUse", "Bash"): ["worktree-guard", "protect-generated-bash"],
    ("PostToolUse", EDIT_CLAUDE): ["go-format", "proto-check", "suppression-warn", "mark-verification-dirty"],
    ("PostToolUse", "Bash"): ["mark-verification-dirty", "commit-message-check"],
    ("Stop", None): ["stop-check"],
}
CODEX_MAP = {
    ("SessionStart", None): ["worktree-guard"],
    ("PreToolUse", EDIT_CODEX): ["worktree-guard", "pre-tool-policy"],
    ("PreToolUse", "Bash"): ["worktree-guard", "protect-generated-bash"],
    ("PostToolUse", EDIT_CODEX): ["go-format", "proto-check", "suppression-warn", "mark-verification-dirty"],
    ("PostToolUse", "Bash"): ["mark-verification-dirty", "commit-message-check"],
    ("Stop", None): ["stop-check"],
}

# The two hooks that keep their shell scripts. Every other hook is a command of the `hooks` group.
SHELL_HOOKS = {"worktree-guard", "create-worktree"}


def claude_hook(name: str) -> dict:
    if name in SHELL_HOOKS:
        return {"type": "command", "command": f'"$CLAUDE_PROJECT_DIR/tools/hooks/{name}.sh"'}
    return {"type": "command", "command": "uv", "args": ["run", f"{PROJECT_DIR}/tools/scripts/run.py", "hooks", name]}


# Codex has no project-directory variable, so its commands ask git for the
# root, and each names what it could not find: a bare
# "$(git rev-parse --show-toplevel)/tools/hooks/x.sh" runs /tools/hooks/x.sh
# when git prints nothing and exits 127 without a reason.
def codex_hook(name: str, event: str) -> dict:
    if name in SHELL_HOOKS:
        command = (
            f"root=$(git rev-parse --show-toplevel 2>/dev/null); hook=$root/tools/hooks/{name}.sh; "
            f'[ -n "$root" ] && [ -x "$hook" ] || {{ echo "FlowSeer hook {name}.sh cannot run from $PWD: '
            f'repository root ${{root:-unresolved}}, git $(command -v git || echo not on PATH)" >&2; exit 1; }}; '
            'exec "$hook"'
        )
    else:
        # Codex blocks a tool call on exit 2 with a reason, and reports any other
        # code as a failed hook that does not block. Exit 2 on Stop would keep the
        # session from stopping, so only PreToolUse takes it.
        refusal = 2 if event == "PreToolUse" else 1
        command = (
            "root=$(git rev-parse --show-toplevel 2>/dev/null); "
            '[ -n "$root" ] && [ -f "$root/tools/scripts/run.py" ] && command -v uv >/dev/null 2>&1 || '
            f'{{ echo "FlowSeer hook {name} cannot run from $PWD: repository root ${{root:-unresolved}}, '
            f'git $(command -v git || echo not on PATH), uv $(command -v uv || echo not on PATH)" >&2; exit {refusal}; }}; '
            f'exec uv run "$root/tools/scripts/run.py" hooks {name}'
        )
    return {"type": "command", "command": command}


def load(path: Path) -> dict:
    return json.loads(path.read_text())


def hooks_of(config: dict, event: str, matcher: str | None) -> list[dict]:
    found = []
    for entry in config["hooks"][event]:
        if entry.get("matcher") == matcher:
            found += entry["hooks"]
    return found


def without_timeout(hook: dict) -> dict:
    return {key: value for key, value in hook.items() if key != "timeout"}


def name_of(hook: dict) -> str:
    """The hook a registration starts, read from its arguments or its command string."""
    if "args" in hook:
        return hook["args"][-1]
    command = hook["command"]
    found = re.search(r" hooks (\S+)$", command) or re.search(r"tools/hooks/([a-z-]+)\.sh", command)
    assert found, f"no hook name in {command}"
    return found.group(1)


def configured(config: dict, event: str, matcher: str, name: str) -> dict:
    (hook,) = [h for h in hooks_of(config, event, matcher) if name_of(h) == name]
    return hook


def entries_of(config: dict):
    for event, entries in config["hooks"].items():
        for entry in entries:
            for hook in entry["hooks"]:
                yield event, hook


def claude_argv(hook: dict, project: Path) -> list[str]:
    """The process Claude Code starts for a registration, with its path placeholder substituted.

    Only the substitution is done here. Claude Code's own is not covered.
    """
    if "args" in hook:
        return [shutil.which(hook["command"]) or hook["command"], *(a.replace(PROJECT_DIR, str(project)) for a in hook["args"])]
    return [BASH, "-c", hook["command"]]


class RegistrationTest(HookCase):
    def test_settings_map_every_event_to_executable_hooks(self):
        """settings map every event to executable hooks"""
        claude, codex = load(CLAUDE_CONFIG), load(CODEX_CONFIG)
        self.assertEqual(sorted(claude["hooks"]), CLAUDE_EVENTS)
        self.assertEqual(sorted(codex["hooks"]), CODEX_EVENTS)
        for config, mapping, render in (
            (claude, CLAUDE_MAP, lambda name, event: claude_hook(name)),
            (codex, CODEX_MAP, codex_hook),
        ):
            entries = sum(len(config["hooks"][event]) for event in config["hooks"])
            self.assertEqual(entries, len(mapping))
            for (event, matcher), names in mapping.items():
                with self.subTest(event=event, matcher=matcher):
                    self.assertEqual(
                        [without_timeout(h) for h in hooks_of(config, event, matcher)],
                        [render(name, event) for name in names],
                    )
        names = {name for found in (*CLAUDE_MAP.values(), *CODEX_MAP.values()) for name in found}
        self.assertEqual(len(names), 10)
        for name in sorted(names & SHELL_HOOKS):
            with self.subTest(script=name):
                script = HOOKS_DIR / f"{name}.sh"
                self.assertTrue(script.is_file(), f"{script} is missing")
                self.assertTrue(os.access(script, os.X_OK), f"configured hook is not executable: {script}")

    def test_every_registered_hook_name_is_a_command_of_the_hooks_group(self):
        """every registered hook name is a command of the hooks group"""
        commands = run.read_commands(HOOK_GROUP)
        self.assertEqual(set(commands), registered())
        for runtime, config in (("claude", load(CLAUDE_CONFIG)), ("codex", load(CODEX_CONFIG))):
            for event, hook in entries_of(config):
                name = name_of(hook)
                if name in SHELL_HOOKS:
                    continue
                with self.subTest(runtime=runtime, event=event, hook=name):
                    self.assertIn(name, commands, f"{name} is not registered in {HOOK_GROUP}")
                    self.assertTrue((HOOK_GROUP.parent / f"{commands[name]}.py").is_file())

    def test_claude_hooks_start_in_exec_form(self):
        """a Claude Code hook starts uv with the project directory placeholder in its arguments"""
        for event, hook in entries_of(load(CLAUDE_CONFIG)):
            name = name_of(hook)
            if name in SHELL_HOOKS:
                continue
            with self.subTest(event=event, hook=name):
                self.assertEqual(hook["command"], "uv")
                self.assertEqual(hook["args"], ["run", f"{PROJECT_DIR}/tools/scripts/run.py", "hooks", name])

    def test_the_stop_gate_carries_the_timeout_both_runtimes_wait_for(self):
        """both runtimes give the Stop gate the same timeout, and no other hook has one"""
        for config in (load(CLAUDE_CONFIG), load(CODEX_CONFIG)):
            timeouts = {(event, name_of(hook)): hook["timeout"] for event, hook in entries_of(config) if "timeout" in hook}
            self.assertEqual(list(timeouts), [("Stop", "stop-check")])
            self.assertEqual(set(timeouts.values()), {STOP_TIMEOUT})

    def test_configured_edit_guards_deny_generated_output(self):
        """configured edit guards deny generated output"""
        payload = self.edit(self.fixture / "generated" / "device.pb.go")
        claude = configured(load(CLAUDE_CONFIG), "PreToolUse", EDIT_CLAUDE, "pre-tool-policy")
        result = run_command(
            claude_argv(claude, REPO), payload, cwd=self.parent, env=self.env_with(CLAUDE_PROJECT_DIR=str(REPO))
        )
        self.assertEqual(result.code, 0, result.stderr)
        self.assertEqual(decision_of(result), "deny")
        codex = configured(load(CODEX_CONFIG), "PreToolUse", EDIT_CODEX, "pre-tool-policy")
        result = run_command([BASH, "-c", codex["command"]], payload, cwd=REPO, env=self.env)
        self.assertEqual(result.code, 0, result.stderr)
        self.assertEqual(decision_of(result), "deny")

    def test_a_codex_hook_command_names_the_root_git_or_uv_it_could_not_find(self):
        """a Codex hook command names the root, git, or uv it could not find"""
        # The four ways the command cannot reach its hook, each reported on
        # standard error with exit 2: a session directory outside any repository,
        # a PATH without git, a PATH without uv, and a checkout without run.py.
        command = configured(load(CODEX_CONFIG), "PreToolUse", EDIT_CODEX, "pre-tool-policy")["command"]
        payload = self.edit(self.fixture / "generated" / "device.pb.go")
        outside = self.parent / "outside any repository"
        outside.mkdir()
        ceiling = self.env_with(GIT_CEILING_DIRECTORIES=str(self.parent))
        result = run_command([BASH, "-c", command], payload, cwd=outside, env=ceiling)
        self.assertEqual(result.code, 2)
        self.assertTrue(
            result.stderr.startswith(
                f"FlowSeer hook pre-tool-policy cannot run from {outside}: repository root unresolved, git "
            ),
            result.stderr,
        )
        result = run_command([BASH, "-c", command], payload, cwd=REPO, env=self.env_with(PATH=str(outside)))
        self.assertEqual(result.code, 2)
        self.assertEqual(
            result.stderr,
            f"FlowSeer hook pre-tool-policy cannot run from {REPO}: repository root unresolved, "
            "git not on PATH, uv not on PATH\n",
        )
        without_uv = self.path_of(self.parent / "path without uv", "git")
        result = run_command([BASH, "-c", command], payload, cwd=REPO, env=self.env_with(PATH=without_uv))
        self.assertEqual(result.code, 2)
        self.assertEqual(
            result.stderr,
            f"FlowSeer hook pre-tool-policy cannot run from {REPO}: repository root {REPO}, "
            f"git {without_uv}/git, uv not on PATH\n",
        )
        result = run_command([BASH, "-c", command], payload, cwd=self.fixture, env=self.env)
        self.assertEqual(result.code, 2)
        self.assertTrue(
            result.stderr.startswith(
                f"FlowSeer hook pre-tool-policy cannot run from {self.fixture}: repository root {self.fixture}, git "
            ),
            result.stderr,
        )

    def test_only_a_codex_pre_tool_use_command_that_cannot_start_blocks(self):
        """a Codex command that cannot start exits 2 for PreToolUse and 1 for every other event"""
        outside = self.parent / "outside any repository"
        outside.mkdir()
        env = self.env_with(GIT_CEILING_DIRECTORIES=str(self.parent))
        for event, hook in entries_of(load(CODEX_CONFIG)):
            name = name_of(hook)
            if name in SHELL_HOOKS:
                continue
            with self.subTest(event=event, hook=name):
                result = run_command([BASH, "-c", hook["command"]], "{}", cwd=outside, env=env)
                self.assertEqual(result.code, 2 if event == "PreToolUse" else 1, result.stderr)
                self.assertIn(f"FlowSeer hook {name} cannot run from", result.stderr)

    def test_configured_bash_guard_denies_generated_file_mutation(self):
        """configured Bash guard denies generated-file mutation"""
        payload = self.shell("sed -i '' -e s/old/new/ generated/device.pb.go")
        claude = configured(load(CLAUDE_CONFIG), "PreToolUse", "Bash", "protect-generated-bash")
        result = run_command(
            claude_argv(claude, REPO), payload, cwd=self.parent, env=self.env_with(CLAUDE_PROJECT_DIR=str(REPO))
        )
        self.assertEqual(result.code, 0, result.stderr)
        self.assertEqual(decision_of(result), "deny")
        codex = configured(load(CODEX_CONFIG), "PreToolUse", "Bash", "protect-generated-bash")
        result = run_command([BASH, "-c", codex["command"]], payload, cwd=REPO, env=self.env)
        self.assertEqual(result.code, 0, result.stderr)
        self.assertEqual(decision_of(result), "deny")

    def test_configured_worktree_create_handles_project_and_repository_paths_with_spaces(self):
        """configured WorktreeCreate handles project and repository paths with spaces"""
        project = self.parent / "project root with spaces"
        project.symlink_to(REPO)
        root = self.parent / "external-worktrees"
        (hook,) = hooks_of(load(CLAUDE_CONFIG), "WorktreeCreate", None)
        payload = {"cwd": str(self.fixture), "hook_event_name": "WorktreeCreate", "name": "external-checkout"}
        env = self.env_with(FLOWSEER_WORKTREE_ROOT=str(root), CLAUDE_PROJECT_DIR=str(project))
        result = run_command([BASH, "-c", hook["command"]], payload, cwd=self.parent, env=env)
        created = root / "external-checkout"
        self.assertEqual((result.code, result.stdout), (0, f"{created}\n"), result.stderr)
        self.assertEqual(self.git(created, "rev-parse", "--is-inside-work-tree"), "true")
        self.assertEqual(self.git(created, "symbolic-ref", "--short", "HEAD"), "worktree-external-checkout")


if __name__ == "__main__":
    unittest.main()
