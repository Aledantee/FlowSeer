import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPTS = Path(__file__).resolve().parents[2]
REPOSITORY = SCRIPTS.parents[1]
SHELL_LISTING = REPOSITORY / "tools" / "hooks" / "tree-state.sh"
IDENTITY = {
    "GIT_AUTHOR_NAME": "t",
    "GIT_AUTHOR_EMAIL": "t@example.com",
    "GIT_COMMITTER_NAME": "t",
    "GIT_COMMITTER_EMAIL": "t@example.com",
}


def git(root: Path, *args: str) -> None:
    subprocess.run(
        ["git", *args], cwd=root, check=True, capture_output=True, env={**os.environ, **IDENTITY}
    )


def write(root: Path, name: str, text: str) -> None:
    path = root / name
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


def command(root: Path) -> bytes:
    done = subprocess.run(
        [sys.executable, str(SCRIPTS / "run.py"), "hooks", "tree-state", str(root)],
        capture_output=True,
        check=True,
    )
    return done.stdout


def shell(root: Path) -> bytes:
    return subprocess.run(
        [str(SHELL_LISTING), str(root)], capture_output=True, check=True, stdin=subprocess.DEVNULL
    ).stdout


class TreeStateTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve() / "a repository"
        self.root.mkdir()
        git(self.root, "init", "-q", "-b", "main")
        write(self.root, "kept.go", "package kept\n")
        write(self.root, "gone.go", "package gone\n")
        write(self.root, "notes.txt", "notes\n")
        write(self.root, "sub/go.mod", "module sub\n")
        git(self.root, "add", ".")
        git(self.root, "commit", "-q", "-m", "base")

    def edit_tree(self):
        write(self.root, "kept.go", "package kept // changed\n")
        (self.root / "gone.go").unlink()
        write(self.root, "fresh.go", "package fresh\n")
        write(self.root, "with space.md", "# spaced\n")
        write(self.root, "notes.txt", "changed\n")
        write(self.root, "data.bin", "bytes\n")
        write(self.root, "sub/go.mod", "module sub // changed\n")

    def test_listing_equals_the_shell_listing_byte_for_byte(self):
        self.edit_tree()
        listing = command(self.root)
        self.assertEqual(listing, shell(self.root))
        lines = listing.decode().splitlines()
        self.assertEqual(lines, sorted(lines, key=str.encode))
        self.assertIn("deleted\tgone.go", lines)
        self.assertEqual(
            {line.split("\t")[1] for line in lines},
            {"gone.go", "fresh.go", "kept.go", "sub/go.mod", "with space.md"},
        )

    def test_a_name_that_only_ends_in_a_listed_name_is_not_listed(self):
        write(self.root, "mygo.mod", "x\n")
        write(self.root, "sub/buf.lock", "x\n")
        write(self.root, "sub/xbuf.lock", "x\n")
        listing = command(self.root)
        self.assertEqual(listing, shell(self.root))
        self.assertEqual([line.split("\t")[1] for line in listing.decode().splitlines()], ["sub/buf.lock"])

    def test_a_carriage_return_inside_a_path_survives(self):
        write(self.root, "a\rb.go", "package a\n")
        listing = command(self.root)
        self.assertEqual(listing, shell(self.root))
        self.assertTrue(listing.endswith(b"\ta\rb.go\n"), listing)

    def test_a_clean_tree_prints_nothing(self):
        self.assertEqual(command(self.root), b"")
        self.assertEqual(shell(self.root), b"")

    def test_a_merge_lists_only_what_differs_from_the_other_side(self):
        git(self.root, "switch", "-q", "-c", "feature")
        write(self.root, "incoming.go", "package incoming\n")
        write(self.root, "touched.go", "package touched\n")
        git(self.root, "add", ".")
        git(self.root, "commit", "-q", "-m", "feature")
        git(self.root, "switch", "-q", "main")
        write(self.root, "local.go", "package local\n")
        git(self.root, "add", ".")
        git(self.root, "commit", "-q", "-m", "main")
        git(self.root, "merge", "-q", "--no-ff", "--no-commit", "feature")
        write(self.root, "touched.go", "package touched // edited after the merge\n")

        listing = command(self.root)

        self.assertEqual(listing, shell(self.root))
        self.assertEqual([line.split("\t")[1] for line in listing.decode().splitlines()], ["touched.go"])

    def test_a_directory_that_is_not_a_repository_prints_nothing(self):
        with tempfile.TemporaryDirectory() as bare:
            self.assertEqual(command(Path(bare)), b"")
            self.assertEqual(shell(Path(bare)), b"")

    def test_a_missing_root_exits_2(self):
        done = subprocess.run(
            [sys.executable, str(SCRIPTS / "run.py"), "hooks", "tree-state"], capture_output=True
        )
        self.assertEqual(done.returncode, 2)
        self.assertEqual(done.stdout, b"")

    def test_a_root_that_is_not_a_directory_exits_2(self):
        done = subprocess.run(
            [sys.executable, str(SCRIPTS / "run.py"), "hooks", "tree-state", str(self.root / "nope")],
            capture_output=True,
        )
        self.assertEqual(done.returncode, 2)


if __name__ == "__main__":
    unittest.main()
