import subprocess
import tempfile
import unittest
from pathlib import Path

VERIFIER = Path(__file__).with_name("verify-change.sh")


class SymlinkedPathTest(unittest.TestCase):
    """A path spelled through a symlinked directory selects what git's spelling selects."""

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.git("init", "-q", "-b", "main")
        (self.root / "real/pkg").mkdir(parents=True)
        (self.root / "real/pkg/go.mod").write_text("module example.invalid/pkg\n\ngo 1.27\n")
        (self.root / "real/pkg/a.go").write_text("package pkg\n")
        (self.root / "real/empty").mkdir()
        (self.root / "alias").symlink_to("real")
        self.git("add", "-A")
        self.git("commit", "-qm", "init")

    def git(self, *args):
        subprocess.run(
            ["git", "-c", "user.name=Verify", "-c", "user.email=verify@example.invalid", *args],
            cwd=self.root,
            check=True,
            capture_output=True,
            text=True,
        )

    def select(self, *paths):
        return subprocess.run(
            [str(VERIFIER), "--print-selection", "--", *paths],
            cwd=self.root,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_fixture_tracks_the_target_spelling_only(self):
        listed = subprocess.run(
            ["git", "ls-files"], cwd=self.root, check=True, capture_output=True, text=True
        ).stdout.split()
        self.assertEqual(listed, ["alias", "real/pkg/a.go", "real/pkg/go.mod"])

    def test_directory_through_a_symlink_expands_to_the_tracked_files(self):
        result = self.select("alias/pkg")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("module=real/pkg mode=full", result.stdout.splitlines())

    def test_file_through_a_symlink_is_rewritten_to_the_tracked_spelling(self):
        result = self.select("alias/pkg/a.go")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("module=real/pkg mode=full", result.stdout.splitlines())

    def test_missing_file_through_a_symlink_keeps_its_tail(self):
        result = self.select("alias/pkg/gone/go.mod")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("module=real/pkg mode=full", result.stdout.splitlines())

    def test_empty_argument_is_refused(self):
        result = self.select("")
        self.assertEqual(result.returncode, 2)
        self.assertIn("empty path argument", result.stderr)

    def test_directory_without_files_says_so(self):
        result = self.select("real/empty")
        self.assertEqual(result.returncode, 2)
        self.assertIn("found no files under the named paths", result.stderr)


if __name__ == "__main__":
    unittest.main()
