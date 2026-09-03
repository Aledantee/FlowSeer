package smi_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// The malformed fixtures are the other half of the oracle. Each one
// writes a single construct the RFCs do not allow, and asserts the exact
// set of diagnostic codes the parser reports for it — no more and no
// fewer.
//
// Exactness is the whole point. A fixture that asserted "at least this
// code" would pass while the parser also reported four cascading
// conditions the construct never caused, and a leniency claim rests on
// the cascade not happening: a malformed declaration is supposed to cost
// that declaration and nothing around it.
//
// The catalog is the specification of what the parser grades, so the
// suite is complete when every code in it has a fixture. TestCoverage in
// coverage_test.go is what enforces that; here each fixture stands on
// its own.

const malformedDir = "testdata/malformed"

// TestMalformedFixtures checks that each fixture raises exactly the
// codes its expect.txt names.
func TestMalformedFixtures(t *testing.T) {
	for _, dir := range fixtureCases(t, malformedDir) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			want := readMalformedExpect(t, dir)
			if len(want) == 0 {
				t.Fatalf("%s asserts no diagnostic code, so it proves nothing", dir)
			}

			got := sortedCodes(loadFixtureDir(t, dir))
			if slices.Equal(got, want) {
				return
			}

			for _, code := range got {
				if !slices.Contains(want, code) {
					t.Errorf("raised %s, which the fixture does not expect", code)
				}
			}
			for _, code := range want {
				if !slices.Contains(got, code) {
					t.Errorf("expected %s, which the fixture did not raise", code)
				}
			}
			t.Logf("got %v, want %v", got, want)
		})
	}
}

// readMalformedExpect returns the codes a fixture's expect.txt names,
// sorted so a comparison against a load's codes is order-independent.
//
// The prose beside them is the reason the file is a file rather than a
// Go table: an expectation without its RFC citation is a claim nobody
// can check, and a comment in a fixture sits where the next reader is
// already looking.
func readMalformedExpect(t *testing.T, dir string) []string {
	t.Helper()

	path := filepath.Join(dir, "expect.txt")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var out []string
	for i, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if fields[0] != "code" || len(fields) != 2 {
			t.Fatalf("%s:%d: %q is not \"code smi/<name>\"", path, i+1, line)
		}
		out = append(out, fields[1])
	}

	slices.Sort(out)

	return out
}

// malformedSet loads one fixture by name for a pinning test.
func malformedSet(t *testing.T, name string) *smi.ModuleSet {
	t.Helper()

	return loadFixtureDir(t, filepath.Join(malformedDir, name))
}

// The tests below pin deliberate tolerances: places where the parser
// knowingly reads something the RFCs forbid rather than dropping it.
// Each one exists so that a later reader who finds the looseness and
// takes it for an oversight has to delete a test that says why it is
// there. Two more live beside the lexer, in internal/lex/lex_test.go,
// for the underscore in a descriptor and for Windows-1252 curly quotes
// used as string delimiters.

// RFC 2578 section 7 fixes the OBJECT-TYPE clause order, and a
// declaration that writes STATUS after DESCRIPTION has still said
// exactly one thing. Refusing it would cost a whole object over the
// order two clauses were typed in, so the order is graded and the
// declaration kept whole.
func TestMalformedOutOfOrderClauseKeepsItsDeclarationThoughRFC2578FixesTheOrder(t *testing.T) {
	set := malformedSet(t, "clause-out-of-order")

	n := node(t, set, "BAD-MIB", "shuffled")
	if n.Unresolved {
		t.Fatal("a declaration whose clauses were typed out of order was thrown away")
	}
	if n.Status != smi.StatusCurrent {
		t.Errorf("status = %s, want current: the out-of-order clause is still a clause", n.Status)
	}
	if n.Description == "" {
		t.Error("the description was dropped along with the order")
	}
}

// RFC 2578 section 7 gives OBJECT-TYPE one DESCRIPTION, and a reader has
// to pick when a module writes two. Keeping the first is the only choice
// a reader can predict, so the second is graded and dropped rather than
// costing the declaration.
func TestMalformedDuplicateClauseKeepsTheFirstThoughRFC2578AllowsOne(t *testing.T) {
	set := malformedSet(t, "duplicate-clause")

	n := node(t, set, "BAD-MIB", "twiceDescribed")
	if n.Unresolved {
		t.Fatal("a declaration with a repeated clause was thrown away")
	}
	if got, want := n.Description, "The first description."; got != want {
		t.Errorf("description = %q, want %q: the first occurrence wins", got, want)
	}
}

