package catalog

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

// TestGeneratedCatalogIsCurrent is the drift gate: regenerating the
// catalog must produce byte-identical source. A divergence means a
// behavior's entry changed without regenerating, and the committed
// catalog is stale.
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
		t.Fatalf("catalog drift: zz_generated_catalog.go does not match the shipped catalog.\n"+
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

// TestGeneratedEntriesCarryKnownCostFields prevents an unknown impact,
// restore kind, or affects from reaching the committed catalog, where the
// ceiling gate would refuse it silently.
func TestGeneratedEntriesCarryKnownCostFields(t *testing.T) {
	for _, e := range GeneratedEntries {
		if err := validateCost(e.Impact, e.Restore, e.Affects); err != nil {
			t.Errorf("generated entry %q/%q: %v", e.Name, e.Mode, err)
		}
	}
	if len(GeneratedEntries) == 0 {
		t.Fatal("the generated catalog is empty")
	}
}

// TestPermanentModesRequireOptIn pins that every permanent entry is a mode
// (the base behaviors are never permanent). This is the invariant `full`
// relies on when it refuses permanent modes regardless of acknowledgments.
func TestPermanentModesRequireOptIn(t *testing.T) {
	for _, e := range GeneratedEntries {
		if e.Impact == check.ImpactPermanent && e.Mode == "" {
			t.Errorf("entry %q has a permanent base impact — permanent state must be a mode opt-in, not the default", e.Name)
		}
	}
}

func noopRun(context.Context, check.Deps) (check.Verdict, error) {
	return check.Verdict{Kind: findings.VerdictPending}, nil
}

func validBehavior(name string) Behavior {
	return Behavior{
		Entry: check.Entry{
			Name: name, Protocols: []string{"arp"}, Legs: check.AttackOnly,
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
	legs := validBehavior("b")
	legs.Legs = "both"
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
		{"unknown legs", []Behavior{legs}, `unknown legs "both"`},
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
		{Name: "alpha", Protocols: []string{"arp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: check.Restore{Kind: check.RestoreDecay, Bound: 6 * time.Second}, Affects: check.AffectsFabric, Help: "fixture"},
		{Name: "alpha", Mode: "persist", Protocols: []string{"arp"}, Legs: check.AttackOnly, Impact: check.ImpactPermanent, Restore: check.Restore{Kind: check.RestoreNone}, Affects: check.AffectsDevice, Help: "keep it"},
		{Name: "zeta", Protocols: []string{"arp"}, Legs: check.AttackOnly, Impact: check.ImpactNone, Restore: check.Restore{Kind: check.RestoreNone}, Affects: check.AffectsNone, Help: "fixture"},
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

// TestMustNewPanicsOnARejectedBehavior pins the init-time contract: a
// catalog assembled from literals fails loudly on the first build.
func TestMustNewPanicsOnARejectedBehavior(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustNew did not panic on a behavior with no name")
		}
	}()
	MustNew(validBehavior(""))
}
