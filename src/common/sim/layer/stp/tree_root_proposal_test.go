package stp

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

func TestMSTIRootChangeProposesOnDesignatedPort(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	region := MST{Name: "region", Instances: map[bpdu.MSTID]Instance{1: {VLANs: []vlan.ID{10}}}}
	l := newLayer(Config{Ports: map[string]Port{"p1": {}, "p2": {}}, MST: &region}.Normalize(layer.Env{}))
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)

	root := bpdu.BridgeID{Priority: 4096, Address: netaddr.MAC{0, 0, 0, 0, 0, 1}}
	id := region.ConfigID()
	peer := bpdu.BPDU{
		Version: 3, Type: bpdu.TypeRapid,
		RootID: l.cist().bridgeID, BridgeID: l.cist().bridgeID,
		RegionalRootID: l.cist().bridgeID,
		PortID:         0x8001, HelloTime: 2 * time.Second,
		MaxAge: 20 * time.Second, ForwardDelay: 15 * time.Second,
		RemainingHops: 20, ConfigID: &id,
		MSTIs: []bpdu.MSTIRecord{{
			MSTID: 1, RegionalRootID: root,
			BridgePriority: 0x10, PortPriority: 0x80, RemainingHops: 20,
		}},
	}
	peer.SetRole(bpdu.RoleDesignated)
	effects := l.Receive(now.Add(time.Second), "p1", peer)
	if got := l.VLANPortInfo(10, "p1"); got.Role != bpdu.RoleRoot {
		t.Fatalf("MSTI p1 role = %v, want Root", got.Role)
	}
	if got := l.VLANPortInfo(10, "p2"); got.Role != bpdu.RoleDesignated || got.State != StateDiscarding {
		t.Fatalf("MSTI p2 = %v/%v, want Designated/Discarding", got.Role, got.State)
	}
	count := 0
	for _, emission := range effects.Emissions {
		if emission.Port != "p2" {
			continue
		}
		count++
		frame, err := bpdu.Decode(emission.Frame)
		if err != nil {
			t.Fatalf("decode p2 frame: %v", err)
		}
		if len(frame.MSTIs) != 1 || !(bpdu.BPDU{Flags: frame.MSTIs[0].Flags}).Proposal() {
			t.Fatalf("p2 MSTI record = %+v, want proposal", frame.MSTIs)
		}
	}
	if count != 1 {
		t.Fatalf("MSTI root change emitted %d frames on p2, want one", count)
	}
}
