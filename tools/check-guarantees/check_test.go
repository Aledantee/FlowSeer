package main

import (
	"bytes"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func createTestPkg(t *testing.T, root string) string {
	t.Helper()
	pkgDir := filepath.Join(root, "src/pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	goMod := filepath.Join(root, "go.mod")
	if err := os.WriteFile(goMod, []byte("module testmod\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	pkgGo := filepath.Join(pkgDir, "pkg.go")
	if err := os.WriteFile(pkgGo, []byte("package pkg\n"), 0o644); err != nil {
		t.Fatalf("write pkg.go failed: %v", err)
	}
	return pkgDir
}

func TestValidFilePasses(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "# Package guarantees\n\nPreamble explaining the package guarantees.\n\n## Valid behavior\n\nIt MUST succeed.\n\n- WHEN called with valid input THEN it returns nil.\n\nProved by: TestValid\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got: %v", errs)
	}
}

func TestNormativeSentenceMustNot(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "# Package guarantees\n\n## Valid behavior\n\nIt MUST NOT fail on valid input.\n\n- WHEN called with valid input THEN it returns nil.\n\nProved by: TestValid\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got: %v", errs)
	}
}

func TestContinuationLinesAccepted(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "# Package guarantees\n\n## Multiline block\n\nIt MUST succeed.\n\n- WHEN input is provided across multiple lines\n  THEN the result is returned cleanly.\n\nProved by: TestA,\n  TestB\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got: %v", errs)
	}
}

func TestMissingProvedByFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\n\nfunc TestValid(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "# Package guarantees\n\n## Output is capped\n\nIt MUST be capped.\n\n- WHEN output exceeds limit THEN it truncates.\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	want := `src/pkg/GUARANTEES.md:3: "Output is capped" has no Proved by: line`
	if errs[0] != want {
		t.Errorf("got %q, want %q", errs[0], want)
	}
}

func TestDuplicateHeadingFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\n\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Same Heading\n\nIt MUST succeed.\n\n- WHEN first THEN ok\n\nProved by: TestA\n\n## Same Heading\n\nIt MUST succeed.\n\n- WHEN second THEN ok\n\nProved by: TestB\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	want := `src/pkg/GUARANTEES.md:9: duplicate guarantee heading "Same Heading"`
	if errs[0] != want {
		t.Errorf("got %q, want %q", errs[0], want)
	}
}

func TestSectionWithoutWhenThenBulletFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## No Scenario\n\nIt MUST do something.\n\nProved by: TestA\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	want := `src/pkg/GUARANTEES.md:1: "No Scenario" has no - WHEN ... THEN scenario bullet`
	if errs[0] != want {
		t.Errorf("got %q, want %q", errs[0], want)
	}
}

func TestWhenBulletWithoutThenFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Incomplete Scenario\n\nIt MUST do something.\n\n- WHEN input is given without consequence\n  and still no result.\n\nProved by: TestA\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	want := `src/pkg/GUARANTEES.md:1: "Incomplete Scenario" has no - WHEN ... THEN scenario bullet`
	if errs[0] != want {
		t.Errorf("got %q, want %q", errs[0], want)
	}
}

func TestCitedTestDoesNotExistFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Output is capped\n\nIt MUST be capped.\n\n- WHEN output exceeds limit THEN it truncates.\n\nProved by: TestNonExistent\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	want := `src/pkg/GUARANTEES.md:7: "Output is capped" cites test "TestNonExistent" which does not exist in src/pkg`
	if errs[0] != want {
		t.Errorf("got %q, want %q", errs[0], want)
	}
}

