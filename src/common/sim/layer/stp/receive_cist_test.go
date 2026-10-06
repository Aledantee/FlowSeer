package stp

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

func TestRootPortDoesNotAcknowledgeTCN(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	l := newLayer(Config{Address: netaddr.MAC{0, 0, 0, 0, 0, 2}, Ports: map[string]Port{"p1": {}}}.Normalize(layer.Env{}))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	root := bpdu.BridgeID{Priority: 4096, Address: netaddr.MAC{0, 0, 0, 0, 0, 1}}
	b := bpdu.BPDU{
		Version: 2, Type: bpdu.TypeRapid, RootID: root, BridgeID: root, PortID: 0x8001,
		HelloTime: 2 * time.Second, MaxAge: 20 * time.Second, ForwardDelay: 15 * time.Second,
	}
	b.SetRole(bpdu.RoleDesignated)
	b.SetProposal(true)
	l.Receive(now.Add(time.Second), "p1", b)
	if got := l.cist().ports["p1"].role; got != bpdu.RoleRoot {
		t.Fatalf("p1 role = %v, want Root", got)
	}
	l.Receive(now.Add(2*time.Second), "p1", bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification})
	if l.cist().ports["p1"].tcAck {
		t.Fatal("Root port acknowledged a TCN")
	}
}

func TestInternalCISTFlagLeavesMSTIUnchanged(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	region := MST{Name: "region-1", Instances: map[bpdu.MSTID]Instance{1: {VLANs: []vlan.ID{10}}}}
	l := newLayer(Config{Address: netaddr.MAC{0, 0, 0, 0, 0, 2}, Ports: map[string]Port{"p1": {}, "p2": {}}, MST: &region}.Normalize(layer.Env{}))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)
	root := bpdu.BridgeID{Priority: 4096, Address: netaddr.MAC{0, 0, 0, 0, 0, 1}}
	cid := region.ConfigID()
	b := bpdu.BPDU{
		Version: 3, Type: bpdu.TypeRapid, RootID: root, BridgeID: root, RegionalRootID: root,
		PortID: 0x8001, HelloTime: 60 * time.Second, MaxAge: 120 * time.Second, ForwardDelay: 15 * time.Second,
		RemainingHops: 20, ConfigID: &cid,
		MSTIs: []bpdu.MSTIRecord{{MSTID: 1, RegionalRootID: root, BridgePriority: 0x10, PortPriority: 0x80, RemainingHops: 20}},
	}
	b.SetRole(bpdu.RoleDesignated)
	b.SetProposal(true)
	l.Receive(now.Add(time.Second), "p1", b)
	l.Advance(now.Add(16 * time.Second))
	l.Advance(now.Add(32 * time.Second))
	if l.links["p1"].external || !l.cist().ports["p1"].tcActive || !l.trees[treeID(1)].ports["p2"].tcActive {
		t.Fatalf("precondition: external=%t CIST active=%t MSTI p2 active=%t",
			l.links["p1"].external, l.cist().ports["p1"].tcActive, l.trees[treeID(1)].ports["p2"].tcActive)
	}
	before := l.trees[treeID(1)].ports["p2"].tcWhile
	b.SetTopologyChange(true)
	l.Receive(now.Add(36*time.Second), "p1", b)
	if after := l.trees[treeID(1)].ports["p2"].tcWhile; after != before {
		t.Fatalf("internal CIST flag changed MSTI p2 timer from %v to %v", before, after)
	}
}

func newTopologyChangeTimerLayer() *Layer {
	region := MST{Name: "region-1", Instances: map[bpdu.MSTID]Instance{1: {VLANs: []vlan.ID{10}}}}
	l := newLayer(Config{
		Address: netaddr.MAC{0, 0, 0, 0, 0, 2},
		Ports:   map[string]Port{"p1": {}, "p2": {}, "p3": {}, "p4": {}},
		MST:     &region,
	}.Normalize(layer.Env{}))

	for _, name := range l.portNames {
		l.links[name].up = true
		l.links[name].pointToPoint = true
		l.links[name].edge = false
	}
	l.links["p1"].sendRSTP = false
	l.links["p3"].sendRSTP = false
	l.links["p4"].sendRSTP = false

	oldRoot := bpdu.BridgeID{Priority: 4096, Address: netaddr.MAC{0, 0, 0, 0, 0, 1}}
	cist := l.cist()
	cist.rootID = oldRoot
	cist.rootPathCost = 10
	cist.rootPort = "p1"
	rootPort := cist.ports["p1"]
	rootPort.role = bpdu.RoleRoot
	rootPort.state = StateForwarding
	rootPort.rcvInfoValid = true
	rootPort.rcvRootID = oldRoot
	rootPort.rcvRootPathCost = 10
	rootPort.rcvBridgeID = oldRoot
	rootPort.rcvPortID = 0x8001
	rootPort.rcvMaxAge = 31 * time.Second
	rootPort.rcvForwardDelay = 17 * time.Second

	for _, name := range []string{"p1", "p2", "p3", "p4"} {
		p := cist.ports[name]
		if name != "p1" {
			p.role = bpdu.RoleDesignated
		}
		p.state = StateForwarding
		p.tcActive = true
	}
	for _, name := range []string{"p1", "p2", "p3", "p4"} {
		p := l.trees[treeID(1)].ports[name]
		if name != "p1" {
			p.role = bpdu.RoleDesignated
		}
		p.state = StateForwarding
		p.tcActive = true
	}

	return l
}

