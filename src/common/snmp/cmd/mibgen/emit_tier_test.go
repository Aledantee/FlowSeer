package main

import (
	"strings"
	"testing"

	"github.com/sleepinggenius2/gosmi"
	gosmimodels "github.com/sleepinggenius2/gosmi/models"
)

// nodeFor builds an in-memory gosmi.SmiNode with the supplied object
// name and optional TC type name. Used by unit tests that exercise
// classifyTier without loading a real MIB file.
func nodeFor(name string, tcName string) gosmi.SmiNode {
	var t *gosmimodels.Type
	if tcName != "" {
		t = &gosmimodels.Type{Name: tcName}
	}
	return gosmi.SmiNode{
		Node: gosmimodels.Node{
			Name: name,
			Type: t,
		},
	}
}

// --- classifyTier ----------------------------------------------------

func TestClassifyTier_Counter32(t *testing.T) {
	got := classifyTier(nodeFor("ifInOctets", ""), "Counter32Var")
	if got != "snmp.TierCounter" {
		t.Errorf("Counter32 column: tier = %q, want snmp.TierCounter", got)
	}
}

func TestClassifyTier_Counter64(t *testing.T) {
	got := classifyTier(nodeFor("ifHCInOctets", ""), "Counter64Var")
	if got != "snmp.TierCounter" {
		t.Errorf("Counter64 column: tier = %q, want snmp.TierCounter", got)
	}
}

func TestClassifyTier_TimeStampTC(t *testing.T) {
	got := classifyTier(nodeFor("sysORLastChange", "TimeStamp"), "Uinteger32Var")
	if got != "snmp.TierIndicator" {
		t.Errorf("TimeStamp TC: tier = %q, want snmp.TierIndicator", got)
	}
}

func TestClassifyTier_NameSuffixHeuristic_LastChange(t *testing.T) {
	// ifLastChange: raw TimeTicks (Type.Name == "") + name matches
	// the indicator-suffix heuristic. The load-bearing case that
	// breaks if classification only consults TC names.
	got := classifyTier(nodeFor("ifLastChange", ""), "TimeTicksVar")
	if got != "snmp.TierIndicator" {
		t.Errorf("ifLastChange: tier = %q, want snmp.TierIndicator", got)
	}
}

func TestClassifyTier_NameSuffixHeuristic_LastUpdated(t *testing.T) {
	got := classifyTier(nodeFor("fooLastUpdated", ""), "TimeTicksVar")
	if got != "snmp.TierIndicator" {
		t.Errorf("fooLastUpdated: tier = %q, want snmp.TierIndicator", got)
	}
}

func TestClassifyTier_NameSuffixHeuristic_LastChangeTime(t *testing.T) {
	got := classifyTier(nodeFor("entLastChangeTime", ""), "Uinteger32Var")
	if got != "snmp.TierIndicator" {
		t.Errorf("entLastChangeTime: tier = %q, want snmp.TierIndicator", got)
	}
}

func TestClassifyTier_DateAndTimeNotIndicator_UnlessNameMatches(t *testing.T) {
	// A DateAndTime-typed column with a non-indicator name → TierState.
	// Pin: TC name is not a tier signal except for TimeStamp.
	got := classifyTier(nodeFor("sysCreated", "DateAndTime"), "OctetStringVar")
	if got != "snmp.TierState" {
		t.Errorf("DateAndTime + non-indicator name: tier = %q, want snmp.TierState", got)
	}
}

func TestClassifyTier_DateAndTimeWithIndicatorName(t *testing.T) {
	// entStateLastChanged: DateAndTime TC + name matches → Indicator.
	got := classifyTier(nodeFor("entStateLastChanged", "DateAndTime"), "OctetStringVar")
	if got != "snmp.TierIndicator" {
		t.Errorf("entStateLastChanged: tier = %q, want snmp.TierIndicator", got)
	}
}

func TestClassifyTier_OctetStringDefault(t *testing.T) {
	got := classifyTier(nodeFor("ifAlias", ""), "OctetStringVar")
	if got != "snmp.TierState" {
		t.Errorf("ifAlias: tier = %q, want snmp.TierState", got)
	}
}

func TestClassifyTier_StaticNeverAuto(t *testing.T) {
	// Static is never produced by classifyTier — it's opt-in only.
	for _, name := range []string{
		"ifDescr",
		"sysDescr",
		"chassisSerial",
	} {
		got := classifyTier(nodeFor(name, ""), "OctetStringVar")
		if got == "snmp.TierStatic" {
			t.Errorf("%s classified as Static; should never auto-classify Static", name)
		}
	}
}

// --- matchesIndicatorNameSuffix -------------------------------------

func TestMatchesIndicatorNameSuffix_Cases(t *testing.T) {
	for _, name := range []string{
		"ifLastChange",
		"IfLastChange",
		"IFLASTCHANGE",
		"entLastChangeTime",
		"fooLastUpdated",
		"sysORLastChange",
	} {
		if !matchesIndicatorNameSuffix(name) {
			t.Errorf("%q should match", name)
		}
	}
	for _, name := range []string{
		"ifLast",
		"lastChunk",
		"updateLast",
		"sysDescr",
	} {
		if matchesIndicatorNameSuffix(name) {
			t.Errorf("%q should NOT match", name)
		}
	}
}

