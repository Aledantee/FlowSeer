import subprocess
import tempfile
import unittest
from pathlib import Path

from lib import repo


class RepoTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve()
        subprocess.run(["git", "init", "-q"], cwd=self.root, check=True)
        (self.root / "sub").mkdir()
        (self.root / "sub" / "a.txt").write_text("a")
        (self.root / "b.txt").write_text("b")
        (self.root / "untracked.txt").write_text("u")
        subprocess.run(["git", "add", "sub/a.txt", "b.txt"], cwd=self.root, check=True)

    def test_root_from_a_subdirectory(self):
        self.assertEqual(repo.root(self.root / "sub").resolve(), self.root)

    def test_root_outside_a_repository_raises(self):
        with tempfile.TemporaryDirectory() as bare:
            with self.assertRaises(RuntimeError):
                repo.root(Path(bare))

    def test_tracked_files_under_a_path_skip_untracked(self):
        self.assertEqual(repo.tracked_files("sub", cwd=self.root), [Path("sub/a.txt")])
        self.assertEqual(
            sorted(repo.tracked_files(".", cwd=self.root)), [Path("b.txt"), Path("sub/a.txt")]
        )


if __name__ == "__main__":
    unittest.main()
