//go:build smi_reference

package smi_test

import (
	"bufio"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi/internal/catalog"
)

// The malformed fixtures assert what this parser reports. That is an
// argument with itself: the fixtures were written from the RFC text by
// the same people who wrote the parser, so a shared misreading would
// leave every one of them green. An outside parser that has read the
// same RFCs for thirty years is the check on that, and this is the test
// that asks it.
//
// What it asks matters more than that it asks. "Both parsers flagged
// the file" is worth nothing — a reference that rejects a file for
// reason X and a parser that rejects it for unrelated reason Y agree on
// nothing at all, and the assertion still passes. So the comparison is
// on the *construct*: the declaration inside the file that each side
// puts its finger on. Agreeing that acmeSlot's SYNTAX is wrong is
// corroboration; agreeing that BAD-MIB.mib is bad is not.
//
// It sits behind a build tag because the verifier runs `go test -race
// ./...` and this test shells out to a tool that is not a dependency of
// anything, is not installed on CI, and whose message text changes
// between releases. Run it deliberately:
//
//	go test -tags=smi_reference -run TestReference ./src/protocol/smi/
//
// With no reference parser on PATH it skips. That is the honest result
// on a machine that cannot run the comparison, and it is not evidence
// of anything.

// referenceDir holds the committed mapping table.
const referenceDir = "testdata/reference"

// netsnmpMapping is the table this test is built on. It is a file rather
// than a Go literal because it is the reviewable artifact here: each row
// is a claim that two independently written parsers grade the same
// construct, and a reviewer has to be able to read the claims without
// reading the assertions around them.
const netsnmpMapping = referenceDir + "/netsnmp.tsv"

// preambleConstruct names the region of a file before its first
// declaration head — the module header line and its IMPORTS. A
// diagnostic can land there, and it needs a name so that landing there
// is comparable rather than unrepresentable.
const preambleConstruct = "<preamble>"

// referenceRow is one row of the mapping table.
//
// Kind is "map", "differ" or "none". A map row claims that in Fixture,
// this parser's Code and any net-snmp message containing Fragment
// describe the same construct. A differ row records that both parsers
// grade the condition but put their finger on different declarations,
// and pins which one each names. A none row records a code net-snmp does
// not grade at all, with the reason.
//
// The differ kind is what keeps the table honest. Without it the only
// way to land a disagreement is to demote the row to none, and a table
// whose disagreements all turn into absences can only ever report
// agreement. A differ row asserts the disagreement instead, so a release
// of either parser that moves its position fails the row and forces the
// adjudication to be redone.
type referenceRow struct {
	Kind     string
	Fixture  string
	Code     string
	Fragment string // map and differ rows: the net-snmp message text to match on
	Ours     string // differ rows: the construct this parser names
	Theirs   string // differ rows: the construct net-snmp names
	Reason   string // none rows: why the reference has no counterpart
}

// TestReferenceCrossCheck runs net-snmp over every mapped fixture and
// requires it to flag the same construct this parser flags.
func TestReferenceCrossCheck(t *testing.T) {
	tool := referenceTool(t)
	rows := readReferenceMapping(t)

	byFixture := map[string][]referenceRow{}
	for _, r := range rows {
		if r.Kind == "map" || r.Kind == "differ" {
			byFixture[r.Fixture] = append(byFixture[r.Fixture], r)
		}
	}

	for _, fixture := range slices.Sorted(maps.Keys(byFixture)) {
		t.Run(fixture, func(t *testing.T) {
			dir := filepath.Join(malformedDir, fixture)
			theirs := runNetSNMP(t, tool, dir)

			for _, row := range byFixture[fixture] {
				if row.Kind == "differ" {
					checkRowDiffers(t, dir, row, theirs)

					continue
				}
				checkRowAgrees(t, dir, row, theirs)
			}
		})
	}
}

// checkRowAgrees is one row's assertion: both sides name at least one
// construct, and every construct this parser names is one the reference
// names too.
//
// The relation is containment rather than equality because net-snmp does
// not recover — one bad token costs it the rest of the declaration and
// often the next one, so it routinely names more constructs than this
// parser does. Requiring equality would fail on that difference in
// recovery, which is a property this package chose deliberately and not
// a disagreement about what is wrong. Constructs only the reference
// names are logged, because a reader deciding whether the leniency went
// too far wants to see them.
func checkRowAgrees(t *testing.T, dir string, row referenceRow, theirs []referenceFinding) {
	t.Helper()

	ours := constructsForCode(t, dir, row.Code)
	if len(ours) == 0 {
		t.Errorf("%s: the mapping claims this fixture raises %s, and the load raised no such diagnostic",
			row.Fixture, row.Code)

		return
	}

	matched := matchFragment(theirs, row.Fragment)
	if len(matched) == 0 {
		t.Errorf("%s: %s is mapped to net-snmp %q and no message matched it. "+
			"Either the reference stopped grading this construct or its wording moved; "+
			"adjudicate it against the RFC and re-map or demote the row to \"none\".\n"+
			"net-snmp said:\n%s",
			row.Fixture, row.Code, row.Fragment, renderFindings(theirs))

		return
	}

	for _, construct := range ours {
		if !slices.Contains(matched, construct) {
			t.Errorf("%s: %s is raised on %s, and net-snmp %q names %v instead",
				row.Fixture, row.Code, construct, row.Fragment, matched)
		}
	}

	for _, construct := range matched {
		if !slices.Contains(ours, construct) {
			t.Logf("%s: net-snmp %q also names %s, which %s does not — net-snmp abandons the declaration where this parser recovers",
				row.Fixture, row.Fragment, construct, row.Code)
		}
	}
}