func TestInternalMSTTopologyChangeUsesPreviousRootTimes(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	l := newTopologyChangeTimerLayer()
	cid := l.mst.ConfigID()
	newRoot := bpdu.BridgeID{Priority: 2048, Address: netaddr.MAC{0, 0, 0, 0, 0, 3}}
	var mstiFlags bpdu.BPDU
	mstiFlags.SetTopologyChange(true)
	b := bpdu.BPDU{
		Version: 3, Type: bpdu.TypeRapid, RootID: newRoot, BridgeID: newRoot,
		RegionalRootID: newRoot, PortID: 0x8002, HelloTime: 2 * time.Second,
		MaxAge: 43 * time.Second, ForwardDelay: 29 * time.Second,
		RemainingHops: 20, ConfigID: &cid,
		MSTIs: []bpdu.MSTIRecord{{
			MSTID: 1, Flags: mstiFlags.Flags, RegionalRootID: newRoot,
			BridgePriority: 0x10, PortPriority: 0x80, RemainingHops: 20,
		}},
	}
	b.SetRole(bpdu.RoleDesignated)
	b.SetTopologyChange(true)

	var flushes []layer.FlushTarget
	l.applyBPDU(l.cist(), l.cist().ports["p2"], now, b, &flushes)

	want := now.Add(48 * time.Second)
	if got := l.cist().ports["p3"].tcWhile; !got.Equal(want) {
		t.Errorf("CIST p3 tcWhile = %v, want old root timer %v", got, want)
	}
	if got := l.trees[treeID(1)].ports["p4"].tcWhile; !got.Equal(want) {
		t.Errorf("MSTI p4 tcWhile = %v, want old root timer %v", got, want)
	}
	if got, port := l.cist().rootID, l.cist().rootPort; got != newRoot || port != "p2" {
		t.Errorf("CIST root = %v via %q, want %v via p2", got, port, newRoot)
	}
}

func TestRSTPTopologyChangeUsesPreviousRootTimes(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	l := newTopologyChangeTimerLayer()
	newRoot := bpdu.BridgeID{Priority: 2048, Address: netaddr.MAC{0, 0, 0, 0, 0, 3}}
	b := bpdu.BPDU{
		Version: 2, Type: bpdu.TypeRapid, RootID: newRoot, BridgeID: newRoot,
		PortID: 0x8001, HelloTime: 2 * time.Second,
		MaxAge: 43 * time.Second, ForwardDelay: 29 * time.Second,
	}
	b.SetRole(bpdu.RoleDesignated)
	b.SetTopologyChange(true)

	var flushes []layer.FlushTarget
	l.applyBPDU(l.cist(), l.cist().ports["p1"], now, b, &flushes)

	want := now.Add(48 * time.Second)
	if got := l.cist().ports["p3"].tcWhile; !got.Equal(want) {
		t.Errorf("CIST p3 tcWhile = %v, want old root timer %v", got, want)
	}
	if got, port := l.cist().rootID, l.cist().rootPort; got != newRoot || port != "p1" {
		t.Errorf("CIST root = %v via %q, want %v via p1", got, port, newRoot)
	}
}

func TestTopologyChangeKeepsOldTimesOnAnUnchangedRootPort(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	l := newTopologyChangeTimerLayer()
	rootPort := l.cist().ports["p1"]
	b := bpdu.BPDU{
		Version: 2, Type: bpdu.TypeRapid, RootID: rootPort.rcvRootID,
		BridgeID: rootPort.rcvBridgeID, PortID: rootPort.rcvPortID,
		HelloTime: 2 * time.Second, MaxAge: 43 * time.Second,
		ForwardDelay: 29 * time.Second,
	}
	b.SetRole(bpdu.RoleDesignated)
	b.SetTopologyChange(true)

	var flushes []layer.FlushTarget
	l.applyBPDU(l.cist(), rootPort, now, b, &flushes)

	want := now.Add(48 * time.Second)
	if got := l.cist().ports["p3"].tcWhile; !got.Equal(want) {
		t.Errorf("CIST p3 tcWhile = %v, want old root timer %v", got, want)
	}
	if rootPort.rcvMaxAge != b.MaxAge || rootPort.rcvForwardDelay != b.ForwardDelay {
		t.Errorf("root port received timers = %v/%v, want new BPDU timers %v/%v", rootPort.rcvMaxAge, rootPort.rcvForwardDelay, b.MaxAge, b.ForwardDelay)
	}
}