// RFC 1212 section 4 defines STATUS mandatory and RFC 2578 section 7.4
// does not, so a module read as SMIv2 that writes it has used the other
// dialect's word. What its author meant is not in doubt, so the value is
// kept and the mismatch graded; dropping it would lose an object over a
// vocabulary the file was probably converted from.
func TestMalformedDialectValueIsKeptThoughItBelongsToTheOtherSMI(t *testing.T) {
	set := malformedSet(t, "dialect-value-mismatch")

	n := node(t, set, "BAD-MIB", "wrongStatus")
	if n.Status != smi.StatusMandatory {
		t.Errorf("status = %s, want mandatory: the value is kept as written", n.Status)
	}
	if n.OID.String() == "" {
		t.Error("the object lost its place in the tree over a status word")
	}
}

// RFC 2578 section 11.1 has range alternatives name disjoint sets, and
// two that overlap still describe the union their author wrote. Both
// alternatives are kept because dropping either would narrow the set the
// object may carry.
func TestMalformedOverlappingRangeKeepsBothAlternativesThoughRFC2578ForbidsThem(t *testing.T) {
	set := malformedSet(t, "overlapping-range")

	n := node(t, set, "BAD-MIB", "overlapping")
	if n.Type == nil {
		t.Fatal("the object lost its type over an overlapping range")
	}
	if got := renderRanges(n.Type.Ranges); got != "1..10 5..20" {
		t.Errorf("ranges = %q, want both alternatives as written", got)
	}
}

// RFC 2578 section 7.9 writes an OBJECT IDENTIFIER default as a
// descriptor, and the sub-identifier list these fixtures use is not in
// the grammar at either brace depth. It says plainly which node it
// means, so the declaration is graded and kept.
//
// The value is asserted as well as the grading. At one brace level the
// list is the shape an integer default also takes, and reading only its
// first number returns 1 where the source wrote 1.3.6.1 — a wrong value
// with nothing said about it, which is worse than either dropping the
// object or reading the list.
func TestMalformedOIDDefaultAsSubIdentifiersKeepsItsDeclarationThoughRFC2578HasNoSuchForm(t *testing.T) {
	for _, fixture := range []string{"non-conforming-oid-default", "non-conforming-oid-default-one-brace"} {
		t.Run(fixture, func(t *testing.T) {
			n := node(t, malformedSet(t, fixture), "BAD-MIB", "pointerDefault")
			if n.Unresolved {
				t.Fatal("an object was thrown away over the spelling of its default")
			}
			if n.Type == nil || n.Type.Base != smi.BaseObjectIdentifier {
				t.Errorf("type = %+v, want OBJECT IDENTIFIER", n.Type)
			}
			if got := renderDefault(n.Default); got != "oid 1.3.6.1" {
				t.Errorf("default = %q, want %q", got, "oid 1.3.6.1")
			}
		})
	}
}

// RFC 2578 section 3.2 has a module import every descriptor it uses from
// elsewhere. The corpus writes the omission by the thousand, so the
// symbol resolves against the loaded modules and the missing import is
// graded: a strict reading here would drop far more definitions than it
// would report.
func TestMalformedMissingImportStillResolvesThoughRFC2578RequiresTheImport(t *testing.T) {
	set := malformedSet(t, "missing-import")

	n := node(t, set, "USER-MIB", "sysName")
	if n.Unresolved {
		t.Fatal("an object using an unimported type was left unresolved")
	}
	if n.Type == nil || n.Type.Name != "DisplayString" || n.Type.Base != smi.BaseOctetString {
		t.Errorf("type = %+v, want DisplayString over OCTET STRING", n.Type)
	}
}

