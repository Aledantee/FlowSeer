package stp

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

func syncMAC(last byte) netaddr.MAC {
	return netaddr.MAC{0, 0, 0, 0, 0, last}
}

// syncBPDU builds a rapid BPDU for one tree. The root is bridge 1 and the
// sender bridge 2 with port identifier 0x8001, so two calls with different
// costs come from the same designated port. The timers are short so a
// forward-delay ladder ends before the stored information ages out.
func syncBPDU(root, sender netaddr.MAC, prio uint16, cost uint32, role bpdu.Role) bpdu.BPDU {
	b := bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		RootID:       bpdu.BridgeID{Priority: prio, Address: root},
		RootPathCost: cost,
		BridgeID:     bpdu.BridgeID{Priority: prio, Address: sender},
		PortID:       0x8001,
		MaxAge:       20 * time.Second,
		HelloTime:    4 * time.Second,
		ForwardDelay: 4 * time.Second,
	}
	b.SetRole(role)

	return b
}

func syncSSTP(vid vlan.ID) SSTPArrival {
	return SSTPArrival{ArrivalVID: vid, TLVVID: vid, Admitted: true}
}

// syncPVSTBridge returns a PVST bridge over VLANs 1, 10, and 20 whose three
// point-to-point ports have each stored a VLAN 1 BPDU, which marks them
// external. On VLAN 10 it holds a Root port p1 with the cost-20000 record
// of designated bridge 2, a Designated port p2 that forwards on an
// agreement, and a Designated port p3 that reached Forwarding through the
// forward-delay ladder. The returned time is the last instant the fixture
// used.
func syncPVSTBridge(t *testing.T) (*Layer, time.Time) {
	t.Helper()

	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	l := newLayer(Config{
		Priority: 32768,
		Address:  syncMAC(9),
		Ports:    map[string]Port{"p1": {}, "p2": {}, "p3": {}},
		PVST:     &PVST{Trees: map[vlan.ID]Tree{1: {}, 10: {}, 20: {}}},
	}.Normalize(layer.Env{}))
	for _, name := range []string{"p1", "p2", "p3"} {
		l.LinkChange(t0, name, true, true, 1_000_000_000)
	}

	// An inferior VLAN 1 BPDU keeps every port Designated on that tree.
	vlan1 := syncBPDU(syncMAC(0xf0), syncMAC(0xf0), 61440|1, 0, bpdu.RoleDesignated)
	for _, name := range []string{"p1", "p2", "p3"} {
		l.ReceiveSSTP(t0, name, syncSSTP(1), vlan1)
	}

	root, bridge := syncMAC(1), syncMAC(2)
	l.ReceiveSSTP(t0, "p1", syncSSTP(10), syncBPDU(root, bridge, 4096|10, 20_000, bpdu.RoleDesignated))

	// The peer behind p2 answers with an agreement.
	agree := syncBPDU(root, syncMAC(3), 4096|10, 1_000_000, bpdu.RoleRoot)
	agree.SetAgreement(true)
	l.ReceiveSSTP(t0, "p2", syncSSTP(10), agree)

	// Refresh p1's record each second so it outlives the ladder.
	now := t0
	for range 12 {
		now = now.Add(time.Second)
		l.ReceiveSSTP(now, "p1", syncSSTP(10), syncBPDU(root, bridge, 4096|10, 20_000, bpdu.RoleDesignated))
		l.Advance(now)
	}

	tr := l.trees[treeID(10)]
	if got := tr.ports["p1"].role; got != bpdu.RoleRoot {
		t.Fatalf("fixture VLAN 10 p1 role = %v, want Root", got)
	}
	p2, p3 := tr.ports["p2"], tr.ports["p3"]
	if p2.role != bpdu.RoleDesignated || p2.state != StateForwarding || !p2.agreed {
		t.Fatalf("fixture VLAN 10 p2 = %v/%v agreed=%v, want Designated/Forwarding/agreed", p2.role, p2.state, p2.agreed)
	}
	if p3.role != bpdu.RoleDesignated || p3.state != StateForwarding || p3.agreed {
		t.Fatalf("fixture VLAN 10 p3 = %v/%v agreed=%v, want Designated/Forwarding/unagreed", p3.role, p3.state, p3.agreed)
	}

	return l, now
}

