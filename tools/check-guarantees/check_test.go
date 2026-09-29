package main

import (
	"bytes"
	"fmt"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"

	"go.aledante.io/FlowSeer/src/common/errs"
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

func TestExternalPackageTestCitation(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "external_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg_test\n\nimport \"testing\"\n\nfunc TestExternal(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## External behavior\n\nIt MUST succeed.\n\n- WHEN called THEN it succeeds.\n\nProved by: TestExternal\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := checkGuaranteesFile(gFile, root); len(got) != 0 {
		t.Fatalf("external package test citation rejected: %v", got)
	}
}

func TestMissingNormativeStillChecksScenarioAndCitation(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt must hold.\n\n- WHEN called THEN it succeeds.\n\nProved by: TestGone\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`src/pkg/GUARANTEES.md:3: unknown block kind: paragraph`,
		`src/pkg/GUARANTEES.md:1: "Heading" has no normative MUST sentence`,
		`src/pkg/GUARANTEES.md:7: "Heading" cites test "TestGone" which does not exist in src/pkg`,
	}
	if got := checkGuaranteesFile(gFile, root); !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestInvalidCitationNamesTokenWithoutMissingLine(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	for _, token := range []string{"TestA.", "TestA and TestB", "TestFoo/sub"} {
		t.Run(token, func(t *testing.T) {
			content := "## Heading\n\nIt MUST hold.\n\n- WHEN called THEN it succeeds.\n\nProved by: " + token + "\n"
			if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("src/pkg/GUARANTEES.md:7: %q Proved by: %q is not a Go test identifier", "Heading", token)
			got := checkGuaranteesFile(gFile, root)
			if !slices.Contains(got, want) {
				t.Fatalf("want %q in %v", want, got)
			}
			for _, diagnostic := range got {
				if strings.Contains(diagnostic, "has no Proved by: line") {
					t.Fatalf("present citation reported missing: %v", diagnostic)
				}
			}
		})
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
func TestMain(t *testing.T) {}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "oracle_test.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}

	tests, err := findTestFunctions(pkgDir)
	if err != nil {
		t.Fatal(err)
	}

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

	contentBad := "## Invalid\n\nIt MUST fail.\n\n- WHEN invalid THEN fail\n\nProved by: TestMissing\n"
	if err := os.WriteFile(gFile, []byte(contentBad), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	err := run([]string{"--root", root, "src/pkg/GUARANTEES.md"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected violation error")
	}
	if got := errs.ExitCode(err); got != 1 {
		t.Fatalf("expected exit code 1 for violations, got %d", got)
	}

	errFlag := run([]string{"--nonexistent-flag"}, &stdout, &stderr)
	if errFlag == nil {
		t.Fatal("expected flag error")
	}
	if got := errs.ExitCode(errFlag); got != 2 {
		t.Fatalf("expected exit code 2 for flag error, got %d", got)
	}

	deletedPath := filepath.Join(pkgDir, "deleted.go")
	stderr.Reset()
	errDeleted := run([]string{"--root", root, "--", deletedPath}, &stdout, &stderr)
	if errDeleted == nil {
		t.Fatal("expected failure when deleted path selects invalid GUARANTEES.md")
	}
	if got := errs.ExitCode(errDeleted); got != 1 {
		t.Fatalf("expected exit code 1 when deleted path selects invalid GUARANTEES.md, got %d", got)
	}

	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if err := run([]string{"--root", root, "--", deletedPath}, &stdout, &stderr); err != nil {
		t.Fatalf("expected exit 0 for run with deleted absolute path and leading --, got: %v", err)
	}
}

func TestSectionNeedsExactlyOneNormativeSentence(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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

	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Backticked\n\nIt MUST hold.\n\n- WHEN x THEN y\n\nProved by: `TestA`, `TestValid`\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 0 {
		t.Fatalf("expected backticked citations to pass, got: %v", errs)
	}

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

func TestEscapedBacktickWithHTMLComment(t *testing.T) {
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

func TestMultilineCodeSpanDoesNotHideTHEN(t *testing.T) {
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

func TestHTMLEntityDuplicateHeadings(t *testing.T) {
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

// A package containing an invalid Test signature fails resolution.
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

func TestListItemNestedBlocksFail(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "nested_paragraph_and_proved_by",
			content: "## Heading\n\nIt MUST succeed.\n\n- WHEN a THEN b\n\n  Proved by: TestGone\n\n  It MUST NOT drop.\n\nProved by: TestA\n",
			wantErr: "unknown block kind: paragraph",
		},
		{
			name:    "nested_fenced_code",
			content: "## Heading\n\nIt MUST succeed.\n\n- WHEN a THEN b\n\n  ```go\n  var x = 1\n  ```\n\nProved by: TestA\n",
			wantErr: "unknown block kind: fenced code block",
		},
		{
			name:    "nested_indented_code",
			content: "## Heading\n\nIt MUST succeed.\n\n- WHEN a THEN b\n\n      var x = 1\n\nProved by: TestA\n",
			wantErr: "unknown block kind: code block",
		},
		{
			name:    "nested_blockquote",
			content: "## Heading\n\nIt MUST succeed.\n\n- WHEN a THEN b\n\n  > blockquote\n\nProved by: TestA\n",
			wantErr: "unknown block kind: blockquote",
		},
		{
			name:    "nested_heading",
			content: "## Heading\n\nIt MUST succeed.\n\n- WHEN a THEN b\n\n  ### Subheading\n\nProved by: TestA\n",
			wantErr: "unknown block kind: heading",
		},
		{
			name:    "nested_html_block",
			content: "## Heading\n\nIt MUST succeed.\n\n- WHEN a THEN b\n\n  <div>decoy</div>\n\nProved by: TestA\n",
			wantErr: "unknown block kind: html block",
		},
		{
			name:    "nested_list",
			content: "## Heading\n\nIt MUST succeed.\n\n- WHEN a THEN b\n  - nested bullet\n\nProved by: TestA\n",
			wantErr: "unknown block kind: list",
		},
		{
			name:    "lazy_continuation_proved_by",
			content: "## Heading\n\nIt MUST succeed.\n\n- WHEN a THEN b\nProved by: TestGone\n\nProved by: TestA\n",
			wantErr: "unknown block kind: list item",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			pkgDir := createTestPkg(t, root)
			if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gFile := filepath.Join(pkgDir, "GUARANTEES.md")
			if err := os.WriteFile(gFile, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			errs := checkGuaranteesFile(gFile, root)
			found := false
			for _, e := range errs {
				if strings.Contains(e, tc.wantErr) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, errs)
			}
		})
	}
}

func TestFencedCodeBlockNoInfoDescendantNoPanic(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestValid(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("blockquote_descendant", func(t *testing.T) {
		content := "> ```\n> x\n> ```\n\n## Heading\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\nProved by: TestValid\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		found := false
		for _, e := range errs {
			if strings.Contains(e, "unknown block kind: blockquote") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected unknown block kind: blockquote, got: %v", errs)
		}
	})

	t.Run("list_item_descendant", func(t *testing.T) {
		content := "## Heading\n\nIt MUST succeed.\n\n- ```\n  x\n  ```\n\nProved by: TestValid\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		found := false
		for _, e := range errs {
			if strings.Contains(e, "unknown block kind: fenced code block") || strings.Contains(e, "unknown block kind: list item") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected unknown block kind error, got: %v", errs)
		}
	})
}

func TestVisibleTextEscapesEntitiesAndAltText(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("escape_duplicate", func(t *testing.T) {
		content := "## A \\& B\n\nIt MUST succeed.\n\n- WHEN first THEN ok\n\nProved by: TestA\n\n## A & B\n\nIt MUST succeed.\n\n- WHEN second THEN ok\n\nProved by: TestB\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
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
			t.Fatalf("expected duplicate heading for escaped ampersand, got: %v", errs)
		}
	})

	t.Run("amp_no_semicolon_not_duplicate", func(t *testing.T) {
		content := "## A &amp B\n\nIt MUST succeed.\n\n- WHEN first THEN ok\n\nProved by: TestA\n\n## A & B\n\nIt MUST succeed.\n\n- WHEN second THEN ok\n\nProved by: TestB\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		for _, e := range errs {
			if strings.Contains(e, "duplicate guarantee heading") {
				t.Fatalf("unexpected duplicate heading error for &amp without semicolon: %s", e)
			}
		}
	})

	t.Run("code_span_entity_not_duplicate", func(t *testing.T) {
		content := "## A `&amp;` B\n\nIt MUST succeed.\n\n- WHEN first THEN ok\n\nProved by: TestA\n\n## A & B\n\nIt MUST succeed.\n\n- WHEN second THEN ok\n\nProved by: TestB\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		for _, e := range errs {
			if strings.Contains(e, "duplicate guarantee heading") {
				t.Fatalf("unexpected duplicate heading error for code-span entity: %s", e)
			}
		}
	})

	t.Run("image_alt_must_not_counted", func(t *testing.T) {
		content := "## Heading\n\nIt is ![MUST](x.png).\n\n- WHEN valid THEN ok\n\nProved by: TestA\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		found := false
		for _, e := range errs {
			if strings.Contains(e, "has no normative MUST sentence") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected has no normative MUST sentence when MUST is image alt, got: %v", errs)
		}
	})

	t.Run("image_alt_then_not_counted", func(t *testing.T) {
		content := "## Heading\n\nIt MUST succeed.\n\n- WHEN valid ![THEN](y) ok\n\nProved by: TestA\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		found := false
		for _, e := range errs {
			if strings.Contains(e, "has no - WHEN ... THEN scenario bullet") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected has no - WHEN ... THEN scenario bullet when THEN is image alt, got: %v", errs)
		}
	})
}

func TestSectionBlockOrderAndSingleList(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("order_probe", func(t *testing.T) {
		content := "## Heading\n\nProved by: TestA\n\n- WHEN valid THEN ok\n\nIt MUST succeed.\n\n* WHEN another THEN ok\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		if len(errs) == 0 {
			t.Fatal("expected order probe to fail")
		}
	})

	t.Run("star_marker_rejected", func(t *testing.T) {
		content := "## Heading\n\nIt MUST succeed.\n\n* WHEN valid THEN ok\n\nProved by: TestA\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
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
			t.Fatalf("expected unknown block kind: list for * marker, got: %v", errs)
		}
	})

	t.Run("list_before_normative_rejected", func(t *testing.T) {
		content := "## Heading\n\n- WHEN valid THEN ok\n\nIt MUST succeed.\n\nProved by: TestA\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		if !slices.Contains(errs, "src/pkg/GUARANTEES.md:5: unknown block kind: paragraph") {
			t.Fatalf("expected order error when normative follows list, got: %v", errs)
		}
		for _, e := range errs {
			if strings.Contains(e, "has no - WHEN") || strings.Contains(e, "has no normative") {
				t.Fatalf("present block reported missing: %v", errs)
			}
		}
	})

	t.Run("proved_by_before_list_rejected", func(t *testing.T) {
		content := "## Heading\n\nIt MUST succeed.\n\nProved by: TestA\n\n- WHEN valid THEN ok\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		found := false
		for _, e := range errs {
			if strings.Contains(e, "unknown block kind: list") || strings.Contains(e, "has no Proved by: line") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected error for proved by before list, got: %v", errs)
		}
	})
}

