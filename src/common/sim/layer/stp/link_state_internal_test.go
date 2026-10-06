package stp

import (
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// TestPortStateAndLinkRecordFieldsAreDisjoint verifies that link ownership is
// strictly separated from per-tree port state: no field on portState repeats
// any field on linkRecord.
func TestPortStateAndLinkRecordFieldsAreDisjoint(t *testing.T) {
	t.Parallel()

	linkFields := map[string]bool{}
	linkType := reflect.TypeOf(linkRecord{})
	for i := 0; i < linkType.NumField(); i++ {
		linkFields[linkType.Field(i).Name] = true
	}

	portType := reflect.TypeOf(portState{})
	for i := 0; i < portType.NumField(); i++ {
		f := portType.Field(i)
		if f.Anonymous {
			t.Errorf("portState must not embed anonymous structs, found %v", f.Type)
		}
		if linkFields[f.Name] {
			t.Errorf("field %q is declared on both linkRecord and portState; link properties must not be duplicated on tree port state", f.Name)
		}
	}

	expectedLinkFields := []string{
		"up", "pointToPoint", "edge", "sendRSTP", "adminEdge", "linkPathCost",
		"external", "bpduGuardDisabled", "pvstBoundary", "mdelayWhile",
		"edgeDelayWhile", "rxBPDUs", "badBPDUs",
	}
	for _, name := range expectedLinkFields {
		if !linkFields[name] {
			t.Errorf("linkRecord is missing expected field %q", name)
		}
	}
}

// TestLinkDownClearsHandshakeStateAndTimersOnEveryTree verifies that bringing
// a link down clears role, state, received information, proposal/agreement
// handshake state, and forward-delay timer on every tree running over the port.
func TestLinkDownClearsHandshakeStateAndTimersOnEveryTree(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	cfg := Config{
		Priority: 32768,
		Address:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		Ports:    map[string]Port{"p1": {PathCost: 100}},
		MST: &MST{
			Name: "region-1",
			Instances: map[bpdu.MSTID]Instance{
				1: {VLANs: []vlan.ID{10}},
			},
		},
	}.Normalize(layer.Env{})
	l := newLayer(cfg)

	l.LinkChange(t0, "p1", true, true, 1_000_000_000)

	// Plant non-zero handshake and timer values on all trees.
	for _, tr := range l.trees {
		p := tr.ports["p1"]
		p.agreed = true
		p.proposing = true
		p.fwdDelayTimer = t0.Add(10 * time.Second)
		p.rcvInfoValid = true
		p.pvidInconsistent = true
		p.loopInconsistent = true
	}

	l.LinkChange(t0.Add(time.Second), "p1", false, true, 0)

	if l.links["p1"].up {
		t.Errorf("link p1 up = true, want false")
	}

	for id, tr := range l.trees {
		p := tr.ports["p1"]
		if p.role != bpdu.RoleDisabled {
			t.Errorf("tree %v p1 role = %v, want RoleDisabled", id, p.role)
		}
		if p.state != StateDiscarding {
			t.Errorf("tree %v p1 state = %v, want StateDiscarding", id, p.state)
		}
		if p.rcvInfoValid {
			t.Errorf("tree %v p1 rcvInfoValid = true, want false", id)
		}
		if p.agreed {
			t.Errorf("tree %v p1 agreed = true, want false", id)
		}
		if p.proposing {
			t.Errorf("tree %v p1 proposing = true, want false", id)
		}
		if !p.fwdDelayTimer.IsZero() {
			t.Errorf("tree %v p1 fwdDelayTimer = %v, want zero", id, p.fwdDelayTimer)
		}
		if p.pvidInconsistent {
			t.Errorf("tree %v p1 pvidInconsistent = true, want false", id)
		}
		if p.loopInconsistent {
			t.Errorf("tree %v p1 loopInconsistent = true, want false", id)
		}
	}
}

func TestPVIDInconsistentSSTPLeavesArrivalTreeLoopMark(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	l := newLayer(Config{
		Priority: 32768,
		Address:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		Ports:    map[string]Port{"p1": {LoopGuard: true}},
		PVST: &PVST{Trees: map[vlan.ID]Tree{
			1:  {},
			10: {},
		}},
	}.Normalize(layer.Env{}))
	l.LinkChange(t0, "p1", true, true, 1_000_000_000)

	p := l.trees[treeID(10)].ports["p1"]
	p.loopInconsistent = true
	b := bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		RootID:       bpdu.BridgeID{Priority: 4096},
		BridgeID:     bpdu.BridgeID{Priority: 4096},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	b.SetRole(bpdu.RoleDesignated)

	_, outcome := l.ReceiveSSTP(t0.Add(time.Second), "p1", SSTPArrival{
		ArrivalVID: vlan.ID(10),
		TLVVID:     vlan.ID(20),
		Admitted:   true,
	}, b)

	if outcome != SSTPPVIDInconsistent {
		t.Fatalf("outcome = %q, want %q", outcome, SSTPPVIDInconsistent)
	}
	if !p.loopInconsistent {
		t.Error("arrival tree loop mark = false after a PVID-inconsistent BPDU, want true")
	}
	if !p.pvidInconsistent {
		t.Error("arrival tree PVID mark = false after a PVID-inconsistent BPDU, want true")
	}
	if p.role != bpdu.RoleAlternate {
		t.Errorf("arrival tree role = %v, want Alternate", p.role)
	}
}

func TestBlockReasonReadsTheTreesOwnMark(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		RootID:       bpdu.BridgeID{Priority: 4096},
		BridgeID:     bpdu.BridgeID{Priority: 4096},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	peer.SetRole(bpdu.RoleDesignated)

	pvst := newLayer(Config{
		Priority: 32768,
		Address:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		Ports:    map[string]Port{"p1": {LoopGuard: true}},
		PVST: &PVST{Trees: map[vlan.ID]Tree{
			1:  {},
			10: {},
		}},
	}.Normalize(layer.Env{}))
	pvst.LinkChange(t0, "p1", true, true, 1_000_000_000)
	pvst.Receive(t0.Add(time.Second), "p1", peer)
	arrival := SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}
	for _, second := range []int{1, 3, 5, 7} {
		pvst.Advance(t0.Add(time.Duration(second) * time.Second))
		if _, outcome := pvst.ReceiveSSTP(t0.Add(time.Duration(second)*time.Second), "p1", arrival, peer); outcome != SSTPApplied {
			t.Fatalf("VLAN 10 refresh at %ds = %v, want applied", second, outcome)
		}
	}
	for _, second := range []int{7, 9} {
		if second == 9 {
			pvst.ReceiveSSTP(t0.Add(9*time.Second), "p1", arrival, peer)
		}
		if got := pvst.PortInfo("p1"); got.Role != bpdu.RoleAlternate || got.BlockReason != BlockReasonLoopInconsistent {
			t.Fatalf("CIST at %ds = %+v, want Alternate/loop-inconsistent", second, got)
		}
		if got := pvst.VLANPortInfo(10, "p1"); got.Role != bpdu.RoleRoot || got.BlockReason != "" {
			t.Fatalf("VLAN 10 at %ds = %+v, want Root with no loop-guard reason", second, got)
		}
	}

	mst := newLayer(Config{
		Priority: 32768,
		Address:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x03},
		Ports:    map[string]Port{"p1": {}},
		MST: &MST{Name: "region-1", Instances: map[bpdu.MSTID]Instance{
			1: {VLANs: []vlan.ID{10}},
		}},
	}.Normalize(layer.Env{}))
	mst.LinkChange(t0, "p1", true, true, 1_000_000_000)
	mst.cist().ports["p1"].loopInconsistent = true
	var flushes []layer.FlushTarget
	mst.recomputeAll(t0.Add(time.Second), &flushes)

	if got := mst.instancePortInfo(1, "p1"); got.BlockReason != BlockReasonLoopInconsistent {
		t.Fatalf("MSTI port info = %+v, want the CIST loop-guard mark", got)
	}
}

