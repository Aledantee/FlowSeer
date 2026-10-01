import importlib.util
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "plan_deviations", Path(__file__).with_name("plan-deviations.py")
)
plan_deviations = importlib.util.module_from_spec(spec)
spec.loader.exec_module(plan_deviations)


def units(text):
    with tempfile.TemporaryDirectory() as directory:
        plan = Path(directory) / "example-plan.md"
        plan.write_text(text)
        return plan_deviations.units(plan)


class FilesFieldTest(unittest.TestCase):
    def test_bare_entries_read_like_quoted_ones(self):
        found = units(
            "### U1. Bare paths with asides\n\n"
            "Files: tools/buf/go.mod, AGENTS.md (line 44 only), generated/ (regenerated), "
            "docs/old.md (renamed to new.md, see Change), pkg/b/{b.go,b_test.go}\n"
            "After: none\n"
            "Change: `pkg/unrelated.go` stays as it is.\n"
        )
        self.assertEqual(
            found["U1"],
            ["tools/buf/go.mod", "AGENTS.md", "generated", "docs/old.md", "pkg/b/b.go", "pkg/b/b_test.go"],
        )

    def test_bare_entries_continue_over_lines_and_skip_prose(self):
        found = units(
            "### U1: Bullet field with bare paths\n\n"
            "- **Files:**\n"
            "  src/a/a.go, src/a/a_test.go,\n"
            "  the README beside them, none\n"
            "  - src/b/b.go\n"
            "- **After:** none\n"
        )
        self.assertEqual(found["U1"], ["src/a/a.go", "src/a/a_test.go", "src/b/b.go"])

    def test_bare_root_file_is_not_a_sibling_of_the_entry_before_it(self):
        found = units("### U1. Root files\n\nFiles: tools/buf/go.sum, buf.gen.yaml, `tools/x.go`\n")
        self.assertEqual(found["U1"], ["tools/x.go"])
        found = units("### U1. Root files\n\nFiles: tools/buf/go.sum, buf.gen.yaml, generated/\n")
        self.assertEqual(found["U1"], ["tools/buf/go.sum", "buf.gen.yaml", "generated"])

    def test_quoted_line_ignores_its_bare_words(self):
        found = units("### U1. Quoted\n\nFiles: `pkg/a/a.go` and docs/aside.md (`Symbol` aside)\n")
        self.assertEqual(found["U1"], ["pkg/a/a.go"])


if __name__ == "__main__":
    unittest.main()
