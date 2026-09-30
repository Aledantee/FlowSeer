import os
import subprocess
import tempfile
import unittest
from itertools import product
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

    def write_bytes(self, name, content):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)

    def commit(self, message):
        self.run("add", ".")
        self.run("commit", "-m", message)
        return self.run("rev-parse", "HEAD").stdout.strip()

    def close(self):
        self.temp.cleanup()

    def base(self, content="a\nb\n"):
        self.write("file.txt", content)
        return self.commit("base")

    def merge(
        self,
        first_content,
        second_content,
        merged_content,
        first_extra=None,
        second_extra=None,
    ):
        self.run("checkout", "-b", "first")
        self.write("file.txt", first_content)
        if first_extra:
            self.write("first-extra.txt", first_extra)
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

    def branch_commit(self, branch, base, content, message, extra=None):
        self.run("checkout", "-B", branch, base)
        self.write("file.txt", content)
        if extra:
            self.write("extra.txt", extra)
        return self.commit(message)

    def plain_merge(self, base, first_content, second_content, index):
        first = self.branch_commit(
            f"first-{index}", base, first_content, f"first {index}"
        )
        second = self.branch_commit(
            f"second-{index}", base, second_content, f"second {index}"
        )
        self.run("checkout", f"first-{index}")
        result = subprocess.run(
            ["git", "merge", "--no-ff", "--no-edit", f"second-{index}"],
            cwd=self.root,
            capture_output=True,
            text=True,
        )
        if result.returncode:
            self.run("merge", "--abort")
            return None
        merge = self.run("rev-parse", "HEAD").stdout.strip()
        return first, second, merge, f"{merge}^..{merge}"

    def reset_merge(self, base, first_content, second_content, index):
        self.branch_commit(
            f"first-reset-{index}",
            base,
            first_content,
            f"first reset {index}",
        )
        self.branch_commit(
            f"second-reset-{index}",
            base,
            second_content,
            f"second reset {index}",
            extra="second parent commit\n",
        )
        self.run("checkout", f"second-reset-{index}")
        self.run(
            "merge",
            "--no-ff",
            "-s",
            "ours",
            f"first-reset-{index}",
            "-m",
            "reset merge",
        )
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

    def test_lost_unique_side_lines_fail_when_other_parent_shares_some_lines(self):
        repo = self.repository()
        repo.base()
        merge, revision_range = repo.merge(
            "a\nb\nshared\nx01\n",
            "a\nb\nshared\ny01\n",
            "a\nb\nshared\ny01\n",
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

    def test_reordering_lines_does_not_report_lost_change(self):
        repo = self.repository()
        repo.base("alpha\nbeta\n")
        _, revision_range = repo.merge(
            "alpha\nbeta\n",
            "beta\nalpha\n",
            "beta\nalpha\n",
            first_extra="first parent commit\n",
        )

        result = repo.check(revision_range)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("lost", result.stdout)

    def test_removing_one_duplicate_line_does_not_report_lost_change(self):
        repo = self.repository()
        repo.base("if err := x(); err != nil {\nreturn err\n}\n" * 2)
        _, revision_range = repo.merge(
            "if err := x(); err != nil {\nreturn err\n}\n",
            "if err := x(); err != nil {\nreturn err\n}\n",
            "if err := x(); err != nil {\nreturn err\n}\n",
            first_extra="first parent commit\n",
            second_extra="second parent commit\n",
        )

        result = repo.check(revision_range)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("lost", result.stdout)

    def test_combined_resolution_keeps_both_line_changes(self):
        repo = self.repository()
        repo.base("value = original\n")
        _, revision_range = repo.merge(
            "value = from main\n",
            "value = from side\n",
            "value = from main and side\n",
        )

        result = repo.check(revision_range)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("lost", result.stdout)

    def test_binary_and_mode_only_changes_are_reported(self):
        repo = self.repository()
        base = repo.base("stable\n")
        repo.run("checkout", "-B", "binary-first", base)
        repo.write_bytes("binary.bin", b"\x00\xff\n")
        repo.commit("binary change")
        repo.run("checkout", "-B", "binary-second", base)
        repo.write("other.txt", "second parent\n")
        repo.commit("other change")
        repo.run("checkout", "binary-first")
        repo.run("merge", "--no-ff", "-s", "ours", "binary-second", "-m", "binary merge")
        repo.run("checkout", "binary-second", "--", "other.txt")
        repo.run("add", ".")
        repo.run("commit", "--amend", "--no-edit")
        binary_merge = repo.run("rev-parse", "HEAD").stdout.strip()

        binary_result = repo.check(f"{binary_merge}^..{binary_merge}")

        self.assertEqual(
            binary_result.returncode,
            0,
            binary_result.stdout + binary_result.stderr,
        )
        self.assertIn("not compared", binary_result.stdout)
        self.assertIn("binary.bin (binary)", binary_result.stdout)

        repo.run("checkout", "-B", "mode-first", base)
        os.chmod(repo.root / "file.txt", 0o755)
        repo.commit("mode change")
        repo.run("checkout", "-B", "mode-second", base)
        repo.write("other.txt", "second parent\n")
        repo.commit("other mode change")
        repo.run("checkout", "mode-first")
        repo.run("merge", "--no-ff", "-s", "ours", "mode-second", "-m", "mode merge")
        repo.run("checkout", "mode-second", "--", "other.txt")
        repo.run("add", ".")
        repo.run("commit", "--amend", "--no-edit")
        mode_merge = repo.run("rev-parse", "HEAD").stdout.strip()

        mode_result = repo.check(f"{mode_merge}^..{mode_merge}")

        self.assertEqual(mode_result.returncode, 0, mode_result.stdout + mode_result.stderr)
        self.assertIn("file.txt (mode-only)", mode_result.stdout)

    def test_comment_like_lines_are_parsed_inside_hunks(self):
        repo = self.repository()
        repo.base("-- x\nstable\n")
        _, revision_range = repo.merge(
            "++ x\nstable\n",
            "stable\ny01\n",
            "stable\ny01\n",
        )

        result = repo.check(revision_range)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("missing first-parent change", result.stdout)
        self.assertIn("+ ++ x", result.stdout)

    def test_generated_clean_merges_preserve_line_count_changes(self):
        repo = self.repository()
        bases = tuple(product(("alpha", "beta"), repeat=2))[:2]
        for base_index, base_lines in enumerate(bases):
            base_content = "".join(f"{line}\n" for line in base_lines)
            if base_index:
                repo.run("checkout", "main")
            repo.write("file.txt", base_content)
            base = repo.commit("generated base")
            reverse = tuple(reversed(base_lines))
            replacement = ("gamma",) + base_lines[1:]
            duplicate = base_lines + (base_lines[0],)
            append_edits = [
                base_lines + (line,) for line in ("gamma", "delta")
            ]
            pairs = list(product(append_edits, repeat=2)) + [
                (reverse, reverse),
                (replacement, replacement),
                (duplicate, duplicate),
            ]
            for index, (first_lines, second_lines) in enumerate(pairs):
                if first_lines == base_lines or second_lines == base_lines:
                    continue
                first_content = "".join(f"{line}\n" for line in first_lines)
                second_content = "".join(f"{line}\n" for line in second_lines)
                merged = repo.plain_merge(base, first_content, second_content, index)
                if merged is None:
                    continue
                _, _, _, revision_range = merged
                result = repo.check(revision_range)
                self.assertEqual(
                    result.returncode,
                    0,
                    f"base={base_lines}, first={first_lines}, "
                    f"second={second_lines}: {result.stdout}{result.stderr}",
                )

    def test_generated_unique_count_changes_fail_when_reset_to_other_parent(self):
        repo = self.repository()
        bases = tuple(product(("alpha", "beta"), repeat=2))[:2]
        for base_index, base_lines in enumerate(bases):
            base_content = "".join(f"{line}\n" for line in base_lines)
            if base_index:
                repo.run("checkout", "main")
            repo.write("file.txt", base_content)
            base = repo.commit("reset base")
            edits = [
                base_lines + ("gamma",),
                base_lines[1:],
                ("gamma",) + base_lines[1:],
                base_lines + (base_lines[0],),
            ]
            for index, first_lines in enumerate(edits):
                first_content = "".join(f"{line}\n" for line in first_lines)
                merge, revision_range = repo.reset_merge(
                    base, first_content, base_content, index
                )
                result = repo.check(revision_range)
                self.assertEqual(
                    result.returncode,
                    1,
                    f"merge={merge}, base={base_lines}, first={first_lines}: "
                    f"{result.stdout}{result.stderr}",
                )

    def test_octopus_merges_fail_loudly(self):
        repo = self.repository()
        base = repo.base()
        branches = []
        for index in range(3):
            branch = f"octopus-{index}"
            repo.run("checkout", "-B", branch, base)
            repo.write(f"file-{index}.txt", f"change {index}\n")
            repo.commit(f"octopus change {index}")
            branches.append(branch)
        repo.run("checkout", branches[0])
        repo.run(
            "merge",
            "--no-ff",
            branches[1],
            branches[2],
            "-m",
            "octopus merge",
        )
        merge = repo.run("rev-parse", "HEAD").stdout.strip()

        result = repo.check(f"{merge}^..{merge}")

        self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
        self.assertIn("more than two parents", result.stderr)

    def test_multiple_merge_bases_are_not_compared(self):
        repo = self.repository()
        base = repo.base()
        repo.run("checkout", "-B", "criss-a", base)
        repo.write("a.txt", "a\n")
        a = repo.commit("a")
        repo.run("checkout", "-B", "criss-b", base)
        repo.write("b.txt", "b\n")
        b = repo.commit("b")
        repo.run("checkout", "criss-b")
        repo.run("merge", "--no-ff", "-s", "ours", a, "-m", "merge b")
        merged_b = repo.run("rev-parse", "HEAD").stdout.strip()
        repo.run("checkout", "criss-a")
        repo.run("merge", "--no-ff", "-s", "ours", b, "-m", "merge a")
        merged_a = repo.run("rev-parse", "HEAD").stdout.strip()
        repo.run("merge", "--no-ff", "--no-edit", merged_b)
        merge = repo.run("rev-parse", "HEAD").stdout.strip()

        result = repo.check(f"{merge}^..{merge}")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("not compared", result.stdout)
        self.assertIn("multiple merge bases", result.stdout)
        self.assertNotEqual(merge, merged_a)

if __name__ == "__main__":
    unittest.main()
