package smi_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/smi"
)

// writeMIB puts one module's source in dir under its own name, which is
// the spelling a search path finds first.
func writeMIB(t *testing.T, dir, name, body string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}

	return path
}

// loadIn loads the named modules out of dir, which is also the search
// path their imports are followed through.
func loadIn(t *testing.T, dir string, modules ...string) *smi.ModuleSet {
	t.Helper()

	set, err := smi.Load(modules, smi.Options{SearchPaths: []string{dir}})
	if err != nil {
		t.Fatalf("loading %v: %v", modules, err)
	}

	return set
}

// codeCount returns how many diagnostics carry code, which is what a
// no-cascade assertion measures.
func codeCount(set *smi.ModuleSet, code errs.Code) int {
	n := 0
	for _, d := range set.Diagnostics() {
		if d.Code() == code {
			n++
		}
	}

	return n
}

func hasCode(set *smi.ModuleSet, code errs.Code) bool { return codeCount(set, code) > 0 }

func TestOIDAssignmentComments(t *testing.T) {
	for _, tc := range []struct{ name, assignment string }{
		{"plain", "{ iso 3 6 1 }"},
		{"line comment", "{ iso 3 -- ignored arc words\n 6 1 }"},
		{"named arc comment", "{ iso org -- ignored words\n (3) 6 1 }"},
		{"parenthesized comment", "{ iso org( -- ignored words\n 3 ) 6 1 }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMIB(t, dir, "COMMENT-MIB", "COMMENT-MIB DEFINITIONS ::= BEGIN\nroot OBJECT IDENTIFIER ::= "+tc.assignment+"\nchild OBJECT IDENTIFIER ::= { root 7 }\nEND\n")
			set := loadIn(t, dir, "COMMENT-MIB")
			for name, want := range map[string]string{"root": "1.3.6.1", "child": "1.3.6.1.7"} {
				n := node(t, set, "COMMENT-MIB", name)
				if n.Unresolved || n.OID.String() != want {
					t.Errorf("%s: got OID %s, unresolved %v; want %s, false", name, n.OID, n.Unresolved, want)
				}
			}
			if n := codeCount(set, smi.ErrCodeUnresolvedDeclaration); n != 0 {
				t.Errorf("got %d unresolved diagnostics, want 0", n)
			}
		})
	}
}

// node fails the test rather than returning a nil that would panic three
// assertions later.
func node(t *testing.T, set *smi.ModuleSet, module, name string) *smi.Node {
	t.Helper()

	m, ok := set.Module(module)
	if !ok {
		t.Fatalf("module %s is not in the set", module)
	}
	n, ok := m.Node(name)
	if !ok {
		t.Fatalf("%s does not declare %s", module, name)
	}

	return n
}

const baseMIB = `BASE-MIB DEFINITIONS ::= BEGIN

enterprises OBJECT IDENTIFIER ::= { iso org(3) dod(6) internet(1) private(4) 1 }

END
`

const leafMIB = `LEAF-MIB DEFINITIONS ::= BEGIN

IMPORTS
    enterprises
        FROM BASE-MIB;

acme OBJECT IDENTIFIER ::= { enterprises 99 }

END
`

// Resolution is a pass over the AST rather than something the parser
// does as it reads, which is the whole reason a symbol may be defined in
// a file nothing has opened yet when the reference to it is parsed.
func TestResolveAcrossFilesWhateverOrderTheyLoadIn(t *testing.T) {
	dir := t.TempDir()
	base := writeMIB(t, dir, "BASE-MIB", baseMIB)
	leaf := writeMIB(t, dir, "LEAF-MIB", leafMIB)

	for _, order := range [][]string{{leaf, base}, {base, leaf}} {
		set, err := smi.LoadFiles(order, smi.Options{SearchPaths: []string{dir}})
		if err != nil {
			t.Fatalf("loading %v: %v", order, err)
		}

		acme := node(t, set, "LEAF-MIB", "acme")
		if got := acme.OID.String(); got != "1.3.6.1.4.1.99" {
			t.Errorf("acme = %s, want 1.3.6.1.4.1.99 (loaded %v first)", got, filepath.Base(order[0]))
		}
		if acme.Unresolved {
			t.Error("acme is marked unresolved")
		}
	}
}

