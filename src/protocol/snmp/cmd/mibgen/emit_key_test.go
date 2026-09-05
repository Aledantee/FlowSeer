package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// fakeModules is the configuration the fake fixtures render under: both
// modules configured, so the cross-package reference resolves.
var fakeModules = map[string]Module{
	"FAKE-MIB":      {Name: "FAKE-MIB", Package: "fakemib"},
	"FAKE-KEYS-MIB": {Name: "FAKE-KEYS-MIB", Package: "fakekeysmib"},
}

// renderFake renders one of the two fake modules under cfgByName.
func renderFake(t *testing.T, set *smi.ModuleSet, name string, cfgByName map[string]Module) (string, []degradedRef) {
	t.Helper()
	mod, ok := set.Module(name)
	if !ok {
		t.Fatalf("%s missing from the resolved set", name)
	}
	out, degraded, err := renderModule(mod, set, fakeModules[name], cfgByName, goldenPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule %s: %v", name, err)
	}

	return string(out), degraded
}

// squash collapses runs of blanks so a fragment can spell a struct
// field without pinning gofmt's column alignment.
var squash = regexp.MustCompile(`[ \t]+`)

func wantFragments(t *testing.T, src string, want ...string) {
	t.Helper()
	src = squash.ReplaceAllString(src, " ")
	for _, w := range want {
		if !strings.Contains(src, squash.ReplaceAllString(w, " ")) {
			t.Errorf("emitted source missing fragment %q", w)
		}
	}
}

func rejectFragments(t *testing.T, src string, reject ...string) {
	t.Helper()
	src = squash.ReplaceAllString(src, " ")
	for _, r := range reject {
		if strings.Contains(src, squash.ReplaceAllString(r, " ")) {
			t.Errorf("emitted source contains forbidden fragment %q", r)
		}
	}
}

// TestEmit_KeyTypeAndHomeTable pins the declaring module's side: the
// keyed convention is a named type whose home table is the table it
// solely indexes, and that table's row is keyed by a struct over it
// rather than by a raw OID.
func TestEmit_KeyTypeAndHomeTable(t *testing.T) {
	_, set := loadFakeMIB(t)
	src, degraded := renderFake(t, set, "FAKE-KEYS-MIB", fakeModules)
	if len(degraded) != 0 {
		t.Errorf("degraded references = %v; want none", degraded)
	}
	wantFragments(t, src,
		"type FakeKeyIndex int32",
		"func (FakeKeyIndex) HomeTable() snmp.TableDescriptor {\n\treturn FakeKeyTable.Descriptor()\n}",
		"type FakeKeyTableKey struct {\n\tFakeKeyIndex FakeKeyIndex\n}",
		"type FakeKeyTableRow struct {\n\tKey FakeKeyTableKey\n\tkeyValid bool\n",
		"func (r FakeKeyTableRow) KeyValid() bool",
		"func decodeFakeKeyTableKey(idx snmp.OID) (FakeKeyTableKey, bool)",
		"FakeKeyIndex: FakeKeyIndex(parts[0].Integer)",
		"row.Key, row.keyValid = decodeFakeKeyTableKey(idx)",
	)
	rejectFragments(t, src, "Index snmp.OID")
}