// checkRowDiffers pins a recorded disagreement: both parsers grade the
// condition, and each names the declaration the row says it names.
//
// Asserting the disagreement rather than excusing it is what stops the
// table from drifting into a record of nothing. If this parser moves
// onto the reference's construct the row fails and has to be promoted to
// a map row; if the reference moves, the row fails and the adjudication
// is redone against the RFC.
func checkRowDiffers(t *testing.T, dir string, row referenceRow, theirs []referenceFinding) {
	t.Helper()

	if ours := constructsForCode(t, dir, row.Code); !slices.Equal(ours, []string{row.Ours}) {
		t.Errorf("%s: %s is raised on %v, and the table records it on [%s]",
			row.Fixture, row.Code, ours, row.Ours)
	}

	matched := matchFragment(theirs, row.Fragment)
	if len(matched) == 0 {
		t.Errorf("%s: %s is mapped to net-snmp %q and no message matched it.\nnet-snmp said:\n%s",
			row.Fixture, row.Code, row.Fragment, renderFindings(theirs))

		return
	}
	if !slices.Equal(matched, []string{row.Theirs}) {
		t.Errorf("%s: net-snmp %q names %v, and the table records [%s]",
			row.Fixture, row.Fragment, matched, row.Theirs)
	}
	if row.Ours == row.Theirs {
		t.Errorf("%s: %s records a disagreement whose two constructs are one declaration; make it a map row",
			row.Fixture, row.Code)
	}
}

// TestReferenceMappingCoversTheCatalog requires every catalog code to be
// accounted for in the table, as either a mapped code or a recorded
// absence.
//
// Without this, a code could quietly have no row and the cross-check
// would pass by not asking about it, which is the failure mode this
// whole unit exists to avoid.
func TestReferenceMappingCoversTheCatalog(t *testing.T) {
	rows := readReferenceMapping(t)

	mapped := map[string]bool{}
	differing := map[string]bool{}
	absent := map[string]bool{}
	for _, r := range rows {
		switch r.Kind {
		case "map":
			mapped[r.Code] = true
		case "differ":
			mapped[r.Code] = true
			differing[r.Code] = true
		case "none":
			if absent[r.Code] {
				t.Errorf("%s is recorded absent twice", r.Code)
			}
			absent[r.Code] = true
		}
	}

	for code := range mapped {
		if absent[code] {
			t.Errorf("%s is both mapped and recorded absent", code)
		}
	}

	known := map[string]bool{}
	for _, e := range catalog.Entries() {
		known[e.Code] = true
		if !mapped[e.Code] && !absent[e.Code] {
			t.Errorf("%s appears in neither half of %s; map it to a net-snmp message or record why it has no counterpart",
				e.Code, netsnmpMapping)
		}
	}
	for _, r := range rows {
		if !known[r.Code] {
			t.Errorf("%s names %s, which the catalog does not carry", netsnmpMapping, r.Code)
		}
	}

	t.Logf("%d codes have a net-snmp counterpart, %d of those on a different construct; %d have none",
		len(mapped), len(differing), len(absent))
}

// TestReferenceConstructComparisonRejectsAMismatch proves the comparison
// can fail. A construct comparison built on a name lookup that silently
// misses would report agreement everywhere, so the lookup is exercised
// on a source whose declarations are known: a line inside a declaration
// resolves to that declaration's descriptor, a line before the first one
// resolves to the preamble, and two different lines do not collapse onto
// one name.
func TestReferenceConstructComparisonRejectsAMismatch(t *testing.T) {
	src := "BAD-MIB DEFINITIONS ::= BEGIN\n" +
		"\n" +
		"acme OBJECT IDENTIFIER ::= { iso 3 }\n" +
		"\n" +
		"widget OBJECT-TYPE\n" +
		"    SYNTAX Integer32\n" +
		"    ::= { acme 1 }\n" +
		"\n" +
		"END\n"

	heads := declarationHeads([]byte(src))

	cases := map[int]string{
		1: "BAD-MIB",
		2: "BAD-MIB",
		3: "acme",
		6: "widget",
		7: "widget",
		9: "END",
	}
	for line, want := range cases {
		if got := constructAt(heads, line); got != want {
			t.Errorf("line %d resolves to %q, want %q", line, got, want)
		}
	}

	if got := constructAt(nil, 4); got != preambleConstruct {
		t.Errorf("a file with no declaration head resolves to %q, want %q", got, preambleConstruct)
	}
	if constructAt(heads, 3) == constructAt(heads, 6) {
		t.Error("two different declarations resolve to one construct, so the comparison cannot tell them apart")
	}
}

