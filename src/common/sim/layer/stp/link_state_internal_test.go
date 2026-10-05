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

// TestPortStateRepeatsNoLinkRecordField verifies the ownership rule the link
// record exists for: a fact about the physical link lives in linkRecord alone.
// A portState field with the name of a linkRecord field is a second copy that
// something has to keep in step, which is the defect class the record removes.
func TestPortStateRepeatsNoLinkRecordField(t *testing.T) {
	t.Parallel()

	portFields := map[string]bool{}
	pt := reflect.TypeOf(portState{})
	for i := range pt.NumField() {
		portFields[pt.Field(i).Name] = true
	}

	lt := reflect.TypeOf(linkRecord{})
	for i := range lt.NumField() {
		name := lt.Field(i).Name
		if portFields[name] {
			t.Errorf("portState repeats linkRecord field %q: the link owns it, a tree must not keep a copy", name)
		}
	}

	// Every port the trees track has a record under the same key.
	l := newLayer(Config{
		Ports: map[string]Port{"p1": {}, "p2": {}},
	}.Normalize(layer.Env{}))
	for _, name := range l.portNames {
		if l.link(name) == nil {
			t.Errorf("no link record for port %q", name)
		}
	}
}

// handshakeLayers builds one layer per mode that runs a tree besides the
// CIST: PVST with VLAN 10 and MSTP with an instance carrying VLAN 10.
func handshakeLayers(t *testing.T, bpduGuard bool) map[string]*Layer {
	t.Helper()

	port := map[string]Port{"p1": {PathCost: 100, BPDUGuard: bpduGuard}}
	addr := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}

	pvst := newLayer(Config{
		Priority: 32768,
		Address:  addr,
		Ports:    port,
		PVST:     &PVST{Trees: map[vlan.ID]Tree{1: {}, 10: {}}},
	}.Normalize(layer.Env{}))

	mst := newLayer(Config{
		Priority: 32768,
		Address:  addr,
		Ports:    port,
		MST: &MST{
			Name:      "region-1",
			Instances: map[bpdu.MSTID]Instance{1: {VLANs: []vlan.ID{10}}},
		},
	}.Normalize(layer.Env{}))

	return map[string]*Layer{"pvst": pvst, "mstp": mst}
}

// plantHandshake leaves every tree's port holding a handshake in progress:
// an agreement, a proposal, a running forward-delay timer, and received
// information. Planting is direct because an MSTI never earns an agreement
// through the public API.
func plantHandshake(l *Layer, port string, now time.Time) {
	for _, id := range l.treeOrder {
		p := l.trees[id].ports[port]
		p.agreed = true
		p.proposing = true
		p.fwdDelayTimer = now.Add(time.Hour)
		p.rcvInfoValid = true
		p.rcvTime = now
		p.rcvHelloTime = 2 * time.Second
	}
}

// assertHandshakeCleared fails for every tree whose port still holds any of
// the state plantHandshake left.
func assertHandshakeCleared(t *testing.T, l *Layer, port string) {
	t.Helper()

	for _, id := range l.treeOrder {
		p := l.trees[id].ports[port]
		if p.agreed {
			t.Errorf("tree %d agreed = true, want false", id)
		}
		if p.proposing {
			t.Errorf("tree %d proposing = true, want false", id)
		}
		if !p.fwdDelayTimer.IsZero() {
			t.Errorf("tree %d fwdDelayTimer = %v, want zero", id, p.fwdDelayTimer)
		}
		if p.rcvInfoValid {
			t.Errorf("tree %d rcvInfoValid = true, want false", id)
		}
	}
}

// TestLinkDownClearsHandshakeStateOnEveryTree verifies that a link down
// clears agreed, proposing, fwdDelayTimer, and received information on every
// tree's port, not only the CIST's: a tree that kept an agreement would
// forward at the next link up without a handshake.
func TestLinkDownClearsHandshakeStateOnEveryTree(t *testing.T) {
	t.Parallel()

	t0 := time.Unix(1_000_000, 0)
	for name, l := range handshakeLayers(t, false) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			l.LinkChange(t0, "p1", true, true, 1_000_000_000)
			plantHandshake(l, "p1", t0)

			l.LinkChange(t0.Add(time.Second), "p1", false, false, 0)

			assertHandshakeCleared(t, l, "p1")
			for _, id := range l.treeOrder {
				if p := l.trees[id].ports["p1"]; p.role != bpdu.RoleDisabled || p.state != StateDiscarding {
					t.Errorf("tree %d after link down = %v/%v, want Disabled/Discarding", id, p.role, p.state)
				}
			}
		})
	}
}

// TestBPDUGuardClearsHandshakeStateOnEveryTree verifies that the frame that
// fires BPDU guard clears the same state on every tree as a link down does.
func TestBPDUGuardClearsHandshakeStateOnEveryTree(t *testing.T) {
	t.Parallel()

	t0 := time.Unix(1_000_000, 0)
	for name, l := range handshakeLayers(t, true) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			l.LinkChange(t0, "p1", true, true, 1_000_000_000)
			plantHandshake(l, "p1", t0)

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
			l.Receive(t0.Add(time.Second), "p1", rogue)

			if !l.link("p1").bpduGuardDisabled {
				t.Fatal("test setup: BPDU guard did not fire on p1")
			}
			assertHandshakeCleared(t, l, "p1")
		})
	}
}

// TestCloneKeepsLinkRecordsApart verifies that Clone deep-copies the link
// records: a link change on the clone leaves its source's record alone.
func TestCloneKeepsLinkRecordsApart(t *testing.T) {
	t.Parallel()

	t0 := time.Unix(1_000_000, 0)
	l := handshakeLayers(t, false)["mstp"]
	l.LinkChange(t0, "p1", true, true, 1_000_000_000)

	cp := l.Clone()
	if cp.link("p1") == l.link("p1") {
		t.Fatal("clone shares its source's link record")
	}

	cp.LinkChange(t0.Add(time.Second), "p1", false, false, 0)

	if !l.link("p1").up || !l.PortLinked("p1") {
		t.Error("link down on the clone took the source's link down")
	}
	if cp.link("p1").up || cp.PortLinked("p1") {
		t.Error("link down on the clone left the clone's own link up")
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
// guard fires on a port, an MSTI's own root election excludes it too, even
// though the MSTI's own rcvInfoValid would otherwise look like a live
// candidate until it ages out on its own. Reaching that state through the
// public API alone is not possible: a BPDU-guarded port's very first
// reception fires the guard before the frame ever reaches an MSTI's own
// applyBPDU, so no BPDU can establish an MSTI's information on a guarded port
// in the first place. This test seeds the MSTI's port state directly with
// the information a peer would have delivered moments earlier, before the
// guard fired, and then marks the link record guard-disabled the way the
// guard does. The guard-firing path itself clears that information (see
// TestBPDUGuardClearsHandshakeStateOnEveryTree), so the election check is
// driven through recompute alone.
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

	l.link("l1").bpduGuardDisabled = true
	var flushes []layer.FlushTarget
	l.recomputeAll(t0.Add(time.Second), &flushes)

	mt := l.trees[treeID(1)]
	if mt.rootID != mt.bridgeID {
		t.Errorf("MSTI 1 root = %v, want this bridge's own %v", mt.rootID, mt.bridgeID)
	}
	if mt.rootPort != "" {
		t.Errorf("MSTI 1 root port = %q, want empty", mt.rootPort)
	}
}