// --- discoverNamePrefixScalarIndicator ------------------------------

func TestDiscoverNamePrefixScalar_BaseForm(t *testing.T) {
	// ifStackLastChange = "ifStack" (table name minus "Table") + suffix.
	table := nodeFor("ifStackTable", "")
	scalars := []gosmi.SmiNode{nodeFor("ifStackLastChange", "")}
	ind, ok := discoverNamePrefixScalarIndicator(table, scalars)
	if !ok {
		t.Fatal("ifStackLastChange should bind ifStackTable")
	}
	if ind.Kind != indicatorScalar || ind.Source != indicatorFromStructuralNamePrefix {
		t.Errorf("kind/source = %v/%v, want scalar/name-prefix", ind.Kind, ind.Source)
	}
	if ind.IndicatorNode.Name != "ifStackLastChange" {
		t.Errorf("bound %q, want ifStackLastChange", ind.IndicatorNode.Name)
	}
}

func TestDiscoverNamePrefixScalar_FullNameForm(t *testing.T) {
	// ifTableLastChange keeps "Table" in the scalar name.
	table := nodeFor("ifTable", "")
	scalars := []gosmi.SmiNode{nodeFor("ifTableLastChange", "")}
	if _, ok := discoverNamePrefixScalarIndicator(table, scalars); !ok {
		t.Error("ifTableLastChange should bind ifTable")
	}
}

func TestDiscoverNamePrefixScalar_FullNamePreferred(t *testing.T) {
	table := nodeFor("fooTable", "")
	scalars := []gosmi.SmiNode{
		nodeFor("fooLastChange", ""),
		nodeFor("fooTableLastChange", ""),
	}
	ind, ok := discoverNamePrefixScalarIndicator(table, scalars)
	if !ok {
		t.Fatal("expected a binding")
	}
	if ind.IndicatorNode.Name != "fooTableLastChange" {
		t.Errorf("bound %q, want the full-name form fooTableLastChange", ind.IndicatorNode.Name)
	}
}

func TestDiscoverNamePrefixScalar_NoMatch(t *testing.T) {
	table := nodeFor("fooTable", "")
	scalars := []gosmi.SmiNode{
		nodeFor("barLastChange", ""),      // different base
		nodeFor("fooLastChangeExtra", ""), // suffix not terminal
		nodeFor("fooTable", ""),           // no suffix at all
	}
	if _, ok := discoverNamePrefixScalarIndicator(table, scalars); ok {
		t.Error("no scalar should bind fooTable")
	}
}

// --- Discovery integration ------------------------------------------

// TestDiscoverIndicators_FakeMIB_PerRow exercises the structural
// per-row discovery rule against the test fixture, which has
// fakeLastChange inside fakeTable's row.
func TestDiscoverIndicators_FakeMIB_PerRow(t *testing.T) {
	mod, cleanup := loadFakeMIB(t)
	defer cleanup()

	ec := &emitCtx{}
	indicators := discoverIndicators(ec, mod)
	if len(indicators) != 1 {
		t.Fatalf("indicators = %v, want 1", indicators)
	}
	ind := indicators[0]
	if ind.Kind != indicatorPerRow {
		t.Errorf("indicator kind = %v, want indicatorPerRow", ind.Kind)
	}
	if !strings.Contains(ind.IndicatorNode.Name, "LastChange") &&
		!strings.Contains(ind.IndicatorNode.Name, "lastChange") {
		t.Errorf("indicator name = %q, want fakeLastChange", ind.IndicatorNode.Name)
	}
	if ind.Source != indicatorFromStructuralPerRow {
		t.Errorf("source = %v, want structural per-row", ind.Source)
	}
}

// TestTierMap_GatedOnIndicator verifies that emitTierMap emits the
// map only when ec.hasIndicator is true. With FAKE-MIB's added
// fakeLastChange the gate is open.
func TestTierMap_GatedOnIndicator(t *testing.T) {
	mod, cleanup := loadFakeMIB(t)
	defer cleanup()

	cm := Module{Name: "FAKE-MIB", Package: "fakemib"}
	got, err := renderModule(mod, cm, nil, "go.aledante.io/FlowSeer/src/common/snmp/cmd/mibgen/testdata/golden")
	if err != nil {
		t.Fatalf("renderModule: %v", err)
	}
	src := string(got)
	if !strings.Contains(src, "ColumnTiers") {
		t.Error("FAKE-MIB has indicator but ColumnTiers map not emitted")
	}
	if !strings.Contains(src, "func ColumnTier(col snmp.AnyColumn) snmp.Tier") {
		t.Error("FAKE-MIB has indicator but ColumnTier accessor not emitted")
	}
}