// Two modules importing from each other leave the load with no acyclic
// order, and nothing else. Both still define everything they define, so
// the back edge leaves the dependency order alone and every declaration
// in both modules still resolves.
func TestMalformedImportCycleKeepsBothModulesThoughItHasNoAcyclicOrder(t *testing.T) {
	set := malformedSet(t, "import-cycle")

	if got := node(t, set, "B-MIB", "bRoot").OID.String(); got != "1.3.6.1.4.1.47100.2" {
		t.Errorf("bRoot = %s, want 1.3.6.1.4.1.47100.2", got)
	}
	if got := node(t, set, "A-MIB", "aLeaf").OID.String(); got != "1.3.6.1.4.1.47100.2.1" {
		t.Errorf("aLeaf = %s, want 1.3.6.1.4.1.47100.2.1", got)
	}
	if len(set.Order()) != 2 {
		t.Errorf("dependency order = %v, want both modules in it", set.Order())
	}
}

// RFC 2578 section 3.7 ends a module at END, and a trailer after the
// last one belongs to no module. It is dropped rather than costing the
// file, because text after a module says nothing about the module.
func TestMalformedContentAfterEndKeepsTheModuleBeforeItThoughItBelongsToNoModule(t *testing.T) {
	set := malformedSet(t, "content-after-end")

	n := node(t, set, "BAD-MIB", "plain")
	if n.Unresolved {
		t.Fatal("a declaration fell because of text after the module's END")
	}
	if got := n.OID.String(); got != "1.3.6.1.4.1.47100.1" {
		t.Errorf("plain = %s, want 1.3.6.1.4.1.47100.1", got)
	}
}

// RFC 2578 section 3.7 ends a comment at a second pair of hyphens or at
// the end of the line. A file written for the paired rule frames into
// nothing under the end-of-line one, so the reading that recovers the
// declarations wins and records which rule produced the result.
func TestMalformedPairedCommentReparseKeepsTheDeclarationsThoughEndOfLineIsTheDefault(t *testing.T) {
	set := malformedSet(t, "paired-comment-mode")

	if got := node(t, set, "BAD-MIB", "widget").OID.String(); got != "1.3.6.1.4.1.47100" {
		t.Errorf("widget = %s, want 1.3.6.1.4.1.47100", got)
	}
	if got := node(t, set, "BAD-MIB", "gadget").OID.String(); got != "1.3.6.1.4.1.47100.1" {
		t.Errorf("gadget = %s, want 1.3.6.1.4.1.47100.1", got)
	}
}

// RFC 2578 section 3.1 lets a descriptor hold hyphens and not end in
// one. Reading the trailing hyphen as the start of a comment would lose
// the rest of the declaration, so the name is taken whole and graded.
func TestMalformedTrailingHyphenNameIsReadWholeThoughRFC2578ForbidsIt(t *testing.T) {
	set := malformedSet(t, "trailing-hyphen-identifier")

	n := node(t, set, "BAD-MIB", "acme-")
	if got := n.OID.String(); got != "1.3.6.1.4.1.47100" {
		t.Errorf("acme- = %s, want the arc its declaration assigns", got)
	}
}

// RFC 2578 section 3.1 leaves the underscore out of a descriptor and the
// corpus writes it anyway. Ending the name at the underscore costs the
// declaration every member after it, so the resolved type has to carry
// both members with the numbers they were declared with.
func TestMalformedUnderscoreDescriptorResolvesItsMembersThoughRFC2578ForbidsIt(t *testing.T) {
	set := malformedSet(t, "underscore-in-descriptor")

	n := node(t, set, "BAD-MIB", "cipherSuite")
	if n.Type == nil {
		t.Fatal("the object lost its type over an underscore")
	}
	if got := renderMembers(n.Type.Members); got != "tls_rsa(1) tls_dhe(2)" {
		t.Errorf("members = %q, want both names whole", got)
	}
}

// RFC 2578 section 3.7 delimits a text value with quotation marks, and
// one IEEE MIB in the corpus arrives with every string re-quoted into
// Windows-1252 by a word processor. Reading those bytes as delimiters is
// what keeps the file's declarations; the lexer's own pinning test
// covers the tokens, and this one covers the declaration that survives.
func TestMalformedCurlyQuotedDescriptionKeepsItsDeclarationThoughSMIWritesASCIIQuotes(t *testing.T) {
	set := malformedSet(t, "curly-quoted-string")

	n := node(t, set, "BAD-MIB", "quoted")
	if n.Unresolved {
		t.Fatal("a declaration fell because a word processor re-quoted its DESCRIPTION")
	}
	if n.Description == "" {
		t.Error("the curly-quoted description did not reach the model")
	}
}
