// Package goldentest compiles the golden fixtures under
// testdata/golden and walks them against a scripted session, so the
// emitted key decoding is exercised as generated code rather than as
// source text. It is separate from the generator's own tests so a golden
// that fails to compile does not take the golden updater down with it.
package goldentest

import (
	"context"
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/snmp"
	"go.aledante.io/FlowSeer/src/protocol/snmp/cmd/mibgen/testdata/golden/fakemib"
)

// scriptedSession answers GetNext and GetBulk from a sorted instance
// list, ending every column with endOfMibView.
type scriptedSession struct {
	snmp.Session
	instances []snmp.VarBind
}

func (s scriptedSession) next(after snmp.OID) snmp.VarBind {
	for _, vb := range s.instances {
		if vb.GetHeader().OID.Compare(after) > 0 {
			return vb
		}
	}
	return snmp.EndOfMibViewVar{Header: snmp.Header{OID: after, Kind: snmp.KindEndOfMibView}}
}

func (s scriptedSession) GetNext(_ context.Context, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
	return s.GetBulk(context.Background(), 0, 1, oids)
}

func (s scriptedSession) GetBulk(_ context.Context, _, reps uint8, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
	cur := append([]snmp.OID(nil), oids...)
	var out []snmp.VarBind
	for range reps {
		for i, o := range cur {
			vb := s.next(o)
			cur[i] = vb.GetHeader().OID
			out = append(out, vb)
		}
	}
	return out, nil
}

func instance(col snmp.OID, value int32, suffix ...uint32) snmp.VarBind {
	return snmp.Integer32Var{Header: snmp.Header{OID: col.Append(suffix...), Kind: snmp.KindInteger32}, Value: value}
}

// TestWalk_MalformedSuffixKeepsTheRow walks fakePairTable, whose INDEX is
// an integer followed by a length-prefixed string, past a row whose
// suffix declares five octets and carries one. That row is delivered
// with a zero key and KeyValid false, and the walk goes on to the next
// row.
func TestWalk_MalformedSuffixKeepsTheRow(t *testing.T) {
	col := fakemib.FakePairValue.OID()
	sess := scriptedSession{instances: []snmp.VarBind{
		instance(col, 10, 1, 3, 'a', 'b', 'c'),
		instance(col, 20, 2, 5, 'x'),
		instance(col, 30, 3, 1, 'z'),
	}}

	var rows []fakemib.FakePairTableRow
	w := fakemib.FakePairTable.Walk(context.Background(), sess, fakemib.FakePairValue)
	for _, row := range w.Iter() {
		rows = append(rows, row)
	}
	if err := w.Err(); err != nil {
		t.Fatalf("walk: %v", err)
	}

	want := []struct {
		key   fakemib.FakePairTableKey
		valid bool
		value int32
	}{
		{fakemib.FakePairTableKey{FakePairSlot: 1, FakePairName: "abc"}, true, 10},
		{fakemib.FakePairTableKey{}, false, 20},
		{fakemib.FakePairTableKey{FakePairSlot: 3, FakePairName: "z"}, true, 30},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if rows[i].Key != w.key || rows[i].KeyValid() != w.valid || rows[i].FakePairValue != w.value {
			t.Errorf("row %d = %+v (valid %v), want key %+v valid %v value %d",
				i, rows[i], rows[i].KeyValid(), w.key, w.valid, w.value)
		}
	}
}

// TestWalk_AddressKey pins the IpAddress part: four arcs become a
// [netip.Addr] the row can be looked up by.
func TestWalk_AddressKey(t *testing.T) {
	col := fakemib.FakeAddrLabel.OID()
	sess := scriptedSession{instances: []snmp.VarBind{
		snmp.OctetStringVar{Header: snmp.Header{OID: col.Append(10, 0, 0, 1), Kind: snmp.KindOctetString}, Value: []byte("lan")},
	}}

	byAddr := map[fakemib.FakeAddrTableKey]string{}
	w := fakemib.FakeAddrTable.Walk(context.Background(), sess, fakemib.FakeAddrLabel)
	for _, row := range w.Iter() {
		byAddr[row.Key] = row.FakeAddrLabel
	}
	if err := w.Err(); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if got := byAddr[fakemib.FakeAddrTableKey{FakeAddrIp: netip.MustParseAddr("10.0.0.1")}]; got != "lan" {
		t.Errorf("label by address = %q, want lan; rows %v", got, byAddr)
	}
}

// TestKeyStructsAreMapKeys pins that the OBJECT IDENTIFIER and IMPLIED
// string key shapes are comparable, and that the augmenting table's row
// is keyed by the augmented table's struct.
func TestKeyStructsAreMapKeys(t *testing.T) {
	byOID := map[fakemib.FakeOidTableKey]int{{FakeOidPath: "1.3.6"}: 1}
	byName := map[fakemib.FakeImpliedTableKey]int{{FakeImpliedName: "eth0"}: 1}
	byAug := map[fakemib.FakeTableKey]fakemib.FakeAugTableRow{}
	byAug[fakemib.FakeAugTableRow{}.Key] = fakemib.FakeAugTableRow{}
	if len(byOID)+len(byName)+len(byAug) != 3 {
		t.Fatalf("map sizes = %d %d %d", len(byOID), len(byName), len(byAug))
	}
}