// referenceTool returns the reference parser to run, skipping when none
// is installed.
//
// libsmi's smilint is the richer reference and the one the mapping table
// would ideally be written against, but a table has to be written
// against the tool that produced it. This one is net-snmp's, so a
// machine carrying only smilint skips rather than comparing net-snmp
// claims against libsmi output.
func referenceTool(t *testing.T) string {
	t.Helper()

	netsnmp, netsnmpErr := exec.LookPath("snmptranslate")
	if netsnmpErr == nil {
		return netsnmp
	}
	if !errors.Is(netsnmpErr, exec.ErrNotFound) {
		t.Fatalf("looking for snmptranslate: %v", netsnmpErr)
	}

	if _, err := exec.LookPath("smilint"); err == nil {
		t.Skipf("libsmi's smilint is installed and net-snmp's snmptranslate is not; %s is net-snmp's table and says nothing about smilint's tags",
			netsnmpMapping)
	}

	t.Skip("no reference MIB parser on PATH (install net-snmp for snmptranslate); the cross-check is not evidence either way here")

	return ""
}

// referenceFinding is one positioned message from the reference parser:
// the line as the tool wrote it, and the construct that line falls in.
type referenceFinding struct {
	construct string
	raw       string
}

// runNetSNMP parses every MIB in dir with net-snmp and returns the
// messages it positioned in a source file.
//
// The invocation is deliberately strict and hermetic. -M replaces the
// search path rather than extending it and -m ALL loads what is in it,
// so nothing but the fixture is read; the environment is emptied of the
// variables and the config file that would otherwise pull in the
// machine's own MIBs. -Pu is *not* passed: allowing underscores in
// descriptors is exactly one of the tolerances under comparison, and
// asking the reference to tolerate it too would delete the row.
func runNetSNMP(t *testing.T, tool, dir string) []referenceFinding {
	t.Helper()

	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("resolving %s: %v", dir, err)
	}

	cmd := exec.Command(tool, "-M", abs, "-m", "ALL", "-PW", "-Le", "-On", ".1.3")
	cmd.Env = []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + abs,
		"MIBDIRS=" + abs,
		"MIBS=ALL",
		"SNMPCONFPATH=" + abs,
	}

	var stderr strings.Builder
	cmd.Stderr = &stderr
	// A non-zero exit is ordinary here: the tool is being handed MIBs it
	// cannot parse. What it wrote to stderr is the result either way.
	_ = cmd.Run()

	return parseNetSNMP(t, stderr.String())
}

// netsnmpAtLine matches net-snmp's positioned message forms. The parser
// writes "At line N in PATH" and the OID linker writes "near line N of
// PATH"; both name a line in a file, which is all this test needs.
var netsnmpAtLine = regexp.MustCompile(`(?:At line (\d+) in|near line (\d+) of) (\S+)$`)

// parseNetSNMP turns net-snmp's stderr into findings, keeping only the
// messages that name a line in a file.
//
// Unpositioned messages are dropped rather than matched loosely.
// net-snmp reports some conditions with no line at all ("Warning: acme.1
// is both acmeSlotTable and acmeSlot"), and a message with no position
// cannot corroborate a construct — matching on its text alone would be
// the weak "something was flagged" assertion this test exists to avoid.
// The codes those messages would have covered are recorded as having no
// counterpart, with that as the reason.
func parseNetSNMP(t *testing.T, stderr string) []referenceFinding {
	t.Helper()

	var out []referenceFinding

	scanner := bufio.NewScanner(strings.NewReader(stderr))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		m := netsnmpAtLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		digits := m[1]
		if digits == "" {
			digits = m[2]
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			t.Fatalf("net-snmp wrote an unreadable line number in %q: %v", line, err)
		}

		path := m[3]
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("net-snmp named %s, which cannot be read: %v", path, err)
		}

		out = append(out, referenceFinding{
			construct: filepath.Base(path) + ":" + constructAt(declarationHeads(src), n),
			raw:       line,
		})
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("reading net-snmp output: %v", err)
	}

	return out
}