func TestSubdirectoryOrTestdataTestDoesNotResolve(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	subDir := filepath.Join(pkgDir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "sub_test.go"), []byte("package sub\n\nimport \"testing\"\n\nfunc TestSub(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tdDir := filepath.Join(pkgDir, "testdata")
	if err := os.MkdirAll(tdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tdDir, "td_test.go"), []byte("package testdata\n\nimport \"testing\"\n\nfunc TestData(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Sub test cited\n\nIt MUST fail.\n\n- WHEN check THEN fail\n\nProved by: TestSub\n\n## Testdata cited\n\nIt MUST fail.\n\n- WHEN check THEN fail\n\nProved by: TestData\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0], `cites test "TestSub" which does not exist`) {
		t.Errorf("expected TestSub error, got %q", errs[0])
	}
	if !strings.Contains(errs[1], `cites test "TestData" which does not exist`) {
		t.Errorf("expected TestData error, got %q", errs[1])
	}
}

func TestChangedPathNoLongerExistsSelectsGuarantees(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Valid\n\nIt MUST hold.\n\n- WHEN x THEN y\n\nProved by: TestA\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	selected, err := selectGuaranteeFiles([]string{"src/pkg/deleted.go"}, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != gFile {
		t.Fatalf("expected %q, got %v", gFile, selected)
	}
}

func TestChangedPathWhoseDirectoryHoldsNoneSelectsNothing(t *testing.T) {
	root := t.TempDir()
	createTestPkg(t, root)
	selected, err := selectGuaranteeFiles([]string{"docs/README.md"}, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 0 {
		t.Fatalf("expected empty, got %v", selected)
	}
}

func TestLowercaseGuaranteesFileIgnored(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "guarantees.md"), []byte("## Foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	selected, err := selectGuaranteeFiles([]string{"src/pkg"}, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 0 {
		t.Fatalf("expected lowercase guarantees.md to be ignored, got: %v", selected)
	}
}

func TestWrappedProvedByListWithMissingTestFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Wrapped proved by\n\nIt MUST succeed.\n\n- WHEN check THEN ok\n\nProved by: TestA,\n  TestGone\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	want := `src/pkg/GUARANTEES.md:8: "Wrapped proved by" cites test "TestGone" which does not exist in src/pkg`
	if errs[0] != want {
		t.Errorf("got %q, want %q", errs[0], want)
	}
}

func TestTestMainTestHelperAndBlockCommentDoNotResolve(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	code := "package pkg\n\nimport \"testing\"\n\nfunc TestMain(m *testing.M) {}\nfunc Testhelper(t *testing.T) {}\n/*\nfunc TestCommented(t *testing.T) {}\n*/\n/* func TestInlineCommented(t *testing.T) {} */\nfunc TestValid(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}

	tests, err := findTestFunctions(pkgDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tests) != 1 || !tests["TestValid"] {
		t.Fatalf("expected {TestValid}, got %v", tests)
	}
}

func TestHeadingWithUnspacedHash(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) {}\nfunc TestCSharp(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Parses C#\n\nIt MUST succeed.\n\n- WHEN C# input is parsed THEN it succeeds.\n\nProved by: TestCSharp\n\n## Parses C\n\nIt MUST succeed.\n\n- WHEN C input is parsed THEN it succeeds.\n\nProved by: TestC\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got: %v", errs)
	}
}

func TestHeadingTrailingHashesPrecededByWhitespaceStripped(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading ##\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\nProved by: TestA\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got: %v", errs)
	}
}

func TestEmptyHeadingTitleFails(t *testing.T) {
	cases := []string{"## \n", "## #\n", "## ##\n"}
	for _, h := range cases {
		root := t.TempDir()
		pkgDir := createTestPkg(t, root)
		if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		content := h + "\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\nProved by: TestValid\n"
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		if len(errs) == 0 {
			t.Errorf("for heading %q, expected error", h)
		}
	}
}

func TestSetextUnderlineRejected(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "Invalid Heading\n---\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\nProved by: TestValid\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) == 0 {
		t.Fatal("expected error for setext heading")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "unknown block kind: heading") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown block kind: heading, got: %v", errs)
	}
}

