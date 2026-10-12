package catalog

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

// TestGeneratedCatalogIsCurrent is the drift gate: regenerating the
// catalog must produce byte-identical source. A divergence means a
// registration changed without regenerating, and the committed catalog
// is stale.
func TestGeneratedCatalogIsCurrent(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "catalog.go")
	cmd := exec.Command("go", "run", "gen_catalog.go", "-output", outputPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run catalog generator: %v\n%s", err, output)
	}
	generated, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read generated catalog: %v", err)
	}

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

// TestFamilyGuardRejectsUnknownClass prevents unknown durability classes from
// silently vanishing from listings and gates.
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
		b    registration
	}{
		{"missing help", registration{Name: "bogus", Class: NonDestructive, Legs: AttackOnly, Help: ""}},
		{"missing name", registration{Name: "", Class: NonDestructive, Legs: AttackOnly, Help: "x"}},
		{"unknown class", registration{Name: "bogus", Class: "nope", Legs: AttackOnly, Help: "x"}},
		{"unknown legs", registration{Name: "bogus", Class: NonDestructive, Legs: "nope", Help: "x"}},
		{"mode missing flag", registration{Name: "bogus", Class: NonDestructive, Legs: AttackOnly, Help: "x", Modes: []registrationMode{{Class: NonDestructive, Help: "m"}}}},
		{"mode missing help", registration{Name: "bogus", Class: NonDestructive, Legs: AttackOnly, Help: "x", Modes: []registrationMode{{Flag: "f", Class: NonDestructive}}}},
		{"mode unknown class", registration{Name: "bogus", Class: NonDestructive, Legs: AttackOnly, Help: "x", Modes: []registrationMode{{Flag: "f", Class: "nope", Help: "m"}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := validate(c.b); err == nil {
				t.Fatalf("validate accepted bad registration: %+v", c.b)
			}
		})
	}
}

// TestRegisterReturnsErrorAndLeavesCatalogUnchanged proves Register reports a
// bad registration and a post-read registration as an error rather than a
// panic, and admits neither into the catalog, and that MustRegister panics on
// each. It swaps in a private registry so the production one is untouched.
func TestRegisterReturnsErrorAndLeavesCatalogUnchanged(t *testing.T) {
	mu.Lock()
	savedBehaviors, savedRegistered := behaviors, registered
	behaviors, registered = nil, false
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		behaviors, registered = savedBehaviors, savedRegistered
		mu.Unlock()
	})

	bad := registration{Name: "", Class: NonDestructive, Legs: AttackOnly, Help: "x"}
	if err := Register(bad); err == nil {
		t.Fatal("Register with an empty Name returned nil; want an error")
	}
	if got := len(registrations()); got != 0 {
		t.Fatalf("registrations() has %d entries after a rejected registration; want 0", got)
	}

	// registrations() above latched the catalog as read, so a well-formed
	// registration now fails with the post-read error.
	good := registration{Name: "late", Class: NonDestructive, Legs: AttackOnly, Help: "x"}
	if err := Register(good); err == nil {
		t.Fatal("Register after the catalog was read returned nil; want an error")
	}

	assertPanics(t, "MustRegister(bad)", func() { MustRegister(bad) })
	assertPanics(t, "MustRegister after read", func() { MustRegister(good) })
}

func noopRun(context.Context, check.Deps) (check.Verdict, error) {
	return check.Verdict{Kind: check.Pending}, nil
}

func validBehavior(name string) Behavior {
	return Behavior{
		Entry: check.Entry{
			Name: name, Protocols: []string{"arp"}, Legs: AttackOnly,
			Impact: check.ImpactNone, Restore: check.Restore{Kind: check.RestoreNone},
			Affects: check.AffectsNone, Help: "fixture",
		},
		Run: noopRun,
	}
}

