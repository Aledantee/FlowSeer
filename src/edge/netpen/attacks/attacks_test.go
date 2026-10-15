package attacks_test

import (
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

// oracleRow is one row of the authoritative cost table: what each
// (behavior, mode) pair costs the network, pinned independently of the
// family packages' literals.
type oracleRow struct {
	name, mode string
	impact     check.Impact
	restore    check.RestoreKind
	bound      time.Duration
	affects    check.Affects
}

var (
	none      = check.ImpactNone
	temporary = check.ImpactTemporary
	permanent = check.ImpactPermanent
	noRestore = check.RestoreNone
	decay     = check.RestoreDecay
	active    = check.RestoreActive
)

// oracle pins all 40 pairs: 34 behaviors and 6 modes. The decay bounds are
// the lifetimes the probes announce: 1800 s for the rogue RA and DHCPv4
// lease, 300 s for the DHCPv6 lease, 6 s for the STP max age, and 30 s for
// the LLMNR and WPAD answers' TTL.
var oracle = []oracleRow{
	{"arpspoof", "", temporary, active, 0, check.AffectsClients},
	{"arpsweep", "", none, noRestore, 0, check.AffectsNone},
	{"camflood", "", temporary, decay, 0, check.AffectsDevice},
	{"daddos", "", temporary, decay, 0, check.AffectsClients},
	{"dhcpstarve", "", temporary, decay, 0, check.AffectsDevice},
	{"doubletag", "", none, noRestore, 0, check.AffectsNone},
	{"dtp", "", temporary, active, 0, check.AffectsDevice},
	{"dtp", "keep-trunk", permanent, noRestore, 0, check.AffectsDevice},
	{"eigrp", "", temporary, active, 0, check.AffectsFabric},
	{"etherchannel", "", temporary, active, 0, check.AffectsDevice},
	{"ghost", "", none, noRestore, 0, check.AffectsNone},
	{"glbp", "", temporary, active, 0, check.AffectsClients},
	{"gratarp", "", temporary, decay, 0, check.AffectsClients},
	{"hsrp", "", temporary, active, 0, check.AffectsClients},
	{"icmpredirect", "", temporary, decay, 0, check.AffectsClients},
	{"lldpspoof", "", temporary, decay, 0, check.AffectsDevice},
	{"llmnr", "", temporary, decay, 30 * time.Second, check.AffectsClients},
	{"mld", "", temporary, decay, 0, check.AffectsDevice},
	{"mvrp", "", temporary, decay, 0, check.AffectsFabric},
	{"ndpspoof", "", temporary, decay, 0, check.AffectsClients},
	{"ospf", "", temporary, active, 0, check.AffectsFabric},
	{"portsteal", "", temporary, decay, 0, check.AffectsDevice},
	{"portsteal", "relay", temporary, active, 0, check.AffectsDevice},
	{"raflood", "", temporary, decay, 0, check.AffectsClients},
	{"raguard", "", none, noRestore, 0, check.AffectsNone},
	{"roguedhcp", "", temporary, decay, 1800 * time.Second, check.AffectsClients},
	{"roguedhcp6", "", temporary, decay, 300 * time.Second, check.AffectsClients},
	{"roguera", "", temporary, decay, 1800 * time.Second, check.AffectsClients},
	{"scan", "", none, noRestore, 0, check.AffectsNone},
	{"stproot", "", temporary, decay, 6 * time.Second, check.AffectsFabric},
	{"vlanenum", "", none, noRestore, 0, check.AffectsNone},
	{"vlanhop", "", temporary, active, 0, check.AffectsDevice},
	{"vlanhop", "persist", permanent, noRestore, 0, check.AffectsDevice},
	{"voicevlan", "", temporary, active, 0, check.AffectsClients},
	{"voicevlan", "persist", permanent, noRestore, 0, check.AffectsClients},
	{"vrrp", "", temporary, decay, 0, check.AffectsClients},
	{"vtp", "", temporary, decay, 0, check.AffectsFabric},
	{"vtp", "set", permanent, noRestore, 0, check.AffectsFabric},
	{"vtp", "wipe", permanent, noRestore, 0, check.AffectsFabric},
	{"wpad", "", temporary, decay, 30 * time.Second, check.AffectsClients},
}

// TestOracleCostFields pins the shipped catalog row for row against the
// oracle, both ways, so a changed cost field or a new or lost pair fails
// here before it reaches the gate.
func TestOracleCostFields(t *testing.T) {
	entries := attacks.Default().Entries()
	if len(entries) != 40 || len(oracle) != 40 {
		t.Fatalf("shipped %d entries against %d oracle rows, want 40 each", len(entries), len(oracle))
	}
	for i, row := range oracle {
		e := entries[i]
		if e.Name != row.name || e.Mode != row.mode {
			t.Errorf("entry %d = %s/%s, want %s/%s (sorted by name then mode)", i, e.Name, e.Mode, row.name, row.mode)
			continue
		}
		if e.Impact != row.impact || e.Restore.Kind != row.restore || e.Restore.Bound != row.bound || e.Affects != row.affects {
			t.Errorf("%s/%s = impact %s, restore %s bound %v, affects %s; want %s, %s %v, %s",
				e.Name, e.Mode, e.Impact, e.Restore.Kind, e.Restore.Bound, e.Affects, row.impact, row.restore, row.bound, row.affects)
		}
	}
}

// TestScanIsTheOnlyOrchestrationEntry pins that scan alone carries no Run
// and that every other entry has one, since the runner refuses an
// orchestration entry.
func TestScanIsTheOnlyOrchestrationEntry(t *testing.T) {
	for _, e := range attacks.Default().Entries() {
		if e.Orchestration != (e.Name == "scan") {
			t.Errorf("%s/%s orchestration = %v", e.Name, e.Mode, e.Orchestration)
		}
	}
	if _, run, ok := attacks.Default().Lookup("scan", ""); !ok || run != nil {
		t.Errorf("scan Lookup = run %v ok %v, want a nil run on a present entry", run, ok)
	}
}

// TestGeneratedEntriesMatchTheShippedCatalog cross-checks the committed
// data view against the live assembly, so the generated file cannot drift
// from what the runner dispatches.
func TestGeneratedEntriesMatchTheShippedCatalog(t *testing.T) {
	live := attacks.Default().Entries()
	if !reflect.DeepEqual(live, catalog.GeneratedEntries) {
		t.Errorf("GeneratedEntries differs from the shipped catalog; run `go generate ./catalog/...`:\nlive %+v\ngenerated %+v", live, catalog.GeneratedEntries)
	}
}

// TestBehaviorsReturnsAFreshSlice pins that a test replacing a Run cannot
// change what the next caller ships.
func TestBehaviorsReturnsAFreshSlice(t *testing.T) {
	bs := attacks.Behaviors()
	bs[1].Run = nil
	bs[1].Name = "changed"
	if again := attacks.Behaviors(); again[1].Run == nil || again[1].Name == "changed" {
		t.Error("Behaviors shares its slice between calls")
	}
}
