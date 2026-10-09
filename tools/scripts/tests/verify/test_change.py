import contextlib
import io
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from tests.verify import support
from verify import change, check_prose

BASH = shutil.which("bash") or "bash"
PRINTABLE = [chr(code) for code in range(0x20, 0x7F)]

# What /bin/bash 3.2.57 prints for `printf %q`.
QUOTED = [
    ("a b", r"a\ b"),
    ("", "''"),
    ("x$y", r"x\$y"),
    ("a,b", r"a\,b"),
    ("a\nb", "$'a\\nb'"),
    ("plain/p.go", "plain/p.go"),
    ("#a", r"\#a"),
    ("a#", "a#"),
    ("it's\n", "$'it\\'s\\n'"),
    ("\x01", "$'\\001'"),
    ("\x1b", "$'\\E'"),
    ("a\tb", "$'a\\tb'"),
    ("\\\n", "$'\\\\\\n'"),
]

LABELS = [
    (["uv", "run", "--quiet", "/abs/tools/scripts/run.py", "verify", "check-prose", "--quiet", "a.md"], "run.py verify check-prose"),
    (["uv", "run", "tools/scripts/run.py", "test"], "run.py test"),
    (["uv", "run", "tools/scripts/run.py"], "run.py"),
    (["uv", "run", "tools/scripts/run.py", "", "x"], "run.py x"),
    (["uv", "run", "run.py", "test"], "uv run"),
    (["uv", "run", "--quiet", "x.py"], "uv run"),
    (["go", "-C", "tools/buf", "mod", "verify"], "go tools/buf"),
    (["go", "tool", "-modfile=/r/tools/buf/go.mod", "buf", "format"], "go tool"),
    (["go", "test", "-race", "-p", "4", "./..."], "go test"),
    (["./node_modules/.bin/vue-tsc", "--noEmit"], "./node_modules/.bin/vue-tsc"),
    (["/tmp/b/check-guarantees", "--", "a.go", "b.go"], "/tmp/b/check-guarantees a.go"),
    (["diff", "-qr", "generated/go/proto", "/x"], "diff generated/go/proto"),
]


class AffectedTest(unittest.TestCase):
    def test_a_chain_of_importers_is_followed_whatever_the_row_order(self):
        rows = [
            ("m/d", {"m/c"}),
            ("m/c", {"m/b", "fmt"}),
            ("m/b", {"m/a"}),
            ("m/other", {"fmt"}),
        ]
        self.assertEqual(change.affected(rows, ["m/a"]), ["m/a", "m/b", "m/c", "m/d"])

    def test_a_package_that_only_its_tests_import_is_affected(self):
        # The fields are .Deps, .TestImports, and .XTestImports, in that order.
        rows = change.parse_rows("m/a|fmt  \nm/e|fmt  m/a\nm/f|fmt m/z \n")
        self.assertEqual(rows, [("m/a", {"fmt"}), ("m/e", {"fmt", "m/a"}), ("m/f", {"fmt", "m/z"})])
        self.assertEqual(change.affected(rows, ["m/a"]), ["m/a", "m/e"])

    def test_a_package_nobody_imports_is_alone(self):
        rows = [("m/a", {"fmt"}), ("m/b", {"m/a"})]
        self.assertEqual(change.affected(rows, ["m/b", "m/b"]), ["m/b"])

    def test_a_changed_package_missing_from_the_rows_is_kept(self):
        self.assertEqual(change.affected([("m/a", {"fmt"})], ["m/gone"]), ["m/gone"])


class QuoteTest(unittest.TestCase):
    def test_the_literals_bash_prints(self):
        for word, expected in QUOTED:
            with self.subTest(word=word):
                self.assertEqual(change.shell_quote(word), expected)

    def test_printable_ascii_is_quoted_as_bash_quotes_it(self):
        # Each character alone, between letters, first, and last, since `#` is
        # quoted only at the start of a word.
        words = [form.replace("c", char) for char in PRINTABLE for form in ("c", "acb", "ca", "ac")]
        done = subprocess.run(
            [BASH, "-c", 'for word; do printf "%q\\0" "$word"; done', "_", *words],
            capture_output=True,
            check=True,
        )
        printed = done.stdout.decode("utf-8").split("\0")[:-1]
        self.assertEqual(len(printed), len(words))
        for word, expected in zip(words, printed):
            with self.subTest(word=word):
                self.assertEqual(change.shell_quote(word), expected)