func TestLinkDownClearsHeldTCNAndAcknowledgment(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	l := newLayer(Config{
		Ports: map[string]Port{"p1": {}},
	}.Normalize(layer.Env{}))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.tx(l.cist(), "p1").helloWhen = time.Time{}
	tx := l.tx(l.cist(), "p1")
	tx.newInfo = true
	tx.tick = now.Add(-time.Second)
	l.cist().ports["p1"].tcAck = true

	l.LinkChange(now.Add(time.Second), "p1", false, true, 0)
	l.tx(l.cist(), "p1").helloWhen = time.Time{}

	if _, ok := l.NextWake(); ok {
		t.Fatal("NextWake reported a timer after link down cleared a held TCN")
	}
	if l.cist().ports["p1"].tcAck {
		t.Error("link down left a pending topology-change acknowledgment")
	}

	guarded := newLayer(Config{
		Ports: map[string]Port{"p1": {BPDUGuard: true}},
	}.Normalize(layer.Env{}))
	guarded.LinkChange(now, "p1", true, true, 1_000_000_000)
	guarded.cist().ports["p1"].tcAck = true
	guarded.Receive(now.Add(4*time.Second), "p1", bpdu.BPDU{})
	if guarded.cist().ports["p1"].tcAck {
		t.Error("BPDU-guard disable left a pending topology-change acknowledgment")
	}
}

