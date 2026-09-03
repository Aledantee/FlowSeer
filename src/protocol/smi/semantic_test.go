package smi_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// The semantic fixtures are the correctness oracle. The corpus harness
// next door proves the parser survives real vendor MIBs; it cannot prove
// the parser understood them, because its snapshots are whatever the
// parser produced last time somebody looked. These fixtures say what the
// RFCs say a module means, and they were written by reading the clause
// text rather than by reading the model.
//
// That is why nothing here has a refresh flag. Every other committed
// artifact in this package regenerates from the code with a -update
// switch, and one here would quietly turn the oracle into a mirror: a
// wrong resolution would be adopted as the expectation on the next run.
// A fixture that disagrees with the parser is edited by hand, after
// somebody has decided which of the two is wrong, or it is marked
// pending.
//
// A "pending" line is an expectation the RFC supports and the parser
// does not meet yet. It is asserted in the negative: the value must
// still be wrong. When somebody fixes the gap the pending line starts
// matching and this test fails, which is what turns a known gap into a
// promotion rather than a comment nobody reads.

const semanticDir = "testdata/semantic"

// absent is what a fact reads when the model carries nothing there. It
// is spelled out rather than left empty so that an expectation asserting
// absence looks like an assertion instead of a truncated line.
const absent = "(none)"

// TestSemanticFixtures checks each fixture's resolved model against the
// expectations committed beside it.
//
// A fixture is expected to load clean: a semantic fixture that raises a
// diagnostic is either testing something the malformed suite owns or has
// a typo in it, and both are worth failing over. A fixture that means to
// raise one says so with an allow-diagnostic line.
func TestSemanticFixtures(t *testing.T) {
	for _, dir := range fixtureCases(t, semanticDir) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			set := loadFixtureDir(t, dir)
			checkExpectations(t, filepath.Join(dir, "expect.txt"), semanticFacts(set), set)
		})
	}
}

// TestSemanticFixturesCoverEveryClauseFamily fails when a declaration
// kind the model can carry appears in no fixture.
//
// The suite is only an oracle for what it looks at, and a clause family
// nothing exercises is a family whose resolution is asserted by the
// corpus snapshots alone — which is to say, by whatever the parser did.
func TestSemanticFixturesCoverEveryClauseFamily(t *testing.T) {
	want := []smi.NodeKind{
		smi.NodeNode, smi.NodeScalar, smi.NodeTable, smi.NodeRow, smi.NodeColumn,
		smi.NodeNotification, smi.NodeGroup, smi.NodeCompliance, smi.NodeCapabilities,
	}

	seen := map[smi.NodeKind]string{}
	for _, dir := range fixtureCases(t, semanticDir) {
		for _, m := range loadFixtureDir(t, dir).Modules() {
			for _, n := range m.Nodes {
				if _, ok := seen[n.Kind]; !ok {
					seen[n.Kind] = filepath.Base(dir)
				}
			}
		}
	}

	for _, kind := range want {
		if _, ok := seen[kind]; !ok {
			t.Errorf("no semantic fixture declares a %s, so nothing asserts how one resolves", kind)
		}
	}
}

// fixtureCases returns the fixture directories under root, sorted, so a
// run visits them in the order a reader would list them.
func fixtureCases(t *testing.T, root string) []string {
	t.Helper()

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("reading %s: %v", root, err)
	}

	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no fixture directories", root)
	}

	return out
}

// loadFixtureDir loads every MIB in one fixture directory, with the
// directory itself as the search path so that a fixture's modules can
// import each other.
func loadFixtureDir(t *testing.T, dir string) *smi.ModuleSet {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(dir, "*.mib"))
	if err != nil {
		t.Fatalf("globbing %s: %v", dir, err)
	}
	if len(paths) == 0 {
		t.Fatalf("%s holds no .mib source", dir)
	}
	slices.Sort(paths)

	set, err := smi.LoadFiles(paths, smi.Options{SearchPaths: []string{dir}})
	if err != nil {
		t.Fatalf("loading %s: %v", dir, err)
	}

	return set
}