func TestTrapOIDLengthIncludesNotificationSuffix(t *testing.T) {
	for _, enterpriseLength := range []int{smi.MaxOIDLength - 2, smi.MaxOIDLength - 1, smi.MaxOIDLength} {
		t.Run(fmt.Sprintf("enterprise-arcs-%d", enterpriseLength), func(t *testing.T) {
			dir := t.TempDir()
			writeMIB(t, dir, "TRAP-MIB", "TRAP-MIB DEFINITIONS ::= BEGIN\n"+
				"enterprise OBJECT IDENTIFIER ::= { iso"+strings.Repeat(" 1", enterpriseLength-1)+" }\n"+
				"alarm TRAP-TYPE\n"+
				"    ENTERPRISE enterprise\n"+
				"    DESCRIPTION \"An enterprise notification.\"\n"+
				"    ::= 1\nEND\n")

			set := loadIn(t, dir, "TRAP-MIB")
			enterprise := node(t, set, "TRAP-MIB", "enterprise")
			if enterprise.Unresolved || enterprise.OID.Len() != enterpriseLength {
				t.Fatalf("enterprise length = %d, unresolved = %v; want %d, false",
					enterprise.OID.Len(), enterprise.Unresolved, enterpriseLength)
			}

			alarm := node(t, set, "TRAP-MIB", "alarm")
			if enterpriseLength+2 > smi.MaxOIDLength {
				if !alarm.Unresolved || alarm.OID.Len() != 0 {
					t.Errorf("alarm length = %d, unresolved = %v; want 0, true",
						alarm.OID.Len(), alarm.Unresolved)
				}
				if got := codeCount(set, smi.ErrCodeUnresolvedDeclaration); got != 1 {
					t.Errorf("unresolved diagnostics = %d, want 1", got)
				}

				return
			}

			want := enterprise.OID.Child(0).Child(1)
			if alarm.Unresolved || alarm.OID.Compare(want) != 0 {
				t.Errorf("alarm OID = %s, unresolved = %v; want %s, false",
					alarm.OID, alarm.Unresolved, want)
			}
			if got := len(set.Diagnostics()); got != 0 {
				t.Errorf("diagnostics = %d, want 0", got)
			}
		})
	}
}

// A module named on the command line has to be there; one only an
// IMPORTS clause asked for is a diagnostic, because a corpus is full of
// imports nothing can satisfy.
func TestLoadReportsAMissingRootAndDiagnosesAMissingImport(t *testing.T) {
	dir := t.TempDir()
	writeMIB(t, dir, "LEAF-MIB", leafMIB)

	if _, err := smi.Load([]string{"NOT-THERE"}, smi.Options{SearchPaths: []string{dir}}); err == nil {
		t.Fatal("loading a module that is not on the search path returned no error")
	}

	set := loadIn(t, dir, "LEAF-MIB")
	if !hasCode(set, smi.ErrCodeModuleNotFound) {
		t.Error("an IMPORTS clause naming an absent module raised no diagnostic")
	}
	if node(t, set, "LEAF-MIB", "acme").Unresolved != true {
		t.Error("acme resolved without the module that defines its parent")
	}
}

// An IMPORTS cycle costs the load nothing. It is diagnosed, the back
// edge leaves the dependency order, and every declaration either module
// makes still resolves.
func TestImportCycleIsBrokenAtTheBackEdge(t *testing.T) {
	dir := t.TempDir()
	writeMIB(t, dir, "A-MIB", `A-MIB DEFINITIONS ::= BEGIN

IMPORTS
    bRoot
        FROM B-MIB;

aRoot OBJECT IDENTIFIER ::= { iso 3 6 1 4 1 100 }

aLeaf OBJECT IDENTIFIER ::= { bRoot 1 }

END
`)
	writeMIB(t, dir, "B-MIB", `B-MIB DEFINITIONS ::= BEGIN

IMPORTS
    aRoot
        FROM A-MIB;

bRoot OBJECT IDENTIFIER ::= { aRoot 2 }

END
`)

	set := loadIn(t, dir, "A-MIB")

	if !hasCode(set, smi.ErrCodeImportCycle) {
		t.Fatal("a two-module cycle raised no diagnostic")
	}
	if got := codeCount(set, smi.ErrCodeImportCycle); got != 1 {
		t.Errorf("cycle diagnostics = %d, want exactly one back edge", got)
	}

	edges := 0
	for _, m := range set.Modules() {
		for _, imp := range m.Imports {
			if imp.BackEdge {
				edges++
			}
		}
	}
	if edges != 1 {
		t.Errorf("back edges = %d, want 1", edges)
	}

	if got := node(t, set, "B-MIB", "bRoot").OID.String(); got != "1.3.6.1.4.1.100.2" {
		t.Errorf("bRoot = %s, want 1.3.6.1.4.1.100.2", got)
	}
	if got := node(t, set, "A-MIB", "aLeaf").OID.String(); got != "1.3.6.1.4.1.100.2.1" {
		t.Errorf("aLeaf = %s, want 1.3.6.1.4.1.100.2.1", got)
	}
	if len(set.Order()) != 2 {
		t.Errorf("dependency order = %v, want both modules", set.Order())
	}
}