class GateLabelTest(unittest.TestCase):
    def test_labels_as_the_shell_names_the_gate(self):
        for command, expected in LABELS:
            with self.subTest(command=command):
                self.assertEqual(change.gate_label(command), expected)


class DiffTreesTest(unittest.TestCase):
    @unittest.skipIf(os.name == "nt", "the mode bits of a directory do not deny a read on Windows")
    def test_a_directory_that_cannot_be_read_is_a_diff_line_and_status_2(self):
        with tempfile.TemporaryDirectory() as directory:
            left, right = Path(directory, "left"), Path(directory, "right")
            for side in (left, right):
                (side / "sub").mkdir(parents=True)
            (left / "sub").chmod(0)
            try:
                if os.access(left / "sub", os.R_OK):
                    self.skipTest("this user reads a directory without its read bit")
                stdout, stderr = io.StringIO(), io.StringIO()
                with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                    code = change.diff_trees(str(left), str(right))
            finally:
                (left / "sub").chmod(0o755)
        self.assertEqual(code, 2)
        self.assertEqual(stdout.getvalue(), "")
        self.assertEqual(stderr.getvalue(), f"diff: {left}/sub: Permission denied\n")


class VerdictLineTest(support.VerifierCase):
    """The verdict is the last line of standard error for every way a check ends."""

    def test_failed_tree_state_listing_refuses_a_receipt_and_removes_the_temporary_file(self):
        self.write("README.md", "# Fixture\n\nChanged.\n")
        with mock.patch.object(change.treestate, "listing", side_effect=RuntimeError("listing failed")):
            result = self.verify_in_process("--", "README.md")
        self.assertEqual(result.code, 1, result.stdout + result.stderr)
        self.assertIn(
            "verify-change: hooks tree-state failed; the dirty marker would mark verified files again",
            result.stderr,
        )
        self.assertIn("listing failed", result.stderr)
        self.assertEqual(support.last_line(result.stderr), support.verdict(1))
        self.assertFalse(self.receipt.exists())
        self.assertEqual(list(self.tree_state.parent.glob("flowseer-tree-state.*")), [])

    def verify_in_process(self, *args: str) -> support.Result:
        stdout, stderr = io.StringIO(), io.StringIO()
        previous = os.getcwd()
        self.addCleanup(os.chdir, previous)
        os.chdir(self.root)
        # The run changes to the root and puts the Go tools' directory on PATH.
        with mock.patch.dict(os.environ, self.stubs.env(self.env), clear=True):
            with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                code = change.main(list(args))
        return support.Result(code, stdout.getvalue(), stderr.getvalue())

    def test_verdict_line_is_last_when_a_check_raises(self):
        self.write("README.md", "# Fixture\n\nChanged.\n")
        cases = [
            (RuntimeError("boom"), 1),
            (KeyboardInterrupt(), 130),
            (SystemExit(3), 3),
        ]
        for error, code in cases:
            with self.subTest(error=repr(error)):
                with mock.patch.object(check_prose, "main", side_effect=error):
                    result = self.verify_in_process("--", "README.md")
                self.assertEqual(result.code, code, result.stdout + result.stderr)
                self.assertEqual(support.last_line(result.stderr), support.verdict(code, "run.py verify check-prose"))
                self.assertEqual(list(self.tmp.glob("flowseer-build.*")), [])
                self.assertFalse(self.receipt.exists())

    def test_a_pass_says_so_without_a_verdict_line(self):
        self.write("README.md", "# Fixture\n\nChanged.\n")
        result = self.verify_in_process("--", "README.md")
        self.assertEqual((result.code, support.last_line(result.stdout)), (0, support.PASSED))
        self.assertNotIn("FAILED", result.stderr)
        self.assertTrue(self.receipt.exists())
        self.assertIn("paths=README.md ", self.receipt.read_text().splitlines())


if __name__ == "__main__":
    unittest.main()
