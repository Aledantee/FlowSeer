import importlib.util
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

spec = importlib.util.spec_from_file_location(
    "check_guarantees", Path(__file__).with_name("check-guarantees.py")
)
check_guarantees = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check_guarantees)


def create_pkg(root: Path, pkg_rel: str = "src/pkg") -> Path:
    pkg = root / pkg_rel
    pkg.mkdir(parents=True, exist_ok=True)
    (root / "go.mod").write_text("module testmod\n\ngo 1.27\n", encoding="utf-8")
    (pkg / "pkg.go").write_text("package pkg\n", encoding="utf-8")
    return pkg


class CheckGuaranteesTest(unittest.TestCase):
    def test_valid_file_passes(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "# Package guarantees\n\n"
                "Preamble explaining the package guarantees.\n\n"
                "## Valid behavior\n\n"
                "It MUST succeed.\n\n"
                "- WHEN called with valid input THEN it returns nil.\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/foo_test.go"], root)
            self.assertEqual(errors, [])

    def test_normative_sentence_must_not(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "# Package guarantees\n\n"
                "## Valid behavior\n\n"
                "It MUST NOT fail on valid input.\n\n"
                "- WHEN called with valid input THEN it returns nil.\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(errors, [])

    def test_continuation_lines_accepted(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\n"
                "func TestA(t *testing.T) {}\n"
                "func TestB(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "# Package guarantees\n\n"
                "## Multiline block\n\n"
                "It MUST succeed.\n\n"
                "- WHEN input is provided across multiple lines\n"
                "  THEN the result is returned cleanly.\n\n"
                "Proved by: TestA,\n"
                "  TestB\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(errors, [])

    def test_missing_proved_by_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nfunc TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "# Package guarantees\n\n"
                "## Output is capped\n\n"
                "It MUST be capped.\n\n"
                "- WHEN output exceeds limit THEN it truncates.\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/foo_test.go"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:3: "Output is capped" has no Proved by: line',
            )

    def test_duplicate_heading_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Same Heading\n\n"
                "It MUST succeed.\n\n"
                "- WHEN first THEN ok\n\n"
                "Proved by: TestA\n\n"
                "## Same Heading\n\n"
                "It MUST succeed.\n\n"
                "- WHEN second THEN ok\n\n"
                "Proved by: TestB\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:9: duplicate guarantee heading "Same Heading"',
            )

    def test_section_without_when_then_bullet_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nfunc TestA(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## No Scenario\n\n"
                "It MUST do something.\n\n"
                "Proved by: TestA\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:1: "No Scenario" has no - WHEN ... THEN scenario bullet',
            )

    def test_when_bullet_without_then_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nfunc TestA(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Incomplete Scenario\n\n"
                "It MUST do something.\n\n"
                "- WHEN input is given without consequence\n"
                "  and still no result.\n\n"
                "Proved by: TestA\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:1: "Incomplete Scenario" has no - WHEN ... THEN scenario bullet',
            )

    def test_cited_test_that_does_not_exist_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nfunc TestExisting(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Output is capped\n\n"
                "It MUST be capped.\n\n"
                "- WHEN output exceeds limit THEN it truncates.\n\n"
                "Proved by: TestMissing\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/foo_test.go"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:7: "Output is capped" cites test "TestMissing" which does not exist in src/pkg',
            )

    def test_test_in_subdirectory_or_testdata_does_not_resolve(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            sub = pkg / "sub"
            sub.mkdir()
            (sub / "sub_test.go").write_text(
                "package sub\n\nfunc TestInSub(t *testing.T) {}\n",
                encoding="utf-8",
            )
            testdata = pkg / "testdata"
            testdata.mkdir()
            (testdata / "data_test.go").write_text(
                "package pkg\n\nfunc TestInData(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Sub test cited\n\n"
                "It MUST fail.\n\n"
                "- WHEN check THEN fail\n\n"
                "Proved by: TestInSub, TestInData\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 2)
            self.assertIn(
                'cites test "TestInSub" which does not exist in src/pkg', errors[0]
            )
            self.assertIn(
                'cites test "TestInData" which does not exist in src/pkg', errors[1]
            )

    def test_changed_path_that_no_longer_exists_selects_guarantees(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nfunc TestFoo(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Foo\n\n"
                "It MUST be foo.\n\n"
                "- WHEN foo THEN bar\n\n"
                "Proved by: TestFoo\n",
                encoding="utf-8",
            )
            selected = check_guarantees.select_guarantee_files(
                ["src/pkg/deleted_test.go"], root
            )
            self.assertEqual(selected, [(pkg / "GUARANTEES.md").resolve()])

    def test_changed_path_whose_directory_holds_none_selects_nothing(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            other = root / "docs"
            other.mkdir(parents=True)
            (other / "README.md").write_text("# Readme\n", encoding="utf-8")
            selected = check_guarantees.select_guarantee_files(["docs/README.md"], root)
            self.assertEqual(selected, [])

    def test_lowercase_guarantees_file_ignored(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            doc_dir = root / "docs" / "conventions"
            doc_dir.mkdir(parents=True)
            (doc_dir / "guarantees.md").write_text("# Guarantees\n", encoding="utf-8")
            selected = check_guarantees.select_guarantee_files(
                ["docs/conventions/guarantees.md"], root
            )
            self.assertEqual(selected, [])

    def test_wrapped_proved_by_list_with_missing_test_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Wrapped proved by\n\n"
                "It MUST succeed.\n\n"
                "- WHEN check THEN ok\n\n"
                "Proved by: TestA,\n"
                "  TestGone\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:8: "Wrapped proved by" cites test "TestGone" which does not exist in src/pkg',
            )

    def test_testmain_testhelper_and_block_comment_do_not_resolve(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\n"
                "import \"testing\"\n\n"
                "func TestMain(m *testing.M) {}\n"
                "func Testhelper(t *testing.T) {}\n"
                "/*\n"
                "func TestCommented(t *testing.T) {}\n"
                "*/\n"
                "/* func TestInlineCommented(t *testing.T) {} */\n"
                "func TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            tests, list_error = check_guarantees.find_test_functions(pkg)
            self.assertIsNone(list_error)
            self.assertEqual(tests, {"TestValid"})
            (pkg / "GUARANTEES.md").write_text(
                "## TestMain cited\n\n"
                "It MUST fail.\n\n"
                "- WHEN check THEN fail\n\n"
                "Proved by: TestMain\n\n"
                "## Testhelper cited\n\n"
                "It MUST fail.\n\n"
                "- WHEN check THEN fail\n\n"
                "Proved by: Testhelper\n\n"
                "## Commented cited\n\n"
                "It MUST fail.\n\n"
                "- WHEN check THEN fail\n\n"
                "Proved by: TestCommented\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 3)
            self.assertIn('cites test "TestMain" which does not exist in src/pkg', errors[0])
            self.assertIn('cites test "Testhelper" which does not exist in src/pkg', errors[1])
            self.assertIn('cites test "TestCommented" which does not exist in src/pkg', errors[2])

    def test_heading_with_unspaced_hash(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\n"
                "func TestSharp(t *testing.T) {}\n"
                "func TestPlain(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "# Package guarantees\n\n"
                "## Parses C#\n\n"
                "It MUST parse C#.\n\n"
                "- WHEN C# input is parsed THEN it succeeds.\n\n"
                "Proved by: TestSharp\n\n"
                "## Parses C\n\n"
                "It MUST parse C.\n\n"
                "- WHEN C input is parsed THEN it succeeds.\n\n"
                "Proved by: TestPlain\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(errors, [])

    def test_heading_trailing_hashes_preceded_by_whitespace_stripped(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Valid Heading ###\n\n"
                "It MUST succeed.\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(errors, [])

    def test_empty_heading_title_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "GUARANTEES.md").write_text(
                "## \n\n"
                "It MUST succeed.\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertIn("src/pkg/GUARANTEES.md:1: unknown line format", errors)

    def test_setext_underline_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "GUARANTEES.md").write_text(
                "Invalid Heading\n"
                "---\n\n"
                "It MUST succeed.\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertIn("src/pkg/GUARANTEES.md:2: unknown line format", errors)

    def test_numbered_list_followed_by_setext_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "GUARANTEES.md").write_text(
                "## Heading\n\n"
                "It MUST succeed.\n\n"
                "1. item\n"
                "---\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertIn("src/pkg/GUARANTEES.md:5: unknown line format", errors)
            self.assertIn("src/pkg/GUARANTEES.md:6: unknown line format", errors)

    def test_code_fences_fail(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "GUARANTEES.md").write_text(
                "## Heading\n\n"
                "It MUST succeed.\n\n"
                "```go\n"
                "func foo() {}\n"
                "```\n\n"
                "~~~\n"
                "~~~\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertIn("src/pkg/GUARANTEES.md:5: unknown line format", errors)
            self.assertIn("src/pkg/GUARANTEES.md:7: unknown line format", errors)
            self.assertIn("src/pkg/GUARANTEES.md:9: unknown line format", errors)

    def test_indented_code_block_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "GUARANTEES.md").write_text(
                "## Heading\n\n"
                "It MUST succeed.\n\n"
                "    some indented code block\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertIn("src/pkg/GUARANTEES.md:5: unknown line format", errors)

    def test_indented_proved_by_fails_and_does_not_count(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Output is capped\n\n"
                "It MUST be capped.\n\n"
                "- WHEN output exceeds limit THEN it truncates.\n\n"
                "    Proved by: TestA\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertIn("src/pkg/GUARANTEES.md:7: unknown line format", errors)
            self.assertIn(
                'src/pkg/GUARANTEES.md:1: "Output is capped" has no Proved by: line',
                errors,
            )

    def test_two_space_indented_proved_by_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "GUARANTEES.md").write_text(
                "## Heading\n\n"
                "It MUST succeed.\n\n"
                "- WHEN valid THEN ok\n\n"
                "  Proved by: TestA\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertIn("src/pkg/GUARANTEES.md:7: unknown line format", errors)

    def test_unallowed_heading_levels_fail(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "GUARANTEES.md").write_text(
                "### Subheading\n\n"
                "# Doc Title\n\n"
                "## First Heading\n\n"
                "It MUST succeed.\n\n"
                "# Second Title\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertIn("src/pkg/GUARANTEES.md:1: unknown line format", errors)
            self.assertIn("src/pkg/GUARANTEES.md:9: unknown line format", errors)

    def test_unindented_non_normative_line_in_section_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "GUARANTEES.md").write_text(
                "## Heading\n\n"
                "Non-normative sentence without uppercase requirement keyword.\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertIn("src/pkg/GUARANTEES.md:3: unknown line format", errors)

    def test_invalid_preamble_line_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "GUARANTEES.md").write_text(
                "# Title\n\n"
                "- bullet in preamble\n\n"
                "Proved by: TestA\n\n"
                "## Heading\n\n"
                "It MUST succeed.\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertIn("src/pkg/GUARANTEES.md:3: unknown line format", errors)
            self.assertFalse(
                any(e.startswith("src/pkg/GUARANTEES.md:5:") for e in errors),
                "preamble paragraph text is not a citation",
            )

    def test_indented_line_without_active_continuation_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "GUARANTEES.md").write_text(
                "## Heading\n\n"
                "It MUST succeed.\n\n"
                "  stray indented line\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertIn("src/pkg/GUARANTEES.md:5: unknown line format", errors)

    def test_trailing_comma_in_proved_by_list_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Capped\n\n"
                "It MUST be capped.\n\n"
                "- WHEN limit reached THEN truncate\n\n"
                "Proved by: TestA,\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:7: "Capped" Proved by: list ends with a comma',
            )

    def test_wrapped_proved_by_ending_with_comma_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Capped\n\n"
                "It MUST be capped.\n\n"
                "- WHEN limit reached THEN truncate\n\n"
                "Proved by: TestA,\n"
                "  TestB,\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:8: "Capped" Proved by: list ends with a comma',
            )

    def test_duplicate_proved_by_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Double Proved\n\n"
                "It MUST succeed.\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestA\n"
                "Proved by: TestB\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:8: "Double Proved" has duplicate Proved by: line',
            )

    def test_proved_by_names_no_tests_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "GUARANTEES.md").write_text(
                "## Empty Proved\n\n"
                "It MUST succeed.\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: \n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:7: "Empty Proved" Proved by: line names no tests',
            )

    def test_no_headings_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "GUARANTEES.md").write_text(
                "# Title\n\nPreamble only.\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                "src/pkg/GUARANTEES.md:1: no guarantee sections (## headings) found",
            )

    def test_ignored_build_tag_and_underscore_file_excluded(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "_foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestUnderscore(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "ignored_test.go").write_text(
                "//go:build ignore\n\npackage pkg\n\nimport \"testing\"\n\nfunc TestIgnored(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Capped\n\n"
                "It MUST be capped.\n\n"
                "- WHEN output exceeds limit THEN it truncates.\n\n"
                "Proved by: TestIgnored, TestUnderscore\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 2)
            self.assertIn('cites test "TestIgnored" which does not exist in src/pkg', errors[0])
            self.assertIn('cites test "TestUnderscore" which does not exist in src/pkg', errors[1])

    def test_unicode_lowercase_test_name_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\n"
                "func Testé(t *testing.T) {}\n"
                "func TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            tests, list_error = check_guarantees.find_test_functions(pkg)
            self.assertIsNone(list_error)
            self.assertEqual(tests, {"TestValid"})

    def test_supported_test_signatures_resolve(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\n"
                "import \"testing\"\n\n"
                "func Test(t *testing.T) {}\n"
                "func TestAnon(*testing.T) {}\n"
                "func TestMain(t *testing.T) {}\n"
                "func TestªX(t *testing.T) {}\n"
                "func TestMultiline(\n"
                "  t *testing.T,\n"
                ") {}\n",
                encoding="utf-8",
            )
            (pkg / "alias_test.go").write_text(
                "package pkg\n\n"
                "import tst \"testing\"\n\n"
                "func TestAliased(t *tst.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "dot_test.go").write_text(
                "package pkg\n\n"
                "import . \"testing\"\n\n"
                "func TestDot(t *T) {}\n",
                encoding="utf-8",
            )
            tests, list_error = check_guarantees.find_test_functions(pkg)
            self.assertIsNone(list_error)
            expected = {
                "Test",
                "TestAnon",
                "TestMain",
                "TestªX",
                "TestMultiline",
                "TestAliased",
                "TestDot",
            }
            self.assertEqual(tests, expected)

    def test_invalid_signatures_do_not_resolve(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\n"
                "import \"testing\"\n\n"
                "func TestBench(b *testing.B) {}\n"
                "func TestNoArgs() {}\n"
                "func TestMultiArgs(t *testing.T, x int) {}\n"
                "func TestWithReturn(t *testing.T) bool { return true }\n"
                "func TestAnonParen((*testing.T)) {}\n"
                "func TestNamedParen(t (*testing.T)) {}\n"
                "func TestMain(m *testing.M) {}\n"
                "type Suite struct{}\n"
                "func (s *Suite) TestMethod(t *testing.T) {}\n"
                "func TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            tests, list_error = check_guarantees.find_test_functions(pkg)
            self.assertIsNone(list_error)
            self.assertEqual(tests, {"TestValid"})

    def test_rejected_signature_forms_fail_go_list(self):
        forms = [
            "func TestAnonParen((*testing.T)) {}",
            "func TestNamedParen(t (*testing.T)) {}",
            "func Testé(t *testing.T) {}",
        ]
        for form in forms:
            with self.subTest(form=form), tempfile.TemporaryDirectory() as d:
                root = Path(d)
                pkg = create_pkg(root)
                (pkg / "bad_test.go").write_text(
                    "package pkg\n\nimport \"testing\"\n\n" + form + "\n",
                    encoding="utf-8",
                )
                env = dict(os.environ)
                env["GOWORK"] = "off"
                res = subprocess.run(
                    ["go", "test", "-list", "^Test", "."],
                    cwd=str(pkg),
                    capture_output=True,
                    text=True,
                    env=env,
                )
                self.assertNotEqual(
                    res.returncode, 0, f"go test -list accepted {form}: {res.stdout}"
                )
                tests, list_error = check_guarantees.find_test_functions(pkg)
                self.assertIsNone(list_error)
                self.assertEqual(tests, set())

    def test_lexer_oracle_against_go_test_list(self):
        go_bin = shutil.which("go")
        if not go_bin:
            self.fail("go binary not found on PATH")

        with tempfile.TemporaryDirectory() as d:
            pkg = Path(d)
            (pkg / "go.mod").write_text(
                "module example.com/fixture\n\ngo 1.27\n", encoding="utf-8"
            )
            (pkg / "fixture_test.go").write_text(
                "package fixture\n\n"
                "import \"testing\"\n\n"
                "// Decoy line comment: /* */ \" ` func TestFakeInLineComment(t *testing.T) {}\n"
                "func TestAfterLineComment(t *testing.T) {}\n\n"
                "/*\n"
                "Decoy block comment: // \" `\n"
                "func TestFakeInBlockComment(t *testing.T) {}\n"
                "*/\n"
                "func TestAfterBlockComment(t *testing.T) {}\n\n"
                "var _ = \"decoy interpreted string: /* */ // ` \\\" \\nfunc TestFakeInInterpretedString(t *testing.T) {}\"\n"
                "func TestAfterInterpretedString(t *testing.T) {}\n\n"
                "var _ = '\\''\n"
                "var _ = '\"'\n"
                "var _ = '/'\n"
                "var _ = '\\n'\n"
                "func TestAfterRuneLiterals(t *testing.T) {}\n\n"
                "var _ = `\n"
                "decoy raw string: /* */ // \" \n"
                "func TestFakeInRawString(t *testing.T) {}\n"
                "`\n"
                "func TestAfterRawString(t *testing.T) {}\n\n"
                "func Test(t *testing.T) {}\n"
                "func TestAnon(*testing.T) {}\n"
                "func TestMain(t *testing.T) {}\n"
                "func TestªX(t *testing.T) {}\n"
                "func TestMultiline(\n"
                "  t *testing.T,\n"
                ") {}\n",
                encoding="utf-8",
            )
            (pkg / "alias_test.go").write_text(
                "package fixture\n\n"
                "import tst \"testing\"\n\n"
                "func TestAliased(t *tst.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "dot_test.go").write_text(
                "package fixture\n\n"
                "import . \"testing\"\n\n"
                "func TestDot(t *T) {}\n",
                encoding="utf-8",
            )

            env = dict(os.environ)
            env["GOWORK"] = "off"
            res = subprocess.run(
                ["go", "test", "-list", "^Test", "."],
                cwd=str(pkg),
                capture_output=True,
                text=True,
                env=env,
            )
            self.assertEqual(res.returncode, 0, f"go test -list failed: {res.stderr}")
            go_tests = {
                line.strip()
                for line in res.stdout.splitlines()
                if line.strip().startswith("Test")
            }
            py_tests, list_error = check_guarantees.find_test_functions(pkg)
            self.assertIsNone(list_error)

            expected = {
                "TestAfterLineComment",
                "TestAfterBlockComment",
                "TestAfterInterpretedString",
                "TestAfterRuneLiterals",
                "TestAfterRawString",
                "Test",
                "TestAnon",
                "TestMain",
                "TestªX",
                "TestMultiline",
                "TestAliased",
                "TestDot",
            }
            self.assertEqual(go_tests, expected)
            self.assertEqual(py_tests, go_tests)

    def test_main_exit_codes(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = create_pkg(root)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "# Package guarantees\n\n"
                "## Valid\n\n"
                "It MUST succeed.\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )

            code = check_guarantees.main(["--root", str(root), "src/pkg/GUARANTEES.md"])
            self.assertEqual(code, 0)

            code_all = check_guarantees.main(["--root", str(root), "--all"])
            self.assertEqual(code_all, 0)
            (pkg / "GUARANTEES.md").write_text(
                "## Invalid\n\n"
                "It MUST fail.\n\n"
                "- WHEN invalid THEN fail\n\n"
                "Proved by: TestMissing\n",
                encoding="utf-8",
            )
            import io
            from contextlib import redirect_stderr

            with redirect_stderr(io.StringIO()):
                code_violation = check_guarantees.main(
                    ["--root", str(root), "src/pkg/GUARANTEES.md"]
                )
                self.assertEqual(code_violation, 1)

                code_all_violation = check_guarantees.main(
                    ["--root", str(root), "--all"]
                )
                self.assertEqual(code_all_violation, 1)

    def _check_grammar(
        self, root: Path, text: str, tests: tuple[str, ...] = ("TestValid", "TestA")
    ) -> list[str]:
        (root / "src" / "pkg" / "GUARANTEES.md").write_text(text, encoding="utf-8")
        with mock.patch.object(
            check_guarantees,
            "find_test_functions",
            return_value=(set(tests), None),
        ):
            return check_guarantees.check_guarantees(
                ["src/pkg/GUARANTEES.md"], root
            )

    def test_section_needs_exactly_one_normative_sentence(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            create_pkg(root)
            errors = self._check_grammar(
                root,
                "## No Normative\n\n"
                "- WHEN x THEN y\n\n"
                "Proved by: TestA\n\n"
                "## Two Normative\n\n"
                "It MUST hold.\n"
                "It MUST also hold.\n\n"
                "- WHEN x THEN y\n\n"
                "Proved by: TestA\n",
            )
            self.assertIn(
                'src/pkg/GUARANTEES.md:1: "No Normative" has no normative MUST sentence',
                errors,
            )
            self.assertIn(
                'src/pkg/GUARANTEES.md:10: "Two Normative" has more than one normative sentence',
                errors,
            )

    def test_document_title_is_optional_and_single(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            create_pkg(root)
            self.assertEqual(
                self._check_grammar(
                    root,
                    "## S\n\nIt MUST hold.\n\n- WHEN x THEN y\n\nProved by: TestA\n",
                ),
                [],
            )
            errors = self._check_grammar(
                root,
                "# One\n\n## S\n\nIt MUST hold.\n\n- WHEN x THEN y\n\n"
                "Proved by: TestA\n\n# Two\n",
            )
            self.assertIn("src/pkg/GUARANTEES.md:11: unknown line format", errors)
            errors = self._check_grammar(
                root,
                "# One\n\n# Two\n\n## S\n\nIt MUST hold.\n\n- WHEN x THEN y\n\n"
                "Proved by: TestA\n",
            )
            self.assertIn("src/pkg/GUARANTEES.md:3: unknown line format", errors)

    def test_citation_items_are_go_identifiers(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            create_pkg(root)
            self.assertEqual(
                self._check_grammar(
                    root,
                    "## Backticked\n\nIt MUST hold.\n\n- WHEN x THEN y\n\n"
                    "Proved by: `TestA`, `TestValid`\n",
                ),
                [],
            )
            errors = self._check_grammar(
                root,
                "## Empty item\n\nIt MUST hold.\n\n- WHEN x THEN y\n\n"
                "Proved by: TestA,, TestValid\n",
            )
            self.assertIn("src/pkg/GUARANTEES.md:7: unknown line format", errors)
            errors = self._check_grammar(
                root,
                "## Backtick only\n\nIt MUST hold.\n\n- WHEN x THEN y\n\n"
                "Proved by: TestA,\n  `\n",
            )
            self.assertIn("src/pkg/GUARANTEES.md:8: unknown line format", errors)

    def test_go_list_failure_reported_once(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## S\n\nIt MUST hold.\n\n- WHEN x THEN y\n\nProved by: TestA\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1, errors)
            self.assertIn("pkg/GUARANTEES.md:1: go list failed in pkg:", errors[0])
            self.assertIn("go.mod", errors[0])
            self.assertNotIn("cites test", errors[0])

    def test_goflags_mod_entry_dropped(self):
        self.assertEqual(check_guarantees.goflags_without_mod("-mod=mod -v"), "-v")
        self.assertEqual(check_guarantees.goflags_without_mod("-v -mod=readonly"), "-v")
        self.assertEqual(check_guarantees.goflags_without_mod(""), "")

    def test_is_valid_test_param_rejects_non_identifier_package(self):
        self.assertFalse(check_guarantees.is_valid_test_param(["*", "9", ".", "T"]))
        self.assertTrue(check_guarantees.is_valid_test_param(["*", "tst", ".", "T"]))
        self.assertTrue(check_guarantees.is_valid_test_param(["*", "T"]))
        self.assertTrue(check_guarantees.is_valid_test_param(["t", "*", "T"]))
        self.assertTrue(
            check_guarantees.is_valid_test_param(["*", "testing", ".", "T", ","])
        )
        self.assertFalse(check_guarantees.is_valid_test_param([]))

    def test_mask_interpreted_string_newline_ends_string(self):
        masked = check_guarantees.mask_comments_and_strings(
            'var _ = "unterminated\nfunc TestVisible(t *testing.T) {}\n'
        )
        self.assertEqual(
            check_guarantees.scan_test_functions(masked), {"TestVisible"}
        )

    def test_mask_rune_newline_ends_rune(self):
        masked = check_guarantees.mask_comments_and_strings(
            "var _ = 'x\nfunc TestVisible(t *testing.T) {}\n"
        )
        self.assertEqual(
            check_guarantees.scan_test_functions(masked), {"TestVisible"}
        )

    def test_mask_backslash_at_eof_is_masked(self):
        masked = check_guarantees.mask_comments_and_strings('var _ = "abc\\')
        self.assertEqual(masked, "var _ = " + " " * 5)
        masked = check_guarantees.mask_comments_and_strings("var _ = 'a\\")
        self.assertEqual(masked, "var _ = " + " " * 3)

    def test_mask_preserves_newlines(self):
        content = (
            "// line comment\n"
            "/* block\ncomment */\n"
            "var _ = `raw\nstring`\n"
            'var _ = "interp"\n'
            "var _ = 'r'\n"
        )
        masked = check_guarantees.mask_comments_and_strings(content)
        self.assertEqual(content.count("\n"), masked.count("\n"))

    def test_scan_ends_interpreted_string_at_closing_quote(self):
        masked = check_guarantees.mask_comments_and_strings(
            'var _ = "x"; func TestAfter(t *testing.T) {}\n'
        )
        self.assertEqual(check_guarantees.scan_test_functions(masked), {"TestAfter"})

    def test_scan_ends_rune_at_closing_quote(self):
        masked = check_guarantees.mask_comments_and_strings(
            "var _ = 'a'; func TestAfter(t *testing.T) {}\n"
        )
        self.assertEqual(check_guarantees.scan_test_functions(masked), {"TestAfter"})

    def test_mask_escaped_quote_stays_in_string(self):
        masked = check_guarantees.mask_comments_and_strings(
            'var _ = "a\\"b"; func TestAfter(t *testing.T) {}\n'
        )
        self.assertEqual(check_guarantees.scan_test_functions(masked), {"TestAfter"})

    def test_mask_escaped_rune_quote_stays_in_rune(self):
        masked = check_guarantees.mask_comments_and_strings(
            "var _ = '\\''; func TestAfter(t *testing.T) {}\n"
        )
        self.assertEqual(check_guarantees.scan_test_functions(masked), {"TestAfter"})

    def test_scan_ignores_func_in_line_comment(self):
        masked = check_guarantees.mask_comments_and_strings(
            "// func TestInLineComment(t *testing.T) {}\n"
            "func TestReal(t *testing.T) {}\n"
        )
        self.assertEqual(check_guarantees.scan_test_functions(masked), {"TestReal"})

    def test_scan_handles_truncated_function_tokens(self):
        self.assertEqual(check_guarantees.scan_test_functions("func"), set())
        self.assertEqual(check_guarantees.scan_test_functions("func Test"), set())
        self.assertEqual(
            check_guarantees.scan_test_functions("func TestX(t *testing.T)"), set()
        )

    def test_scan_ignores_nested_func_tokens(self):
        masked = check_guarantees.mask_comments_and_strings(
            "func TestOuter(t *testing.T) {\n"
            "func TestInner(t *testing.T) {}\n"
            "}\n"
        )
        self.assertEqual(
            check_guarantees.scan_test_functions(masked), {"TestOuter"}
        )

    def test_find_test_functions_missing_directory(self):
        tests, error = check_guarantees.find_test_functions(
            Path("/nonexistent-flowseer-dir")
        )
        self.assertEqual(tests, set())
        self.assertEqual(error, "package directory does not exist")

    def test_find_test_functions_reports_oserror(self):
        with tempfile.TemporaryDirectory() as d:
            with mock.patch.object(
                check_guarantees.subprocess, "run", side_effect=OSError("no go")
            ):
                tests, error = check_guarantees.find_test_functions(Path(d))
        self.assertEqual(tests, set())
        self.assertEqual(error, "no go")

    def test_find_test_functions_reports_bad_json(self):
        with tempfile.TemporaryDirectory() as d:
            fake = mock.Mock(returncode=0, stdout="not json", stderr="")
            with mock.patch.object(
                check_guarantees.subprocess, "run", return_value=fake
            ):
                tests, error = check_guarantees.find_test_functions(Path(d))
        self.assertEqual(tests, set())
        self.assertIn("invalid go list JSON", error)

    def test_find_test_functions_skips_unreadable_listed_file(self):
        with tempfile.TemporaryDirectory() as d:
            fake = mock.Mock(
                returncode=0, stdout='{"TestGoFiles": ["gone_test.go"]}', stderr=""
            )
            with mock.patch.object(
                check_guarantees.subprocess, "run", return_value=fake
            ):
                tests, error = check_guarantees.find_test_functions(Path(d))
        self.assertEqual(tests, set())
        self.assertIsNone(error)

    def test_single_backtick_text_line_is_paragraph(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            create_pkg(root)
            errors = self._check_grammar(
                root,
                "## Backtick text\n\n"
                "`Run` MUST return nil.\n\n"
                "- WHEN x THEN y\n\n"
                "Proved by: TestA\n",
            )
            self.assertEqual(errors, [])

    def test_blank_line_ends_when_continuation(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            create_pkg(root)
            errors = self._check_grammar(
                root,
                "## S\n\nIt MUST hold.\n\n- WHEN x THEN y\n\n"
                "  stray continuation\n\nProved by: TestA\n",
            )
            self.assertIn("src/pkg/GUARANTEES.md:7: unknown line format", errors)

    def test_blank_line_ends_proved_by_list(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            create_pkg(root)
            errors = self._check_grammar(
                root,
                "## S\n\nIt MUST hold.\n\n- WHEN x THEN y\n\n"
                "Proved by: TestA,\n\n  TestB\n",
            )
            self.assertIn("src/pkg/GUARANTEES.md:9: unknown line format", errors)
            self.assertFalse(any('cites test "TestB"' in e for e in errors), errors)

    def test_indented_proved_by_inside_when_bullet_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            create_pkg(root)
            errors = self._check_grammar(
                root,
                "## S\n\nIt MUST hold.\n\n- WHEN x THEN y\n  Proved by: TestA\n",
            )
            self.assertIn("src/pkg/GUARANTEES.md:6: unknown line format", errors)
            self.assertIn(
                'src/pkg/GUARANTEES.md:1: "S" has no Proved by: line', errors
            )

    def test_four_space_continuation_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            create_pkg(root)
            errors = self._check_grammar(
                root,
                "## S\n\nIt MUST hold.\n\n- WHEN x THEN y\n"
                "    THEN ok\n\nProved by: TestA\n",
            )
            self.assertIn("src/pkg/GUARANTEES.md:6: unknown line format", errors)

    def test_block_starts_rejected_in_every_context(self):
        block_starts = [
            ("***", (0, 1, 2, 3)),
            ("---", (0, 1, 2, 3)),
            ("___", (0, 1, 2, 3)),
            ("* * *", (0, 1, 2, 3)),
            ("#", (0, 1, 2, 3)),
            ("##", (0, 1, 2, 3)),
            ("### x", (0, 1, 2, 3)),
            ("#### x", (0, 1, 2, 3)),
            ("##### x", (0, 1, 2, 3)),
            ("###### x", (0, 1, 2, 3)),
            ("## x", (1, 2, 3)),
            ("=", (0, 1, 2, 3)),
            ("--", (0, 1, 2, 3)),
            ("===", (0, 1, 2, 3)),
            ("```", (0, 1, 2, 3)),
            ("````", (0, 1, 2, 3)),
            ("~~~", (0, 1, 2, 3)),
            ("```go", (0, 1, 2, 3)),
            ("<script", (0, 1, 2, 3)),
            ("<pre", (0, 1, 2, 3)),
            ("<style", (0, 1, 2, 3)),
            ("<textarea", (0, 1, 2, 3)),
            ("<!--", (0, 1, 2, 3)),
            ("<?", (0, 1, 2, 3)),
            ("<!D", (0, 1, 2, 3)),
            ("<![CDATA[", (0, 1, 2, 3)),
            ("<div", (0, 1, 2, 3)),
            ("</div>", (0, 1, 2, 3)),
            ("<x-y>", (0, 1, 2, 3)),
            ("[x]: /u", (0, 1, 2, 3)),
            ("> quote", (0, 1, 2, 3)),
            ("- item", (0, 1, 2, 3)),
            ("+ item", (0, 1, 2, 3)),
            ("* item", (0, 1, 2, 3)),
            ("1. item", (0, 1, 2, 3)),
            ("1) item", (0, 1, 2, 3)),
            ("| a |", (0, 1, 2, 3)),
        ]
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            create_pkg(root)
            for construct, indents in block_starts:
                for indent in indents:
                    for context in ("preamble", "normative", "when", "proved"):
                        with self.subTest(
                            construct=construct, indent=indent, context=context
                        ):
                            doc, line_no = self._block_start_document(
                                construct, indent, context
                            )
                            errors = self._check_grammar(root, doc)
                            self.assertIn(
                                f"src/pkg/GUARANTEES.md:{line_no}: unknown line format",
                                errors,
                            )

    @staticmethod
    def _block_start_document(
        construct: str, indent: int, context: str
    ) -> tuple[str, int]:
        line = " " * indent + construct
        if context == "preamble":
            return (
                f"# T\n\n{line}\n\n## S\n\nIt MUST hold.\n\n"
                f"- WHEN x THEN y\n\nProved by: TestValid\n",
                3,
            )
        if context == "normative":
            return (
                f"# T\n\n## S\n\n{line}\n\n- WHEN x THEN y\n\n"
                f"Proved by: TestValid\n",
                5,
            )
        if context == "when":
            return (
                f"# T\n\n## S\n\nIt MUST hold.\n\n- WHEN x THEN y\n"
                f"{line}\n\nProved by: TestValid\n",
                8,
            )
        return (
            f"# T\n\n## S\n\nIt MUST hold.\n\n- WHEN x THEN y\n\n"
            f"Proved by: TestA,\n{line}\n",
            10,
        )

    def test_continuation_blocks_do_not_count_hidden_citations(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            create_pkg(root)
            for construct in ("```", "~~~", "<!--", "| a |"):
                with self.subTest(construct=construct):
                    errors = self._check_grammar(
                        root,
                        "## S\n\nIt MUST hold.\n\n- WHEN x THEN y\n\n"
                        f"Proved by: TestA,\n  {construct}\n  TestB\n",
                    )
                    self.assertIn(
                        "src/pkg/GUARANTEES.md:8: unknown line format", errors
                    )
                    self.assertIn(
                        "src/pkg/GUARANTEES.md:9: unknown line format", errors
                    )
                    self.assertFalse(
                        any('cites test "TestB"' in e for e in errors), errors
                    )

    def test_pilot_file_passes(self):
        root = Path(__file__).resolve().parents[4]
        pilot_path = root / "src" / "protocol" / "ssh" / "GUARANTEES.md"
        self.assertTrue(pilot_path.is_file(), f"{pilot_path} does not exist")
        errors = check_guarantees.check_guarantees_file(pilot_path, root)
        self.assertEqual(errors, [])


if __name__ == "__main__":
    unittest.main()