const tcMIB = `TC-MIB DEFINITIONS ::= BEGIN

DisplayString ::= TEXTUAL-CONVENTION
    DISPLAY-HINT "255a"
    STATUS       current
    DESCRIPTION  "An ASCII string."
    SYNTAX       OCTET STRING (SIZE (0..255))

END
`

// A symbol used without an import resolves and is graded, because a
// vendor corpus writes far more of these than a strict reading could
// afford to drop.
func TestWellKnownTypeUsedWithoutAnImportResolves(t *testing.T) {
	dir := t.TempDir()
	writeMIB(t, dir, "TC-MIB", tcMIB)
	writeMIB(t, dir, "USER-MIB", `USER-MIB DEFINITIONS ::= BEGIN

sysName OBJECT-TYPE
    SYNTAX      DisplayString
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "The name."
    ::= { iso 3 6 1 2 1 1 5 }

END
`)

	set, err := smi.LoadFiles(
		[]string{filepath.Join(dir, "USER-MIB"), filepath.Join(dir, "TC-MIB")},
		smi.Options{SearchPaths: []string{dir}},
	)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	n := node(t, set, "USER-MIB", "sysName")
	if n.Unresolved {
		t.Fatal("sysName is unresolved")
	}
	if n.Type == nil || n.Type.Name != "DisplayString" || n.Type.Base != smi.BaseOctetString {
		t.Fatalf("sysName type = %+v, want DisplayString over OCTET STRING", n.Type)
	}
	if n.Type.DisplayHint != "255a" {
		t.Errorf("display hint = %q, want 255a", n.Type.DisplayHint)
	}
	if !hasCode(set, smi.ErrCodeMissingImport) {
		t.Error("a type used without an import raised no diagnostic")
	}
}

// A declaration missing a clause a renderer reads stays unresolved. It
// must not come back carrying a defaulted type, because a defaulted type
// renders as if somebody had written it.
func TestDeclarationMissingARequiredClauseStaysUnresolved(t *testing.T) {
	dir := t.TempDir()
	writeMIB(t, dir, "GAP-MIB", `GAP-MIB DEFINITIONS ::= BEGIN

whole OBJECT-TYPE
    SYNTAX      Integer32
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "Has everything."
    ::= { iso 3 6 1 4 1 200 1 }

noDescription OBJECT-TYPE
    SYNTAX      Integer32
    MAX-ACCESS  read-only
    STATUS      current
    ::= { iso 3 6 1 4 1 200 2 }

END
`)

	set := loadIn(t, dir, "GAP-MIB")

	if n := node(t, set, "GAP-MIB", "whole"); n.Unresolved {
		t.Error("the complete declaration is marked unresolved")
	}

	gap := node(t, set, "GAP-MIB", "noDescription")
	if !gap.Unresolved {
		t.Fatal("a declaration missing DESCRIPTION resolved")
	}
	if gap.Type != nil {
		t.Errorf("unresolved declaration carries type %+v; nothing may default it", gap.Type)
	}
	if !hasCode(set, smi.ErrCodeMissingClause) {
		t.Error("the missing clause raised no diagnostic")
	}
}

