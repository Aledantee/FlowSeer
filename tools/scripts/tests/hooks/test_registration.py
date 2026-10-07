import json
import os
import unittest
from pathlib import Path

from tests.hooks.support import BASH, HOOKS_DIR, REPO, HookCase, decision_of, run_command

CLAUDE_CONFIG = REPO / ".claude" / "settings.json"
CODEX_CONFIG = REPO / ".codex" / "hooks.json"

EDIT_CLAUDE = "Edit|Write|MultiEdit|NotebookEdit"
EDIT_CODEX = "Edit|Write"
STOP_TIMEOUT = 30

CLAUDE_EVENTS = ["PostToolUse", "PreToolUse", "SessionStart", "Stop", "WorktreeCreate"]
CODEX_EVENTS = ["PostToolUse", "PreToolUse", "SessionStart", "Stop"]

# The scripts each event runs, in order, for the runtime's matcher. None is an entry with no matcher.
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


def claude_command(name: str) -> str:
    return f'"$CLAUDE_PROJECT_DIR/tools/hooks/{name}.sh"'


# Codex has no project-directory variable, so its commands ask git for the
# root, and each names what it could not find: a bare
# "$(git rev-parse --show-toplevel)/tools/hooks/x.sh" runs /tools/hooks/x.sh
# when git prints nothing and exits 127 without a reason.
def codex_command(name: str) -> str:
    return (
        f"root=$(git rev-parse --show-toplevel 2>/dev/null); hook=$root/tools/hooks/{name}.sh; "
        f'[ -n "$root" ] && [ -x "$hook" ] || {{ echo "FlowSeer hook {name}.sh cannot run from $PWD: '
        f'repository root ${{root:-unresolved}}, git $(command -v git || echo not on PATH)" >&2; exit 1; }}; '
        'exec "$hook"'
    )


def load(path: Path) -> dict:
    return json.loads(path.read_text())


def commands(config: dict, event: str, matcher: str | None) -> list[str]:
    found = []
    for entry in config["hooks"][event]:
        if entry.get("matcher") == matcher:
            found += [hook["command"] for hook in entry["hooks"]]
    return found


def configured(config: dict, event: str, matcher: str, name: str) -> str:
    (command,) = [c for c in commands(config, event, matcher) if name in c]
    return command