func TestSubdirectoryFileSelectsNothing(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\nProved by: TestA\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	subDir := filepath.Join(pkgDir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	subFile := filepath.Join(subDir, "sub.go")
	if err := os.WriteFile(subFile, []byte("package sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	selected, err := selectGuaranteeFiles([]string{"src/pkg/sub/sub.go"}, root, false)
	if err != nil {
		t.Fatalf("selectGuaranteeFiles failed: %v", err)
	}
	if len(selected) != 0 {
		t.Fatalf("expected 0 files selected for subpackage file without GUARANTEES.md, got: %v", selected)
	}
}

func TestTestMainWithTestingTResolves(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	code := "package pkg\nimport \"testing\"\nfunc TestMain(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "main_test.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}

	funcs, err := findTestFunctions(pkgDir)
	if err != nil {
		t.Fatalf("findTestFunctions failed: %v", err)
	}
	if !funcs["TestMain"] {
		t.Fatal("expected TestMain(t *testing.T) to resolve")
	}

	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\nProved by: TestMain\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors when citing TestMain(t *testing.T), got: %v", errs)
	}
}

func TestLinelessBlockStartLine(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n\n\n***\n\n- WHEN valid THEN ok\n\nProved by: TestA\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "GUARANTEES.md:7: unknown block kind: thematic break") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected error at line 7 for thematic break, got: %v", errs)
	}
}