// One name nothing defines costs one diagnostic per declaration that
// wanted it, never one per place the name is written.
//
// The dependents are marked and reported, and they are also placed: the
// declaration they hang off wrote the value it was assigned, so what
// they are waiting on is the clause it lost and not the arc they were
// registered at. Withholding the arc as well would take a whole subtree
// out of the tree over a DESCRIPTION one arc up, and a consumer walking
// the tree could not then tell it from a subtree the MIB never wrote.
func TestUnresolvedReferenceDoesNotCascade(t *testing.T) {
	const dependents = 20

	var b strings.Builder
	b.WriteString("DEP-MIB DEFINITIONS ::= BEGIN\n\n")
	b.WriteString("broken OBJECT-IDENTITY\n    ::= { iso 3 6 1 4 1 300 }\n\n")
	for i := 1; i <= dependents; i++ {
		fmt.Fprintf(&b, "dep%d OBJECT IDENTIFIER ::= { broken %d }\n\n", i, i)
	}
	b.WriteString("END\n")

	dir := t.TempDir()
	writeMIB(t, dir, "DEP-MIB", b.String())

	set := loadIn(t, dir, "DEP-MIB")

	if got := codeCount(set, smi.ErrCodeUnresolvedDeclaration); got != dependents {
		t.Errorf("unresolved-declaration diagnostics = %d, want %d (one per dependent)", got, dependents)
	}

	broken := node(t, set, "DEP-MIB", "broken")
	if !broken.Unresolved {
		t.Error("the declaration that failed to parse is not marked unresolved")
	}

	if broken.OID.String() != "1.3.6.1.4.1.300" {
		t.Errorf("the withheld declaration is registered at %q, want its ::= value", broken.OID)
	}

	for i := 1; i <= dependents; i++ {
		name := fmt.Sprintf("dep%d", i)
		n := node(t, set, "DEP-MIB", name)
		if !n.Unresolved {
			t.Errorf("%s resolved against a declaration that did not", name)
		}

		want := fmt.Sprintf("1.3.6.1.4.1.300.%d", i)
		if n.OID.String() != want {
			t.Errorf("%s is registered at %q, want %q", name, n.OID, want)
		}
		if _, placed := set.Node(n.OID); !placed {
			t.Errorf("%s is not in the tree, so nothing walking it can see what fell", name)
		}
	}
}

const tableMIB = `TABLE-MIB DEFINITIONS ::= BEGIN

ifTable OBJECT-TYPE
    SYNTAX      SEQUENCE OF IfEntry
    MAX-ACCESS  not-accessible
    STATUS      current
    DESCRIPTION "A list of interfaces."
    ::= { iso 3 6 1 2 1 2 2 }

ifEntry OBJECT-TYPE
    SYNTAX      IfEntry
    MAX-ACCESS  not-accessible
    STATUS      current
    DESCRIPTION "One interface."
    INDEX       { ifIndex }
    ::= { ifTable 1 }

IfEntry ::= SEQUENCE {
    ifIndex Integer32,
    ifDescr OCTET STRING
}

ifIndex OBJECT-TYPE
    SYNTAX      Integer32 (1..2147483647)
    MAX-ACCESS  not-accessible
    STATUS      current
    DESCRIPTION "The index."
    ::= { ifEntry 1 }

ifDescr OBJECT-TYPE
    SYNTAX      OCTET STRING
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "The description."
    ::= { ifEntry 2 }

END
`

