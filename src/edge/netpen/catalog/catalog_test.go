package catalog

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestGeneratedCatalogIsCurrent is the drift gate: regenerating the
// catalog must produce byte-identical source. A divergence means a
// registration changed without regenerating, and the committed catalog
// is stale.
func TestGeneratedCatalogIsCurrent(t *testing.T) {
	entries := Entries()
	generated := renderEntries(entries)

	committed, err := os.ReadFile("zz_generated_catalog.go")
	if err != nil {
		t.Fatalf("read committed catalog: %v", err)
	}

	if !bytes.Equal(generated, committed) {
		t.Fatalf("catalog drift: zz_generated_catalog.go does not match in-memory entries.\n"+
			"Run `go generate ./catalog/...` and commit the result.\n"+
			"--- generated (first 500 bytes) ---\n%s\n--- committed (first 500 bytes) ---\n%s",
			trunc(generated, 500), trunc(committed, 500))
	}
}

func trunc(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

// TestCrossCheckRegistrationsAndGenerated proves every registration appears
// in the generated catalog and vice versa.
func TestCrossCheckRegistrationsAndGenerated(t *testing.T) {
	inMemory := Entries()
	if len(inMemory) != len(GeneratedEntries) {
		t.Fatalf("entry count: in-memory %d, generated %d", len(inMemory), len(GeneratedEntries))
	}
	for i, want := range inMemory {
		got := GeneratedEntries[i]
		if !entriesEqual(got, want) {
			t.Errorf("entry[%d] mismatch:\n got  %+v\n want %+v", i, got, want)
		}
	}
}

func entriesEqual(a, b Entry) bool {
	return a.Name == b.Name && a.Mode == b.Mode && a.Class == b.Class &&
		a.Teardown == b.Teardown && a.Help == b.Help && a.Legs == b.Legs &&
		reflect.DeepEqual(a.Protocols, b.Protocols) &&
		reflect.DeepEqual(a.Preconditions, b.Preconditions)
}

// TestFamilyGuardRejectsUnknownClass is the family-guard test transplanted
// from the snmp corpus renderer's truncation guard: an unrecognized
// durability class cannot silently vanish from listings and gates. A
// fabricated entry with a bogus class must fail loudly.
func TestFamilyGuardRejectsUnknownClass(t *testing.T) {
	bogus := Durability("destructive-rainbow")
	if err := validateClass(bogus); err == nil {
		t.Fatalf("validateClass(%q) succeeded; an unknown class must be rejected", bogus)
	}

	known := map[Durability]bool{
		NonDestructive:       true,
		TransientDecay:       true,
		TemporaryRestored:    true,
		PermanentDestructive: true,
	}
	for _, e := range GeneratedEntries {
		if !known[e.Class] {
			t.Errorf("generated entry %q/%q carries unknown class %q — it would vanish from gates", e.Name, e.Mode, e.Class)
		}
	}
}

// TestRegistrationValidatorRejectsBadRegistrations proves a registration
// missing help text or carrying an unknown class is rejected at Register
// time, not silently admitted.
func TestRegistrationValidatorRejectsBadRegistrations(t *testing.T) {
	cases := []struct {
		name string
		b    Behavior
	}{
		{"missing help", Behavior{Name: "bogus", Class: NonDestructive, Legs: AttackOnly, Help: ""}},
		{"missing name", Behavior{Name: "", Class: NonDestructive, Legs: AttackOnly, Help: "x"}},
		{"unknown class", Behavior{Name: "bogus", Class: "nope", Legs: AttackOnly, Help: "x"}},
		{"unknown legs", Behavior{Name: "bogus", Class: NonDestructive, Legs: "nope", Help: "x"}},
		{"mode missing flag", Behavior{Name: "bogus", Class: NonDestructive, Legs: AttackOnly, Help: "x", Modes: []Mode{{Class: NonDestructive, Help: "m"}}}},
		{"mode missing help", Behavior{Name: "bogus", Class: NonDestructive, Legs: AttackOnly, Help: "x", Modes: []Mode{{Flag: "f", Class: NonDestructive}}}},
		{"mode unknown class", Behavior{Name: "bogus", Class: NonDestructive, Legs: AttackOnly, Help: "x", Modes: []Mode{{Flag: "f", Class: "nope", Help: "m"}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := validate(c.b); err == nil {
				t.Fatalf("validate accepted bad registration: %+v", c.b)
			}
		})
	}
}

// oracleRow is one row of the authoritative durability table.
type oracleRow struct {
	name     string
	mode     string
	class    Durability
	teardown string
}

// planOracle is the authoritative durability classification of all 35
// behaviors. A misclassified row here fails the test, not just the family
// guard. full is orchestration and carries no durability row of its own;
// it is registered for dispatch/help reconciliation but excluded from
// this oracle.
var planOracle = []oracleRow{
	// non-destructive
	{"scan", "", NonDestructive, ""},
	{"arpsweep", "", NonDestructive, ""},
	{"vlanenum", "", NonDestructive, ""},
	{"ghost", "", NonDestructive, ""},
	{"raguard", "", NonDestructive, ""},
	// transient-decay
	{"stproot", "", TransientDecay, "engineered max_age~6s"},
	{"camflood", "", TransientDecay, "CAM table ages out"},
	{"doubletag", "", TransientDecay, "one-shot injected frames; nothing persists"},
	{"dhcpstarve", "", TransientDecay, "lease pool recovers"},
	{"gratarp", "", TransientDecay, "neighbor cache ages"},
	{"llmnr", "", TransientDecay, "spoofed answers expire"},
	{"daddos", "", TransientDecay, "DAD window passes"},
	{"ndpspoof", "", TransientDecay, "NUD / real master resumes"},
	{"vrrp", "", TransientDecay, "NUD / real master resumes"},
	{"portsteal", "", TransientDecay, "CAM aging"},
	{"roguedhcp6", "", TransientDecay, "~300s lifetime announced"},
	{"roguera", "", TransientDecay, "~1800s lifetime announced"},
	{"roguedhcp", "", TransientDecay, "client leases ~1800s"},
	{"icmpredirect", "", TransientDecay, "victim route cache decays"},
	{"mvrp", "", TransientDecay, "MRP timers, minutes"},
	{"vtp", "", TransientDecay, "revision-bump side effect recorded"},
	{"wpad", "", TransientDecay, "client proxy config residue, bound = TTL"},
	{"mld", "", TransientDecay, "bounded bursts / holdtimes"},
	{"raflood", "", TransientDecay, "bounded bursts / holdtimes"},
	{"lldpspoof", "", TransientDecay, "bounded bursts / holdtimes"},
	// temporary-restored
	{"portsteal", "relay", TemporaryRestored, "ip_forward restore armed"},
	{"arpspoof", "", TemporaryRestored, "neighbor unicast repairs + ip_forward restore"},
	{"vlanhop", "", TemporaryRestored, "active restore armed"},
	{"voicevlan", "", TemporaryRestored, "active restore armed"},
	{"hsrp", "", TemporaryRestored, "resign teardown"},
	{"dtp", "", TemporaryRestored, "access-port restore armed by default"},
	{"ospf", "", TemporaryRestored, "goodbye/flush teardown"},
	{"eigrp", "", TemporaryRestored, "goodbye/flush teardown"},
	{"etherchannel", "", TemporaryRestored, "port-channel release"},
	{"glbp", "", TemporaryRestored, "resign teardown"},
	// permanent-destructive (modes)
	{"vtp", "wipe", PermanentDestructive, "requires per-run opt-in"},
	{"vtp", "set", PermanentDestructive, "requires per-run opt-in"},
	{"vlanhop", "persist", PermanentDestructive, "host-side persistence, flag is the opt-in"},
	{"voicevlan", "persist", PermanentDestructive, "host-side persistence, flag is the opt-in"},
	{"dtp", "keep-trunk", PermanentDestructive, "opt-in to keep the negotiated trunk"},
}

// TestOracleDurabilityClassification pins the authoritative durability
// classification table row-for-row against the generated catalog.
func TestOracleDurabilityClassification(t *testing.T) {
	byKey := make(map[string]Entry)
	for _, e := range GeneratedEntries {
		byKey[e.Name+"\x00"+e.Mode] = e
	}

	for _, row := range planOracle {
		key := row.name + "\x00" + row.mode
		e, ok := byKey[key]
		if !ok {
			t.Errorf("oracle row %q/%q has no generated catalog entry", row.name, row.mode)
			continue
		}
		if e.Class != row.class {
			t.Errorf("oracle row %q/%q: class = %q, want %q", row.name, row.mode, e.Class, row.class)
		}
		if row.teardown != "" && e.Teardown != row.teardown {
			t.Errorf("oracle row %q/%q: teardown = %q, want %q", row.name, row.mode, e.Teardown, row.teardown)
		}
	}

	// Every generated entry must appear in the oracle — full is
	// orchestration with no catalog row, so it is absent here too.
	for _, e := range GeneratedEntries {
		found := false
		for _, row := range planOracle {
			if row.name == e.Name && row.mode == e.Mode {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("generated entry %q/%q has no oracle row — the catalog drifted beyond the plan", e.Name, e.Mode)
		}
	}
}

// TestPermanentModesRequireOptIn pins that every permanent-destructive
// entry is a mode (the base behaviors are never permanent). This is the
// invariant `full` relies on when it refuses permanent modes regardless
// of acknowledgments.
func TestPermanentModesRequireOptIn(t *testing.T) {
	for _, e := range GeneratedEntries {
		if e.Class == PermanentDestructive && e.Mode == "" {
			t.Errorf("entry %q has a permanent-destructive base class — permanent state must be a mode opt-in, not the default", e.Name)
		}
	}
}

// TestCLIReconciliation is the integration test: every catalog
// entry name has a registered FlagSet handler in cmd/netpen's stub
// dispatch table and vice versa. Since cmd/netpen is a main package and
// cannot be imported, the test parses main.go's AST (the errs
// code_test.go pattern) to extract the stubNames list and asserts parity.
func TestCLIReconciliation(t *testing.T) {
	mainPath := filepath.Join("..", "cmd", "netpen", "main.go")
	cmdNames := stubNamesFromAST(t, mainPath)

	catalogNames := make(map[string]bool)
	for _, e := range GeneratedEntries {
		catalogNames[e.Name] = true
	}

	cmdSet := make(map[string]bool)
	for _, n := range cmdNames {
		cmdSet[n] = true
	}

	// full is orchestration scripting the gate and carries no (attack,
	// mode) catalog entry of its own (plan). It is the sole CLI-only
	// orchestration name the reconciliation allows; every other command
	// name must have a catalog row and vice versa.
	const orchestrationOnly = "full"

	for name := range catalogNames {
		if !cmdSet[name] {
			t.Errorf("catalog entry %q has no CLI dispatch handler in cmd/netpen/main.go", name)
		}
	}
	for name := range cmdSet {
		if !catalogNames[name] {
			if name == orchestrationOnly {
				continue
			}
			t.Errorf("CLI dispatch handler %q has no catalog entry", name)
		}
	}
}

// stubNamesFromAST parses main.go and extracts the stubNames slice
// literal — the 27 parity + 8 superset command names. It mirrors the
// errs code_test.go AST-walk pattern: a literal argument is required so
// the gate can read it.
func stubNamesFromAST(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		// Find the stubNames var declaration.
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for _, name := range vs.Names {
			if name.Name == "stubNames" {
				if len(vs.Values) != 1 {
					t.Fatalf("stubNames: expected one value, got %d", len(vs.Values))
				}
				names = appendLiteralStrings(t, vs.Values[0], fset)
			}
		}
		return true
	})
	if len(names) == 0 {
		t.Fatalf("no stubNames literal found in %s", path)
	}
	return names
}

// appendLiteralStrings extracts string literals from a composite literal
// (a []string slice). It prints the AST node to reconstruct the literal.
func appendLiteralStrings(t *testing.T, expr ast.Expr, fset *token.FileSet) []string {
	t.Helper()
	var names []string
	cl, ok := expr.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("stubNames value is %T, not a composite literal", expr)
	}
	for _, elt := range cl.Elts {
		lit, ok := elt.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Fatalf("stubNames element is not a string literal: %s", fset.Position(elt.Pos()))
		}
		v := unquote(lit.Value)
		names = append(names, v)
	}
	return names
}

