package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
	"go.aledante.io/FlowSeer/generated/go/mib/lldpmib"
	"go.aledante.io/FlowSeer/src/common/snmp"
)

// These tests exercise generated row identity, column presence and BITS
// decoding against the checked-in bindings. In-process sessions let them
// distinguish an absent value from a reported zero and control when that
// distinction changes during a watch.

// sparseIfRowFixtures reports one ifTable row that carries ifDescr but
// no ifMtu — the shape a device produces when it does not implement a
// column the walk asked for.
func sparseIfRowFixtures(idx uint32, descr string) []vbFixture {
	return []vbFixture{
		{
			oid: ifEntry.Append(2, idx),
			vb:  snmp.OctetStringVar{Header: snmp.Header{OID: ifEntry.Append(2, idx), Kind: snmp.KindOctetString}, Value: []byte(descr)},
		},
	}
}

// zeroValuedIfRowFixtures reports a row whose ifMtu is a genuine zero.
// Without per-column observation this row is indistinguishable from the
// sparse one above.
func zeroValuedIfRowFixtures(idx uint32) []vbFixture {
	return []vbFixture{
		{
			oid: ifEntry.Append(4, idx),
			vb:  snmp.Integer32Var{Header: snmp.Header{OID: ifEntry.Append(4, idx), Kind: snmp.KindInteger32}, Value: 0},
		},
	}
}

// TestIfTableRow_ObservedDistinguishesZeroFromAbsent is the core
// presence contract: a column that returned zero reads observed, one
// that never landed reads unobserved.
func TestIfTableRow_ObservedDistinguishesZeroFromAbsent(t *testing.T) {
	var vbs []vbFixture
	vbs = append(vbs, sparseIfRowFixtures(1, "eth0")...)
	vbs = append(vbs, zeroValuedIfRowFixtures(2)...)
	sess := &fakeSession{vbs: vbs}

	tw := ifmib.IfTable.Walk(context.Background(), sess, ifmib.IfDescr, ifmib.IfMtu)
	rows := map[uint32]ifmib.IfTableRow{}
	for idx, row := range tw.Iter() {
		if idx.Len() == 0 {
			t.Fatal("row yielded with an empty index")
		}
		if !row.Index.Equal(idx) {
			t.Errorf("Row.Index = %v, want iterator index %v", row.Index, idx)
		}
		rows[idx.At(idx.Len()-1)] = row
	}
	if err := tw.Err(); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("walk yielded %d rows, want 2", len(rows))
	}

	sparse := rows[1]
	if !sparse.Observed(ifmib.IfDescr) {
		t.Error("row 1: IfDescr unobserved, want observed")
	}
	if sparse.Observed(ifmib.IfMtu) {
		t.Error("row 1: IfMtu observed, want unobserved (the agent never reported it)")
	}

	zeroed := rows[2]
	if !zeroed.Observed(ifmib.IfMtu) {
		t.Error("row 2: IfMtu unobserved, want observed (the agent reported a genuine zero)")
	}
	if zeroed.IfMtu != 0 {
		t.Errorf("row 2: IfMtu = %d, want 0", zeroed.IfMtu)
	}
	if zeroed.Observed(ifmib.IfDescr) {
		t.Error("row 2: IfDescr observed, want unobserved")
	}
}

// TestIfTableRow_ObservedOnUnrequestedColumnRow covers the walker's
// row-presence rule: an index seen only through a column the caller did
// not request still yields a row, and every requested column on it
// reads unobserved.
func TestIfTableRow_ObservedOnUnrequestedColumnRow(t *testing.T) {
	sess := &fakeSession{vbs: []vbFixture{{
		oid: ifEntry.Append(16, 7),
		vb:  snmp.Counter32Var{Header: snmp.Header{OID: ifEntry.Append(16, 7), Kind: snmp.KindCounter32}, Value: 4242},
	}}}

	tw := ifmib.IfTable.Walk(context.Background(), sess, ifmib.IfDescr, ifmib.IfOperStatus)
	n := 0
	for idx, row := range tw.Iter() {
		n++
		if !row.Index.Equal(idx) {
			t.Errorf("unrequested-column Row.Index = %v, want %v", row.Index, idx)
		}
		if row.Observed(ifmib.IfDescr) || row.Observed(ifmib.IfOperStatus) {
			t.Error("requested column reads observed on a row assembled from an unrequested column")
		}
		// A column the walk never asked for is never observed either.
		if row.Observed(ifmib.IfOutOctets) {
			t.Error("unrequested IfOutOctets reads observed")
		}
	}
	if err := tw.Err(); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if n != 1 {
		t.Fatalf("walk yielded %d rows, want 1", n)
	}
}

