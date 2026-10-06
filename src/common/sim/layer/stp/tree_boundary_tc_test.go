package stp

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

func TestBoundaryCISTFlagEmitsOrderedMSTIRecordsOnRootPort(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	region := MST{Name: "region-1", Instances: map[bpdu.MSTID]Instance{
		1: {VLANs: []vlan.ID{10}}, 2: {VLANs: []vlan.ID{20}},
		3: {VLANs: []vlan.ID{30}}, 4: {VLANs: []vlan.ID{40}},
	}}
	l := newLayer(Config{
		Address: netaddr.MAC{0, 0, 0, 0, 0, 2},
		Ports:   map[string]Port{"p1": {}, "p2": {}}, MST: &region,
	}.Normalize(layer.Env{}))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)

	root := bpdu.BridgeID{Priority: 4096, Address: netaddr.MAC{0, 0, 0, 0, 0, 1}}
	localID := region.ConfigID()
	upstream := bpdu.BPDU{
		Version: 3, Type: bpdu.TypeRapid, RootID: root, BridgeID: root,
		RegionalRootID: root, PortID: 0x8001, HelloTime: 2 * time.Second,
		MaxAge: 20 * time.Second, ForwardDelay: 15 * time.Second,
		RemainingHops: 20, ConfigID: &localID,
		MSTIs: []bpdu.MSTIRecord{
			{MSTID: 1, RegionalRootID: bpdu.BridgeID{Priority: 4097, Address: root.Address}, BridgePriority: 0x10, PortPriority: 0x80, RemainingHops: 20},
			{MSTID: 2, RegionalRootID: bpdu.BridgeID{Priority: 4098, Address: root.Address}, BridgePriority: 0x10, PortPriority: 0x80, RemainingHops: 20},
			{MSTID: 3, RegionalRootID: bpdu.BridgeID{Priority: 4099, Address: root.Address}, BridgePriority: 0x10, PortPriority: 0x80, RemainingHops: 20},
			{MSTID: 4, RegionalRootID: bpdu.BridgeID{Priority: 4100, Address: root.Address}, BridgePriority: 0x10, PortPriority: 0x80, RemainingHops: 20},
		},
	}
	upstream.SetRole(bpdu.RoleDesignated)
	upstream.SetProposal(true)
	l.Receive(now, "p1", upstream)

	foreign := MST{Name: "foreign"}.ConfigID()
	boundary := bpdu.BPDU{
		Version: 3, Type: bpdu.TypeRapid, RootID: root, RootPathCost: 100_000,
		RegionalRootID: root, BridgeID: bpdu.BridgeID{Priority: 61440, Address: netaddr.MAC{0, 0, 0, 0, 0, 3}},
		PortID: 0x8002, HelloTime: 2 * time.Second, MaxAge: 20 * time.Second,
		ForwardDelay: 15 * time.Second, RemainingHops: 20, ConfigID: &foreign,
	}
	boundary.SetRole(bpdu.RoleRoot)
	boundary.SetAgreement(true)
	l.Receive(now, "p2", boundary)
	if info := l.PortInfo("p1"); info.Role != bpdu.RoleRoot || info.State != StateForwarding {
		t.Fatalf("p1 CIST = %v/%v, want Root/Forwarding", info.Role, info.State)
	}
	if info := l.PortInfo("p2"); info.Role != bpdu.RoleDesignated || info.State != StateForwarding {
		t.Fatalf("p2 CIST = %v/%v, want Designated/Forwarding", info.Role, info.State)
	}
	if !l.links["p2"].external {
		t.Fatal("p2 did not become a boundary port")
	}

	boundary.SetTopologyChange(true)
	fx := l.Receive(now.Add(4*time.Second), "p2", boundary)
	var rootFrames int
	for _, emission := range fx.Emissions {
		if emission.Port != "p1" {
			continue
		}
		rootFrames++
		decoded, err := bpdu.Decode(emission.Frame)
		if err != nil {
			t.Fatalf("decode p1 frame: %v", err)
		}
		if len(decoded.MSTIs) != 4 {
			t.Fatalf("p1 MSTI records = %+v, want four", decoded.MSTIs)
		}
		for i, record := range decoded.MSTIs {
			if record.MSTID != bpdu.MSTID(i+1) || !(bpdu.BPDU{Flags: record.Flags}).TopologyChange() {
				t.Errorf("record %d = %+v, want ordered and flagged", i, record)
			}
		}
	}
	if rootFrames != 1 {
		t.Errorf("boundary Receive emitted %d frames on Root port, want one", rootFrames)
	}
}