// A table's shape is read off the OID tree. Index structure is not
// computed at all: index decoding is generic at runtime, so the columns
// come back with the access their declarations gave them and the INDEX
// clause comes back as the names the source wrote.
func TestTableAssemblesWithoutComputingIndexStructure(t *testing.T) {
	dir := t.TempDir()
	writeMIB(t, dir, "TABLE-MIB", tableMIB)

	set := loadIn(t, dir, "TABLE-MIB")
	m, _ := set.Module("TABLE-MIB")

	if len(m.Tables) != 1 {
		t.Fatalf("tables = %d, want 1", len(m.Tables))
	}

	tbl := m.Tables[0]
	if tbl.Node.Name != "ifTable" || tbl.Node.Kind != smi.NodeTable {
		t.Fatalf("table node = %s/%s, want ifTable/table", tbl.Node.Name, tbl.Node.Kind)
	}
	if tbl.Row == nil || tbl.Row.Name != "ifEntry" || tbl.Row.Kind != smi.NodeRow {
		t.Fatalf("row = %+v, want ifEntry/row", tbl.Row)
	}

	var names []string
	for _, c := range tbl.Columns {
		if c.Kind != smi.NodeColumn {
			t.Errorf("%s is a %s, want column", c.Name, c.Kind)
		}
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "ifIndex,ifDescr" {
		t.Errorf("columns = %v, want [ifIndex ifDescr] in OID order", names)
	}

	if got := node(t, set, "TABLE-MIB", "ifIndex").Access; got != smi.AccessNotAccessible {
		t.Errorf("ifIndex access = %s, want not-accessible", got)
	}
	if len(tbl.Index) != 1 || tbl.Index[0].Name != "ifIndex" || tbl.Index[0].Implied {
		t.Errorf("index = %+v, want the single name ifIndex with no IMPLIED", tbl.Index)
	}
	if got := node(t, set, "TABLE-MIB", "ifDescr").OID.String(); got != "1.3.6.1.2.1.2.2.1.2" {
		t.Errorf("ifDescr = %s, want 1.3.6.1.2.1.2.2.1.2", got)
	}
}

const refinementMIB = `REFINE-MIB DEFINITIONS ::= BEGIN

acme OBJECT IDENTIFIER ::= { iso 3 6 1 4 1 47100 }

Switch ::= TEXTUAL-CONVENTION
    STATUS      current
    DESCRIPTION "Whether a thing is on, off, or between the two."
    SYNTAX      INTEGER { on(1), off(2), unknown(3) }

narrowed OBJECT-TYPE
    SYNTAX      Switch { on(1), off(2) }
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "Restricts the convention to two of its three values."
    ::= { acme 1 }

renumbered OBJECT-TYPE
    SYNTAX      Switch { off(0), on(1) }
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "Gives off a number the convention does not give it."
    ::= { acme 2 }

END
`

// RFC 2579 section 3.5 lets a SYNTAX clause restrict the enumeration of
// the textual convention it names, so the object's own members are what
// it reports. Reporting the convention's whole set would hand a renderer
// values the object was written to exclude.
func TestEnumerationRefinementReportsWhatTheObjectWrote(t *testing.T) {
	dir := t.TempDir()
	writeMIB(t, dir, "REFINE-MIB", refinementMIB)

	set := loadIn(t, dir, "REFINE-MIB")

	n := node(t, set, "REFINE-MIB", "narrowed")
	if n.Unresolved {
		t.Error("a narrowing the RFC allows left the declaration unresolved")
	}
	if n.Type == nil {
		t.Fatal("the refining declaration has no type")
	}
	if got := members(n.Type); got != "on=1 off=2" {
		t.Errorf("members = %q, want %q", got, "on=1 off=2")
	}
	if n.Type.Name != "Switch" {
		t.Errorf("type name = %q, want the convention it refines", n.Type.Name)
	}

	if tc, ok := set.Type("Switch"); !ok || members(tc) != "on=1 off=2 unknown=3" {
		t.Error("refining an enumeration changed the convention it refines")
	}
}

// A refinement may narrow the set of values and may not renumber it: the
// two readings decode the same wire value differently, and neither can
// be preferred. The members stay as the source wrote them so a reader
// can see which two definitions disagree, and the declaration is left
// unresolved so nothing renders either reading as the author's.
func TestEnumerationRenumberingLeavesTheDeclarationUnresolved(t *testing.T) {
	dir := t.TempDir()
	writeMIB(t, dir, "REFINE-MIB", refinementMIB)

	set := loadIn(t, dir, "REFINE-MIB")

	n := node(t, set, "REFINE-MIB", "renumbered")
	if !n.Unresolved {
		t.Error("a declaration that renumbers the convention it names resolved")
	}
	if n.Type == nil || !n.Type.Unresolved {
		t.Fatalf("type = %+v, want one marked unresolved", n.Type)
	}
	if got := members(n.Type); got != "off=0 on=1" {
		t.Errorf("members = %q, want what the source wrote", got)
	}

	if got := codeCount(set, smi.ErrCodeEnumerationRefinementConflict); got != 1 {
		t.Errorf("refinement-conflict diagnostics = %d, want 1 (off, not on)", got)
	}
}

// members renders a type's named numbers in source order.
func members(t *smi.Type) string {
	out := make([]string, 0, len(t.Members))
	for _, m := range t.Members {
		out = append(out, fmt.Sprintf("%s=%d", m.Name, m.Number))
	}

	return strings.Join(out, " ")
}

// Two descriptors at one OBJECT IDENTIFIER value is RFC 2578 section
// 3.6's own prohibition, and the parser has to keep one of them. It
// keeps the one a subtree is read through: dropping the conceptual table
// here would leave its row and its columns classified as scalars, which
// costs a whole subtree over a collision one arc up.
func TestDuplicateOIDKeepsTheClaimantThatCarriesTheShape(t *testing.T) {
	set := malformedSet(t, "duplicate-oid")

	tbl := node(t, set, "BAD-MIB", "acmeSlotTable")
	got, ok := set.Node(tbl.OID)
	if !ok || got != tbl {
		t.Fatalf("1.3.6.1.4.1.47100.1 resolves to %v, want the conceptual table", got)
	}

	if row := node(t, set, "BAD-MIB", "acmeSlotEntry"); row.Kind != smi.NodeRow {
		t.Errorf("acmeSlotEntry is a %s, want row", row.Kind)
	}
	if col := node(t, set, "BAD-MIB", "acmeSlotIndex"); col.Kind != smi.NodeColumn {
		t.Errorf("acmeSlotIndex is a %s, want column", col.Kind)
	}

	// The loser is reported, not damaged: it wrote everything its own
	// macro asks for, and what it lost is a registration the RFC never
	// let two declarations share.
	loser := node(t, set, "BAD-MIB", "acmeSlot")
	if loser.Unresolved {
		t.Error("the losing claimant is marked unresolved though nothing about it failed to resolve")
	}
	if loser.OID.String() != tbl.OID.String() {
		t.Errorf("the losing claimant lost its OID as well: %q", loser.OID)
	}
	if !hasCode(set, smi.ErrCodeDuplicateOID) {
		t.Error("two claimants of one OID raised no diagnostic")
	}
}

// The unqualified lookup's order is a contract, because which definition
// wins decides what a renderer emits. Base types come first under the
// spellings RFC 2578 gives them, then module-declared types in module
// name order.
func TestUnqualifiedTypeLookupOrderIsFirstMatchWins(t *testing.T) {
	dir := t.TempDir()
	writeMIB(t, dir, "AAA-MIB", `AAA-MIB DEFINITIONS ::= BEGIN

Shared ::= TEXTUAL-CONVENTION
    STATUS      current
    DESCRIPTION "Declared by the alphabetically first module."
    SYNTAX      Integer32

Counter32 ::= TEXTUAL-CONVENTION
    STATUS      current
    DESCRIPTION "A module may write this; it cannot change the wire."
    SYNTAX      OCTET STRING

END
`)
	writeMIB(t, dir, "ZZZ-MIB", `ZZZ-MIB DEFINITIONS ::= BEGIN

Shared ::= TEXTUAL-CONVENTION
    STATUS      current
    DESCRIPTION "Declared by the alphabetically last module."
    SYNTAX      OCTET STRING

END
`)

	set, err := smi.LoadFiles(
		[]string{filepath.Join(dir, "ZZZ-MIB"), filepath.Join(dir, "AAA-MIB")},
		smi.Options{SearchPaths: []string{dir}},
	)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	shared, ok := set.Type("Shared")
	if !ok {
		t.Fatal("Shared did not resolve")
	}
	if shared.Module != "AAA-MIB" {
		t.Errorf("Shared came from %s, want AAA-MIB: modules are searched in name order", shared.Module)
	}

	counter, ok := set.Type("Counter32")
	if !ok {
		t.Fatal("Counter32 did not resolve")
	}
	if counter.Base != smi.BaseCounter32 || counter.Module != "" {
		t.Errorf("Counter32 = %+v, want the base type, which no module declares", counter)
	}

	aaa, _ := set.Module("AAA-MIB")
	own, ok := aaa.Type("Counter32")
	if !ok || own.Base != smi.BaseOctetString {
		t.Errorf("the module-qualified lookup lost the module's own Counter32: %+v", own)
	}
}

// A load holds nothing between calls, so two loads of the same files
// produce equal models and neither can observe the other.
func TestTwoLoadsOfTheSameFilesAreEqual(t *testing.T) {
	dir := t.TempDir()
	writeMIB(t, dir, "BASE-MIB", baseMIB)
	writeMIB(t, dir, "LEAF-MIB", leafMIB)
	writeMIB(t, dir, "TABLE-MIB", tableMIB)

	first := digest(loadIn(t, dir, "LEAF-MIB", "TABLE-MIB"))
	second := digest(loadIn(t, dir, "LEAF-MIB", "TABLE-MIB"))

	if first != second {
		t.Error("two independent loads of the same files produced different models")
	}
}

// A descriptor the lexer had to end early leaves a fragment where a
// name stood, and a fragment renders exactly like a name somebody
// meant: here two members would both come out called "ready". The
// declaration keeps what parsed and is never resolved, so a renderer
// reading only the model cannot emit the fragment as the real name.
func TestATruncatedNameLeavesItsDeclarationUnresolved(t *testing.T) {
	dir := t.TempDir()
	writeMIB(t, dir, "TRUNC-MIB", "TRUNC-MIB DEFINITIONS ::= BEGIN\n\n"+
		"acme OBJECT IDENTIFIER ::= { iso 3 6 1 4 1 101 }\n\n"+
		"cipherState OBJECT-TYPE\n"+
		"    SYNTAX      INTEGER { ready(1), not\xffready(2) }\n"+
		"    MAX-ACCESS  read-only\n"+
		"    STATUS      current\n"+
		"    DESCRIPTION \"One state.\"\n"+
		"    ::= { acme 1 }\n\n"+
		"cipherCount OBJECT-TYPE\n"+
		"    SYNTAX      Integer32\n"+
		"    MAX-ACCESS  read-only\n"+
		"    STATUS      current\n"+
		"    DESCRIPTION \"How many.\"\n"+
		"    ::= { acme 2 }\n\n"+
		"END\n")

	set := loadIn(t, dir, "TRUNC-MIB")

	damaged := node(t, set, "TRUNC-MIB", "cipherState")
	if !damaged.Unresolved {
		t.Errorf("cipherState resolved with members %v, though a name in it was cut short", damaged.Type.Members)
	}
	if got := damaged.OID.String(); got != "1.3.6.1.4.1.101.1" {
		t.Errorf("cipherState OID = %s, want it kept alongside the mark", got)
	}

	if whole := node(t, set, "TRUNC-MIB", "cipherCount"); whole.Unresolved {
		t.Error("cipherCount fell with the declaration beside it")
	}
}

// digest renders everything a consumer reads, so a comparison over it
// catches an ordering difference as well as a content one.
func digest(set *smi.ModuleSet) string {
	var b strings.Builder

	for _, m := range set.Modules() {
		fmt.Fprintf(&b, "module %s %s\n", m.Name, m.Dialect)
		for _, imp := range m.Imports {
			fmt.Fprintf(&b, "  import %s %v %v\n", imp.Module, imp.Symbols, imp.BackEdge)
		}
		for _, n := range m.Nodes {
			fmt.Fprintf(&b, "  node %s %s %s %s %s %t\n",
				n.Name, n.OID, n.Kind, n.Access, n.Status, n.Unresolved)
			if n.Type != nil {
				fmt.Fprintf(&b, "    type %s %s %v %v %v\n",
					n.Type.Name, n.Type.Base, n.Type.Members, n.Type.Ranges, n.Type.Sizes)
			}
			for _, part := range n.Index {
				fmt.Fprintf(&b, "    index %s %t\n", part.Name, part.Implied)
			}
		}
		for _, ty := range m.Types {
			fmt.Fprintf(&b, "  type %s %s %q\n", ty.Name, ty.Base, ty.DisplayHint)
		}
		for _, tbl := range m.Tables {
			fmt.Fprintf(&b, "  table %s row %v\n", tbl.Node.Name, tbl.Row != nil)
			for _, c := range tbl.Columns {
				fmt.Fprintf(&b, "    column %s %s\n", c.Name, c.OID)
			}
		}
	}

	fmt.Fprintf(&b, "order %v\n", set.Order())
	for _, r := range set.Render() {
		fmt.Fprintf(&b, "diag %s\n", r)
	}

	return b.String()
}