type syncPortView struct {
	role   bpdu.Role
	state  State
	agreed bool
}

func syncView(tr *tree, name string) syncPortView {
	p := tr.ports[name]

	return syncPortView{role: p.role, state: p.state, agreed: p.agreed}
}

func TestProposalCutsOnlyTheDesignatedPortsThatAreNotSynced(t *testing.T) {
	t.Parallel()

	root, bridge := syncMAC(1), syncMAC(2)
	proposal := func(cost uint32) bpdu.BPDU {
		b := syncBPDU(root, bridge, 4096|10, cost, bpdu.RoleDesignated)
		b.SetProposal(true)

		return b
	}

	t.Run("a lower root path cost cuts the port without an agreement", func(t *testing.T) {
		t.Parallel()

		l, now := syncPVSTBridge(t)
		before20 := map[string]syncPortView{}
		for _, name := range []string{"p1", "p2", "p3"} {
			before20[name] = syncView(l.trees[treeID(20)], name)
		}

		l.ReceiveSSTP(now.Add(time.Second), "p1", syncSSTP(10), proposal(10_000))

		tr := l.trees[treeID(10)]
		if got := syncView(tr, "p2"); got.state != StateForwarding || !got.agreed {
			t.Errorf("VLAN 10 p2 = %v agreed=%v, want Forwarding on its agreement", got.state, got.agreed)
		}
		if got := syncView(tr, "p3"); got.state != StateDiscarding {
			t.Errorf("VLAN 10 p3 state = %v, want Discarding: a better vector costs a port without an agreement its synced flag", got.state)
		}
		for name, want := range before20 {
			if got := syncView(l.trees[treeID(20)], name); got != want {
				t.Errorf("VLAN 20 %s = %+v, want %+v", name, got, want)
			}
		}
	})

	t.Run("the stored root path cost leaves the ladder port forwarding", func(t *testing.T) {
		t.Parallel()

		l, now := syncPVSTBridge(t)

		l.ReceiveSSTP(now.Add(time.Second), "p1", syncSSTP(10), proposal(20_000))

		tr := l.trees[treeID(10)]
		if got := syncView(tr, "p3"); got.state != StateForwarding {
			t.Errorf("VLAN 10 p3 state = %v, want Forwarding: its vector did not change", got.state)
		}
		if got := syncView(tr, "p2"); got.state != StateForwarding || !got.agreed {
			t.Errorf("VLAN 10 p2 = %v agreed=%v, want Forwarding on its agreement", got.state, got.agreed)
		}
	})

	t.Run("a higher root path cost takes the agreement with the vector", func(t *testing.T) {
		t.Parallel()

		l, now := syncPVSTBridge(t)

		l.ReceiveSSTP(now.Add(time.Second), "p1", syncSSTP(10), proposal(30_000))

		tr := l.trees[treeID(10)]
		if got := syncView(tr, "p2"); got.state != StateDiscarding || got.agreed {
			t.Errorf("VLAN 10 p2 = %v agreed=%v, want Discarding without an agreement", got.state, got.agreed)
		}
	})

	t.Run("without the proposal flag the vector rule alone leaves the port forwarding", func(t *testing.T) {
		t.Parallel()

		l, now := syncPVSTBridge(t)

		l.ReceiveSSTP(now.Add(time.Second), "p1", syncSSTP(10), syncBPDU(root, bridge, 4096|10, 30_000, bpdu.RoleDesignated))

		tr := l.trees[treeID(10)]
		if got := syncView(tr, "p2"); got.state != StateForwarding || got.agreed {
			t.Errorf("VLAN 10 p2 = %v agreed=%v, want Forwarding with no agreement", got.state, got.agreed)
		}
	})
}

