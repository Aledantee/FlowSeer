package main

import (
	"strings"
	"testing"

	"github.com/sleepinggenius2/gosmi"
	gosmimodels "github.com/sleepinggenius2/gosmi/models"
	gosmitypes "github.com/sleepinggenius2/gosmi/types"
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

func TestDiscoverNamePrefixScalar_SkipsNotAccessible(t *testing.T) {
	// A not-accessible scalar cannot be polled; binding it would leave
	// the Watcher probing a dead OID forever, silently.
	table := nodeFor("fooTable", "")
	sc := nodeFor("fooLastChange", "")
	sc.Access = gosmitypes.AccessNotAccessible
	if _, ok := discoverNamePrefixScalarIndicator(table, []gosmi.SmiNode{sc}); ok {
		t.Error("a not-accessible scalar must not bind as an indicator")
	}

	// Same name, readable: binds. Confirms the guard is what rejected
	// the case above, not the name.
	sc.Access = gosmitypes.AccessReadOnly
	if _, ok := discoverNamePrefixScalarIndicator(table, []gosmi.SmiNode{sc}); !ok {
		t.Error("a read-only scalar with a matching name should bind")
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

// TestDiscoverIndicators_FakeMIB exercises both structural discovery
// rules end-to-end against a real gosmi-parsed module: fakeTable binds
// its per-row fakeLastChange column, and fakeStackTable binds the
// module-level fakeStackLastChange scalar through the name-prefix rule.
//
// The synthetic-node unit tests above pin the matching logic; this one
// is the guard that the rules still fire after gosmi parsing, which is
// what the emitted bindings actually depend on.
func TestDiscoverIndicators_FakeMIB(t *testing.T) {
	mod, cleanup := loadFakeMIB(t)
	defer cleanup()

	ec := &emitCtx{}
	indicators := discoverIndicators(ec, mod)
	if len(indicators) != 2 {
		t.Fatalf("indicators = %v, want 2", indicators)
	}

	byTable := make(map[string]tableIndicator, len(indicators))
	for _, ind := range indicators {
		byTable[ind.Table.Name] = ind
	}

	perRow, ok := byTable["fakeTable"]
	if !ok {
		t.Fatalf("no indicator bound to fakeTable; got %v", byTable)
	}
	if perRow.Kind != indicatorPerRow {
		t.Errorf("fakeTable kind = %v, want indicatorPerRow", perRow.Kind)
	}
	if perRow.Source != indicatorFromStructuralPerRow {
		t.Errorf("fakeTable source = %v, want structural per-row", perRow.Source)
	}
	if !strings.EqualFold(perRow.IndicatorNode.Name, "fakeLastChange") {
		t.Errorf("fakeTable indicator = %q, want fakeLastChange", perRow.IndicatorNode.Name)
	}

	namePrefix, ok := byTable["fakeStackTable"]
	if !ok {
		t.Fatalf("no indicator bound to fakeStackTable; got %v", byTable)
	}
	if namePrefix.Kind != indicatorScalar {
		t.Errorf("fakeStackTable kind = %v, want indicatorScalar", namePrefix.Kind)
	}
	if namePrefix.Source != indicatorFromStructuralNamePrefix {
		t.Errorf("fakeStackTable source = %v, want structural name-prefix", namePrefix.Source)
	}
	if !strings.EqualFold(namePrefix.IndicatorNode.Name, "fakeStackLastChange") {
		t.Errorf("fakeStackTable indicator = %q, want fakeStackLastChange", namePrefix.IndicatorNode.Name)
	}
}

// TestDiscoverIndicators_ConfigOutranksNamePrefix pins the precedence
// that protects an operator override: when mibgen.yaml declares an
// indicator for a table, that declaration wins over a name-prefix
// scalar that would otherwise match. Without this ordering a wrong
// structural guess could not be corrected from config.
func TestDiscoverIndicators_ConfigOutranksNamePrefix(t *testing.T) {
	mod, cleanup := loadFakeMIB(t)
	defer cleanup()

	// fakeScalar (fakeMIB 1) is not an indicator by name, so only the
	// config declaration can bind it to fakeStackTable.
	ec := &emitCtx{cm: Module{
		Name:    "FAKE-MIB",
		Package: "fakemib",
		Indicators: []IndicatorDecl{{
			ScalarOID:    "1.3.6.1.4.1.99999.1.1",
			CoversTables: []string{"1.3.6.1.4.1.99999.1.4"},
		}},
	}}

	for _, ind := range discoverIndicators(ec, mod) {
		if ind.Table.Name != "fakeStackTable" {
			continue
		}
		if ind.Source != indicatorFromConfig {
			t.Errorf("fakeStackTable source = %v, want config-declared", ind.Source)
		}
		if !strings.EqualFold(ind.IndicatorNode.Name, "fakeScalar") {
			t.Errorf("fakeStackTable indicator = %q, want fakeScalar", ind.IndicatorNode.Name)
		}
		return
	}
	t.Fatal("no indicator bound to fakeStackTable")
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
