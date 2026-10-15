import shutil
import subprocess

from tests.hooks.support import RUN_PY, HookCase


class CreateTest(HookCase):
    def create(self, *args, cwd=None):
        return subprocess.run(
            [shutil.which("uv"), "run", str(RUN_PY), "worktree", "create", *args],
            cwd=cwd or self.fixture,
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
            env=self.env,
            timeout=30,
        )

    def test_cli_creates_from_current_checkout_without_an_event(self):
        self.git(self.linked, "commit", "-q", "--allow-empty", "-m", "linked work")
        result = self.create("cli-task", cwd=self.linked)
        target = self.parent / "worktrees" / self.fixture.name / "cli-task"
        self.assertEqual((result.returncode, result.stdout), (0, f"{target}\n"), result.stderr)
        self.assertEqual(self.git(target, "rev-parse", "HEAD"), self.git(self.linked, "rev-parse", "HEAD"))
        self.assertEqual(self.git(target, "branch", "--show-current"), "worktree-cli-task")

    def test_cli_accepts_explicit_checkout_from_outside_repository(self):
        result = self.create("explicit-task", "--cwd", str(self.fixture), cwd=self.parent)
        target = self.parent / "worktrees" / self.fixture.name / "explicit-task"
        self.assertEqual((result.returncode, result.stdout), (0, f"{target}\n"), result.stderr)
        self.assertEqual(self.git(target, "rev-parse", "HEAD"), self.git(self.fixture, "rev-parse", "HEAD"))

    def test_cli_rejects_unsafe_name_without_creating_a_worktree(self):
        result = self.create("../escape")
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertIn("unsafe worktree name", result.stderr)
        self.assertFalse((self.parent / "worktrees").exists())
