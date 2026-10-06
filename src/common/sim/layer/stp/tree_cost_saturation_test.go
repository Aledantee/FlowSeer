package stp

import (
	"math"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

func TestRootElectionSaturatesInternalAndPVSTCosts(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		info func(*Layer) (bpdu.BridgeID, uint32, string)
	}{
		{
			name: "CIST internal",
			cfg:  Config{MST: &MST{Name: "region"}},
			info: func(l *Layer) (bpdu.BridgeID, uint32, string) { return l.Root() },
		},
		{
			name: "PVST VLAN 10",
			cfg:  Config{PVST: &PVST{Trees: map[vlan.ID]Tree{1: {}, 10: {}}}},
			info: func(l *Layer) (bpdu.BridgeID, uint32, string) {
				tr := l.trees[treeID(10)]
				return tr.rootID, tr.rootPathCost, tr.rootPort
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
			cfg := tc.cfg
			cfg.Ports = map[string]Port{"p1": {PathCost: 100}, "p2": {PathCost: 100}}
			l := newLayer(cfg.Normalize(layer.Env{}))
			l.LinkChange(now, "p1", true, true, 1_000_000_000)
			l.LinkChange(now, "p2", true, true, 1_000_000_000)

			root := bpdu.BridgeID{Priority: 4096, Address: netaddr.MAC{0, 0, 0, 0, 0, 1}}
			for i, name := range []string{"p1", "p2"} {
				cost := uint32(math.MaxUint32 - 50)
				if i == 1 {
					cost = 100
				}
				peer := bpdu.BPDU{
					Version: 3, Type: bpdu.TypeRapid,
					RootID: root, RegionalRootID: root, BridgeID: root,
					PortID: 0x8001, HelloTime: 2 * time.Second,
					MaxAge: 20 * time.Second, ForwardDelay: 15 * time.Second,
					RemainingHops: 20,
				}
				peer.SetRole(bpdu.RoleDesignated)
				if tc.cfg.MST != nil {
					id := tc.cfg.MST.ConfigID()
					peer.ConfigID = &id
					peer.InternalRootPathCost = cost
				} else {
					peer.RootPathCost = cost
				}
				if tc.cfg.PVST != nil {
					l.ReceiveSSTP(now.Add(time.Duration(i+1)*time.Second), name, SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}, peer)
				} else {
					l.Receive(now.Add(time.Duration(i+1)*time.Second), name, peer)
				}
			}
			_, _, port := tc.info(l)
			if port != "p2" {
				t.Fatalf("root port = %q, want p2 with cost 100 rather than overflowing p1", port)
			}
		})
	}
}