func TestFindWordLineWholeIdentifierMatch(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	code := "package pkg\nimport \"testing\"\nfunc TestAB(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}

	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\nProved by: TestAB,\n  TestA\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "GUARANTEES.md:8: \"Heading\" cites test \"TestA\" which does not exist in src/pkg") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected missing test error on line 8 for TestA, got: %v", errs)
	}
}

func TestInvalidSignatureFormattedError(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	code := "package pkg\nimport \"testing\"\nfunc TestGood(t *testing.T) {}\nfunc TestBad(x int) {}\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "bad_test.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}

	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	content := "## Heading\n\nIt MUST succeed.\n\n- WHEN valid THEN ok\n\nProved by: TestGood\n"
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	want := "src/pkg/GUARANTEES.md:1: src/pkg/bad_test.go:4: TestBad has invalid test signature"
	found := false
	for _, e := range errs {
		if e == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected formatted signature error %q, got: %v", want, errs)
	}
}

func TestWalkDirPropagatesError(t *testing.T) {
	_, err := selectGuaranteeFiles(nil, "/nonexistent/root/path/for/walk", true)
	if err == nil {
		t.Fatal("expected error from selectGuaranteeFiles with nonexistent root under --all, got nil")
	}
}

func TestUnreadableTestFileFails(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\nimport \"testing\"\nfunc TestFoo(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldReadFile := readFile
	defer func() { readFile = oldReadFile }()
	readFile = func(_ string) ([]byte, error) {
		return nil, os.ErrPermission
	}

	_, err := findTestFunctions(pkgDir)
	if err == nil {
		t.Fatal("expected findTestFunctions to fail on unreadable test file")
	}
}