func TestNumberedListFollowedBySetextRejected(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n1. item\n---\n\n- WHEN valid THEN ok\n\nProved by: TestValid\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) < 2 {
		t.Fatalf("expected at least 2 errors for ordered list and setext/thematic break, got: %v", errs)
	}
}

func TestCodeFencesFail(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n```go\nfunc decoy() {}\n```\n\n- WHEN valid THEN ok\n\nProved by: TestValid\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "unknown block kind: fenced code block") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown block kind: fenced code block, got: %v", errs)
	}
}

func TestIndentedCodeBlockFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n    code block\n\n- WHEN valid THEN ok\n\nProved by: TestValid\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "unknown block kind: code block") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown block kind: code block, got: %v", errs)
	}
}

func TestIndentedProvedByFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\n    Proved by: TestValid\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "has no Proved by: line") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected has no Proved by: line, got: %v", errs)
	}
}

func TestTwoSpaceIndentedProvedByFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\n  Proved by: TestValid\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, `has no Proved by: line`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected has no Proved by: line, got: %v", errs)
	}
}

func TestUnallowedHeadingLevelsFail(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n### Subheading\n\n- WHEN valid THEN ok\n\nProved by: TestValid\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "unknown block kind: heading") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown block kind: heading, got: %v", errs)
	}
}

func TestUnindentedNonNormativeLineInSectionFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\nExtra non-normative paragraph.\n\n- WHEN valid THEN ok\n\nProved by: TestValid\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "unknown block kind: paragraph") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown block kind: paragraph, got: %v", errs)
	}
}

func TestInvalidPreambleLineFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "# Title\n\n- WHEN invalid THEN fail\n\n## Heading\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\nProved by: TestValid\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "unknown block kind: list") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown block kind: list in preamble, got: %v", errs)
	}
}

func TestIndentedLineWithoutActiveContinuationFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n    stray indented line\n\n- WHEN valid THEN ok\n\nProved by: TestValid\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "unknown block kind: code block") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown block kind: code block, got: %v", errs)
	}
}

func TestTrailingCommaInProvedByListFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\nProved by: TestA,\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, `Proved by: list ends with a comma`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected ends with a comma error, got: %v", errs)
	}
}

func TestWrappedProvedByEndingWithCommaFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Capped\n\nIt MUST be capped.\n\n- WHEN limit reached THEN truncate\n\nProved by: TestA,\n  TestB,\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	want := `src/pkg/GUARANTEES.md:8: "Capped" Proved by: list ends with a comma`
	found := false
	for _, e := range errs {
		if e == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %q, got: %v", want, errs)
	}
}

func TestDuplicateProvedByFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\nProved by: TestA\n\nProved by: TestB\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, `has duplicate Proved by: line`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected duplicate Proved by: line, got: %v", errs)
	}
}

func TestProvedByNamesNoTestsFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\nProved by: \n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, `Proved by: line names no tests`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Proved by: line names no tests, got: %v", errs)
	}
}

func TestNoHeadingsFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "# Only title\n\nSome preamble.\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	want := `src/pkg/GUARANTEES.md:1: no guarantee sections (## headings) found`
	found := false
	for _, e := range errs {
		if e == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %q, got: %v", want, errs)
	}
}

func TestIgnoredBuildTagAndUnderscoreFileExcluded(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "_foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestIgnoredUnderscore(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "ignored_test.go"), []byte("//go:build ignore\n\npackage pkg\n\nimport \"testing\"\n\nfunc TestIgnoredBuildTag(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "real_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestReal(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests, err := findTestFunctions(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	if !tests["TestReal"] {
		t.Errorf("expected TestReal to resolve")
	}
	if tests["TestIgnoredUnderscore"] {
		t.Errorf("expected TestIgnoredUnderscore to be excluded")
	}
	if tests["TestIgnoredBuildTag"] {
		t.Errorf("expected TestIgnoredBuildTag to be excluded")
	}
}

