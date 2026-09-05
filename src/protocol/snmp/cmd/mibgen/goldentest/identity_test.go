package goldentest

import (
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/snmp"
	"go.aledante.io/FlowSeer/src/protocol/snmp/cmd/mibgen/testdata/golden/sysobjectid"
)

// TestLookup_DeepestKnownNode pins the identity resolution a device
// probe relies on: a registered product resolves to its own node, and a
// model the module predates resolves to the family above it.
func TestLookup_DeepestKnownNode(t *testing.T) {
	cases := []struct {
		name string
		oid  snmp.OID
		want string
	}{
		{"leaf product", snmp.MustOID(1, 3, 6, 1, 4, 1, 99998, 1, 1, 1), "fakeVendorASwitch24"},
		{"unknown child of a family", snmp.MustOID(1, 3, 6, 1, 4, 1, 99998, 1, 1, 7), "fakeVendorASwitchFamily"},
		{"below a leaf", snmp.MustOID(1, 3, 6, 1, 4, 1, 99998, 1, 1, 2, 3), "fakeVendorASwitch48"},
		{"enterprise root only", snmp.MustOID(1, 3, 6, 1, 4, 1, 99997, 9), "fakeVendorB"},
		{"other vendor leaf", snmp.MustOID(1, 3, 6, 1, 4, 1, 99997, 1, 1), "fakeVendorBAccessPoint"},
		{"shared OID keeps the first module in name order", snmp.MustOID(1, 3, 6, 1, 4, 1, 99996, 1, 5), "fakeSharedByA"},
	}
	for _, c := range cases {
		got, ok := sysobjectid.Lookup(c.oid)
		if !ok {
			t.Errorf("%s: Lookup(%s) not found; want %s", c.name, c.oid, c.want)
			continue
		}
		if got.Name != c.want {
			t.Errorf("%s: Lookup(%s) = %s (%s); want %s", c.name, c.oid, got.Name, got.OID, c.want)
		}
		if !c.oid.HasPrefix(got.OID) {
			t.Errorf("%s: returned OID %s is not a prefix of %s", c.name, got.OID, c.oid)
		}
	}
}

// TestLookup_UnknownEnterpriseIsNotFound pins that a sysObjectID under an
// enterprise number no configured module declares is reported as unknown
// rather than attributed to a neighboring vendor.
func TestLookup_UnknownEnterpriseIsNotFound(t *testing.T) {
	for _, oid := range []snmp.OID{
		snmp.MustOID(1, 3, 6, 1, 4, 1, 99995, 1, 1),
		snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 1),
		snmp.MustOID(1, 3, 6, 1, 4, 1),
		snmp.MustOID(1, 3, 6, 1, 2, 1, 99999),
	} {
		if e, ok := sysobjectid.Lookup(oid); ok {
			t.Errorf("Lookup(%s) = %s; want not found", oid, e.Name)
		}
	}
}

// TestEntries_SortedAndAttributed pins the table shape the lookup rests
// on: OID order, one entry per OID, and the declaring module on each.
func TestEntries_SortedAndAttributed(t *testing.T) {
	entries := sysobjectid.Entries()
	if len(entries) != 10 {
		t.Fatalf("got %d entries, want 10", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].OID.Compare(entries[i].OID) >= 0 {
			t.Errorf("entries out of order at %d: %s then %s", i, entries[i-1].OID, entries[i].OID)
		}
	}
	e, ok := sysobjectid.Exact(snmp.MustOID(1, 3, 6, 1, 4, 1, 99996, 1))
	if !ok || e.Name != "fakeSharedByA" || e.Module != "FAKE-VENDOR-A-MIB" {
		t.Errorf("Exact(shared) = %+v, %v; want fakeSharedByA from FAKE-VENDOR-A-MIB", e, ok)
	}
	if _, ok := sysobjectid.Exact(snmp.MustOID(1, 3, 6, 1, 4, 1, 99996)); ok {
		t.Error("Exact(99996) found an entry; the enterprise root of the shared node is not declared")
	}

	entries[0].Name = "mutated"
	if sysobjectid.Entries()[0].Name == "mutated" {
		t.Error("Entries returned the package table rather than a copy")
	}
}