func TestARoleChangeCostsALadderPortItsSyncedFlag(t *testing.T) {
	t.Parallel()

	l, now := syncPVSTBridge(t)
	tr := l.trees[treeID(10)]
	p3 := tr.ports["p3"]
	if !p3.synced {
		t.Fatalf("fixture VLAN 10 p3 synced = false, want true after the ladder")
	}

	// p3 becomes the Root port: the sender behind it offers a lower cost.
	root, bridge := syncMAC(1), syncMAC(2)
	at := now.Add(time.Second)
	l.ReceiveSSTP(at, "p3", syncSSTP(10), syncBPDU(root, syncMAC(4), 4096|10, 5_000, bpdu.RoleDesignated))
	if p3.role != bpdu.RoleRoot {
		t.Fatalf("VLAN 10 p3 role = %v, want Root", p3.role)
	}

	// The better information on p3 ages out while p1 stays fresh, so p3 is
	// Designated again while it still forwards.
	for range 13 {
		at = at.Add(time.Second)
		l.ReceiveSSTP(at, "p1", syncSSTP(10), syncBPDU(root, bridge, 4096|10, 20_000, bpdu.RoleDesignated))
		l.Advance(at)
	}
	if p3.role != bpdu.RoleDesignated || p3.state != StateForwarding {
		t.Fatalf("VLAN 10 p3 = %v/%v, want Designated/Forwarding", p3.role, p3.state)
	}
	if p3.synced {
		t.Errorf("VLAN 10 p3 synced = true, want false: it took the Designated role from Root while forwarding")
	}

	repeat := syncBPDU(root, bridge, 4096|10, 20_000, bpdu.RoleDesignated)
	repeat.SetProposal(true)
	l.ReceiveSSTP(at.Add(time.Second), "p1", syncSSTP(10), repeat)
	if p3.state != StateDiscarding {
		t.Errorf("VLAN 10 p3 state = %v, want Discarding after a repeated proposal", p3.state)
	}
}

// syncMSTFlags names the handshake flags the CIST part and the MSTI 1 record
// of one BPDU carry.
type syncMSTFlags struct {
	cistProposal bool
	mstiProposal bool
	agreement    bool
}

// syncMSTBPDU builds an internal BPDU from sender, the designated bridge of
// both the CIST and MSTI 1 behind it. The CIST costs are internal ones.
func syncMSTBPDU(id bpdu.ConfigID, sender netaddr.MAC, cistCost, mstiCost uint32, role bpdu.Role, f syncMSTFlags) bpdu.BPDU {
	cistRoot := bpdu.BridgeID{Priority: 4096, Address: syncMAC(1)}
	b := bpdu.BPDU{
		Version:              3,
		Type:                 bpdu.TypeRapid,
		RootID:               cistRoot,
		RegionalRootID:       cistRoot,
		InternalRootPathCost: cistCost,
		BridgeID:             bpdu.BridgeID{Priority: 8192, Address: sender},
		PortID:               0x8001,
		MaxAge:               20 * time.Second,
		HelloTime:            4 * time.Second,
		ForwardDelay:         4 * time.Second,
		RemainingHops:        20,
		ConfigID:             &id,
	}
	b.SetRole(role)
	b.SetProposal(f.cistProposal)
	b.SetAgreement(f.agreement)

	var rec bpdu.BPDU
	rec.SetRole(role)
	rec.SetProposal(f.mstiProposal)
	rec.SetAgreement(f.agreement)
	b.MSTIs = []bpdu.MSTIRecord{{
		MSTID:                1,
		Flags:                rec.Flags,
		RegionalRootID:       bpdu.BridgeID{Priority: 4096 | 1, Address: syncMAC(1)},
		InternalRootPathCost: mstiCost,
		BridgePriority:       0x20,
		PortPriority:         0x80,
		RemainingHops:        20,
	}}

	return b
}