func TestMultilineCodeSpanAndCommentBoundaries(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("multiline_code_span_containing_html_comment_opener", func(t *testing.T) {
		content := "## Heading\n\nIt MUST succeed.\n\n- WHEN `a\n  b <!--` THEN c\n\nProved by: TestA\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		if len(errs) != 0 {
			t.Fatalf("expected 0 errors for multiline code span with <!-- not at line start, got: %v", errs)
		}
	})

	t.Run("html_comment_opener_at_line_start_interrupts_item", func(t *testing.T) {
		content := "## Heading\n\nIt MUST succeed.\n\n- WHEN `a\n<!--` THEN b\n\nProved by: TestA\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		hasHTMLBlock := false
		hasNoThen := false
		for _, e := range errs {
			if strings.Contains(e, "unknown block kind: html block") {
				hasHTMLBlock = true
			}
			if strings.Contains(e, "has no - WHEN ... THEN scenario bullet") {
				hasNoThen = true
			}
		}
		if !hasHTMLBlock || !hasNoThen {
			t.Fatalf("expected html block and missing THEN errors for line-starting <!--, got: %v", errs)
		}
	})

	t.Run("multiline_html_comment_containing_backtick_and_then", func(t *testing.T) {
		content := "## Heading\n\nIt MUST succeed.\n\n- WHEN a\n<!-- `\nTHEN ` -->\n\nProved by: TestA\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		found := false
		for _, e := range errs {
			if strings.Contains(e, "has no - WHEN ... THEN scenario bullet") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected has no - WHEN ... THEN scenario bullet for THEN inside multiline HTML comment, got: %v", errs)
		}
	})
}

func TestVisibleTextExtractionExecutableProperty(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		content     string
		expectError string
	}{
		{
			name:        "escaped_numeric_reference_not_normative",
			content:     "## Heading\n\nIt \\&#77;UST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n",
			expectError: "has no normative MUST sentence",
		},
		{
			name:        "long_hex_numeric_reference_not_normative",
			content:     "## Heading\n\nIt &#x000004D;UST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n",
			expectError: "has no normative MUST sentence",
		},
		{
			name:        "numeric_reference_s_not_normative",
			content:     "## Heading\n\nIt &#0115;UST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n",
			expectError: "has no normative MUST sentence",
		},
		{
			name:        "numeric_reference_M_is_normative",
			content:     "## Heading\n\nIt &#077;UST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n",
			expectError: "",
		},
		{
			name:        "escaped_numeric_reference_in_then_has_no_then",
			content:     "## Heading\n\nIt MUST hold.\n\n- WHEN a \\&#84;HEN b\n\nProved by: TestA\n",
			expectError: "has no - WHEN ... THEN scenario bullet",
		},
		{
			name:        "escaped_colon_in_proved_by_not_citation",
			content:     "## Heading\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by\\&#58; TestA\n",
			expectError: "has no Proved by: line",
		},
		{
			name: "amp_entity_not_duplicate_of_ampersand",
			content: "## A \\&amp; B\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n\n" +
				"## A & B\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n",
			expectError: "",
		},
		{
			name: "escaped_ampersand_duplicates_literal_ampersand",
			content: "## A \\& B\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n\n" +
				"## A & B\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n",
			expectError: `duplicate guarantee heading "A & B"`,
		},
		{
			name:        "image_alt_must_not_counted",
			content:     "## Heading\n\nIt is ![MUST](x.png).\n\n- WHEN a THEN b\n\nProved by: TestA\n",
			expectError: "has no normative MUST sentence",
		},
		{
			name:        "html_block_in_section_reported",
			content:     "## Heading\n\nIt MUST succeed.\n\n<!--\n\n- WHEN a THEN b\n\nProved by: TestA\n",
			expectError: "unknown block kind: html block",
		},
		{
			name:        "html_block_does_not_satisfy_normative",
			content:     "## Heading\n\nIt is fine.\n<div>\nIt MUST hold.\n</div>\n\n- WHEN a THEN b\n\nProved by: TestA\n",
			expectError: "has no normative MUST sentence",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gFile := filepath.Join(pkgDir, "GUARANTEES.md")
			if err := os.WriteFile(gFile, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			errs := checkGuaranteesFile(gFile, root)
			if tc.expectError == "" {
				if len(errs) != 0 {
					t.Fatalf("expected 0 errors, got: %v", errs)
				}
				return
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e, tc.expectError) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected error containing %q, got: %v", tc.expectError, errs)
			}
		})
	}
}