func TestUnicodeLowercaseTestNameRejected(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc Testé(t *testing.T) {}\nfunc TestValid(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests, err := findTestFunctions(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	if tests["Testé"] {
		t.Errorf("expected Testé to be rejected")
	}
	if !tests["TestValid"] {
		t.Errorf("expected TestValid to resolve")
	}
}

func TestSupportedTestSignaturesResolve(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	code := `package pkg

import tst "testing"

func TestAnon(*tst.T) {}
func TestMultiline(
	t *tst.T,
) {}
func TestEmptyReturn(t *tst.T) () {}
func TestAliased(t *tst.T) {}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}

	tests, err := findTestFunctions(pkgDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"TestAnon", "TestMultiline", "TestEmptyReturn", "TestAliased"} {
		if !tests[want] {
			t.Errorf("expected %s to resolve", want)
		}
	}
}

func TestInvalidSignaturesFailResolution(t *testing.T) {
	cases := []struct {
		name string
		code string
		bad  string
	}{
		{"bench", "package pkg\nimport \"testing\"\nfunc TestBench(b *testing.B) {}\n", "TestBench"},
		{"no_args", "package pkg\nfunc TestNoArgs() {}\n", "TestNoArgs"},
		{"multi_args", "package pkg\nimport \"testing\"\nfunc TestMultiArgs(t *testing.T, x int) {}\n", "TestMultiArgs"},
		{"two_params", "package pkg\nimport \"testing\"\nfunc TestTwoParams(t, u *testing.T) {}\n", "TestTwoParams"},
		{"with_return", "package pkg\nimport \"testing\"\nfunc TestWithReturn(t *testing.T) bool { return true }\n", "TestWithReturn"},
		{"anon_paren", "package pkg\nimport \"testing\"\nfunc TestAnonParen((*testing.T)) {}\n", "TestAnonParen"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			pkgDir := createTestPkg(t, root)
			if err := os.WriteFile(filepath.Join(pkgDir, "bad_test.go"), []byte(tc.code), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := findTestFunctions(pkgDir)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.bad+" has invalid test signature") {
				t.Errorf("expected %s has invalid test signature, got: %v", tc.bad, err)
			}
		})
	}
}

func TestGenericTestSignatureFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	code := "package pkg\nimport \"testing\"\nfunc TestGeneric[T any](t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "bad_test.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := findTestFunctions(pkgDir)
	if err == nil {
		t.Fatal("expected error for generic test")
	}
	if !strings.Contains(err.Error(), "TestGeneric has invalid test signature") {
		t.Errorf("got %v", err)
	}
}

func TestLexerOracleAgainstGoTestList(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	code := `package pkg

import "testing"

// Decoy line comment: /* */ " ` + "`" + ` func TestFakeInLineComment(t *testing.T) {}
func TestAfterLineComment(t *testing.T) {}

/*
Decoy block comment: // " ` + "`" + `
func TestFakeInBlockComment(t *testing.T) {}
*/
func TestAfterBlockComment(t *testing.T) {}

var _ = "decoy interpreted string: /* */ // ` + "`" + ` \" \nfunc TestFakeInInterpretedString(t *testing.T) {}"
func TestAfterInterpretedString(t *testing.T) {}

var _ = '\''
var _ = '"'
var _ = '/'
var _ = '\n'
func TestAfterRuneLiterals(t *testing.T) {}

var _ = ` + "`" + `
decoy raw string: /* */ // " 
func TestFakeInRawString(t *testing.T) {}
` + "`" + `
func TestAfterRawString(t *testing.T) {}

func Test(t *testing.T) {}
func TestAnon(*testing.T) {}
func TestEmptyResults(t *testing.T) () {}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "oracle_test.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}

	tests, err := findTestFunctions(pkgDir)
	if err != nil {
		t.Fatal(err)
	}

	// Compare with go test -list
	cmd := exec.Command("go", "test", "-list=^Test", ".")
	cmd.Dir = pkgDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go test -list failed: %v", err)
	}

	var goTestNames []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Test") {
			goTestNames = append(goTestNames, line)
		}
	}

	for _, name := range goTestNames {
		if !tests[name] {
			t.Errorf("go test listed %s but findTestFunctions did not find it", name)
		}
	}
	for name := range tests {
		found := false
		for _, gName := range goTestNames {
			if gName == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("findTestFunctions found %s but go test did not list it", name)
		}
	}
}

