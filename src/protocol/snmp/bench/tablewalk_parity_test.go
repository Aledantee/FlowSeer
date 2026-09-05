package bench

import (
	"context"
	"reflect"
	"testing"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// Embedding only Session hides the native column requester, forcing generated
// walks through GetBulk's decoded varbinds against the same UDP responder.
type decodedSession struct{ snmp.Session }

func TestTableWalkRawDecodedParity(t *testing.T) {
	var mib []mibEntry
	add := func(col, idx uint32, value []byte) {
		oid := []uint32{1, 3, 6, 1, 2, 1, 2, 2, 1, col, idx}
		mib = append(mib, mibEntry{oid: oid, vb: varbind(oid, value)})
	}
	add(2, 1, octetTLV(""))
	add(2, 2, octetTLV("uplink"))
	add(2, 10, octetTLV("wifi"))
	add(5, 1, tlv(0x42, []byte{0}))
	add(5, 2, tlv(0x42, []byte{0x3b, 0x9a, 0xca, 0x00}))
	add(5, 10, counter32TLV(54000000)) // Off-spec Counter32 for a Gauge32 column.
	add(8, 1, intTLV(1))
	add(8, 2, tlv(0x42, []byte{2})) // Off-spec Gauge32 for an INTEGER enum.
	add(8, 10, intTLV(7))
	add(9, 1, tlv(0x43, []byte{0}))
	add(9, 2, tlv(0x43, []byte{1, 0, 0}))
	add(9, 10, tlv(0x43, []byte{0x03, 0xe7}))
	add(10, 1, counter32TLV(0))
	add(10, 10, counter32TLV(0xffffffff)) // Index 2 is absent, unlike index 1's zero.
	add(16, 1, counter32TLV(1234))        // Present on the agent but not selected.
	addr, _ := startResponderMIB(t, mib)
	native := dialNative(t, addr)
	t.Cleanup(func() { _ = native.Close() })

	cols := []snmp.AnyColumn{ifmib.IfInOctets, ifmib.IfDescr, ifmib.IfSpeed, ifmib.IfOperStatus, ifmib.IfLastChange, ifmib.IfOutErrors}
	observedCols := append(append([]snmp.AnyColumn(nil), cols...), ifmib.IfOutOctets)
	type rowSnapshot struct {
		key        snmp.OID
		index      ifmib.IfTableKey
		inOctets   uint32
		descr      string
		speed      uint32
		oper       ifmib.IfOperStatusValue
		lastChange uint32
		observed   [7]bool
	}
	want := []rowSnapshot{
		{key: snmp.MustOID(1), index: ifmib.IfTableKey{IfIndex: 1}, oper: ifmib.IfOperStatusValueUp, observed: [7]bool{true, true, true, true, true, false, false}},
		{key: snmp.MustOID(2), index: ifmib.IfTableKey{IfIndex: 2}, descr: "uplink", speed: 1000000000, oper: ifmib.IfOperStatusValueDown, lastChange: 65536, observed: [7]bool{false, true, true, true, true, false, false}},
		{key: snmp.OID{}.Append(10), index: ifmib.IfTableKey{IfIndex: 10}, inOctets: 0xffffffff, descr: "wifi", speed: 54000000, oper: ifmib.IfOperStatusValueLowerLayerDown, lastChange: 999, observed: [7]bool{true, true, true, true, true, false, false}},
	}
	for _, reps := range []int{0, 1} {
		name := "defaults"
		if reps == 1 {
			name = "one_repetition"
		}
		t.Run(name, func(t *testing.T) {
			var rawRows []rowSnapshot
			for _, path := range []struct {
				name string
				sess snmp.Session
				raw  bool
			}{
				{name: "raw", sess: native, raw: true},
				{name: "decoded", sess: decodedSession{native}},
			} {
				// Check the path actually taken so two decoded runs cannot
				// accidentally satisfy the differential assertion.
				probe := snmp.WalkColumns(context.Background(), path.sess, []snmp.OID{ifmib.IfInOctets.OID()}, snmp.TableWalkOptions{MaxRepetitions: 1})
				probed := false
				for _, cells := range probe.Iter() {
					probed = true
					if len(cells) != 1 || (cells[0].Value.VB == nil) != path.raw {
						t.Fatalf("%s: unexpected column path: %+v", path.name, cells)
					}
					break
				}
				if !probed || probe.Err() != nil {
					t.Fatalf("%s: column path probe: yielded=%v err=%v", path.name, probed, probe.Err())
				}

				var w *ifmib.IfTableWalker
				if reps == 0 {
					w = ifmib.IfTable.Walk(context.Background(), path.sess, cols...)
				} else {
					w = ifmib.IfTable.WalkWithOptions(context.Background(), path.sess, snmp.TableWalkOptions{MaxRepetitions: reps}, cols...)
				}
				var got []rowSnapshot
				for idx, row := range w.Iter() {
					snapshot := rowSnapshot{key: idx, index: row.Key, inOctets: row.IfInOctets, descr: row.IfDescr, speed: row.IfSpeed, oper: row.IfOperStatus, lastChange: row.IfLastChange}
					for i, col := range observedCols {
						snapshot.observed[i] = row.Observed(col)
					}
					got = append(got, snapshot)
				}
				if w.Err() != nil {
					t.Fatalf("%s: walk: %v", path.name, w.Err())
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s: rows = %+v, want %+v", path.name, got, want)
				}
				if path.raw {
					rawRows = got
				} else if !reflect.DeepEqual(got, rawRows) {
					t.Fatalf("decoded rows = %+v, raw rows = %+v", got, rawRows)
				}
			}
		})
	}
}
