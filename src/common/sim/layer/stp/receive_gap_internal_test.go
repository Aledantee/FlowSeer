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