func TestHeldTCNIsNotReleasedOnAnRSTPPort(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	l := newLayer(Config{
		Ports: map[string]Port{"p1": {}},
	}.Normalize(layer.Env{}))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.tx(l.cist(), "p1").helloWhen = time.Time{}
	p := l.cist().ports["p1"]
	p.role = bpdu.RoleRoot
	p.tcActive = true
	p.tcWhile = now.Add(time.Minute)
	tx := l.tx(l.cist(), "p1")
	tx.newInfo = true
	tx.tick = now

	effects := l.Advance(now.Add(time.Second))
	if len(effects.Emissions) != 1 || effects.Emissions[0].Port != "p1" {
		t.Fatalf("released request emissions = %+v, want one on p1", effects.Emissions)
	}
	for _, emission := range effects.Emissions {
		decoded, err := bpdu.Decode(emission.Frame)
		if err != nil {
			t.Fatalf("decode emission: %v", err)
		}
		if decoded.Type != bpdu.TypeRapid {
			t.Errorf("released request type = %v, want Rapid", decoded.Type)
		}
	}
}

func TestPointToPointChangeDeactivatesAnActivePort(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	l := newLayer(Config{
		Ports: map[string]Port{"p1": {}},
	}.Normalize(layer.Env{}))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	p := l.cist().ports["p1"]
	p.state = StateForwarding
	p.tcActive = true
	p.tcWhile = now.Add(time.Minute)

	fx := l.LinkChange(now.Add(time.Second), "p1", true, false, 1_000_000_000)
	if len(fx.Emissions) != 1 || fx.Emissions[0].Port != "p1" {
		t.Fatalf("point-to-point reset emissions = %+v, want one on p1", fx.Emissions)
	}
	if p.tcActive || !p.tcWhile.IsZero() {
		t.Errorf("point-to-point change left topology state active=%t timer=%v", p.tcActive, p.tcWhile)
	}
	if p.state != StateDiscarding {
		t.Errorf("point-to-point change state = %v, want Discarding", p.state)
	}
	flushed := false
	for _, target := range fx.Flush {
		if target.Port == "p1" {
			flushed = true
		}
	}
	if !flushed {
		t.Errorf("point-to-point change flushes = %v, want p1", fx.Flush)
	}
}

// TestCloneLinkRecordIsIndependent verifies that mutating a clone's linkRecord
// does not affect the source Layer's linkRecord.
func TestCloneLinkRecordIsIndependent(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	cfg := Config{
		Priority: 32768,
		Address:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		Ports:    map[string]Port{"p1": {PathCost: 100}},
	}.Normalize(layer.Env{})
	l := newLayer(cfg)

	l.LinkChange(t0, "p1", true, true, 1_000_000_000)

	origLink := l.links["p1"]
	cp := l.Clone()
	cloneLink := cp.links["p1"]

	if origLink == cloneLink {
		t.Fatalf("Clone did not create independent linkRecord pointer")
	}

	cloneLink.edge = !origLink.edge
	cloneLink.linkPathCost += 9999
	cloneLink.sendRSTP = !origLink.sendRSTP

	if origLink.edge == cloneLink.edge {
		t.Errorf("mutating clone edge mutated source edge")
	}
	if origLink.linkPathCost == cloneLink.linkPathCost {
		t.Errorf("mutating clone linkPathCost mutated source linkPathCost")
	}
	if origLink.sendRSTP == cloneLink.sendRSTP {
		t.Errorf("mutating clone sendRSTP mutated source sendRSTP")
	}
}

// TestPortLinkedAgreesWithReceiveSSTPsOwnPortDownCheck is evidence for the
// divergence PortLinked closes (switch.go's Peek arm has no other read-only
// way to ask what ReceiveSSTP itself checks first): for a port this layer
// never configured, for one it configured but has not yet linked, and for
// one it has linked, PortLinked(port) is false in exactly the cases
// ReceiveSSTP itself returns SSTPPortDown, never diverging on either input.
// switch.go relies on this equivalence to make Peek agree with Forward
// without calling ReceiveSSTP itself, since Peek must not mutate the link
// half of a receive.
func TestPortLinkedAgreesWithReceiveSSTPsOwnPortDownCheck(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Priority: 32768,
		Address:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x03},
		Ports: map[string]Port{
			"p1": {PathCost: 100},
			"p3": {PathCost: 100},
		},
	}.Normalize(layer.Env{})

	l := newLayer(cfg)
	t0 := time.Unix(0, 0)
	l.LinkChange(t0, "p1", true, true, 1_000_000_000)
	// "p2" names no port at all: never configured. "p3" is configured but
	// LinkChange is never called for it, so it stays down. "p1" is linked.

	b := bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		RootID:       bpdu.BridgeID{Priority: 4096, Address: netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	b.SetRole(bpdu.RoleDesignated)

	for _, port := range []string{"p1", "p2", "p3"} {
		linked := l.PortLinked(port)

		_, outcome := l.ReceiveSSTP(t0.Add(time.Second), port, SSTPArrival{ArrivalVID: 1, TLVVID: 1, Admitted: true}, b)
		down := outcome == SSTPPortDown

		if linked == down {
			t.Errorf("port %q: PortLinked = %v, ReceiveSSTP outcome = %v (down = %v), want PortLinked == !down",
				port, linked, outcome, down)
		}
	}
}