// matchFragment returns the constructs of every finding whose text holds
// fragment, deduplicated and sorted.
func matchFragment(findings []referenceFinding, fragment string) []string {
	var out []string
	for _, f := range findings {
		if strings.Contains(f.raw, fragment) && !slices.Contains(out, f.construct) {
			out = append(out, f.construct)
		}
	}
	slices.Sort(out)

	return out
}

// renderFindings writes what the reference actually said, for a failure
// that has to be adjudicated by hand.
func renderFindings(findings []referenceFinding) string {
	if len(findings) == 0 {
		return "  (nothing positioned in a source file)"
	}

	var b strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&b, "  %s\t%s\n", f.construct, f.raw)
	}

	return b.String()
}

// constructsForCode loads a fixture and returns the constructs its
// diagnostics with this code sit on, in the same "file:descriptor" form
// the reference side produces.
func constructsForCode(t *testing.T, dir, code string) []string {
	t.Helper()

	set := loadFixtureDir(t, dir)

	var out []string

	for _, d := range set.Diagnostics() {
		if string(d.Code()) != code {
			continue
		}

		pos := d.Position()
		src := readFileOnce(t, pos.File)

		construct := filepath.Base(pos.File) + ":" + constructAt(declarationHeads(src), lineAt(src, pos.Offset))
		if !slices.Contains(out, construct) {
			out = append(out, construct)
		}
	}
	slices.Sort(out)

	return out
}

// fileCache keeps a fixture's bytes for the handful of lookups one
// fixture needs, since a diagnostic carries a byte offset and turning
// offsets into lines means having the file.
var fileCache = map[string][]byte{}

func readFileOnce(t *testing.T, path string) []byte {
	t.Helper()

	if src, ok := fileCache[path]; ok {
		return src
	}

	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	fileCache[path] = src

	return src
}

// lineAt returns the 1-based line holding offset.
func lineAt(src []byte, offset int) int {
	if offset > len(src) {
		offset = len(src)
	}

	line := 1
	for i := range offset {
		if src[i] == '\n' {
			line++
		}
	}

	return line
}

// declarationHead is where one declaration starts and what it is called.
type declarationHead struct {
	line int
	name string
}

// declarationHeads finds the declarations in a MIB source by the one
// convention every MIB in the corpus keeps: a declaration starts in
// column zero and its clauses are indented.
//
// This is a lexical approximation rather than a parse, and it has to be:
// the two sides being compared disagree about how to parse the file, so
// asking either one where a declaration begins would put the answer
// inside the question. Column zero is a rule both parsers' inputs obey
// and neither parser owns.
func declarationHeads(src []byte) []declarationHead {
	var out []declarationHead

	for i, raw := range strings.Split(string(src), "\n") {
		if raw == "" || raw[0] == ' ' || raw[0] == '\t' || raw[0] == '\r' {
			continue
		}
		if strings.HasPrefix(raw, "--") {
			continue
		}

		fields := strings.Fields(raw)
		if len(fields) == 0 {
			continue
		}

		out = append(out, declarationHead{line: i + 1, name: fields[0]})
	}

	return out
}

// constructAt names the declaration that line falls in: the nearest head
// at or above it.
func constructAt(heads []declarationHead, line int) string {
	name := preambleConstruct
	for _, h := range heads {
		if h.line > line {
			break
		}
		name = h.name
	}

	return name
}

// readReferenceMapping reads the committed table.
func readReferenceMapping(t *testing.T) []referenceRow {
	t.Helper()

	body, err := os.ReadFile(netsnmpMapping)
	if err != nil {
		t.Fatalf("reading %s: %v", netsnmpMapping, err)
	}

	var out []referenceRow
	for i, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Split(line, "\t")
		switch {
		case fields[0] == "map" && len(fields) == 4:
			out = append(out, referenceRow{Kind: "map", Fixture: fields[1], Code: fields[2], Fragment: fields[3]})
		case fields[0] == "differ" && len(fields) == 6:
			out = append(out, referenceRow{
				Kind:     "differ",
				Fixture:  fields[1],
				Code:     fields[2],
				Fragment: fields[3],
				Ours:     fields[4],
				Theirs:   fields[5],
			})
		case fields[0] == "none" && len(fields) == 3:
			out = append(out, referenceRow{Kind: "none", Code: fields[1], Reason: fields[2]})
		default:
			t.Fatalf("%s:%d: %q is none of \"map<TAB>fixture<TAB>code<TAB>fragment\", "+
				"\"differ<TAB>fixture<TAB>code<TAB>fragment<TAB>ours<TAB>theirs\" or \"none<TAB>code<TAB>reason\"",
				netsnmpMapping, i+1, line)
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no rows", netsnmpMapping)
	}

	return out
}