func TestBlockStartLines(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("blockquote_with_infoless_fence_at_line_1", func(t *testing.T) {
		content := "> ```\n> x\n> ```\n\n## Heading\n\nIt MUST succeed.\n\n- WHEN a THEN b\n\nProved by: TestA\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		expected := "src/pkg/GUARANTEES.md:1: unknown block kind: blockquote"
		found := false
		for _, e := range errs {
			if strings.Contains(e, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %q, got: %v", expected, errs)
		}
	})

	t.Run("indented_infoless_fence_in_section", func(t *testing.T) {
		content := "## Heading\n\nIt MUST succeed.\n\n   ```\n   x\n   ```\n\n- WHEN a THEN b\n\nProved by: TestA\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		expected := "src/pkg/GUARANTEES.md:5: unknown block kind: fenced code block"
		found := false
		for _, e := range errs {
			if strings.Contains(e, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %q, got: %v", expected, errs)
		}
	})

	t.Run("fence_nested_in_list_item", func(t *testing.T) {
		content := "## Heading\n\nIt MUST succeed.\n\n- WHEN a THEN b\n  ```\n  x\n  ```\n\nProved by: TestA\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		expected := "src/pkg/GUARANTEES.md:6: unknown block kind: fenced code block"
		found := false
		for _, e := range errs {
			if strings.Contains(e, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %q, got: %v", expected, errs)
		}
	})

	t.Run("thematic_break", func(t *testing.T) {
		content := "## Heading\n\nIt MUST succeed.\n\n- WHEN a THEN b\n\n***\n\nProved by: TestA\n"
		gFile := filepath.Join(pkgDir, "GUARANTEES.md")
		if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		errs := checkGuaranteesFile(gFile, root)
		expected := "src/pkg/GUARANTEES.md:7: unknown block kind: thematic break"
		found := false
		for _, e := range errs {
			if strings.Contains(e, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %q, got: %v", expected, errs)
		}
	})
}

func TestUnreadableTestFileFormattedError(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\nimport \"testing\"\nfunc TestFoo(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	if err := os.WriteFile(gFile, []byte("## Heading\n\nIt MUST succeed.\n\n- WHEN a THEN b\n\nProved by: TestFoo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldReadFile := readFile
	defer func() { readFile = oldReadFile }()
	readFile = func(p string) ([]byte, error) {
		if strings.HasSuffix(p, "foo_test.go") {
			return nil, os.ErrPermission
		}
		return os.ReadFile(p)
	}

	errs := checkGuaranteesFile(gFile, root)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %v", errs)
	}
	expected := "src/pkg/GUARANTEES.md:1: cannot read src/pkg/foo_test.go: "
	if !strings.HasPrefix(errs[0], expected) {
		t.Fatalf("expected error prefix %q, got %q", expected, errs[0])
	}
}

func TestCitationLineAfterRawHTMLComment(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\nimport \"testing\"\nfunc TestB(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	content := "## Heading\n\nIt MUST succeed.\n\n- WHEN a THEN b\n\nProved by: TestB <!-- TestA -->,\n  TestA\n"
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "src/pkg/GUARANTEES.md:8:") && strings.Contains(e, "TestA") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected error at line 8 for missing TestA on line 2 of Proved by paragraph, got: %v", errs)
	}
}

func TestImageAltTextWithHardBreakMUSTRejected(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := "## Heading\n\n![x\\\nMUST](https://example.com/y.png)\n\n- WHEN a THEN b\n\nProved by: TestA\n"
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "unknown inline kind: image") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown inline kind: image, got: %v", errs)
	}
}

func TestImageAltTextHardBreakTHENInWHENRejected(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := "## Heading\n\nIt MUST succeed.\n\n- WHEN ![x\\\nTHEN](https://example.com/y.png) a\n\nProved by: TestA\n"
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "unknown inline kind: image") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown inline kind: image, got: %v", errs)
	}
}

func TestRawHTMLCommentWithMUSTRejected(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := "## Heading\n\nIt MUST succeed <!-- MUST -->.\n\n- WHEN a THEN b\n\nProved by: TestA\n"
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "unknown inline kind: raw html") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown inline kind: raw html, got: %v", errs)
	}
}

func TestInlineLinkInNormativeSentenceRejected(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := "## Heading\n\nIt [MUST](https://example.com) succeed.\n\n- WHEN a THEN b\n\nProved by: TestA\n"
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "unknown inline kind: link") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown inline kind: link, got: %v", errs)
	}
}

func TestProvedByEntityCitationTracksSourceLine(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	testFile := filepath.Join(pkgDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package pkg\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := "## Heading\n\nIt MUST succeed.\n\n- WHEN a THEN b\n\nProved by: Test&#65;,\n  TestB\n"
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	errs := checkGuaranteesFile(gFile, root)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "src/pkg/GUARANTEES.md:8:") && strings.Contains(e, `cites test "TestB" which does not exist in src/pkg`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected missing TestB citation at line 8, got: %v", errs)
	}
}