// TestAnMSTIDoesNotElectThroughAGuardDisabledPort is evidence that once BPDU
// guard fires on a port, an MSTI's own root election excludes it too.
func TestAnMSTIDoesNotElectThroughAGuardDisabledPort(t *testing.T) {
	t.Parallel()

	t0 := time.Unix(1_000_000, 0)

	cfg := Config{
		Priority: 32768,
		Address:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		Ports:    map[string]Port{"l1": {BPDUGuard: true}},
		MST: &MST{
			Name: "region-1",
			Instances: map[bpdu.MSTID]Instance{
				1: {VLANs: []vlan.ID{10}},
			},
		},
	}.Normalize(layer.Env{})

	l := newLayer(cfg)
	l.LinkChange(t0, "l1", true, true, 1_000_000_000)

	mstP := l.trees[treeID(1)].ports["l1"]
	peer := bpdu.BridgeID{Priority: 4096, Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0xee}}
	mstP.rcvInfoValid = true
	mstP.rcvRootID = peer
	mstP.rcvBridgeID = peer
	mstP.rcvPortID = 0x8001
	mstP.rcvRemainingHops = 20
	mstP.rcvHelloTime = 2 * time.Second
	mstP.rcvTime = t0

	rogue := bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		RootID:       bpdu.BridgeID{Priority: 0, Address: netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x99}},
		BridgeID:     bpdu.BridgeID{Priority: 0, Address: netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x99}},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	rogue.SetRole(bpdu.RoleDesignated)

	l.Receive(t0.Add(time.Second), "l1", rogue)

	if !l.links["l1"].bpduGuardDisabled {
		t.Fatal("test setup: BPDU guard did not fire on l1")
	}

	mt := l.trees[treeID(1)]
	if mt.rootID != mt.bridgeID {
		t.Errorf("MSTI 1 root = %v, want this bridge's own %v", mt.rootID, mt.bridgeID)
	}
	if mt.rootPort != "" {
		t.Errorf("MSTI 1 root port = %q, want empty", mt.rootPort)
	}
}

func TestDisabledTransmitRecordsHoldBothRequests(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, guarded := range []bool{false, true} {
		l := newLayer(Config{
			Ports: map[string]Port{"p1": {BPDUGuard: guarded}},
			PVST:  &PVST{Trees: map[vlan.ID]Tree{1: {}, 10: {}}},
		}.Normalize(layer.Env{}))
		check := func(stage string) {
			t.Helper()
			for key, tx := range l.portTx {
				if !tx.newInfo || !tx.newInfoMsti || tx.count != 0 {
					t.Errorf("%s guarded=%t record %v = %+v, want both requests and zero count", stage, guarded, key, tx)
				}
			}
		}
		check("construction")
		l.Advance(now)
		check("disabled pass")
		for _, tx := range l.portTx {
			tx.newInfo = false
			tx.newInfoMsti = false
			tx.count = 4
			tx.tick = now.Add(time.Second)
			tx.helloWhen = now.Add(time.Second)
		}
		l.resetTransmit("p1")
		check("transmit initialization")
		for _, tx := range l.portTx {
			if !tx.tick.IsZero() || !tx.helloWhen.IsZero() {
				t.Errorf("initialization left timers = %v/%v, want zero", tx.tick, tx.helloWhen)
			}
		}
		l.LinkChange(now, "p1", true, true, 1_000_000_000)
		if guarded {
			l.Receive(now.Add(time.Second), "p1", bpdu.BPDU{Type: bpdu.TypeRapid})
		} else {
			l.LinkChange(now.Add(time.Second), "p1", false, true, 0)
		}
		check("reset")
		l.Advance(now.Add(2 * time.Second))
		check("reset pass")
	}
}
