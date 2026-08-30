package smi_test

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/smi/internal/catalog"
)

// The diagnostic catalog is this package's statement of what it grades,
// so the catalog is also the specification of what the fixture suite has
// to cover. COVERAGE.md is that mapping written down: one row per code,
// naming the fixture that provokes it and the test that asserts it.
//
// The document is generated and gated the way the SNMP conformance
// matrix next door is. Nobody edits it by hand — a hand edit and a
// forgotten regeneration fail the same test — and the gate it carries is
// two-way. A code the catalog declares and no fixture provokes is a
// condition nothing proves the parser still reports; a fixture asserting
// a code the catalog does not carry is a fixture pinned to a name that
// no longer exists. Both are build failures rather than a missing row in
// a table nobody reads.

var updateCoverage = flag.Bool("update-coverage", false,
	"rewrite COVERAGE.md from the catalog and the committed fixtures")

// coveragePath is the committed document, relative to this package.
const coveragePath = "COVERAGE.md"

// malformedTest is the test that asserts a malformed fixture's codes.
// The subtest is the fixture's directory name, which is why fixture
// directories are named after the code they provoke.
const malformedTest = "TestMalformedFixtures"

// fixtureAssertion is one fixture and the codes it claims.
type fixtureAssertion struct {
	name  string
	codes []string
}

// TestCoverageMatrixIsCurrent regenerates COVERAGE.md and compares it
// against the committed file.
func TestCoverageMatrixIsCurrent(t *testing.T) {
	fixtures := collectFixtureAssertions(t)

	if problems := coverageProblems(catalog.Entries(), fixtures); len(problems) > 0 {
		for _, p := range problems {
			t.Error(p)
		}

		return
	}

	msg, err := compareCoverage(coveragePath, renderCoverage(catalog.Entries(), fixtures), *updateCoverage)
	if err != nil {
		t.Fatal(err)
	}
	if msg != "" {
		t.Error(msg)
	}
}

// TestCoverageGateCatchesAnUncoveredCode proves the first half of the
// gate bites. A catalog row nothing provokes has to fail rather than
// render as a row with an empty fixture column.
func TestCoverageGateCatchesAnUncoveredCode(t *testing.T) {
	rows := []catalog.Entry{
		{Severity: 2, Code: "smi/covered", Tag: "Covered", Format: "x", Description: "d"},
		{Severity: 2, Code: "smi/orphan", Tag: "Orphan", Format: "x", Description: "d"},
	}
	fixtures := []fixtureAssertion{{name: "covered", codes: []string{"smi/covered"}}}

	problems := coverageProblems(rows, fixtures)
	if len(problems) != 1 || !strings.Contains(problems[0], "smi/orphan") {
		t.Fatalf("problems = %v, want one naming smi/orphan", problems)
	}
}

// TestCoverageGateCatchesACodeOutsideTheCatalog proves the other half.
// A fixture pinned to a code the catalog does not carry is asserting a
// name nothing can raise, which passes silently without this check.
func TestCoverageGateCatchesACodeOutsideTheCatalog(t *testing.T) {
	rows := []catalog.Entry{
		{Severity: 2, Code: "smi/covered", Tag: "Covered", Format: "x", Description: "d"},
	}
	fixtures := []fixtureAssertion{
		{name: "covered", codes: []string{"smi/covered"}},
		{name: "invented", codes: []string{"smi/invented"}},
	}

	problems := coverageProblems(rows, fixtures)
	if len(problems) != 1 || !strings.Contains(problems[0], "smi/invented") {
		t.Fatalf("problems = %v, want one naming smi/invented", problems)
	}
}

// TestCoverageGateCatchesAFixtureThatAssertsNothing keeps a fixture from
// sitting in the tree proving nothing. A directory with no expected code
// is loaded, produces whatever it produces, and agrees with itself.
func TestCoverageGateCatchesAFixtureThatAssertsNothing(t *testing.T) {
	rows := []catalog.Entry{
		{Severity: 2, Code: "smi/covered", Tag: "Covered", Format: "x", Description: "d"},
	}
	fixtures := []fixtureAssertion{
		{name: "covered", codes: []string{"smi/covered"}},
		{name: "idle"},
	}

	problems := coverageProblems(rows, fixtures)
	if len(problems) != 1 || !strings.Contains(problems[0], "idle") {
		t.Fatalf("problems = %v, want one naming the idle fixture", problems)
	}
}

// TestCoverageMatrixRejectsAHandEdit checks the drift gate itself. The
// document is only worth generating if editing it is caught, so this
// compares a mutated copy rather than trusting that the committed one
// happens to match today.
func TestCoverageMatrixRejectsAHandEdit(t *testing.T) {
	fixtures := collectFixtureAssertions(t)
	want := renderCoverage(catalog.Entries(), fixtures)

	dir := t.TempDir()
	path := filepath.Join(dir, coveragePath)
	edited := strings.Replace(want, "| `smi/", "| `smi-edited/", 1)
	if edited == want {
		t.Fatal("the mutation changed nothing, so this test proves nothing")
	}
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatalf("writing the edited copy: %v", err)
	}

	msg, err := compareCoverage(path, want, false)
	if err != nil {
		t.Fatalf("comparing: %v", err)
	}
	if msg == "" {
		t.Error("a hand-edited COVERAGE.md compared equal")
	}

	if msg, err := compareCoverage(path, want, true); err != nil || msg != "" {
		t.Errorf("refreshing the edited copy: (%q, %v)", msg, err)
	}
	if msg, err := compareCoverage(path, want, false); err != nil || msg != "" {
		t.Errorf("the refreshed copy still mismatches: (%q, %v)", msg, err)
	}
}