// semanticFacts renders everything an expectation may assert, keyed by
// "<what> <subject>".
//
// Building the whole fact set up front rather than looking each
// expectation up on demand is what lets a misspelled subject fail as a
// missing fact instead of passing as a zero value.
func semanticFacts(set *smi.ModuleSet) map[string]string {
	facts := map[string]string{}
	put := func(what, subject, value string) {
		if value == "" {
			value = absent
		}
		facts[what+" "+subject] = value
	}

	for _, m := range set.Modules() {
		put("dialect", m.Name, m.Dialect.String())
		if m.Identity != nil {
			put("identity", m.Name, m.Identity.Name)
		} else {
			put("identity", m.Name, absent)
		}

		for _, n := range m.Nodes {
			subject := m.Name + "." + n.Name

			put("oid", subject, n.OID.String())
			put("kind", subject, n.Kind.String())
			put("access", subject, n.Access.String())
			put("status", subject, n.Status.String())
			put("units", subject, n.Units)
			put("unresolved", subject, strconv.FormatBool(n.Unresolved))
			put("index", subject, renderIndex(n.Index))
			put("augments", subject, n.Augments)
			put("default", subject, renderDefault(n.Default))

			put("syntax-name", subject, typeField(n.Type, func(t *smi.Type) string { return t.Name }))
			put("syntax-base", subject, typeField(n.Type, func(t *smi.Type) string { return t.Base.String() }))
			put("syntax-hint", subject, typeField(n.Type, func(t *smi.Type) string { return t.DisplayHint }))
			put("syntax-members", subject, typeField(n.Type, func(t *smi.Type) string { return renderMembers(t.Members) }))
			put("syntax-ranges", subject, typeField(n.Type, func(t *smi.Type) string { return renderRanges(t.Ranges) }))
			put("syntax-sizes", subject, typeField(n.Type, func(t *smi.Type) string { return renderRanges(t.Sizes) }))
		}

		for _, ty := range m.Types {
			subject := m.Name + "." + ty.Name

			put("type-base", subject, ty.Base.String())
			put("type-parent", subject, ty.Parent)
			put("type-hint", subject, ty.DisplayHint)
			put("type-status", subject, ty.Status.String())
			put("type-members", subject, renderMembers(ty.Members))
			put("type-ranges", subject, renderRanges(ty.Ranges))
			put("type-sizes", subject, renderRanges(ty.Sizes))
			put("type-unresolved", subject, strconv.FormatBool(ty.Unresolved))
		}

		for _, tbl := range m.Tables {
			subject := m.Name + "." + tbl.Node.Name

			row := absent
			if tbl.Row != nil {
				row = tbl.Row.Name
			}
			put("table-row", subject, row)

			names := make([]string, 0, len(tbl.Columns))
			for _, c := range tbl.Columns {
				names = append(names, c.Name)
			}
			put("table-columns", subject, strings.Join(names, " "))
			put("table-index", subject, renderIndex(tbl.Index))
			put("table-augments", subject, tbl.Augments)
		}
	}

	return facts
}

// typeField reads one field of a node's resolved type, reporting a node
// that carries no value as absent rather than as an empty field.
func typeField(t *smi.Type, read func(*smi.Type) string) string {
	if t == nil {
		return absent
	}

	return read(t)
}

// renderIndex writes an INDEX clause the way its source does, IMPLIED
// included, because the model keeps the clause as written and an
// expectation should read like the MIB it came from.
func renderIndex(parts []smi.IndexPart) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p.Implied {
			out = append(out, "IMPLIED "+p.Name)

			continue
		}
		out = append(out, p.Name)
	}

	return strings.Join(out, " ")
}

// renderDefault writes a DEFVAL as its shape followed by its value.
//
// The shape leads because it is half of what an expectation asserts: an
// empty bit set is a default of no bits and renders as "bits" with
// nothing after it, which has to be distinguishable from the object that
// wrote no DEFVAL at all.
func renderDefault(d smi.Default) string {
	switch d.Kind {
	case smi.DefaultNone:
		return ""
	case smi.DefaultInteger:
		return fmt.Sprintf("integer %d", d.Number)
	case smi.DefaultLabel:
		return "label " + d.Name
	case smi.DefaultOctets:
		return fmt.Sprintf("octets %x", d.Octets)
	case smi.DefaultOID:
		return "oid " + d.OID.String()
	default:
		return strings.TrimRight("bits "+strings.Join(d.Bits, " "), " ")
	}
}

