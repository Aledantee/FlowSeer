import contextlib
import io
import os
import tempfile
import unittest
from pathlib import Path

from verify import check_markdown_links


class CheckMarkdownLinksTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve()
        (self.root / "present.md").write_text("x\n")
        previous = Path.cwd()
        os.chdir(self.root)
        self.addCleanup(os.chdir, previous)

    def check(self, text: str) -> tuple[int, str]:
        (self.root / "doc.md").write_text(text)
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            code = check_markdown_links.main(["doc.md"])
        return code, stderr.getvalue()

    def test_link_to_a_present_file_passes(self):
        self.assertEqual(self.check("[a](present.md)\n"), (0, ""))

    def test_link_to_a_missing_file_names_path_and_line(self):
        code, stderr = self.check("ok\n[a](gone.md)\n")
        self.assertEqual(code, 1)
        self.assertIn("doc.md:2: missing link target: gone.md", stderr)

    def test_fragment_is_dropped_and_the_path_checked(self):
        self.assertEqual(self.check("[a](present.md#x)\n")[0], 0)
        self.assertEqual(self.check("[a](gone.md#x)\n")[0], 1)

    def test_http_link_is_skipped(self):
        self.assertEqual(self.check("[a](https://example.invalid/x)\n")[0], 0)

    def test_link_inside_a_fenced_block_is_skipped(self):
        self.assertEqual(self.check("```\n[a](gone.md)\n```\n")[0], 0)

    def test_registered_under_the_group(self):
        from verify import COMMANDS

        self.assertEqual(COMMANDS["check-markdown-links"], "check_markdown_links")


if __name__ == "__main__":
    unittest.main()
