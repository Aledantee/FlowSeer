package main

import (
	"os"
	"path/filepath"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi"
)

// TestEmit_DescriptorNamesSharedIndicator pins the descriptor of a table
// covered by a scalar indicator shared with other tables: ENTITY-MIB's
// entLastChangeTime covers five tables, and each one's descriptor names
// its own indicator var and its own key struct.
func TestEmit_DescriptorNamesSharedIndicator(t *testing.T) {
	src := renderConfigured(t, "ENTITY-MIB")
	for _, table := range []string{"EntPhysicalTable", "EntLogicalTable", "EntLPMappingTable", "EntAliasMappingTable", "EntPhysicalContainsTable"} {
		wantFragments(t, src,
			"func ("+unexported(table)+"T) Descriptor() snmp.TableDescriptor",
			"Indicator: "+table+"Indicator,",
			`KeyType: "`+table+`Key",`,
		)
	}
	wantFragments(t, src, "Root: snmp.MustOID(1, 3, 6, 1, 2, 1, 47, 1, 1, 1),")
}

// TestEmit_DescriptorShapes pins the three descriptor shapes on the fake
// fixture: a per-row indicator, a scalar indicator, and none, plus the
// key-type name an augmenting table borrows from the augmented one.
func TestEmit_DescriptorShapes(t *testing.T) {
	_, set := loadFakeMIB(t)
	src, _ := renderFake(t, set, "FAKE-MIB", fakeModules)
	wantFragments(t, src,
		"func (fakeTableT) Descriptor() snmp.TableDescriptor {\n\treturn snmp.TableDescriptor{\n\t\tIndicator: FakeTableIndicator,\n\t\tKeyType: \"FakeTableKey\",\n\t\tRoot: snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 1, 3),\n\t}\n}",
		"func (fakeStackTableT) Descriptor() snmp.TableDescriptor {\n\treturn snmp.TableDescriptor{\n\t\tIndicator: FakeStackTableIndicator,\n\t\tKeyType: \"FakeStackTableKey\",\n\t\tRoot: snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 1, 4),\n\t}\n}",
		"func (fakePairTableT) Descriptor() snmp.TableDescriptor {\n\treturn snmp.TableDescriptor{\n\t\tKeyType: \"FakePairTableKey\",\n\t\tRoot: snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 1, 7),\n\t}\n}",
		"func (fakeAugTableT) Descriptor() snmp.TableDescriptor {\n\treturn snmp.TableDescriptor{\n\t\tKeyType: \"FakeTableKey\",",
	)
	rejectFragments(t, src, "FakePairTableIndicator")
}

// TestEmit_HomeTableReturnsDescriptor pins that a keyed convention's
// HomeTable is the home table's own descriptor rather than a second
// literal that could drift from it.
func TestEmit_HomeTableReturnsDescriptor(t *testing.T) {
	_, set := loadFakeMIB(t)
	src, _ := renderFake(t, set, "FAKE-KEYS-MIB", fakeModules)
	wantFragments(t, src,
		"func (FakeKeyIndex) HomeTable() snmp.TableDescriptor {\n\treturn FakeKeyTable.Descriptor()\n}",
		"func (fakeKeyTableT) Descriptor() snmp.TableDescriptor {\n\treturn snmp.TableDescriptor{\n\t\tKeyType: \"FakeKeyTableKey\",\n\t\tRoot: snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 2, 1),\n\t}\n}",
	)
}

// TestEmit_UnimportedKeyTypeDegradesKeyAndColumn pins the IMPORTS rule on
// the key struct: a module that names a keyed convention without
// importing it gets neither the key type in its key struct nor in its
// column, and the report names both objects. A table indexed by the
// home table's own column, imported without the convention, is the
// other side of the rule: the column import is the edge to the
// declaring module, so its key field carries the type.
func TestEmit_UnimportedKeyTypeDegradesKeyAndColumn(t *testing.T) {
	mibDir, err := filepath.Abs("testdata/mibs")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	ietfDir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "spec", "mib", "ietf"))
	if err != nil {
		t.Fatalf("abs ietf: %v", err)
	}
	if _, err := os.Stat(ietfDir); err != nil {
		t.Skipf("spec/mib/ietf not available: %v", err)
	}
	set, err := smi.Load([]string{"FAKE-NOIMPORT-MIB", "FAKE-KEYS-MIB"}, smi.Options{SearchPaths: []string{mibDir, ietfDir}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	mod, ok := set.Module("FAKE-NOIMPORT-MIB")
	if !ok {
		t.Fatal("FAKE-NOIMPORT-MIB missing from the resolved set")
	}
	cfg := map[string]Module{
		"FAKE-NOIMPORT-MIB": {Name: "FAKE-NOIMPORT-MIB", Package: "fakenoimportmib"},
		"FAKE-KEYS-MIB":     fakeModules["FAKE-KEYS-MIB"],
	}
	out, degraded, err := renderModule(mod, set, cfg["FAKE-NOIMPORT-MIB"], cfg, goldenPkgPrefix)
	if err != nil {
		t.Fatalf("renderModule: %v", err)
	}
	src := string(out)
	wantFragments(t, src,
		"type FakeNoImportTableKey struct {\n\tFakeNoImportIndex int32\n}",
		"var FakeNoImportRef = snmp.NewColumn[[]byte]",
		"type FakeNoImportByKeyTableKey struct {\n\tFakeKeyIndex fakekeysmib.FakeKeyIndex\n}",
		"FakeKeyIndex: fakekeysmib.FakeKeyIndex(parts[0].Integer)",
	)
	rejectFragments(t, src, "FakeNoImportIndex fakekeysmib", "NewColumn[fakekeysmib")

	want := map[string]bool{"fakeNoImportIndex": true, "fakeNoImportRef": true}
	for _, d := range degraded {
		if d.Convention != "FakeKeyIndex" || d.DeclaringModule != "FAKE-KEYS-MIB" || d.Reason != degradedNotImported {
			t.Errorf("degraded reference %+v; want FakeKeyIndex from FAKE-KEYS-MIB, not imported", d)
		}
		delete(want, d.Object)
	}
	for object := range want {
		t.Errorf("degraded report does not name %s: %v", object, degraded)
	}
}