// syncMSTBridge returns an MSTP bridge with MSTI 1 whose three ports are
// internal. On the CIST and on MSTI 1 it holds a Root port p1, a Designated
// port p2 that forwards on an agreement, and a Designated port p3 that
// reached Forwarding through the forward-delay ladder.
func syncMSTBridge(t *testing.T) (*Layer, MST, time.Time) {
	t.Helper()

	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	region := MST{Name: "region", Instances: map[bpdu.MSTID]Instance{1: {VLANs: []vlan.ID{10}}}}
	l := newLayer(Config{
		Priority: 32768,
		Address:  syncMAC(9),
		Ports:    map[string]Port{"p1": {}, "p2": {}, "p3": {}},
		MST:      &region,
	}.Normalize(layer.Env{}))
	for _, name := range []string{"p1", "p2", "p3"} {
		l.LinkChange(t0, name, true, true, 1_000_000_000)
	}

	id := region.ConfigID()
	base := func(cost uint32) bpdu.BPDU {
		return syncMSTBPDU(id, syncMAC(2), cost, cost, bpdu.RoleDesignated, syncMSTFlags{})
	}
	l.Receive(t0, "p1", base(20_000))
	l.Receive(t0, "p2", syncMSTBPDU(id, syncMAC(3), 1_000_000, 1_000_000, bpdu.RoleRoot, syncMSTFlags{agreement: true}))

	now := t0
	for range 12 {
		now = now.Add(time.Second)
		l.Receive(now, "p1", base(20_000))
		l.Advance(now)
	}

	for _, tr := range []*tree{l.cist(), l.trees[treeID(1)]} {
		if got := tr.ports["p1"].role; got != bpdu.RoleRoot {
			t.Fatalf("fixture tree %d p1 role = %v, want Root", tr.id, got)
		}
		p2, p3 := tr.ports["p2"], tr.ports["p3"]
		if p2.role != bpdu.RoleDesignated || p2.state != StateForwarding || !p2.agreed {
			t.Fatalf("fixture tree %d p2 = %v/%v agreed=%v, want Designated/Forwarding/agreed", tr.id, p2.role, p2.state, p2.agreed)
		}
		if p3.role != bpdu.RoleDesignated || p3.state != StateForwarding || p3.agreed {
			t.Fatalf("fixture tree %d p3 = %v/%v agreed=%v, want Designated/Forwarding/unagreed", tr.id, p3.role, p3.state, p3.agreed)
		}
	}

	return l, region, now
}

func TestProposalCutsOnlyTheUnsyncedPortsOfTheCISTAndOfAnMSTI(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		flags syncMSTFlags
		cut   func(l *Layer) *tree
		other func(l *Layer) *tree
	}{
		{
			name:  "CIST",
			flags: syncMSTFlags{cistProposal: true},
			cut:   func(l *Layer) *tree { return l.cist() },
			other: func(l *Layer) *tree { return l.trees[treeID(1)] },
		},
		{
			name:  "MSTI",
			flags: syncMSTFlags{mstiProposal: true},
			cut:   func(l *Layer) *tree { return l.trees[treeID(1)] },
			other: func(l *Layer) *tree { return l.cist() },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l, region, now := syncMSTBridge(t)
			otherBefore := map[string]syncPortView{}
			for _, name := range []string{"p2", "p3"} {
				otherBefore[name] = syncView(tc.other(l), name)
			}

			l.Receive(now.Add(time.Second), "p1", syncMSTBPDU(region.ConfigID(), syncMAC(2), 10_000, 10_000, bpdu.RoleDesignated, tc.flags))

			tr := tc.cut(l)
			if got := syncView(tr, "p2"); got.state != StateForwarding || !got.agreed {
				t.Errorf("p2 = %v agreed=%v, want Forwarding on its agreement", got.state, got.agreed)
			}
			if got := syncView(tr, "p3"); got.state != StateDiscarding {
				t.Errorf("p3 state = %v, want Discarding", got.state)
			}
			for name, want := range otherBefore {
				if got := syncView(tc.other(l), name); got != want {
					t.Errorf("the other tree's %s = %+v, want %+v", name, got, want)
				}
			}
		})
	}
}