// TestIfTableRow_ObservedRejectsForeignColumn keeps the presence lookup
// keyed on column identity, not on the column's last sub-id — two
// tables' columns collide on that arc constantly.
func TestIfTableRow_ObservedRejectsForeignColumn(t *testing.T) {
	sess := fakeIfTableSession()
	tw := ifmib.IfTable.Walk(context.Background(), sess, ifmib.IfDescr)
	for _, row := range tw.Iter() {
		if row.Observed(lldpmib.LldpRemPortId) {
			t.Error("a column of another table reads observed")
		}
	}
	if err := tw.Err(); err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// TestIfTableWalk_RejectsForeignColumnWithCollidingSubID is the
// collision case: ifInMulticastPkts lives in ifXTable at
// …31.1.1.1.2, and its last sub-id 2 is ifDescr's sub-id in ifTable.
// Keying the walk's column lookup on that bare sub-id would decode
// every ifDescr answer into the row and report it observed, handing the
// caller data for a column it never asked for.
func TestIfTableWalk_RejectsForeignColumnWithCollidingSubID(t *testing.T) {
	sess := fakeIfTableSession()

	tw := ifmib.IfTable.Walk(context.Background(), sess, ifmib.IfInMulticastPkts)
	for _, row := range tw.Iter() {
		if row.Observed(ifmib.IfDescr) {
			t.Error("a foreign column enabled the local column sharing its sub-id")
		}
		t.Error("walk over a foreign column yielded a row, want none")
	}
	if err := tw.Err(); err == nil {
		t.Fatal("walk over a foreign column succeeded, want an error")
	} else if !errors.Is(err, snmp.ErrForeignColumn) {
		t.Errorf("walk error = %v, want ErrForeignColumn", err)
	}
}

// TestIfTableWalk_RejectsForeignColumnWithoutCollision covers the other
// half: a foreign column whose sub-id matches nothing local must not
// leave the caller with a silently empty, all-unobserved table.
func TestIfTableWalk_RejectsForeignColumnWithoutCollision(t *testing.T) {
	sess := fakeIfTableSession()

	tw := ifmib.IfTable.Walk(context.Background(), sess, ifmib.IfDescr, lldpmib.LldpRemPortId)
	for range tw.Iter() { //nolint:revive // draining the iterator is the point
		t.Error("walk over a foreign column yielded a row, want none")
	}
	if err := tw.Err(); !errors.Is(err, snmp.ErrForeignColumn) {
		t.Fatalf("walk error = %v, want ErrForeignColumn", err)
	}
}

// lldpRemEntry is the lldpRemTable.entry prefix.
var lldpRemEntry = snmp.MustOID(1, 0, 8802, 1, 1, 2, 1, 4, 1, 1)

// lldpCapFixture reports one lldpRemTable row whose capability columns
// carry the supplied BITS octets. The index is the RFC 4957 triple
// (timeMark, localPortNum, remIndex).
func lldpCapFixture(remIndex uint32, supported, enabled []byte) []vbFixture {
	sup := lldpRemEntry.Append(11, 0, 1, remIndex)
	ena := lldpRemEntry.Append(12, 0, 1, remIndex)
	return []vbFixture{
		{oid: sup, vb: snmp.OctetStringVar{Header: snmp.Header{OID: sup, Kind: snmp.KindOctetString}, Value: supported}},
		{oid: ena, vb: snmp.OctetStringVar{Header: snmp.Header{OID: ena, Kind: snmp.KindOctetString}, Value: enabled}},
	}
}

// TestLldpRemTable_CapabilitiesDecodeAsBitSet is the bit-string
// contract: a real agent answers the capability columns with a
// two-octet OCTET STRING, and a bridge-plus-router device sets two bits
// at once — a shape no single integer can carry.
func TestLldpRemTable_CapabilitiesDecodeAsBitSet(t *testing.T) {
	// 0x28 = 0010 1000 -> bridge(2) + router(4); second octet sets
	// bit 9, which LLDP-MIB gives no name.
	sess := &fakeSession{vbs: lldpCapFixture(1, []byte{0x28, 0x40}, []byte{0x20, 0x00})}

	tw := lldpmib.LldpRemTable.Walk(context.Background(), sess, lldpmib.LldpRemSysCapSupported, lldpmib.LldpRemSysCapEnabled)
	n := 0
	for idx, row := range tw.Iter() {
		n++
		if !row.Index.Equal(idx) || idx.Len() != 3 {
			t.Errorf("composite Row.Index = %v, iterator index = %v; want matching three-arc indexes", row.Index, idx)
		}
		if !row.LldpRemSysCapSupported.Has(lldpmib.LldpSystemCapabilitiesMapBridge) {
			t.Error("supported: bridge bit not set")
		}
		if !row.LldpRemSysCapSupported.Has(lldpmib.LldpSystemCapabilitiesMapRouter) {
			t.Error("supported: router bit not set")
		}
		if !row.LldpRemSysCapSupported.Has(9) {
			t.Error("supported: unnamed bit 9 dropped")
		}
		if row.LldpRemSysCapSupported.Has(lldpmib.LldpSystemCapabilitiesMapOther) {
			t.Error("supported: other bit set, want unset")
		}
		if !row.LldpRemSysCapEnabled.Equal(snmp.NewBitSet(lldpmib.LldpSystemCapabilitiesMapBridge)) {
			t.Errorf("enabled = %v, want just bridge", row.LldpRemSysCapEnabled)
		}
	}
	if err := tw.Err(); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if n != 1 {
		t.Fatalf("walk yielded %d rows, want 1", n)
	}
}

// TestLldpRemTable_EmptyCapabilitiesIsEmptySet pins the "no
// capabilities" answer to the empty set rather than to bit zero.
func TestLldpRemTable_EmptyCapabilitiesIsEmptySet(t *testing.T) {
	sess := &fakeSession{vbs: lldpCapFixture(1, []byte{}, []byte{0x00, 0x00})}

	tw := lldpmib.LldpRemTable.Walk(context.Background(), sess, lldpmib.LldpRemSysCapSupported, lldpmib.LldpRemSysCapEnabled)
	for _, row := range tw.Iter() {
		if !row.LldpRemSysCapSupported.Empty() {
			t.Errorf("supported = %v, want empty", row.LldpRemSysCapSupported)
		}
		if row.LldpRemSysCapSupported.Has(lldpmib.LldpSystemCapabilitiesMapOther) {
			t.Error("empty value reports the zero-position bit as set")
		}
		if !row.LldpRemSysCapEnabled.Empty() {
			t.Errorf("enabled = %v, want empty", row.LldpRemSysCapEnabled)
		}
		// The columns were requested and answered, so both read observed
		// even though the sets are empty.
		if !row.Observed(lldpmib.LldpRemSysCapSupported) || !row.Observed(lldpmib.LldpRemSysCapEnabled) {
			t.Error("an answered capability column reads unobserved")
		}
	}
	if err := tw.Err(); err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// TestLldpRemTable_MalformedCapabilityDeclines keeps the walk's
// decline-not-panic semantics: an agent answering the capability column
// with an Integer32 fails the walk with an error.
func TestLldpRemTable_MalformedCapabilityDeclines(t *testing.T) {
	sup := lldpRemEntry.Append(11, 0, 1, 1)
	sess := &fakeSession{vbs: []vbFixture{{
		oid: sup,
		vb:  snmp.Integer32Var{Header: snmp.Header{OID: sup, Kind: snmp.KindInteger32}, Value: 20},
	}}}

	tw := lldpmib.LldpRemTable.Walk(context.Background(), sess, lldpmib.LldpRemSysCapSupported)
	for range tw.Iter() { //nolint:revive // draining the iterator is the point
	}
	if tw.Err() == nil {
		t.Fatal("walk over a malformed BITS value succeeded, want a decode error")
	}
}

// presenceWatchSession serves targeted counter fetches from the same
// fixtures as its full walks. Each walk keeps its own snapshot while
// the test replaces the fixtures for the next tick.
type presenceWatchSession struct {
	*fakeSession
	mu sync.Mutex
}

func (s *presenceWatchSession) setFixtures(vbs []vbFixture) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vbs = vbs
}

func (s *presenceWatchSession) BulkWalk(ctx context.Context, _ snmp.OID, _ ...snmp.CallOption) *snmp.Walker {
	s.mu.Lock()
	snapshot := &fakeSession{vbs: s.vbs}
	s.mu.Unlock()
	return snapshot.pump(ctx)
}

func (s *presenceWatchSession) Get(_ context.Context, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var vbs []snmp.VarBind
	for _, oid := range oids {
		for _, f := range s.vbs {
			if f.oid.Equal(oid) {
				vbs = append(vbs, f.vb)
			}
		}
	}
	return vbs, nil
}

func TestIfTableWatch_ReportsPresenceChangesAtZero(t *testing.T) {
	for _, partial := range []bool{false, true} {
		name := "full-walk"
		if partial {
			name = "counter-fetch"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				base := append(sparseIfRowFixtures(7, "eth7"), vbFixture{
					oid: ifEntry.Append(9, 7),
					vb: snmp.TimeTicksVar{
						Header: snmp.Header{OID: ifEntry.Append(9, 7), Kind: snmp.KindTimeTicks},
						Value:  1,
					},
				})
				sess := &presenceWatchSession{fakeSession: &fakeSession{vbs: base}}
				walkInterval, counterInterval := time.Second, time.Hour
				if partial {
					walkInterval, counterInterval = counterInterval, walkInterval
				}
				tw := ifmib.IfTable.Watch(t.Context(), sess,
					[]snmp.AnyColumn{ifmib.IfDescr, ifmib.IfInOctets},
					snmp.WithCadenceBounds(time.Hour, time.Hour),
					snmp.WithForcedWalkInterval(walkInterval),
					snmp.WithCounterCadence(ifmib.IfInOctets, counterInterval),
					snmp.WithPrevRow(),
				)
				defer func() { _ = tw.Close() }()
				var events []snmp.WatchEvent[ifmib.IfTableRow]
				go func() {
					for _, ev := range tw.Iter() {
						events = append(events, ev)
					}
				}()
				synctest.Wait()
				if len(events) != 1 || events[0].Kind != snmp.ChangeKindAdded || events[0].Row.Observed(ifmib.IfInOctets) {
					t.Fatalf("cold start = %+v, want one Added event with an absent counter", events)
				}

				sess.setFixtures(append(append([]vbFixture(nil), base...), vbFixture{
					oid: ifEntry.Append(10, 7),
					vb:  snmp.Counter32Var{Header: snmp.Header{OID: ifEntry.Append(10, 7), Kind: snmp.KindCounter32}, Value: 0},
				}))
				time.Sleep(time.Second)
				synctest.Wait()
				if len(events) != 2 {
					t.Fatalf("absent counter became zero: got %d events, want Added then Modified", len(events))
				}
				ev := events[1]
				if ev.Kind != snmp.ChangeKindModified || !ev.Row.Observed(ifmib.IfInOctets) || ev.Row.IfInOctets != 0 {
					t.Errorf("presence change = %+v, want Modified with an observed zero", ev)
				}
				if ev.Prev == nil || ev.Prev.Observed(ifmib.IfInOctets) || ev.Row.IfDescr != "eth7" {
					t.Errorf("presence change lost the previous absence or existing description: %+v", ev)
				}

				time.Sleep(time.Second)
				synctest.Wait()
				if len(events) != 2 {
					t.Errorf("unchanged observed zero emitted another event: %d events", len(events))
				}
				if !partial {
					sess.setFixtures(base)
					time.Sleep(time.Second)
					synctest.Wait()
					if len(events) != 3 {
						t.Fatalf("zero counter became absent: got %d events, want 3", len(events))
					}
					ev = events[2]
					if ev.Kind != snmp.ChangeKindModified || ev.Row.Observed(ifmib.IfInOctets) || ev.Prev == nil || !ev.Prev.Observed(ifmib.IfInOctets) {
						t.Errorf("presence loss = %+v, want Modified from observed to absent", ev)
					}
				}
				if err := tw.LastTickErr(); err != nil {
					t.Errorf("tick: %v", err)
				}
			})
		})
	}
}
