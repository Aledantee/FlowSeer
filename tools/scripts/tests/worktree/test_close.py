import shutil
import subprocess

from tests.hooks.support import RUN_PY, HookCase


class CloseTest(HookCase):
    def close(self, target, *args, cwd=None):
        return subprocess.run(
            [shutil.which("uv"), "run", str(RUN_PY), "worktree", "close", str(target), *args],
            cwd=cwd or self.fixture,
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
            env=self.env,
            timeout=30,
        )

    def unique_commit(self):
        self.git(self.linked, "commit", "-q", "--allow-empty", "-m", "child work")
        return self.git(self.linked, "rev-parse", "HEAD")

    def test_close_accepts_history_preserved_on_another_local_branch(self):
        head = self.unique_commit()
        self.git(self.fixture, "branch", "parked/task", head)
        result = self.close(self.linked)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.linked.exists())
        self.assertEqual(self.git(self.fixture, "branch", "--list", "hook-test"), "")
        self.assertEqual(self.git(self.fixture, "rev-parse", "parked/task"), head)

    def test_close_refuses_unmerged_history(self):
        head = self.unique_commit()
        result = self.close(self.linked)
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("not contained in another local branch", result.stderr)
        self.assertTrue(self.linked.exists())
        self.assertEqual(self.git(self.fixture, "rev-parse", "hook-test"), head)

    def test_force_closes_clean_unmerged_worktree(self):
        self.unique_commit()
        result = self.close(self.linked, "--force")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.linked.exists())
        self.assertEqual(self.git(self.fixture, "branch", "--list", "hook-test"), "")

    def test_force_still_refuses_tracked_and_untracked_changes(self):
        for filename in ("README.md", "untracked.txt"):
            with self.subTest(filename=filename):
                self.fresh()
                path = self.linked / filename
                path.write_text("keep this work\n")
                result = self.close(self.linked, "--force")
                self.assertEqual(result.returncode, 1, result.stderr)
                self.assertIn("dirty", result.stderr)
                self.assertEqual(path.read_text(), "keep this work\n")

    def test_force_still_refuses_a_locked_worktree(self):
        self.git(self.fixture, "worktree", "lock", str(self.linked))
        result = self.close(self.linked, "--force")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("locked", result.stderr)
        self.assertTrue(self.linked.exists())

    def test_force_still_refuses_primary_checkout(self):
        result = self.close(self.fixture, "--force", cwd=self.linked)
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("primary", result.stderr)
        self.assertTrue(self.fixture.exists())

    def test_force_still_refuses_current_checkout_with_explicit_source(self):
        result = self.close(self.linked, "--cwd", str(self.fixture), "--force", cwd=self.linked)
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("current", result.stderr)
        self.assertTrue(self.linked.exists())

    def test_force_still_refuses_an_unregistered_directory(self):
        path = self.parent / "not a worktree"
        path.mkdir()
        result = self.close(path, "--force")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("registered", result.stderr)
        self.assertTrue(path.exists())

    def test_detached_worktree_can_close_when_another_branch_contains_head(self):
        self.git(self.linked, "checkout", "--detach")
        result = self.close(self.linked)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.linked.exists())
        self.assertEqual(self.git(self.fixture, "branch", "--list", "hook-test"), "  hook-test".strip())

    def test_force_still_refuses_nested_registered_worktrees(self):
        child = self.linked / "child"
        self.git(self.fixture, "worktree", "add", "-qb", "nested", str(child))
        result = self.close(self.linked, "--force")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("nested worktree", result.stderr)
        self.assertTrue(child.exists())