// tolerance is one place the parser knowingly reads something the RFCs
// forbid, and the test that keeps it that way.
//
// The table exists because a tolerance is indistinguishable from an
// oversight to a reader who has not been told: the natural response to
// finding one is to tighten it, and tightening any of these costs the
// corpus real declarations. Each row names the test that fails if
// somebody does.
type tolerance struct {
	what string
	file string
	test string
}

// tolerances is the inventory. The two lexer rows came first, with the
// parser; the rest were written down here.
var tolerances = []tolerance{
	{
		what: "an underscore inside a descriptor is read as part of the name",
		file: "internal/lex/lex_test.go",
		test: "TestUnderscoreDescriptorKeepsItsMembersThoughRFC2578ForbidsIt",
	},
	{
		what: "Windows-1252 curly quotes delimit a string",
		file: "internal/lex/lex_test.go",
		test: "TestCurlyQuotedFileKeepsItsDeclarationsThoughSMIWritesASCIIQuotes",
	},
	{
		what: "a curly-quoted DESCRIPTION still reaches the resolved model",
		file: "malformed_test.go",
		test: "TestMalformedCurlyQuotedDescriptionKeepsItsDeclarationThoughSMIWritesASCIIQuotes",
	},
	{
		what: "an underscored descriptor keeps every member of its enumeration",
		file: "malformed_test.go",
		test: "TestMalformedUnderscoreDescriptorResolvesItsMembersThoughRFC2578ForbidsIt",
	},
	{
		what: "a descriptor ending in a hyphen is read whole",
		file: "malformed_test.go",
		test: "TestMalformedTrailingHyphenNameIsReadWholeThoughRFC2578ForbidsIt",
	},
	{
		what: "clauses written out of their macro's order are kept",
		file: "malformed_test.go",
		test: "TestMalformedOutOfOrderClauseKeepsItsDeclarationThoughRFC2578FixesTheOrder",
	},
	{
		what: "a repeated clause keeps its first occurrence",
		file: "malformed_test.go",
		test: "TestMalformedDuplicateClauseKeepsTheFirstThoughRFC2578AllowsOne",
	},
	{
		what: "a clause value belonging to the other SMI dialect is kept",
		file: "malformed_test.go",
		test: "TestMalformedDialectValueIsKeptThoughItBelongsToTheOtherSMI",
	},
	{
		what: "overlapping range alternatives are both kept",
		file: "malformed_test.go",
		test: "TestMalformedOverlappingRangeKeepsBothAlternativesThoughRFC2578ForbidsThem",
	},
	{
		what: "an OID default written as sub-identifiers keeps its declaration",
		file: "malformed_test.go",
		test: "TestMalformedOIDDefaultAsSubIdentifiersKeepsItsDeclarationThoughRFC2578HasNoSuchForm",
	},
	{
		what: "a symbol used without an IMPORTS clause naming it still resolves",
		file: "malformed_test.go",
		test: "TestMalformedMissingImportStillResolvesThoughRFC2578RequiresTheImport",
	},
	{
		what: "an IMPORTS cycle is broken at the back edge and costs neither module",
		file: "malformed_test.go",
		test: "TestMalformedImportCycleKeepsBothModulesThoughItHasNoAcyclicOrder",
	},
	{
		what: "text after the final END is dropped and the module before it kept",
		file: "malformed_test.go",
		test: "TestMalformedContentAfterEndKeepsTheModuleBeforeItThoughItBelongsToNoModule",
	},
	{
		what: "a file written for paired comments is re-read under that rule",
		file: "malformed_test.go",
		test: "TestMalformedPairedCommentReparseKeepsTheDeclarationsThoughEndOfLineIsTheDefault",
	},
}

// TestCoverageTolerancesHavePinningTests checks that every tolerance in
// the inventory names a test that exists.
//
// A row whose test was renamed or deleted is worse than no row at all:
// it reads as a guarantee and guarantees nothing, and the document it
// renders into would say the tolerance is pinned when it is not.
func TestCoverageTolerancesHavePinningTests(t *testing.T) {
	byFile := map[string]map[string]bool{}

	for _, tol := range tolerances {
		if _, done := byFile[tol.file]; !done {
			byFile[tol.file] = testFuncsIn(t, tol.file)
		}
		if !byFile[tol.file][tol.test] {
			t.Errorf("%s names %s, which %s does not declare", tol.what, tol.test, tol.file)
		}
	}
}

// testFuncsIn returns the test function names one file declares.
func testFuncsIn(t *testing.T, path string) map[string]bool {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	out := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && strings.HasPrefix(fn.Name.Name, "Test") {
			out[fn.Name.Name] = true
		}
	}

	return out
}

