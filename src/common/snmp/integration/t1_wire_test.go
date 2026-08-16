//go:build snmp_integration_t1

package integration

import (
	"context"
	"testing"

	"go.aledante.io/FlowSeer/src/common/snmp"
)

// TestT1Bootstrap is the smoke test that proves the snmpd container
// is reachable and the library can decode a basic OCTET STRING off
// the wire. sysDescr.0 is RFC-1213-mandated non-empty on every
// SNMPv2 agent; if this fails every downstream T1 test is downstream
// of the failure.
func TestT1Bootstrap(t *testing.T) {
	sess := t1DialV2c(t)
	sysDescr := snmp.MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	vbs, err := sess.Get(context.Background(), []snmp.OID{sysDescr})
	if err != nil {
		t.Fatalf("Get sysDescr.0: %v", err)
	}
	if len(vbs) != 1 {
		t.Fatalf("got %d varbinds, want 1", len(vbs))
	}
	os, ok := vbs[0].(snmp.OctetStringVar)
	if !ok {
		t.Fatalf("varbind type = %T, want OctetStringVar", vbs[0])
	}
	if len(os.Value) == 0 {
		t.Fatal("sysDescr.0 is empty")
	}
	t.Logf("sysDescr.0 = %q", os.Value)
}

// TestT1_GetNext_ifEntry exercises GetNext at the ifTable.entry
// prefix; snmpd's native IF-MIB implementation walks the container's
// network interfaces and returns the first column of the first row.
// The returned VarBind's OID must sit inside the ifTable subtree —
// proof we are walking the right MIB region.
func TestT1_GetNext_ifEntry(t *testing.T) {
	sess := t1DialV2c(t)
	ifEntry := snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
	vbs, err := sess.GetNext(context.Background(), []snmp.OID{ifEntry})
	if err != nil {
		t.Fatalf("GetNext ifEntry: %v", err)
	}
	if len(vbs) != 1 {
		t.Fatalf("got %d varbinds, want 1", len(vbs))
	}
	got := vbs[0].GetHeader().OID
	if !got.HasPrefix(ifEntry) {
		t.Errorf("returned OID %s not under ifEntry %s", got, ifEntry)
	}
}

// TestT1_GetBulk_ifTable exercises GetBulk with non-rep=0,
// max-rep=10 over the ifTable.entry subtree. snmpd returns up to
// 10 varbinds in one PDU. The container has at least loopback, so
// the response must contain at least one ifTable varbind; trailing
// EndOfMibView varbinds are tolerated if the table is shorter than
// max-rep.
func TestT1_GetBulk_ifTable(t *testing.T) {
	sess := t1DialV2c(t)
	ifEntry := snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
	vbs, err := sess.GetBulk(context.Background(), 0, 10, []snmp.OID{ifEntry})
	if err != nil {
		t.Fatalf("GetBulk ifEntry: %v", err)
	}
	if len(vbs) == 0 {
		t.Fatal("GetBulk returned zero varbinds; expected at least loopback's ifIndex")
	}
	ifTable := 0
	for i, vb := range vbs {
		k := vb.GetHeader().Kind
		if k == snmp.KindEndOfMibView {
			continue
		}
		if !vb.GetHeader().OID.HasPrefix(ifEntry) {
			t.Errorf("varbind %d OID %s not under ifEntry", i, vb.GetHeader().OID)
		}
		ifTable++
	}
	if ifTable == 0 {
		t.Fatal("GetBulk returned only EndOfMibView varbinds; expected at least one ifTable row")
	}
}