// TestEmit_ReferencingModuleUsesQualifiedKeyType pins the referencing
// side: a column typed by another configured module's keyed convention
// carries that package's type, the referencing table's own inline
// Integer32 index stays a plain int32 with no key type of its own, and
// an augmenting table reuses the key struct of the table at the root of
// its AUGMENTS chain, whether that root is local, in the other package,
// reached through another augmenting row, or index-only. An index
// column refining the imported convention keeps the imported key type
// rather than spawning a local one, and a table whose INDEX part does
// not resolve keeps the raw suffix.
func TestEmit_ReferencingModuleUsesQualifiedKeyType(t *testing.T) {
	_, set := loadFakeMIB(t)
	src, degraded := renderFake(t, set, "FAKE-MIB", fakeModules)
	if len(degraded) != 0 {
		t.Errorf("degraded references = %v; want none", degraded)
	}
	wantFragments(t, src,
		"var FakeRef = snmp.NewColumn[fakekeysmib.FakeKeyIndex]",
		"FakeRef fakekeysmib.FakeKeyIndex",
		"type FakeTableKey struct {\n\tFakeIndex int32\n}",
		"type FakeAugTableRow struct {\n\tKey FakeTableKey\n\tkeyValid bool\n",
		"func decodeFakeAugTableKey(idx snmp.OID) (FakeTableKey, bool)",
		"type FakeChainTableRow struct {\n\tKey FakeTableKey\n\tkeyValid bool\n",
		"func decodeFakeChainTableKey(idx snmp.OID) (FakeTableKey, bool)",
		"type FakeKeyAugTableRow struct {\n\tKey fakekeysmib.FakeKeyTableKey\n\tkeyValid bool\n",
		"func decodeFakeKeyAugTableKey(idx snmp.OID) (fakekeysmib.FakeKeyTableKey, bool)",
		"KeyType: \"fakekeysmib.FakeKeyTableKey\"",
		"type FakeBareTableKey struct {\n\tFakeBareIndex int32\n}",
		"type FakeBareAugTableRow struct {\n\tKey FakeBareTableKey\n\tkeyValid bool\n",
		"type FakeRefinedTableKey struct {\n\tFakeRefinedIndex fakekeysmib.FakeKeyIndex\n}",
		"FakeRefinedIndex: fakekeysmib.FakeKeyIndex(parts[0].Integer)",
		"type FakeUnresolvedTableRow struct {\n\tIndex snmp.OID\n",
		"row := FakeUnresolvedTableRow{Index: idx}",
		"KeyType: \"snmp.OID\"",
		"type FakePairTableKey struct {\n\tFakePairSlot int32\n\tFakePairName string\n}",
		"FakePairName: string(parts[1].Octets)",
		"type FakeImpliedTableKey struct {\n\tFakeImpliedName string\n}",
		"{Kind: snmp.IndexImpliedOctets}",
		"{Kind: snmp.IndexLengthPrefixedOctets}",
		"type FakeAddrTableKey struct {\n\tFakeAddrIp netip.Addr\n}",
		"FakeAddrIp: parts[0].Addr",
		"{Kind: snmp.IndexIPv4}",
		"type FakeOidTableKey struct {\n\tFakeOidPath string\n}",
		"FakeOidPath: parts[0].OID.String()",
		"{Kind: snmp.IndexLengthPrefixedOID}",
		"a.Key == b.Key && a.keyValid == b.keyValid",
	)
	rejectFragments(t, src,
		"type FakeIndex ",
		"type FakeAugTableKey ",
		"type FakeChainTableKey ",
		"type FakeKeyAugTableKey ",
		"type FakeBareAugTableKey ",
		"type FakeKeyIndex ",
		"type FakeUnresolvedTableKey ",
		"func (r FakeUnresolvedTableRow) KeyValid()",
		"var FakeBareTable ",
		"fakeBareTableIndexShapes",
	)
}

// TestEmit_UnconfiguredKeyModuleDegrades pins the fallback: with the
// declaring module dropped from the configuration the column falls back
// to its base type, the table augmenting that module's row keeps the
// raw suffix as its key, and the render reports both references it
// degraded. The refining index column is a third reference to the
// same module and degrades to the base type of its own refinement.
func TestEmit_UnconfiguredKeyModuleDegrades(t *testing.T) {
	_, set := loadFakeMIB(t)
	only := map[string]Module{"FAKE-MIB": fakeModules["FAKE-MIB"]}
	src, degraded := renderFake(t, set, "FAKE-MIB", only)
	wantFragments(t, src,
		"var FakeRef = snmp.NewColumn[int32]",
		"type FakeKeyAugTableRow struct {\n\tIndex snmp.OID\n",
		"type FakeRefinedTableKey struct {\n\tFakeRefinedIndex int32\n}",
	)
	rejectFragments(t, src, "fakekeysmib", "func (r FakeKeyAugTableRow) KeyValid()")

	want := []degradedRef{
		{Module: "FAKE-MIB", Object: "fakeRef", Convention: "FakeKeyIndex", DeclaringModule: "FAKE-KEYS-MIB", Reason: degradedNotConfigured},
		{Module: "FAKE-MIB", Object: "fakeKeyAugEntry", Convention: "FakeKeyTableKey", DeclaringModule: "FAKE-KEYS-MIB", Reason: degradedNotConfigured},
		{Module: "FAKE-MIB", Object: "fakeRefinedIndex", Convention: "FakeKeyIndex", DeclaringModule: "FAKE-KEYS-MIB", Reason: degradedNotConfigured},
	}
	if len(degraded) != len(want) {
		t.Fatalf("degraded references = %v; want %v", degraded, want)
	}
	for _, w := range want {
		if !slices.Contains(degraded, w) {
			t.Errorf("degraded references %v lack %+v", degraded, w)
		}
	}
	if line := want[0].String(); !strings.Contains(line, "fakeRef") || !strings.Contains(line, "FAKE-KEYS-MIB") {
		t.Errorf("report line %q names neither the column nor the module", line)
	}
}