// collectFixtureAssertions reads every malformed fixture's expectations.
func collectFixtureAssertions(t *testing.T) []fixtureAssertion {
	t.Helper()

	dirs := fixtureCases(t, malformedDir)

	out := make([]fixtureAssertion, 0, len(dirs))
	for _, dir := range dirs {
		out = append(out, fixtureAssertion{
			name:  filepath.Base(dir),
			codes: readMalformedExpect(t, dir),
		})
	}

	return out
}

// coverageProblems returns everything wrong with the mapping between the
// catalog and the fixtures, in a stable order.
//
// It takes both sides as arguments rather than reading them itself so
// that the tests above can prove each check bites — the same shape the
// catalog's own Validate uses, and for the same reason.
func coverageProblems(rows []catalog.Entry, fixtures []fixtureAssertion) []string {
	cataloged := make(map[string]bool, len(rows))
	for _, r := range rows {
		cataloged[r.Code] = true
	}

	covered := map[string]bool{}

	var problems []string
	for _, f := range fixtures {
		if len(f.codes) == 0 {
			problems = append(problems, fmt.Sprintf(
				"fixture %s asserts no diagnostic code, so nothing it does can fail", f.name))

			continue
		}
		for _, code := range f.codes {
			if !cataloged[code] {
				problems = append(problems, fmt.Sprintf(
					"fixture %s asserts %s, which is not in the diagnostic catalog", f.name, code))

				continue
			}
			covered[code] = true
		}
	}

	for _, r := range rows {
		if !covered[r.Code] {
			problems = append(problems, fmt.Sprintf(
				"%s has no fixture, so nothing proves the parser still reports it", r.Code))
		}
	}

	return problems
}

// renderCoverage writes the committed document: one row per catalog
// code, naming every fixture that provokes it and the test that asserts
// them.
//
// Rows are in catalog order, which is code order, so a new code lands
// where a reader would look for it and the diff shows one added line.
func renderCoverage(rows []catalog.Entry, fixtures []fixtureAssertion) string {
	byCode := map[string][]string{}
	for _, f := range fixtures {
		for _, code := range f.codes {
			byCode[code] = append(byCode[code], f.name)
		}
	}

	var b strings.Builder

	b.WriteString("# SMI diagnostic coverage\n\n")
	b.WriteString("Generated from the diagnostic catalog in `internal/catalog` and the\n")
	b.WriteString("fixtures under `testdata/malformed`. Do not edit by hand — run\n")
	b.WriteString("`go test ./src/common/smi/ -run TestCoverageMatrixIsCurrent -update-coverage`.\n\n")
	b.WriteString("Every code the catalog declares has a fixture that provokes it, and every\n")
	b.WriteString("fixture asserts the exact set of codes its load produces. A code with no\n")
	b.WriteString("fixture, and a fixture naming a code the catalog does not carry, both fail\n")
	b.WriteString("the build.\n\n")

	fmt.Fprintf(&b, "**%d codes, %d fixtures.**\n\n", len(rows), len(fixtures))

	b.WriteString("| code | severity | fixture | asserted by |\n")
	b.WriteString("|---|---|---|---|\n")

	for _, r := range rows {
		names := slices.Clone(byCode[r.Code])
		slices.Sort(names)

		paths := make([]string, 0, len(names))
		tests := make([]string, 0, len(names))
		for _, n := range names {
			paths = append(paths, "`testdata/malformed/"+n+"`")
			tests = append(tests, "`"+malformedTest+"/"+n+"`")
		}

		fmt.Fprintf(&b, "| `%s` | %d | %s | %s |\n",
			r.Code, r.Severity, strings.Join(paths, "<br>"), strings.Join(tests, "<br>"))
	}

	b.WriteString("\n## Deliberate tolerances\n\n")
	b.WriteString("Each row is a place the parser knowingly reads something the RFCs forbid,\n")
	b.WriteString("because refusing it costs the corpus more than it reports. Tightening one\n")
	b.WriteString("means deleting the test beside it, which is the point: a tolerance nobody\n")
	b.WriteString("wrote down reads as an oversight to the next person who finds it.\n\n")
	b.WriteString("| tolerance | pinned by |\n")
	b.WriteString("|---|---|\n")

	for _, tol := range tolerances {
		fmt.Fprintf(&b, "| %s | `%s` in `%s` |\n", tol.what, tol.test, tol.file)
	}

	return b.String()
}

// compareCoverage writes the document when update is set and otherwise
// compares it, returning the failure message rather than failing itself
// so that the test covering the mismatch path can assert on what a
// reviewer is told.
func compareCoverage(path, want string, update bool) (string, error) {
	if update {
		if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
			return "", fmt.Errorf("writing %s: %w", path, err)
		}

		return "", nil
	}

	got, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w (rerun with -update-coverage)", path, err)
	}
	if string(got) == want {
		return "", nil
	}

	return fmt.Sprintf("%s is stale or hand-edited. Rerun with -update-coverage "+
		"and review the diff.\n%s", path, snapshotDiff(string(got), want)), nil
}