// TestNewRejectsInvalidBehaviors pins that the cost fields the ceiling gate
// reads cannot be unknown, that a bound only a decay could announce is
// refused elsewhere, that a check without a Run cannot ship unless it is
// orchestration, and that two rows cannot share a (name, mode) key.
func TestNewRejectsInvalidBehaviors(t *testing.T) {
	negative := validBehavior("b")
	negative.Restore = check.Restore{Kind: check.RestoreDecay, Bound: -time.Second}
	boundOnActive := validBehavior("b")
	boundOnActive.Restore = check.Restore{Kind: check.RestoreActive, Bound: time.Second}
	impact := validBehavior("b")
	impact.Impact = "catastrophic"
	affects := validBehavior("b")
	affects.Affects = "everyone"
	noRun := validBehavior("b")
	noRun.Run = nil
	baseMode := validBehavior("b")
	baseMode.Mode = "relay"
	dupMode := validBehavior("b")
	dupMode.Modes = []Mode{
		{Flag: "relay", Help: "m", Impact: check.ImpactNone, Restore: check.Restore{Kind: check.RestoreNone}, Affects: check.AffectsNone},
		{Flag: "relay", Help: "m", Impact: check.ImpactNone, Restore: check.Restore{Kind: check.RestoreNone}, Affects: check.AffectsNone},
	}
	badMode := validBehavior("b")
	badMode.Modes = []Mode{{Flag: "relay", Help: "m", Impact: "nope", Restore: check.Restore{Kind: check.RestoreNone}, Affects: check.AffectsNone}}

	cases := []struct {
		name      string
		behaviors []Behavior
		want      string
	}{
		{"negative decay bound", []Behavior{negative}, "negative"},
		{"bound on active", []Behavior{boundOnActive}, "only decay carries a bound"},
		{"unknown impact", []Behavior{impact}, `unknown impact "catastrophic"`},
		{"unknown affects", []Behavior{affects}, `unknown affects "everyone"`},
		{"nil run", []Behavior{noRun}, "run function is required"},
		{"mode on the base entry", []Behavior{baseMode}, "base entry carries mode"},
		{"duplicate behavior", []Behavior{validBehavior("b"), validBehavior("b")}, `duplicate behavior "b"`},
		{"duplicate mode", []Behavior{dupMode}, `duplicate entry "b" mode "relay"`},
		{"unknown mode impact", []Behavior{badMode}, `mode "relay": unknown impact "nope"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.behaviors...)
			if err == nil {
				t.Fatalf("New accepted %+v", c.behaviors)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to contain %q", err, c.want)
			}
		})
	}
}

// TestNewAcceptsOrchestrationWithoutRun pins the one case a nil Run is
// valid: an entry that scripts other behaviors.
func TestNewAcceptsOrchestrationWithoutRun(t *testing.T) {
	b := validBehavior("full")
	b.Run = nil
	b.Orchestration = true
	if _, err := New(b); err != nil {
		t.Fatalf("New(orchestration without Run): %v", err)
	}
}

// TestNewEntriesComeBackSorted pins the (Name, Mode) order the generator
// relies on for byte stability, with modes expanded into their own rows
// carrying the mode's own cost fields.
func TestNewEntriesComeBackSorted(t *testing.T) {
	zeta := validBehavior("zeta")
	alpha := validBehavior("alpha")
	alpha.Impact = check.ImpactTemporary
	alpha.Restore = check.Restore{Kind: check.RestoreDecay, Bound: 6 * time.Second}
	alpha.Affects = check.AffectsFabric
	alpha.Modes = []Mode{
		{Flag: "persist", Help: "keep it", Impact: check.ImpactPermanent, Restore: check.Restore{Kind: check.RestoreNone}, Affects: check.AffectsDevice},
	}
	c, err := New(zeta, alpha)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := c.Entries()
	want := []check.Entry{
		{Name: "alpha", Protocols: []string{"arp"}, Legs: AttackOnly, Impact: check.ImpactTemporary, Restore: check.Restore{Kind: check.RestoreDecay, Bound: 6 * time.Second}, Affects: check.AffectsFabric, Help: "fixture"},
		{Name: "alpha", Mode: "persist", Protocols: []string{"arp"}, Legs: AttackOnly, Impact: check.ImpactPermanent, Restore: check.Restore{Kind: check.RestoreNone}, Affects: check.AffectsDevice, Help: "keep it"},
		{Name: "zeta", Protocols: []string{"arp"}, Legs: AttackOnly, Impact: check.ImpactNone, Restore: check.Restore{Kind: check.RestoreNone}, Affects: check.AffectsNone, Help: "fixture"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Entries() =\n%+v\nwant\n%+v", got, want)
	}

	entry, run, ok := c.Lookup("alpha", "persist")
	if !ok || run == nil || entry.Impact != check.ImpactPermanent {
		t.Errorf("Lookup(alpha, persist) = %+v, run nil %v, ok %v", entry, run == nil, ok)
	}
	if _, _, ok := c.Lookup("alpha", "relay"); ok {
		t.Error("Lookup(alpha, relay) found a mode the catalog does not have")
	}

	got[0].Protocols[0] = "changed"
	if again := c.Entries(); again[0].Protocols[0] != "arp" {
		t.Errorf("Entries() shares its Protocols slice with the caller: %q", again[0].Protocols[0])
	}
}

func assertPanics(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", what)
		}
	}()
	fn()
}

// oracleRow is one row of the authoritative durability table.
type oracleRow struct {
	name     string
	mode     string
	class    Durability
	teardown string
}

// planOracle pins the durability classifications independently of registrations.
// The full command orchestrates other behaviors and has no catalog row.
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
			t.Errorf("generated entry %q/%q has no oracle row", e.Name, e.Mode)
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
// entry name has a registered FlagSet handler in cmd/netpen's dispatch table
// and vice versa. The test parses main.go because main packages cannot be imported.
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

	// full orchestrates other behaviors and has no catalog row of its own.
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

func stubNamesFromAST(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
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
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("unquote command name at %s: %v", fset.Position(elt.Pos()), err)
		}
		names = append(names, v)
	}
	return names
}