// renderMembers writes named numbers as the source writes them, so an
// expectation carries each member's declared number rather than its
// place in the list.
func renderMembers(members []smi.Member) string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		out = append(out, fmt.Sprintf("%s(%d)", m.Name, m.Number))
	}

	return strings.Join(out, " ")
}

// renderRanges writes every alternative as "min..max", a single value
// included, so that a one-value alternative and a range that happens to
// be one wide are not told apart by their spelling.
func renderRanges(ranges []smi.Range) string {
	out := make([]string, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, fmt.Sprintf("%d..%d", r.Min, r.Max))
	}

	return strings.Join(out, " ")
}

// expectation is one line of a fixture's expect.txt.
type expectation struct {
	line    int
	what    string
	subject string
	value   string
	pending bool
}

// checkExpectations reads the fixture's expectations and asserts them
// against the facts, then asserts the fixture raised only the
// diagnostics it declared.
func checkExpectations(t *testing.T, path string, facts map[string]string, set *smi.ModuleSet) {
	t.Helper()

	expectations, allowed := readExpectations(t, path)
	if len(expectations) == 0 {
		t.Fatalf("%s asserts nothing", path)
	}

	for _, e := range expectations {
		key := e.what + " " + e.subject

		got, ok := facts[key]
		if !ok {
			t.Errorf("%s:%d: nothing in the model answers %q — check the name and the assertion", path, e.line, key)

			continue
		}

		switch {
		case e.pending && got == e.value:
			t.Errorf("%s:%d: %s is now %q, which the pending line said it would not be. "+
				"The gap is closed: drop the pending marker.", path, e.line, key, got)
		case !e.pending && got != e.value:
			t.Errorf("%s:%d: %s = %q, want %q", path, e.line, key, got, e.value)
		}
	}

	checkDiagnostics(t, path, set, allowed)
}

// checkDiagnostics holds a semantic fixture to the diagnostics it
// declared. A fixture is written to be well-formed, so an undeclared
// diagnostic means either the fixture is wrong or the parser reads a
// well-formed construct as a deviation; both are findings.
func checkDiagnostics(t *testing.T, path string, set *smi.ModuleSet, allowed map[string]int) {
	t.Helper()

	got := map[string]int{}
	for _, d := range set.Diagnostics() {
		got[string(d.Code())]++
	}

	for code, want := range allowed {
		if got[code] != want {
			t.Errorf("%s: allow-diagnostic %s expects %d, got %d", path, code, want, got[code])
		}
	}

	for _, r := range set.Render() {
		if _, ok := allowed[string(r.Code)]; !ok {
			t.Errorf("%s: the fixture raised %s, which it does not declare: %s", path, r.Code, r)
		}
	}
}

// readExpectations parses one expect.txt. Comment lines carry the RFC
// citation an expectation rests on and are the reason the file is prose
// rather than a Go table.
func readExpectations(t *testing.T, path string) ([]expectation, map[string]int) {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var out []expectation
	allowed := map[string]int{}

	for i, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}

		e := expectation{line: i + 1}
		if fields[0] == "pending" {
			e.pending = true
			fields = fields[1:]
		}
		if len(fields) < 3 {
			t.Fatalf("%s:%d: %q is not \"<what> <subject> <value>\"", path, i+1, line)
		}

		e.what, e.subject, e.value = fields[0], fields[1], strings.Join(fields[2:], " ")

		if e.what == "allow-diagnostic" {
			n, err := strconv.Atoi(e.value)
			if err != nil {
				t.Fatalf("%s:%d: allow-diagnostic wants a count, got %q", path, i+1, e.value)
			}
			allowed[e.subject] = n

			continue
		}

		out = append(out, e)
	}

	return out, allowed
}

// sortedCodes returns the distinct diagnostic codes a load raised, in
// sorted order, which is the shape a fixture's expectations are compared
// against.
func sortedCodes(set *smi.ModuleSet) []string {
	seen := map[string]bool{}
	for _, d := range set.Diagnostics() {
		seen[string(d.Code())] = true
	}

	out := make([]string, 0, len(seen))
	for code := range seen {
		out = append(out, code)
	}
	sort.Strings(out)

	return out
}