func TestLowercaseDeclarationFailsClosed(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		content string
		line    int
	}{
		{"normative", "## H\n\nIt is advisory <!x MUST>.\n\n- WHEN a <!x THEN> b\n\nProved by: TestA\n", 3},
		{"preamble", "# T\n\n<!x\n## Hidden\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n\n## Real\n\nIt MUST x>\n\n- WHEN a THEN b\n\nProved by: TestA\n", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gFile := filepath.Join(pkgDir, "GUARANTEES.md")
			if err := os.WriteFile(gFile, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("src/pkg/GUARANTEES.md:%d: unknown inline kind: raw html", tc.line)
			if errs := checkGuaranteesFile(gFile, root); !slices.Contains(errs, want) {
				t.Fatalf("want %q in %v", want, errs)
			}
		})
	}
}

func TestLoneCarriageReturnsFailClosed(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	valid := "## H\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n"
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			"paragraph_interruption",
			"## H\n\nIt is advisory.\r## Hidden\rIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n",
			[]string{"src/pkg/GUARANTEES.md:3: lone carriage return", "src/pkg/GUARANTEES.md:4: lone carriage return"},
		},
		{"end_of_file", strings.TrimSuffix(valid, "\n") + "\r", []string{"src/pkg/GUARANTEES.md:7: lone carriage return"}},
		{
			"inside_code_span",
			"## H\n\nIt MUST `a\r<!x` hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n",
			[]string{"src/pkg/GUARANTEES.md:3: lone carriage return"},
		},
		{"crlf", strings.ReplaceAll(valid, "\n", "\r\n"), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(gFile, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := checkGuaranteesFile(gFile, root); !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTextHTMLStartBranchesFailClosed(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	tests := []struct {
		name    string
		content string
		line    int
	}{
		{"open_tag", "## H\n\nIt MUST see a <div\tx.\n\n- WHEN a THEN b\n\nProved by: TestA\n", 3},
		{"close_tag", "## H\n\nIt MUST see a </div\tx.\n\n- WHEN a THEN b\n\nProved by: TestA\n", 3},
		{"processing", "## H\n\nIt MUST see a <?x.\n\n- WHEN a THEN b\n\nProved by: TestA\n", 3},
		{"preamble_emphasis", "# T\n\n*<!x*\n\n## H\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n", 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(gFile, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("src/pkg/GUARANTEES.md:%d: unknown inline kind: raw html", tc.line)
			if got := checkGuaranteesFile(gFile, root); !slices.Contains(got, want) {
				t.Fatalf("want %q in %v", want, got)
			}
		})
	}
}

func TestHTMLBlockStartLinesFailClosed(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")

	starts := []string{"<script", "<pre", "<!--", "<?", "<!x", "<!X", "<![CDATA[", "<div\t", "<div>", "</div>", "<b>"}
	carriers := []struct {
		name   string
		format string
	}{
		{"plain", "a\n%s b"},
		{"emphasis", "*a\n%s* b"},
		{"strong", "**a\n%s** b"},
		{"code_span", "`a\n%s` b"},
		{"link_label", "[a\n%s](u) b"},
		{"link_destination", "[a](u\n%s) b"},
		{"link_title", "[a](u \"t\n%s\") b"},
		{"image_alt", "![a\n%s](u) b"},
		{"html_attribute", "<span title=\"\n%s\"> b"},
	}
	contexts := []struct {
		name   string
		before string
		after  string
		indent int
	}{
		{"preamble", "# T\n\n", "\n\n## H\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n", 0},
		{"normative", "## H\n\nIt MUST ", ".\n\n- WHEN a THEN b\n\nProved by: TestA\n", 0},
		{"when_indented", "## H\n\nIt MUST hold.\n\n- WHEN a ", " THEN b\n\nProved by: TestA\n", 2},
		{"when_lazy", "## H\n\nIt MUST hold.\n\n- WHEN a ", " THEN b\n\nProved by: TestA\n", 0},
		{"proved_by", "## H\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestA ", "\n", 0},
	}

	for _, start := range starts {
		for _, carrier := range carriers {
			for _, context := range contexts {
				for spaces := 0; spaces <= 3; spaces++ {
					name := fmt.Sprintf("%q/%s/%s/%d", start, carrier.name, context.name, spaces)
					t.Run(name, func(t *testing.T) {
						body := fmt.Sprintf(carrier.format, strings.Repeat(" ", context.indent+spaces)+start)
						content := context.before + body + context.after
						if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
							t.Fatal(err)
						}
						line := 2 + strings.Count(context.before, "\n")
						want := fmt.Sprintf("src/pkg/GUARANTEES.md:%d: unknown block kind: html block", line)
						if errs := checkGuaranteesFile(gFile, root); !slices.Contains(errs, want) {
							t.Fatalf("want %q in %v", want, errs)
						}
					})
				}
			}
		}
	}
}

func TestCitationLineTracksDecodedSource(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\nimport \"testing\"\nfunc TestProbe(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, citation := range []string{"Test&#66;", "Test\\_B", "&#84;estB"} {
		t.Run(citation, func(t *testing.T) {
			gFile := filepath.Join(pkgDir, "GUARANTEES.md")
			content := "## H\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestProbe,\n  " + citation + "\n"
			if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			want := `src/pkg/GUARANTEES.md:8: "H" cites test "TestB" which does not exist in src/pkg`
			if citation == "Test\\_B" {
				want = `src/pkg/GUARANTEES.md:8: "H" cites test "Test_B" which does not exist in src/pkg`
			}
			if errs := checkGuaranteesFile(gFile, root); !slices.Contains(errs, want) {
				t.Fatalf("want %q in %v", want, errs)
			}
		})
	}
}

