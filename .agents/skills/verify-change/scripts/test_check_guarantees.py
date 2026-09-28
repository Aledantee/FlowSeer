import importlib.util
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "check_guarantees", Path(__file__).with_name("check-guarantees.py")
)
check_guarantees = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check_guarantees)


class CheckGuaranteesTest(unittest.TestCase):
    def test_valid_file_passes(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "# Package guarantees\n\n"
                "## Valid behavior\n\n"
                "It MUST succeed.\n\n"
                "- WHEN called with valid input THEN it returns nil.\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/foo_test.go"], root)
            self.assertEqual(errors, [])

    def test_missing_proved_by_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
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
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Same Heading\n\n"
                "- WHEN first THEN ok\n\n"
                "Proved by: TestA\n\n"
                "## Same Heading\n\n"
                "- WHEN second THEN ok\n\n"
                "Proved by: TestB\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:7: duplicate guarantee heading "Same Heading"',
            )

    def test_section_without_when_then_bullet_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nfunc TestA(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## No Scenario\n\n"
                "It MUST do something.\n\n"
                "- Just a bullet without when and then.\n\n"
                "Proved by: TestA\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:1: "No Scenario" has no - WHEN ... THEN scenario bullet',
            )

    def test_cited_test_that_does_not_exist_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nfunc TestExisting(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Output is capped\n\n"
                "- WHEN output exceeds limit THEN it truncates.\n\n"
                "Proved by: TestMissing\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/foo_test.go"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:5: "Output is capped" cites test "TestMissing" which does not exist in src/pkg',
            )

    def test_test_in_subdirectory_or_testdata_does_not_resolve(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
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
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nfunc TestFoo(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Foo\n\n"
                "- WHEN foo THEN bar\n\n"
                "Proved by: TestFoo\n",
                encoding="utf-8",
            )
            # "src/pkg/deleted_test.go" does not exist on disk
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
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Wrapped proved by\n\n"
                "- WHEN check THEN ok\n\n"
                "Proved by: TestA,\n"
                "  TestGone\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:6: "Wrapped proved by" cites test "TestGone" which does not exist in src/pkg',
            )

    def test_testmain_testhelper_and_block_comment_do_not_resolve(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
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
            tests = check_guarantees.find_test_functions(pkg)
            self.assertEqual(tests, {"TestValid"})
            (pkg / "GUARANTEES.md").write_text(
                "## TestMain cited\n\n"
                "- WHEN check THEN fail\n\n"
                "Proved by: TestMain\n\n"
                "## Testhelper cited\n\n"
                "- WHEN check THEN fail\n\n"
                "Proved by: Testhelper\n\n"
                "## Commented cited\n\n"
                "- WHEN check THEN fail\n\n"
                "Proved by: TestCommented\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 3)
            self.assertIn('cites test "TestMain" which does not exist in src/pkg', errors[0])
            self.assertIn('cites test "Testhelper" which does not exist in src/pkg', errors[1])
            self.assertIn('cites test "TestCommented" which does not exist in src/pkg', errors[2])

    def test_tilde_code_fences_recognized(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Valid\n\n"
                "- WHEN valid THEN ok\n\n"
                "~~~\n"
                "```markdown\n"
                "## Ignored inside fence\n"
                "Proved by: TestMissing\n"
                "```\n"
                "~~~\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(errors, [])

    def test_unclosed_code_fence_reports_error(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Valid\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n\n"
                "```\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertIn("src/pkg/GUARANTEES.md:7: unclosed code fence", errors)

    def test_main_exit_codes(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Valid\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )

            # Clean run exits 0
            code = check_guarantees.main(["--root", str(root), "src/pkg/GUARANTEES.md"])
            self.assertEqual(code, 0)

            # --all path selects nested file and exits 0
            code_all = check_guarantees.main(["--root", str(root), "--all"])
            self.assertEqual(code_all, 0)

            # Violation exits 1
            (pkg / "GUARANTEES.md").write_text(
                "## Invalid\n\n"
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

                # --all with violation exits 1
                code_all_violation = check_guarantees.main(
                    ["--root", str(root), "--all"]
                )
                self.assertEqual(code_all_violation, 1)

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
                "func TestAfterRawString(t *testing.T) {}\n",
                encoding="utf-8",
            )

            env = dict(os.environ)
            env["GOFLAGS"] = "-mod=mod"
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
            py_tests = check_guarantees.find_test_functions(pkg)

            expected = {
                "TestAfterLineComment",
                "TestAfterBlockComment",
                "TestAfterInterpretedString",
                "TestAfterRuneLiterals",
                "TestAfterRawString",
            }
            self.assertEqual(go_tests, expected)
            self.assertEqual(py_tests, go_tests)
            self.assertEqual(py_tests, expected)

    def test_non_compiled_signature_exclusions(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\n"
                "import \"testing\"\n\n"
                "func TestBench(b *testing.B) {}\n"
                "func TestNoArg() {}\n"
                "func TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            tests = check_guarantees.find_test_functions(pkg)
            self.assertEqual(tests, {"TestValid"})

    def test_non_compiled_block_comment_spanning_lines(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\n"
                "import \"testing\"\n\n"
                "var x = 1 /* a */ /*\n"
                "func TestHidden(t *testing.T) {}\n"
                "*/\n"
                "func TestVisible(t *testing.T) {}\n",
                encoding="utf-8",
            )
            tests = check_guarantees.find_test_functions(pkg)
            self.assertEqual(tests, {"TestVisible"})

    def test_non_compiled_string_literal_with_comment_marker(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\n"
                "import \"testing\"\n\n"
                "var pattern = \"testdata/*.json\"\n"
                "func TestEmit(t *testing.T) {}\n",
                encoding="utf-8",
            )
            tests = check_guarantees.find_test_functions(pkg)
            self.assertEqual(tests, {"TestEmit"})

    def test_non_compiled_line_comment_with_block_marker(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\n"
                "import \"testing\"\n\n"
                "// a /* b\n"
                "func TestAfterComment(t *testing.T) {}\n",
                encoding="utf-8",
            )
            tests = check_guarantees.find_test_functions(pkg)
            self.assertEqual(tests, {"TestAfterComment"})

    def test_tilde_code_fence_without_inner_backtick_fence(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Valid\n\n"
                "- WHEN valid THEN ok\n\n"
                "~~~\n"
                "## Hidden\n"
                "Proved by: TestMissing\n"
                "~~~\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(errors, [])

    def test_trailing_comma_in_proved_by_list_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\nfunc TestGone(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Capped\n\n"
                "- WHEN limit reached THEN truncate\n\n"
                "Proved by: TestA,\n\n"
                "TestGone\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:5: "Capped" Proved by: list ends with a comma',
            )

    def test_wrapped_proved_by_ending_with_comma_fails(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "## Capped\n\n"
                "- WHEN limit reached THEN truncate\n\n"
                "Proved by: TestA,\n"
                "  TestB,\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(len(errors), 1)
            self.assertEqual(
                errors[0],
                'src/pkg/GUARANTEES.md:6: "Capped" Proved by: list ends with a comma',
            )

    def test_indented_heading_and_trailing_hashes(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "   ## Indented Heading ###\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(errors, [])

    def test_setext_h2_heading_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "Invalid Setext Heading\n"
                "----------------------\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertIn(
                "src/pkg/GUARANTEES.md:2: setext heading not allowed; use ##", errors
            )

    def test_backtick_fence_with_backtick_in_info_not_fence(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            pkg = root / "src" / "pkg"
            pkg.mkdir(parents=True)
            (pkg / "foo_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n",
                encoding="utf-8",
            )
            (pkg / "GUARANTEES.md").write_text(
                "``` `inline` ```\n\n"
                "## Real Heading\n\n"
                "- WHEN valid THEN ok\n\n"
                "Proved by: TestValid\n",
                encoding="utf-8",
            )
            errors = check_guarantees.check_guarantees(["src/pkg/GUARANTEES.md"], root)
            self.assertEqual(errors, [])

if __name__ == "__main__":
    unittest.main()
