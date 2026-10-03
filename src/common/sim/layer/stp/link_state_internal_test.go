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