func TestInlineAllowlistProperty(t *testing.T) {
	cases := []struct {
		name       string
		source     string
		visible    string
		rejectKind string
		parserKind string
	}{
		{"text", "A", "A", "", ""},
		{"escaped punctuation", `\_`, "_", "", ""},
		{"named reference", "&amp;", "&", "", ""},
		{"decimal reference", "&#65;", "A", "", ""},
		{"hex reference", "&#x4D;", "M", "", ""},
		{"escaped HTML start", `\<x`, "<x", "", ""},
		{"entity HTML start", "&lt;x", "<x", "", ""},
		{"replacement character", "A\x00B", "A�B", "", ""},
		{"code span", "`A`", "A", "", "*parser.codeSpanParser"},
		{"code span replacement", "`A\x00B`", "A�B", "", ""},
		{"emphasis", "*A*", "A", "", "*parser.emphasisParser"},
		{"strong", "**A**", "A", "", ""},
		{"link", "[A](u)", "", "link", "*parser.linkParser"},
		{"image", "![A](u)", "", "image", ""},
		{"URL autolink", "<https://example.com>", "", "autolink", "*parser.autoLinkParser"},
		{"email autolink", "<a@example.com>", "", "autolink", ""},
		{"open tag", "<b>", "", "raw html", "*parser.rawHTMLParser"},
		{"close tag", "</b>", "", "raw html", ""},
		{"comment", "<!--x-->", "", "raw html", ""},
		{"processing instruction", "<?x?>", "", "raw html", ""},
		{"upper declaration", "<!X x>", "", "raw html", ""},
		{"lower declaration", "<!x x>", "", "raw html", ""},
		{"CDATA", "<![CDATA[x]]>", "", "raw html", ""},
	}

	coveredParsers := make(map[string]bool)
	for _, tc := range cases {
		if tc.parserKind != "" {
			coveredParsers[tc.parserKind] = true
		}
	}
	for _, entry := range parser.DefaultInlineParsers() {
		name := fmt.Sprintf("%T", entry.Value)
		if !coveredParsers[name] {
			t.Errorf("default inline parser %s has no allowlist case", name)
		}
		delete(coveredParsers, name)
	}
	for name := range coveredParsers {
		t.Errorf("allowlist case names missing default inline parser %s", name)
	}

	contexts := []string{"heading", "normative", "when", "proved by"}
	wrappers := []struct {
		name string
		open string
		end  string
	}{
		{"plain", "", ""},
		{"emphasis", "*", "*"},
		{"strong", "**", "**"},
	}
	for _, tc := range cases {
		for _, context := range contexts {
			for _, wrapper := range wrappers {
				for _, secondLine := range []bool{false, true} {
					if context == "heading" && secondLine {
						continue
					}
					name := fmt.Sprintf("%s/%s/%s/second=%t", tc.name, context, wrapper.name, secondLine)
					t.Run(name, func(t *testing.T) {
						wrapped := wrapper.open + tc.source + wrapper.end
						var prefix, suffix, wantPrefix, wantSuffix string
						switch context {
						case "heading":
							prefix, suffix = "## a ", " z\n"
							wantPrefix, wantSuffix = "a ", " z"
						case "normative":
							prefix, suffix = "## H\n\nIt MUST a ", " z\n"
							wantPrefix, wantSuffix = "It MUST a ", " z"
						case "when":
							prefix, suffix = "## H\n\nIt MUST hold.\n\n- WHEN a ", " z THEN b\n"
							wantPrefix, wantSuffix = "WHEN a ", " z THEN b"
						case "proved by":
							prefix, suffix = "## H\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: a ", " z\n"
							wantPrefix, wantSuffix = "Proved by: a ", " z"
						}
						if secondLine {
							prefix = strings.TrimSuffix(prefix, " ") + "\n"
							if context == "when" {
								prefix += "  q "
							} else {
								prefix += "q "
							}
							wantPrefix = strings.TrimSuffix(wantPrefix, " ") + "\nq "
						}
						source := []byte(prefix + wrapped + suffix)
						line := 1 + bytes.Count([]byte(prefix), []byte{'\n'})
						doc := goldmark.DefaultParser().Parse(text.NewReader(source))
						var block ast.Node
						switch context {
						case "heading":
							block = doc.FirstChild()
						case "normative":
							block = doc.FirstChild().NextSibling()
						case "when":
							block = doc.FirstChild().NextSibling().NextSibling().FirstChild().FirstChild()
						case "proved by":
							block = doc.LastChild()
						}
						got, errs := extractInlines(block, source, "GUARANTEES.md")
						if tc.rejectKind != "" {
							want := fmt.Sprintf("GUARANTEES.md:%d: unknown inline kind: %s", line, tc.rejectKind)
							if len(errs) != 1 || errs[0] != want {
								t.Fatalf("want only %q, got %v", want, errs)
							}
							return
						}
						if len(errs) != 0 || got != wantPrefix+tc.visible+wantSuffix {
							t.Fatalf("want text %q and no errors, got %q and %v", wantPrefix+tc.visible+wantSuffix, got, errs)
						}
					})
				}
			}
		}
	}

	for _, tc := range []struct {
		name    string
		source  string
		visible string
	}{
		{"soft break with CRLF", "It MUST a\r\nb", "It MUST a\nb"},
		{"hard break", "It MUST a\\\nb", "It MUST a\nb"},
		{"code span newline", "It MUST `a\nb` hold", "It MUST a b hold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := []byte(tc.source + "\n")
			doc := goldmark.DefaultParser().Parse(text.NewReader(source))
			got, errs := extractInlines(doc.FirstChild(), source, "GUARANTEES.md")
			if got != tc.visible || len(errs) != 0 {
				t.Fatalf("want %q and no errors, got %q and %v", tc.visible, got, errs)
			}
		})
	}

	t.Run("unknown code span child", func(t *testing.T) {
		source := []byte("`a`\n")
		doc := goldmark.DefaultParser().Parse(text.NewReader(source))
		code := doc.FirstChild().FirstChild()
		code.AppendChild(code, ast.NewString([]byte("hidden")))
		_, errs := extractInlines(doc.FirstChild(), source, "GUARANTEES.md")
		want := "GUARANTEES.md:1: unknown inline kind: string"
		if len(errs) != 1 || errs[0] != want {
			t.Fatalf("want only %q, got %v", want, errs)
		}
	})

	t.Run("text segment CR trimming", func(t *testing.T) {
		source := []byte("A\r")
		paragraph := ast.NewParagraph()
		paragraph.AppendChild(paragraph, ast.NewTextSegment(text.NewSegment(0, len(source))))
		got, errs := extractInlines(paragraph, source, "GUARANTEES.md")
		if got != "A" || len(errs) != 0 {
			t.Fatalf("want %q and no errors, got %q and %v", "A", got, errs)
		}
	})
}