func TestMainExitCodes(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "# Package guarantees\n\n## Valid\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\nProved by: TestValid\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"--root", root, "src/pkg/GUARANTEES.md"}, &stdout, &stderr); err != nil {
		t.Fatalf("expected exit 0, got error: %v", err)
	}

	if err := run([]string{"--root", root, "--all"}, &stdout, &stderr); err != nil {
		t.Fatalf("expected exit 0 for --all, got: %v", err)
	}

	// Violation exit
	contentBad := "## Invalid\n\nIt MUST fail.\n\n- WHEN invalid THEN fail\n\nProved by: TestMissing\n"
	if err := os.WriteFile(gFile, []byte(contentBad), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	err := run([]string{"--root", root, "src/pkg/GUARANTEES.md"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected violation error")
	}

	// Flag error exit
	errFlag := run([]string{"--nonexistent-flag"}, &stdout, &stderr)
	if errFlag == nil {
		t.Fatal("expected flag error")
	}
}

func TestSectionNeedsExactlyOneNormativeSentence(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Two normative sentences
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Two Normative\n\nIt MUST succeed.\n\nIt MUST also hold.\n\n- WHEN check THEN ok\n\nProved by: TestA\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "has more than one normative sentence") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected more than one normative sentence, got: %v", errs)
	}
}

func TestDocumentTitleIsOptionalAndSingle(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Two H1 titles
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "# Title 1\n\n# Title 2\n\n## Section\n\nIt MUST hold.\n\n- WHEN x THEN y\n\nProved by: TestA\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "unknown block kind: heading") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown block kind: heading for second H1, got: %v", errs)
	}
}

func TestCitationItemsAreGoIdentifiers(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\nfunc TestValid(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Backticked citations pass
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Backticked\n\nIt MUST hold.\n\n- WHEN x THEN y\n\nProved by: `TestA`, `TestValid`\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 0 {
		t.Fatalf("expected backticked citations to pass, got: %v", errs)
	}

	// Empty citation item fails
	contentEmpty := "## Empty item\n\nIt MUST hold.\n\n- WHEN x THEN y\n\nProved by: TestA,, TestValid\n"
	if err := os.WriteFile(gFile, []byte(contentEmpty), 0o644); err != nil {
		t.Fatal(err)
	}
	errsEmpty := checkGuaranteesFile(gFile, root)
	if len(errsEmpty) == 0 {
		t.Fatal("expected error for empty citation item")
	}
}

func TestGoListFailureReportedOnce(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "src/pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No go.mod in root, so go list will fail
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Section\n\nIt MUST hold.\n\n- WHEN x THEN y\n\nProved by: TestA\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "go list failed in src/pkg:") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected go list failed in src/pkg, got: %v", errs)
	}
}