// loadConfigured loads the repository's mibgen.yaml so a test can ask
// how the shipped configuration keys its conventions.
func loadConfigured(t *testing.T) (*Config, *smi.ModuleSet) {
	t.Helper()
	root := repoRoot(t)
	if _, err := os.Stat(filepath.Join(root, "spec", "mib")); err != nil {
		t.Skipf("spec/mib not available: %v", err)
	}
	cfg, err := LoadConfig(filepath.Join(root, "mibgen.yaml"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	set, err := LoadModules(cfg)
	if err != nil {
		t.Fatalf("load modules: %v", err)
	}

	return cfg, set
}

// TestKeyedConventions_ExcludesWellKnownAndEnumerated pins the two
// exclusions on the shipped MIBs. dot1dTpFdbTable is indexed solely by
// MacAddress, which the well-known branch owns; ipSystemStatsTable is
// indexed solely by InetVersion, an enumerated convention. Neither may
// become a key type, so a column of either type is not a reference.
func TestKeyedConventions_ExcludesWellKnownAndEnumerated(t *testing.T) {
	_, set := loadConfigured(t)
	keyed := keyedConventions(set)
	for _, name := range []string{"MacAddress", "InetVersion"} {
		for k := range keyed {
			if k.Name == name {
				t.Errorf("%s is keyed by %s; want excluded", name, keyed[k].Home.Node.Name)
			}
		}
	}
	for _, name := range []string{"InterfaceIndex", "PhysicalIndex", "LldpPortNumber"} {
		found := false
		for k := range keyed {
			found = found || k.Name == name
		}
		if !found {
			t.Errorf("%s is not keyed; want a key type", name)
		}
	}

	src := renderConfigured(t, "BRIDGE-MIB")
	wantFragments(t, src,
		"type Dot1dTpFdbTableKey struct {\n\tDot1dTpFdbAddress string\n}",
		"var Dot1dStaticAddress = snmp.NewColumn[net.HardwareAddr]",
		"Dot1dBasePortIfIndex ifmib.InterfaceIndex",
		"type Dot1dBasePortTableKey struct {\n\tDot1dBasePort int32\n}",
	)
	rejectFragments(t, src, "type MacAddress ")

	src = renderConfigured(t, "IP-MIB")
	wantFragments(t, src, "type IpSystemStatsTableKey struct {\n\tIpSystemStatsIPVersion int32\n}")
}

// TestKeyedConventions_HomeTableTiebreak pins the rule for a convention
// that solely indexes several tables of its module: LLDP-MIB keys four
// tables by LldpPortNumber, and a consumer following the key needs the
// table that describes the port, lldpLocPortTable.
func TestKeyedConventions_HomeTableTiebreak(t *testing.T) {
	_, set := loadConfigured(t)
	keyed := keyedConventions(set)
	kc, ok := keyed[typeKey{Module: "LLDP-MIB", Name: "LldpPortNumber"}]
	if !ok {
		t.Fatal("LldpPortNumber is not keyed")
	}
	if got := kc.Home.Node.Name; got != "lldpLocPortTable" {
		t.Errorf("home table of LldpPortNumber = %s; want lldpLocPortTable", got)
	}
}