func TestHeadingInlineErrorsReported(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	section := "## Good\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n"
	for _, tc := range []struct {
		name    string
		content string
		line    int
	}{
		{"title", "# [Title](u)\n\n" + section, 1},
		{"first section", "## [First](u)\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n", 1},
		{"later section", section + "\n## [Second](u)\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n", 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gFile := filepath.Join(pkgDir, "GUARANTEES.md")
			if err := os.WriteFile(gFile, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("src/pkg/GUARANTEES.md:%d: unknown inline kind: link", tc.line)
			if errs := checkGuaranteesFile(gFile, root); !slices.Contains(errs, want) {
				t.Fatalf("want %q in %v", want, errs)
			}
		})
	}
}

func TestNULAndReplacementHeadingsCollide(t *testing.T) {
	root := t.TempDir()
	pkgDir := createTestPkg(t, root)
	if err := os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package pkg\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	section := "\n\nIt MUST hold.\n\n- WHEN a THEN b\n\nProved by: TestA\n"
	content := "## A\x00B" + section + "\n## A�B" + section
	gFile := filepath.Join(pkgDir, "GUARANTEES.md")
	if err := os.WriteFile(gFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	want := `src/pkg/GUARANTEES.md:9: duplicate guarantee heading "A�B"`
	if errs := checkGuaranteesFile(gFile, root); !slices.Contains(errs, want) {
		t.Fatalf("want %q in %v", want, errs)
	}
}