func TestGoListUsesReadonlyModWithoutTouchingGoflags(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests, err := findTestFunctions(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	if !tests["TestA"] {
		t.Errorf("expected TestA to resolve")
	}
}

func TestIsValidTestParam(t *testing.T) {
	toks := func(s string) []tokenItem {
		fset := token.NewFileSet()
		file := fset.AddFile("t.go", fset.Base(), len(s))
		var sc scanner.Scanner
		sc.Init(file, []byte(s), nil, 0)
		var out []tokenItem
		for {
			pos, tok, lit := sc.Scan()
			if tok == token.EOF {
				break
			}
			out = append(out, tokenItem{pos: pos, tok: tok, lit: lit})
		}
		return out
	}

	if !isValidTestParamTokens(toks("t *testing.T")) {
		t.Error("t *testing.T should be valid")
	}
	if !isValidTestParamTokens(toks("*testing.T")) {
		t.Error("*testing.T should be valid")
	}
	if !isValidTestParamTokens(toks("t *pkg.T")) {
		t.Error("t *pkg.T should be valid")
	}
	if !isValidTestParamTokens(toks("*T")) {
		t.Error("*T should be valid")
	}
	if isValidTestParamTokens(toks("t int")) {
		t.Error("t int should not be valid")
	}
	if isValidTestParamTokens(toks("t *testing.B")) {
		t.Error("t *testing.B should not be valid")
	}
}

func TestScannerStringAndCommentMasking(t *testing.T) {
	code := `package pkg
import "testing"
func TestValid(t *testing.T) {
	s := "func TestFake1(t *testing.T) {}"
	r := 'a'
	raw := ` + "`" + `func TestFake2(t *testing.T) {}` + "`" + `
	_ = s
	_ = r
	_ = raw
}
// func TestFake3(t *testing.T) {}
/* func TestFake4(t *testing.T) {} */
`
	funcs, err := scanTestFile([]byte(code), "foo_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(funcs) != 1 || !funcs["TestValid"] {
		t.Fatalf("expected only TestValid, got: %v", funcs)
	}
}

func TestFindTestFunctionsErrors(t *testing.T) {
	// Missing directory
	_, err := findTestFunctions("/nonexistent/directory/path")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestSingleBacktickTextLineIsParagraph(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\n`a<b` MUST hold.\n\n- WHEN x THEN y\n\nProved by: TestA\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got: %v", errs)
	}
}

func TestBlankLineSeparatesBlocks(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST hold.\n\n- WHEN x THEN y\n\nProved by: TestA\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got: %v", errs)
	}
}

// Open review regression case 1: Escaped backtick followed by <!-- MUST --> must not count as MUST
func TestRegressionEscapedBacktickWithHTMLComment(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Escaped Backtick\n\n\\` <!-- MUST -->\n\n- WHEN x THEN y\n\nProved by: TestA\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, `has no normative MUST sentence`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected has no normative MUST sentence, got: %v", errs)
	}
}

// Open review regression case 2: Multiline code span on WHEN line must not hide THEN
func TestRegressionMultilineCodeSpanDoesNotHideTHEN(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Multiline Code Span\n\nIt MUST succeed.\n\n- WHEN `code\nmore` THEN it passes.\n\nProved by: TestA\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got: %v", errs)
	}
}

// Open review regression case 3: ## A &amp; B and ## A & B are duplicate headings
func TestRegressionHTMLEntityDuplicateHeadings(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## A &amp; B\n\nIt MUST succeed.\n\n- WHEN first THEN ok\n\nProved by: TestA\n\n## A & B\n\nIt MUST succeed.\n\n- WHEN second THEN ok\n\nProved by: TestB\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, `duplicate guarantee heading "A & B"`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected duplicate guarantee heading \"A & B\", got: %v", errs)
	}
}

// Requirement 14: Package containing TestGood(t *testing.T) and TestBad(x int) fails resolution
func TestBadlySignedTestFailsPackageResolution(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	code := `package pkg

import "testing"

func TestGood(t *testing.T) {}
func TestBad(x int) {}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "bad_test.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\nProved by: TestGood\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "bad_test.go:6: TestBad has invalid test signature") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected TestBad has invalid test signature, got: %v", errs)
	}
}

func TestPilotFilePasses(t *testing.T) {
	// Look up repo root
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := cwd
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("could not find repo root with go.mod")
		}
		root = parent
	}

	pilotPath := filepath.Join(root, "src", "protocol", "ssh", "GUARANTEES.md")
	if _, err := os.Stat(pilotPath); err != nil {
		t.Fatalf("pilot file %s does not exist", pilotPath)
	}

	errs := checkGuaranteesFile(pilotPath, root)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors for pilot file, got: %v", errs)
	}
}
