import contextlib
import io
import os
import tempfile
import unittest
from pathlib import Path

from verify import check_json


class CheckJsonTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve()
        previous = Path.cwd()
        os.chdir(self.root)
        self.addCleanup(os.chdir, previous)

    def check(self, *names: str) -> tuple[int, str]:
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            code = check_json.main(list(names))
        return code, stderr.getvalue()

    def check_text(self, text: str) -> tuple[int, str]:
        (self.root / "a.json").write_text(text)
        return self.check("a.json")

    def test_a_valid_file_passes(self):
        self.assertEqual(self.check_text('{"hooks": [1, 2]}\n'), (0, ""))

    def test_a_truncated_file_names_the_line_and_column_of_the_error(self):
        code, stderr = self.check_text('{\n  "hooks": [\n    1,\n')
        self.assertEqual(code, 1)
        self.assertEqual(stderr, "a.json:4:1: Expecting value\n")

    def test_an_empty_file_fails(self):
        code, stderr = self.check_text("")
        self.assertEqual(code, 1)
        self.assertEqual(stderr, "a.json:1:1: Expecting value\n")

    def test_two_values_in_one_file_fail_at_the_second(self):
        code, stderr = self.check_text("{}\n{}\n")
        self.assertEqual(code, 1)
        self.assertEqual(stderr, "a.json:2:1: Extra data\n")

    def test_a_missing_file_names_the_path_and_the_cause(self):
        code, stderr = self.check("gone.json")
        self.assertEqual(code, 1)
        self.assertTrue(stderr.startswith("gone.json: "), stderr)
        self.assertIn("No such file or directory", stderr)

    def test_invalid_utf8_names_the_path_and_exits_1(self):
        (self.root / "a.json").write_bytes(b"\xff")
        try:
            code, stderr = self.check("a.json")
        except UnicodeDecodeError as error:
            self.fail(f"check-json raised instead of returning exit 1: {error}")
        self.assertEqual(code, 1)
        self.assertTrue(stderr.startswith("a.json: "), stderr)

    def test_every_failing_file_is_named_and_a_good_one_is_not(self):
        (self.root / "good.json").write_text("{}\n")
        (self.root / "bad.json").write_text("[")
        code, stderr = self.check("bad.json", "good.json", "gone.json")
        self.assertEqual(code, 1)
        self.assertEqual([line.split(":")[0] for line in stderr.splitlines()], ["bad.json", "gone.json"])

    def test_no_arguments_print_the_usage_and_exit_2(self):
        self.assertEqual(self.check(), (2, "Usage: run.py verify check-json PATH...\n"))


if __name__ == "__main__":
    unittest.main()
