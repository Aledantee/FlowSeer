import subprocess
import tempfile
import unittest
from pathlib import Path


MERGE_CHECK = Path(__file__).with_name("merge-check.py")


class ThrowawayRepository:
    def __init__(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.run("init", "-b", "main")
        self.run("config", "user.name", "Test User")
        self.run("config", "user.email", "test@example.com")

    def run(self, *args):
        return subprocess.run(
            ["git", *args],
            cwd=self.root,
            check=True,
            capture_output=True,
            text=True,
        )

    def write(self, name, content):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)

    def commit(self, message):
        self.run("add", ".")
        self.run("commit", "-m", message)
        return self.run("rev-parse", "HEAD").stdout.strip()

    def close(self):
        self.temp.cleanup()

    def base(self, content="a\nb\n"):
        self.write("file.txt", content)
        return self.commit("base")

    def merge(self, first_content, second_content, merged_content, second_extra=None):
        self.run("checkout", "-b", "first")
        self.write("file.txt", first_content)
        self.commit("first change")

        self.run("checkout", "main")
        self.run("checkout", "-b", "second")
        self.write("file.txt", second_content)
        if second_extra:
            self.write("other.txt", second_extra)
        self.commit("second change")

        self.run("checkout", "first")
        self.run("merge", "--no-ff", "-s", "ours", "second", "-m", "merge")
        self.write("file.txt", merged_content)
        if second_extra:
            self.run("checkout", "second", "--", "other.txt")
        self.run("add", "file.txt")
        if second_extra:
            self.run("add", "other.txt")
        self.run("commit", "--amend", "--no-edit")
        merge = self.run("rev-parse", "HEAD").stdout.strip()
        return merge, f"{merge}^..{merge}"

    def check(self, revision_range):
        return subprocess.run(
            ["python3", str(MERGE_CHECK), revision_range],
            cwd=self.root,
            capture_output=True,
            text=True,
        )


class MergeCheckTest(unittest.TestCase):
    def repository(self):
        repo = ThrowawayRepository()
        self.addCleanup(repo.close)
        return repo

    def test_lost_side_fails_when_other_side_changed(self):
        repo = self.repository()
        repo.base()
        merge, revision_range = repo.merge(
            "a\nb\nx01\n", "a\nb\ny01\n", "a\nb\ny01\n"
        )

        result = repo.check(revision_range)

        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("lost first-parent change", result.stdout)
        self.assertIn("file.txt", result.stdout)
        self.assertIn(merge[:12], result.stdout)

    def test_lost_side_fails_when_other_side_is_unchanged(self):
        repo = self.repository()
        repo.base()
        merge, revision_range = repo.merge(
            "a\nb\nx01\n", "a\nb\n", "a\nb\n", second_extra="keep merge commit\n"
        )

        result = repo.check(revision_range)

        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("lost first-parent change", result.stdout)
        self.assertIn("file.txt", result.stdout)
        self.assertIn(merge[:12], result.stdout)

    def test_missing_added_lines_are_reported_without_failure(self):
        repo = self.repository()
        repo.base()
        _, revision_range = repo.merge(
            "a\nb\nx01\nx02\n", "a\nb\ny01\n", "a\nb\nx01\ny01\n"
        )

        result = repo.check(revision_range)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("missing first-parent change", result.stdout)
        self.assertIn("+ x02", result.stdout)

    def test_change_already_present_on_other_parent_passes(self):
        repo = self.repository()
        repo.base()
        _, revision_range = repo.merge(
            "a\nb\nx01\n", "a\nb\nx01\ny01\n", "a\nb\nx01\ny01\n"
        )

        result = repo.check(revision_range)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("lost", result.stdout)
        self.assertNotIn("missing", result.stdout)

    def test_renamed_paths_are_not_compared_and_no_merge_range_is_clean(self):
        repo = self.repository()
        base = repo.base()
        repo.run("checkout", "-b", "first")
        repo.write("other.txt", "first change\n")
        first = repo.commit("first change")
        repo.run("checkout", "main")
        repo.run("checkout", "-b", "second")
        repo.run("mv", "file.txt", "renamed.txt")
        repo.commit("rename")
        repo.run("checkout", "first")
        repo.run("merge", "--no-ff", "-s", "ours", "second", "-m", "merge")
        merge = repo.run("rev-parse", "HEAD").stdout.strip()

        renamed = repo.check(f"{merge}^..{merge}")
        clean = repo.check(f"{base}..{first}")

        self.assertEqual(renamed.returncode, 0, renamed.stdout + renamed.stderr)
        self.assertIn("not compared", renamed.stdout)
        self.assertIn("file.txt", renamed.stdout)
        self.assertIn("renamed.txt", renamed.stdout)
        self.assertEqual(clean.returncode, 0, clean.stdout + clean.stderr)
        self.assertEqual(clean.stdout.strip(), "no merges")

    def test_trivial_lines_do_not_count_as_a_lost_change(self):
        repo = self.repository()
        repo.base()
        _, revision_range = repo.merge(
            "a\nb\n\n{\n}\n[]\n//\n",
            "a\nb\ny01\n",
            "a\nb\ny01\n",
        )

        result = repo.check(revision_range)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("lost", result.stdout)


if __name__ == "__main__":
    unittest.main()
