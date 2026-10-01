import contextlib
import importlib.util
import io
import os
import subprocess
import tempfile
import unittest
from itertools import product
from pathlib import Path
from unittest import mock


MERGE_CHECK = Path(__file__).with_name("merge-check.py")
CHECKER_SPEC = importlib.util.spec_from_file_location("merge_checker", MERGE_CHECK)
MERGE_CHECKER = importlib.util.module_from_spec(CHECKER_SPEC)
CHECKER_SPEC.loader.exec_module(MERGE_CHECKER)


def expected_lost_sides(
    outcome,
    base,
    first,
    second,
    first_operator=None,
    second_operator=None,
):
    """Derive loss from merge construction and the reset decision.

    It uses complete file values and operator semantics, never line counts.
    """
    if outcome == "auto-merge":
        return set()
    if outcome == "merge-equals-base":
        return {
            side
            for side, lines in (("first", first), ("second", second))
            if lines != base
        }
    if outcome == "combined-resolution":
        return {"first", "second"}
    if outcome == "partial-keep":
        return set()
    kept = {"reset-first": ("first", first), "reset-second": ("second", second)}
    kept_side, kept_lines = kept[outcome]
    dropped_side = "second" if kept_side == "first" else "first"
    dropped_lines = second if dropped_side == "second" else first
    kept_operator = first_operator if kept_side == "first" else second_operator
    dropped_operator = (
        second_operator if dropped_side == "second" else first_operator
    )
    if kept_lines == base and dropped_lines != base:
        return {dropped_side}
    if dropped_operator in ("none", "swap"):
        return set()
    if dropped_lines == base:
        return set()
    if change_signature(base, dropped_operator, dropped_side) <= change_signature(
        base, kept_operator, kept_side
    ):
        return set()
    return {dropped_side}


def change_signature(base, operator, side):
    if operator == "none":
        return set()
    if operator == "append":
        return {("add", f"{side}-append")}
    if operator == "delete":
        return {("remove", base[1])}
    if operator == "replace":
        return {("remove", base[1]), ("add", f"{side}-replace")}
    if operator == "shared-replace":
        return {("remove", base[1]), ("add", "shared-replace")}
    if operator == "duplicate":
        return {("add", base[0])}
    if operator == "swap":
        return {("reorder",)}
    raise AssertionError(f"unknown edit operator: {operator}")


def apply_edit(base, operator, side):
    if operator == "none":
        return base
    if operator == "append":
        return base + (f"{side}-append",)
    if operator == "delete":
        return base[:1] + base[2:]
    if operator == "replace":
        return base[:1] + (f"{side}-replace",) + base[2:]
    if operator == "shared-replace":
        return base[:1] + ("shared-replace",) + base[2:]
    if operator == "duplicate":
        return base + (base[0],)
    if operator == "swap":
        return (base[1], base[0], base[2])
    raise AssertionError(f"unknown edit operator: {operator}")