class RegistrationTest(HookCase):
    def test_settings_map_every_event_to_executable_hooks(self):
        """settings map every event to executable hooks"""
        claude, codex = load(CLAUDE_CONFIG), load(CODEX_CONFIG)
        self.assertEqual(sorted(claude["hooks"]), CLAUDE_EVENTS)
        self.assertEqual(sorted(codex["hooks"]), CODEX_EVENTS)
        for config, mapping, render in ((claude, CLAUDE_MAP, claude_command), (codex, CODEX_MAP, codex_command)):
            entries = sum(len(config["hooks"][event]) for event in config["hooks"])
            self.assertEqual(entries, len(mapping))
            for (event, matcher), names in mapping.items():
                with self.subTest(event=event, matcher=matcher):
                    self.assertEqual(commands(config, event, matcher), [render(name) for name in names])
        registered = {name for names in (*CLAUDE_MAP.values(), *CODEX_MAP.values()) for name in names}
        self.assertEqual(len(registered), 10)
        for name in sorted(registered):
            with self.subTest(script=name):
                script = HOOKS_DIR / f"{name}.sh"
                self.assertTrue(script.is_file(), f"{script} is missing")
                self.assertTrue(os.access(script, os.X_OK), f"configured hook is not executable: {script}")

    def test_the_stop_gate_carries_the_timeout_both_runtimes_wait_for(self):
        """both runtimes give the Stop gate the same timeout, and no other hook has one"""
        for config in (load(CLAUDE_CONFIG), load(CODEX_CONFIG)):
            timeouts = {
                (event, hook["command"]): hook["timeout"]
                for event, entries in config["hooks"].items()
                for entry in entries
                for hook in entry["hooks"]
                if "timeout" in hook
            }
            self.assertEqual([event for event, _ in timeouts], ["Stop"])
            self.assertEqual(set(timeouts.values()), {STOP_TIMEOUT})

    def test_configured_edit_guards_deny_generated_output(self):
        """configured edit guards deny generated output"""
        payload = self.edit(self.fixture / "generated" / "device.pb.go")
        claude = configured(load(CLAUDE_CONFIG), "PreToolUse", EDIT_CLAUDE, "pre-tool-policy")
        result = run_command([BASH, "-c", claude], payload, cwd=self.parent, env=self.env_with(CLAUDE_PROJECT_DIR=str(REPO)))
        self.assertEqual(result.code, 0, result.stderr)
        self.assertEqual(decision_of(result), "deny")
        codex = configured(load(CODEX_CONFIG), "PreToolUse", EDIT_CODEX, "pre-tool-policy")
        result = run_command([BASH, "-c", codex], payload, cwd=REPO, env=self.env)
        self.assertEqual(result.code, 0, result.stderr)
        self.assertEqual(decision_of(result), "deny")

    def test_a_codex_hook_command_names_the_root_or_script_it_could_not_find(self):
        """a Codex hook command names the root or script it could not find"""
        # The three ways the command cannot reach its script, each reported on
        # standard error with exit 1: a session directory outside any repository,
        # a PATH without git, and a checkout that lacks the script.
        command = configured(load(CODEX_CONFIG), "PreToolUse", EDIT_CODEX, "pre-tool-policy")
        payload = self.edit(self.fixture / "generated" / "device.pb.go")
        outside = self.parent / "outside any repository"
        outside.mkdir()
        ceiling = self.env_with(GIT_CEILING_DIRECTORIES=str(self.parent))
        result = run_command([BASH, "-c", command], payload, cwd=outside, env=ceiling)
        self.assertEqual(result.code, 1)
        self.assertTrue(
            result.stderr.startswith(
                f"FlowSeer hook pre-tool-policy.sh cannot run from {outside}: repository root unresolved, git "
            ),
            result.stderr,
        )
        result = run_command([BASH, "-c", command], payload, cwd=REPO, env=self.env_with(PATH=str(outside)))
        self.assertEqual(result.code, 1)
        self.assertEqual(
            result.stderr,
            f"FlowSeer hook pre-tool-policy.sh cannot run from {REPO}: repository root unresolved, git not on PATH\n",
        )
        result = run_command([BASH, "-c", command], payload, cwd=self.fixture, env=self.env)
        self.assertEqual(result.code, 1)
        self.assertTrue(
            result.stderr.startswith(
                f"FlowSeer hook pre-tool-policy.sh cannot run from {self.fixture}: repository root {self.fixture}, git "
            ),
            result.stderr,
        )

    def test_configured_bash_guard_denies_generated_file_mutation(self):
        """configured Bash guard denies generated-file mutation"""
        payload = self.shell("sed -i '' -e s/old/new/ generated/device.pb.go")
        claude = configured(load(CLAUDE_CONFIG), "PreToolUse", "Bash", "protect-generated-bash")
        result = run_command([BASH, "-c", claude], payload, cwd=self.parent, env=self.env_with(CLAUDE_PROJECT_DIR=str(REPO)))
        self.assertEqual(result.code, 0, result.stderr)
        self.assertEqual(decision_of(result), "deny")
        codex = configured(load(CODEX_CONFIG), "PreToolUse", "Bash", "protect-generated-bash")
        result = run_command([BASH, "-c", codex], payload, cwd=REPO, env=self.env)
        self.assertEqual(result.code, 0, result.stderr)
        self.assertEqual(decision_of(result), "deny")

    def test_configured_worktree_create_handles_project_and_repository_paths_with_spaces(self):
        """configured WorktreeCreate handles project and repository paths with spaces"""
        project = self.parent / "project root with spaces"
        project.symlink_to(REPO)
        root = self.parent / "external-worktrees"
        command = commands(load(CLAUDE_CONFIG), "WorktreeCreate", None)[0]
        payload = {"cwd": str(self.fixture), "hook_event_name": "WorktreeCreate", "name": "external-checkout"}
        env = self.env_with(FLOWSEER_WORKTREE_ROOT=str(root), CLAUDE_PROJECT_DIR=str(project))
        result = run_command([BASH, "-c", command], payload, cwd=self.parent, env=env)
        created = root / "external-checkout"
        self.assertEqual((result.code, result.stdout), (0, f"{created}\n"), result.stderr)
        self.assertEqual(self.git(created, "rev-parse", "--is-inside-work-tree"), "true")
        self.assertEqual(self.git(created, "symbolic-ref", "--short", "HEAD"), "worktree-external-checkout")


if __name__ == "__main__":
    unittest.main()