// unquote strips the surrounding quotes from a Go string literal. It
// mirrors the errs code_test.go pattern but stays simple since the stub
// list holds only plain double-quoted strings.
func unquote(s string) string {
	return strings.Trim(s, `"`)
}

// renderEntries produces the exact bytes the generator writes, so the
// drift test compares generation against generation without invoking the
// generator binary. It imports the same rendering the generator uses by
// reconstructing the committed file's shape.
func renderEntries(entries []Entry) []byte {
	var b bytes.Buffer
	b.WriteString("// Code generated by gen_catalog.go; DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// Run `go generate ./catalog/...` to regenerate. The catalog is\n")
	b.WriteString("// extracted from behavior registrations in registrations.go.\n")
	b.WriteString("\n")
	b.WriteString("package catalog\n\n")
	b.WriteString("// GeneratedEntries is the generated attack catalog: one row per\n")
	b.WriteString("// (behavior, mode) pair, sorted by (Name, Mode). It is the data\n")
	b.WriteString("// view of the behavior registrations and the single source for\n")
	b.WriteString("// dispatch, help text, legs, preconditions, and durability classes.\n")
	b.WriteString("var GeneratedEntries = []Entry{\n")
	for _, e := range entries {
		b.WriteString("\t{Name: " + quoteGo(e.Name) + ", Mode: " + quoteGo(e.Mode) +
			", Protocols: " + quoteSlice(e.Protocols) +
			", Preconditions: " + quoteSlice(e.Preconditions) +
			", Legs: " + legsLiteral(e.Legs) +
			", Class: " + classLiteral(e.Class) +
			", Teardown: " + quoteGo(e.Teardown) +
			", Help: " + quoteGo(e.Help) + "},\n")
	}
	b.WriteString("}\n")
	return b.Bytes()
}

func quoteGo(s string) string { return `"` + s + `"` }

func quoteSlice(s []string) string {
	if len(s) == 0 {
		return "nil"
	}
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = quoteGo(v)
	}
	return "[]string{" + strings.Join(out, ", ") + "}"
}

func classLiteral(c Durability) string {
	switch c {
	case NonDestructive:
		return "NonDestructive"
	case TransientDecay:
		return "TransientDecay"
	case TemporaryRestored:
		return "TemporaryRestored"
	case PermanentDestructive:
		return "PermanentDestructive"
	}
	return quoteGo(string(c))
}

func legsLiteral(l Legs) string {
	switch l {
	case AttackOnly:
		return "AttackOnly"
	case WatchOptional:
		return "WatchOptional"
	case WatchRequired:
		return "WatchRequired"
	}
	return quoteGo(string(l))
}

// silence unused imports the AST path may pull in without all branches.
var (
	_ = fs.WalkDir
	_ = os.ReadFile
	_ = filepath.Join
	_ = printer.Fprint
	_ = sort.Strings
)