class ThrowawayRepository:
    def __init__(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.run("init", "-b", "main")
        self.run("config", "user.name", "Test User")
        self.run("config", "user.email", "test@example.com")

    def run(self, *args, input=None):
        return subprocess.run(
            ["git", *args],
            cwd=self.root,
            check=True,
            capture_output=True,
            text=True,
            input=input,
        )

    def write(self, name, content):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)

    def write_bytes(self, name, content):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)

    def commit(self, message, allow_empty=False):
        self.run("add", ".")
        args = ["commit"]
        if allow_empty:
            args.append("--allow-empty")
        args.extend(("-m", message))
        self.run(*args)
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
        return self.commit(message, allow_empty=True)

    def commit_tree(self, content, message, parents=()):
        blob = self.run("hash-object", "-w", "--stdin", input=content).stdout.strip()
        tree = self.run(
            "mktree", input=f"100644 blob {blob}\tfile.txt\n"
        ).stdout.strip()
        args = ["commit-tree", tree]
        for parent in parents:
            args.extend(("-p", parent))
        return self.run(*args, input=f"{message}\n").stdout.strip()

    def resolved_merge(self, first, second, index, kept=None):
        self.run("checkout", "--detach", first)
        result = subprocess.run(
            ["git", "merge", "--no-ff", "--no-edit", second],
            cwd=self.root,
            capture_output=True,
            text=True,
        )
        if result.returncode == 0:
            if kept is not None:
                self.run("checkout", kept, "--", "file.txt")
                self.run("add", "file.txt")
                self.run("commit", "--amend", "--no-edit")
            return self.run("rev-parse", "HEAD").stdout.strip(), False
        if kept is None:
            self.run("merge", "--abort")
            return None, True
        option = "--ours" if kept == first else "--theirs"
        self.run("checkout", option, "--", "file.txt")
        self.run("add", "file.txt")
        self.run("commit", "-m", f"resolve property {index}")
        return self.run("rev-parse", "HEAD").stdout.strip(), True

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

    def reset_parent_merge(self, first, second, index, kept):
        self.run("checkout", "--detach", first)
        self.run(
            "merge",
            "--no-ff",
            "-s",
            "ours",
            second,
            "-m",
            f"reset property {index}",
        )
        if kept == second:
            self.run("checkout", second, "--", "file.txt")
            self.run("add", "file.txt")
            self.run("commit", "--amend", "--no-edit")
        return self.run("rev-parse", "HEAD").stdout.strip()

    def check(self, revision_range):
        return subprocess.run(
            ["python3", str(MERGE_CHECK), revision_range],
            cwd=self.root,
            capture_output=True,
            text=True,
        )

    def check_fast(self, revision_range):
        stdout = io.StringIO()
        stderr = io.StringIO()
        previous_directory = os.getcwd()
        os.chdir(self.root)
        try:
            with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                returncode = MERGE_CHECKER.main([revision_range])
        finally:
            os.chdir(previous_directory)
        return subprocess.CompletedProcess(
            [], returncode, stdout.getvalue(), stderr.getvalue()
        )

    def check_merge_fast(self, merge):
        stdout = io.StringIO()
        stderr = io.StringIO()
        previous_directory = os.getcwd()
        os.chdir(self.root)
        try:
            with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                lost = MERGE_CHECKER.check_merge(merge)
        finally:
            os.chdir(previous_directory)
        return subprocess.CompletedProcess(
            [], 1 if lost else 0, stdout.getvalue(), stderr.getvalue()
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

    def test_reset_to_base_reports_loss_without_rejecting_clean_counter_moves(self):
        repo = self.repository()
        base = repo.commit_tree(
            "alpha\nalpha\nbeta\n",
            "base",
        )
        first = repo.commit_tree(
            "alpha\nalpha\nbeta\nalpha\n",
            "first",
            parents=(base,),
        )
        second = repo.commit_tree(
            "alpha\nbeta\n",
            "second",
            parents=(base,),
        )
        merge = repo.commit_tree(
            "alpha\nalpha\nbeta\n",
            "merge equals base",
            parents=(first, second),
        )

        result = repo.check(f"{merge}^..{merge}")

        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("lost first-parent change in file.txt", result.stdout)
        self.assertIn("lost second-parent change in file.txt", result.stdout)

        base_content = (
            "func a() error {\n"
            "\treturn err\n"
            "}\n"
            "\n"
            "func b() {\n"
            '\tprintln("b")\n'
            "}\n"
        )
        first_content = (
            "func a() error {\n"
            "\treturn err\n"
            "}\n"
            "\n"
            "func b() {\n"
            '\tprintln("b")\n'
            "\treturn err\n"
            "}\n"
        )
        second_content = base_content.replace("\treturn err", "\tpanic(1)", 1)
        go_base = repo.commit_tree(base_content, "go base")
        clean = repo.plain_merge(
            go_base,
            first_content,
            second_content,
            "clean-counter-moves",
        )

        self.assertIsNotNone(clean)
        clean_result = repo.check(clean[3])
        self.assertEqual(
            clean_result.returncode,
            0,
            clean_result.stdout + clean_result.stderr,
        )
        self.assertNotIn("lost", clean_result.stdout)

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

    def test_combined_resolution_drops_both_line_changes(self):
        repo = self.repository()
        repo.base("value = original\n")
        _, revision_range = repo.merge(
            "value = from main\n",
            "value = from side\n",
            "value = from main and side\n",
        )

        result = repo.check(revision_range)

        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("lost first-parent change", result.stdout)
        self.assertIn("lost second-parent change", result.stdout)

    def test_f5be45ac_shape_reports_dropped_added_lines(self):
        repo = self.repository()
        repo.base(
            "keep\n"
            "remove-shared\n"
            "remove-first-only\n"
            "remove-second-only\n"
        )
        _, revision_range = repo.merge(
            "keep\nremove-second-only\nfirst-one\nfirst-two\n",
            "keep\nremove-first-only\nsecond-one\n",
            "keep\nremove-first-only\nsecond-one\n",
        )

        result = repo.check(revision_range)

        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("lost first-parent change", result.stdout)
        self.assertNotIn("lost second-parent change", result.stdout)

    def test_binary_mode_and_empty_file_changes_are_reported(self):
        repo = self.repository()
        base = repo.base("stable\n")
        repo.run("checkout", "-B", "binary-first", base)
        repo.write_bytes("binary.bin", b"\x00\xff\n")
        repo.write("empty.txt", "")
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
        self.assertNotIn("empty.txt (mode-only)", binary_result.stdout)

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

    def test_comment_like_lines_are_counted_as_changed_lines(self):
        repo = self.repository()
        repo.base("-- x\nstable\n")
        _, revision_range = repo.merge(
            "++ x\n++ y\nstable\n",
            "stable\ny01\n",
            "++ x\nstable\ny01\n",
        )

        result = repo.check(revision_range)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("missing first-parent change", result.stdout)
        self.assertIn("+ ++ y", result.stdout)

    def test_submodule_pointer_changes_are_not_compared(self):
        repo = self.repository()
        base = repo.base("stable\n")
        repo.run("checkout", "-B", "submodule-first", base)
        submodule_commit = repo.commit_tree("submodule\n", "submodule")
        repo.run(
            "update-index",
            "--add",
            "--cacheinfo",
            f"160000,{submodule_commit},sub",
        )
        repo.run("commit", "-m", "submodule pointer")
        repo.run("checkout", "-B", "submodule-second", base)
        repo.write("other.txt", "second parent\n")
        repo.commit("other change")
        repo.run("checkout", "submodule-first")
        repo.run(
            "merge",
            "--no-ff",
            "-s",
            "ours",
            "submodule-second",
            "-m",
            "submodule merge",
        )
        repo.run("checkout", "submodule-second", "--", "other.txt")
        repo.run("add", "other.txt")
        repo.run("commit", "--amend", "--no-edit")
        merge = repo.run("rev-parse", "HEAD").stdout.strip()

        result = repo.check(f"{merge}^..{merge}")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("sub (submodule)", result.stdout)

    def test_no_merge_base_lists_paths_without_failing(self):
        repo = self.repository()
        first = repo.commit_tree("first\n", "first")
        second = repo.commit_tree("second\n", "second")
        merge = repo.commit_tree(
            "merge\n",
            "unrelated merge",
            parents=(first, second),
        )

        result = repo.check(f"{merge}^..{merge}")

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("first-parent file.txt (no merge base)", result.stdout)
        self.assertIn("second-parent file.txt (no merge base)", result.stdout)

    def test_unexpected_checker_errors_return_exit_two(self):
        repo = self.repository()
        repo.base()
        _, revision_range = repo.merge(
            "a\nb\nx01\n", "a\nb\ny01\n", "a\nb\nx01\ny01\n"
        )

        with mock.patch.object(
            MERGE_CHECKER,
            "check_merge",
            side_effect=IndexError("missing cat-file header"),
        ):
            result = repo.check_fast(revision_range)

        self.assertEqual(result.returncode, 2)
        self.assertIn("IndexError: missing cat-file header", result.stderr)

    def test_generated_edits_follow_the_line_change_rule(self):
        repo = self.repository()
        bases = (
            ("alpha", "beta", "gamma"),
            ("alpha", "alpha", "beta"),
        )
        operators = (
            "none",
            "append",
            "delete",
            "replace",
            "shared-replace",
            "duplicate",
            "swap",
        )
        case = 0
        for base_index, base_lines in enumerate(bases):
            base_content = "".join(f"{line}\n" for line in base_lines)
            base = repo.commit_tree(
                base_content,
                "generated base",
                parents=() if not base_index else (previous_base,),
            )
            previous_base = base
            first_commits = {}
            second_commits = {}
            for operator in operators:
                first_lines = apply_edit(base_lines, operator, "first")
                second_lines = apply_edit(base_lines, operator, "second")
                first_commits[operator] = repo.commit_tree(
                    "".join(f"{line}\n" for line in first_lines),
                    f"first {operator}",
                    parents=(base,),
                )
                second_commits[operator] = repo.commit_tree(
                    "".join(f"{line}\n" for line in second_lines),
                    f"second {operator}",
                    parents=(base,),
                )
            for first_operator, second_operator in product(operators, repeat=2):
                first_lines = apply_edit(base_lines, first_operator, "first")
                second_lines = apply_edit(base_lines, second_operator, "second")
                first = first_commits[first_operator]
                second = second_commits[second_operator]
                auto_merge, conflicted = repo.resolved_merge(
                    first, second, case
                )
                case += 1

                if base_index == 0:
                    base_merge = repo.commit_tree(
                        base_content,
                        f"merge equals base {case}",
                        parents=(first, second),
                    )
                    case += 1
                    result = repo.check_merge_fast(base_merge)
                    self.assert_checker_result(
                        result,
                        expected_lost_sides(
                            "merge-equals-base",
                            base_lines,
                            first_lines,
                            second_lines,
                            first_operator,
                            second_operator,
                        ),
                        f"base reset base={base_lines}, "
                        f"first={first_operator}, second={second_operator}",
                    )

                if conflicted:
                    for kept, _merged_lines in (
                        (first, first_lines),
                        (second, second_lines),
                    ):
                        merge, _ = repo.resolved_merge(
                            first, second, case, kept=kept
                        )
                        case += 1
                        expected = expected_lost_sides(
                            "reset-first" if kept == first else "reset-second",
                            base_lines,
                            first_lines,
                            second_lines,
                            first_operator,
                            second_operator,
                        )
                        result = repo.check_merge_fast(merge)
                        self.assert_checker_result(
                            result,
                            expected,
                            f"conflict base={base_lines}, "
                            f"first={first_operator}, second={second_operator}, "
                            f"kept={kept}",
                        )
                    continue

                result = repo.check_merge_fast(auto_merge)
                self.assert_checker_result(
                    result,
                    expected_lost_sides(
                        "auto-merge",
                        base_lines,
                        first_lines,
                        second_lines,
                        first_operator,
                        second_operator,
                    ),
                    f"auto base={base_lines}, first={first_operator}, "
                    f"second={second_operator}",
                )
                for kept, _merged_lines in (
                    (first, first_lines),
                    (second, second_lines),
                ):
                    merge = repo.reset_parent_merge(
                        first, second, case, kept
                    )
                    case += 1
                    expected = expected_lost_sides(
                        "reset-first" if kept == first else "reset-second",
                        base_lines,
                        first_lines,
                        second_lines,
                        first_operator,
                        second_operator,
                    )
                    result = repo.check_merge_fast(merge)
                    self.assert_checker_result(
                        result,
                        expected,
                        f"reset base={base_lines}, "
                        f"first={first_operator}, second={second_operator}, "
                        f"kept={kept}",
                    )

    def assert_checker_result(self, result, expected_lost, case):
        actual_lost = {
            side
            for side in ("first", "second")
            if f"lost {side}-parent change" in result.stdout
        }
        self.assertEqual(
            result.returncode,
            1 if expected_lost else 0,
            f"{case}: expected {expected_lost}, got {actual_lost}: "
            f"{result.stdout}{result.stderr}",
        )
        self.assertEqual(actual_lost, expected_lost, case)

    def test_octopus_merges_are_reported_and_skipped(self):
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

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(
            f"merge {merge[:12]}: not compared {len(branches)} "
            "parents (octopus)",
            result.stdout,
        )
        self.assertNotIn("more than two parents", result.stderr)

    def test_octopus_and_lost_merge_report_both_results(self):
        repo = self.repository()
        base = repo.base()
        branches = []
        for index in range(3):
            branch = f"octopus-loss-{index}"
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
        octopus = repo.run("rev-parse", "HEAD").stdout.strip()

        repo.run("checkout", "-B", "lost-first", octopus)
        repo.write("file.txt", "a\nb\nfirst-only\n")
        repo.commit("lost first change")
        repo.run("checkout", "-B", "lost-second", octopus)
        repo.write("file.txt", "a\nb\nsecond-only\n")
        repo.commit("lost second change")
        repo.run("checkout", "lost-first")
        repo.run(
            "merge",
            "--no-ff",
            "-s",
            "ours",
            "lost-second",
            "-m",
            "lost merge",
        )
        lost_merge = repo.run("rev-parse", "HEAD").stdout.strip()

        result = repo.check(f"{octopus}^..{lost_merge}")

        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn(
            f"merge {octopus[:12]}: not compared {len(branches)} "
            "parents (octopus)",
            result.stdout,
        )
        self.assertIn("lost second-parent change", result.stdout)
        self.assertNotIn("more than two parents", result.stderr)

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
