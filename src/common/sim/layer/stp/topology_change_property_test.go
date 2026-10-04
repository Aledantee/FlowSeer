package stp

import (
	"fmt"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

func TestTopologyChangeEmissionProperty(t *testing.T) {
	t.Parallel()

	modes := []struct {
		name string
		new  func() *Layer
	}{
		{name: "RSTP", new: func() *Layer { return newTopologyPropertyLayer(t, "rstp") }},
		{name: "MST", new: func() *Layer { return newTopologyPropertyLayer(t, "mst") }},
		{name: "PVST", new: func() *Layer { return newTopologyPropertyLayer(t, "pvst") }},
	}

	for _, mode := range modes {
		mode := mode
		t.Run(mode.name, func(t *testing.T) {
			t.Run("Receive", func(t *testing.T) {
				l, now := mode.new(), topologyPropertyTime()
				prepareTopologyPropertyReceive(l, now)
				if l.cist().ports["p1"].role != bpdu.RoleRoot || l.cist().ports["p2"].role != bpdu.RoleDesignated {
					t.Fatalf("prerequisite roles = %v/%v, want Root/Designated", l.cist().ports["p1"].role, l.cist().ports["p2"].role)
				}
				if l.cist().ports["p1"].state != StateForwarding || l.cist().ports["p2"].state != StateForwarding {
					t.Fatalf("prerequisite states = %v/%v, want Forwarding/Forwarding", l.cist().ports["p1"].state, l.cist().ports["p2"].state)
				}

				effects := l.Receive(now, "p2", topologyPropertyBPDU(l, l.cist(), true))
				assertTopologyProperty(t, l, now, effects)
			})

			t.Run("LinkChange", func(t *testing.T) {
				l, now := mode.new(), topologyPropertyTime()
				prepareTopologyPropertySpeedChange(l, now)
				if l.cist().ports["p2"].role != bpdu.RoleAlternate || l.cist().ports["p2"].state != StateDiscarding {
					t.Fatalf("prerequisite p2 = %v/%v, want Alternate/Discarding", l.cist().ports["p2"].role, l.cist().ports["p2"].state)
				}

				effects := l.LinkChange(now, "p2", true, true, 100_000_000_000)
				assertTopologyProperty(t, l, now, effects)
			})

			t.Run("Mcheck", func(t *testing.T) {
				l, now := mode.new(), topologyPropertyTime()
				prepareTopologyPropertyMcheck(l, now)
				if l.cist().ports["p2"].role != bpdu.RoleRoot || l.cist().ports["p2"].state != StateDiscarding {
					t.Fatalf("prerequisite p2 = %v/%v, want Root/Discarding", l.cist().ports["p2"].role, l.cist().ports["p2"].state)
				}

				effects := l.Mcheck(now, "p2")
				assertTopologyProperty(t, l, now, effects)
			})

			t.Run("Advance", func(t *testing.T) {
				l, now := mode.new(), topologyPropertyTime()
				prepareTopologyPropertyAdvance(l, now)
				if l.cist().ports["p2"].role != bpdu.RoleRoot || l.cist().ports["p2"].state != StateLearning {
					t.Fatalf("prerequisite p2 = %v/%v, want Root/Learning", l.cist().ports["p2"].role, l.cist().ports["p2"].state)
				}

				effects := l.Advance(now)
				assertTopologyProperty(t, l, now, effects)
			})
		})
	}

	t.Run("ReceiveSSTP", func(t *testing.T) {
		l, now := newTopologyPropertyLayer(t, "pvst"), topologyPropertyTime()
		prepareTopologyPropertyReceive(l, now)
		tree := l.trees[treeID(10)]
		if tree.ports["p1"].role != bpdu.RoleRoot || tree.ports["p2"].role != bpdu.RoleDesignated {
			t.Fatalf("prerequisite VLAN 10 roles = %v/%v, want Root/Designated", tree.ports["p1"].role, tree.ports["p2"].role)
		}
		if tree.ports["p1"].state != StateForwarding || tree.ports["p2"].state != StateForwarding {
			t.Fatalf("prerequisite VLAN 10 states = %v/%v, want Forwarding/Forwarding", tree.ports["p1"].state, tree.ports["p2"].state)
		}

		effects, outcome := l.ReceiveSSTP(now, "p2", SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}, topologyPropertyBPDU(l, tree, true))
		if outcome != SSTPApplied {
			t.Fatalf("ReceiveSSTP outcome = %v, want %v", outcome, SSTPApplied)
		}
		assertTopologyProperty(t, l, now, effects)
	})
}

func topologyPropertyTime() time.Time {
	return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
}

func newTopologyPropertyLayer(t *testing.T, mode string) *Layer {
	t.Helper()
	ports := map[string]Port{"p1": {}, "p2": {}}
	cfg := Config{Priority: 32768, Ports: ports}
	switch mode {
	case "mst":
		cfg.MST = &MST{Instances: map[bpdu.MSTID]Instance{
			1: {VLANs: []vlan.ID{10}},
			2: {VLANs: []vlan.ID{20}},
			3: {VLANs: []vlan.ID{30}},
			4: {VLANs: []vlan.ID{40}},
		}}
	case "pvst":
		cfg.PVST = &PVST{Trees: map[vlan.ID]Tree{1: {}, 10: {}, 20: {}, 30: {}, 40: {}}}
	case "rstp":
	default:
		t.Fatalf("unknown topology property mode %q", mode)
	}

	return newLayer(cfg.Normalize(layer.Env{}))
}

func prepareTopologyPropertyReceive(l *Layer, now time.Time) {
	setTopologyPropertyLinks(l, false)
	for _, id := range l.treeOrder {
		t := l.trees[id]
		t.rootID = topologyPropertyRoot()
		t.rootPathCost = 0
		t.rootPort = "p1"
		t.regionalRootID = t.rootID
		t.internalRootPathCost = 0
		setTopologyPropertyReceived(t, t.ports["p1"], 0x8001, now)
		t.ports["p1"].role = bpdu.RoleRoot
		t.ports["p1"].state = StateForwarding
		t.ports["p1"].tcActive = true
		t.ports["p2"].role = bpdu.RoleDesignated
		t.ports["p2"].state = StateForwarding
		t.ports["p2"].tcActive = true
	}
}

func prepareTopologyPropertySpeedChange(l *Layer, now time.Time) {
	setTopologyPropertyLinks(l, false)
	l.links["p1"].edge = true
	for _, id := range l.treeOrder {
		t := l.trees[id]
		t.rootID = topologyPropertyRoot()
		t.rootPathCost = 0
		t.rootPort = "p1"
		t.regionalRootID = t.rootID
		setTopologyPropertyReceived(t, t.ports["p1"], 0x8001, now)
		setTopologyPropertyReceived(t, t.ports["p2"], 0x8002, now)
		t.ports["p1"].role = bpdu.RoleRoot
		t.ports["p1"].state = StateForwarding
		t.ports["p2"].role = bpdu.RoleAlternate
		t.ports["p2"].state = StateDiscarding
		t.ports["p1"].tcActive = false
		t.ports["p2"].tcActive = false
		t.ports["p2"].pathCost = 20_000
	}
	l.links["p2"].linkPathCost = 20_000
}

func prepareTopologyPropertyMcheck(l *Layer, now time.Time) {
	setTopologyPropertyLinks(l, false)
	l.links["p1"].edge = true
	l.links["p2"].sendRSTP = false
	for _, id := range l.treeOrder {
		t := l.trees[id]
		t.rootID = topologyPropertyRoot()
		t.rootPathCost = 0
		t.rootPort = "p2"
		t.regionalRootID = t.rootID
		setTopologyPropertyReceived(t, t.ports["p2"], 0x8002, now)
		t.ports["p1"].role = bpdu.RoleDesignated
		t.ports["p1"].state = StateForwarding
		t.ports["p2"].role = bpdu.RoleRoot
		t.ports["p2"].state = StateDiscarding
		t.ports["p1"].tcActive = false
		t.ports["p2"].tcActive = false
	}
}

func prepareTopologyPropertyAdvance(l *Layer, now time.Time) {
	prepareTopologyPropertyMcheck(l, now)
	l.links["p2"].sendRSTP = true
	for _, id := range l.treeOrder {
		p := l.trees[id].ports["p2"]
		p.state = StateLearning
		p.fwdDelayTimer = now
	}
}

func setTopologyPropertyLinks(l *Layer, edge bool) {
	for _, name := range []string{"p1", "p2"} {
		link := l.links[name]
		link.up = true
		link.pointToPoint = true
		link.edge = edge
		link.external = false
		link.sendRSTP = true
	}
}

func setTopologyPropertyReceived(t *tree, p *portState, portID uint16, now time.Time) {
	p.rcvInfoValid = true
	p.rcvRootID = t.rootID
	p.rcvRootPathCost = 0
	p.rcvRegionalRootID = t.rootID
	p.rcvInternalRootPathCost = 0
	p.rcvBridgeID = t.rootID
	p.rcvPortID = portID
	p.rcvHelloTime = 2 * time.Second
	p.rcvTime = now
}

func topologyPropertyRoot() bpdu.BridgeID {
	return bpdu.BridgeID{Priority: 4096}
}

func topologyPropertyBPDU(l *Layer, t *tree, topologyChange bool) bpdu.BPDU {
	b := bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		RootID:       t.rootID,
		RootPathCost: 0,
		BridgeID:     t.rootID,
		PortID:       0x8002,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	b.SetRole(bpdu.RoleDesignated)
	b.SetTopologyChange(topologyChange)
	if l.mst == nil {
		return b
	}

	cid := *l.configID
	b.Version = 3
	b.ConfigID = &cid
	b.RegionalRootID = t.regionalRootID
	b.RemainingHops = 20
	for _, id := range l.treeOrder {
		if id == cistID {
			continue
		}
		mt := l.trees[id]
		var flags bpdu.BPDU
		flags.SetRole(bpdu.RoleDesignated)
		flags.SetTopologyChange(topologyChange)
		b.MSTIs = append(b.MSTIs, bpdu.MSTIRecord{
			MSTID:          bpdu.MSTID(id),
			Flags:          flags.Flags,
			RegionalRootID: mt.rootID,
			BridgePriority: 0x80,
			PortPriority:   0x80,
			RemainingHops:  20,
		})
	}

	return b
}

func assertTopologyProperty(t *testing.T, l *Layer, now time.Time, effects layer.Effects) {
	t.Helper()
	type topologyFrame struct {
		emission layer.Emission
		decoded  bpdu.BPDU
		tree     *tree
	}

	var frames []topologyFrame
	seen := make(map[string]struct{})
	for _, emission := range effects.Emissions {
		var decoded bpdu.BPDU
		var err error
		if l.pvst != nil && emission.VID != 0 {
			decoded, _, err = bpdu.DecodeSSTP(emission.Frame)
		} else {
			decoded, err = bpdu.Decode(emission.Frame)
		}
		if err != nil {
			t.Fatalf("decode returned emission on %s: %v", emission.Port, err)
		}
		if l.pvst != nil && emission.VID == 0 {
			continue
		}
		treeID := cistID
		if l.pvst != nil {
			var ok bool
			treeID, ok = l.vidToTree[emission.VID]
			if !ok {
				t.Fatalf("emission on unknown PVST VLAN %d", emission.VID)
			}
		}
		treeValue := l.trees[treeID]
		hasTopologyChange := decoded.TopologyChange()
		for _, record := range decoded.MSTIs {
			if (bpdu.BPDU{Flags: record.Flags}).TopologyChange() {
				hasTopologyChange = true
			}
		}
		if !hasTopologyChange {
			continue
		}
		key := fmt.Sprintf("%d/%s", treeID, emission.Port)
		if _, ok := seen[key]; ok {
			t.Fatalf("topology-change frame repeated for tree/port %s", key)
		}
		seen[key] = struct{}{}
		frames = append(frames, topologyFrame{emission: emission, decoded: decoded, tree: treeValue})
	}

	if len(frames) == 0 {
		t.Fatalf("returned emissions contain no topology-change frame: %+v", effects.Emissions)
	}

	lastTree := -1
	lastPort := ""
	for _, frame := range frames {
		treeIndex := 0
		for i, id := range l.treeOrder {
			if id == frame.tree.id {
				treeIndex = i
				break
			}
		}
		if treeIndex < lastTree || treeIndex == lastTree && frame.emission.Port < lastPort {
			t.Fatalf("topology-change frames are not ordered by tree then port: %v before %v", lastTree, frame.emission.Port)
		}
		lastTree = treeIndex
		lastPort = frame.emission.Port

		p := frame.tree.ports[frame.emission.Port]
		if frame.decoded.RootID != frame.tree.rootID {
			t.Errorf("tree %d frame on %s names root %v, want final %v", frame.tree.id, frame.emission.Port, frame.decoded.RootID, frame.tree.rootID)
		}
		if frame.decoded.Role() != p.role {
			t.Errorf("tree %d frame on %s role %v, want final %v", frame.tree.id, frame.emission.Port, frame.decoded.Role(), p.role)
		}

		if l.pvst == nil {
			if frame.decoded.TopologyChange() != topologyChangeTimerActive(p, now) {
				t.Errorf("tree %d frame on %s CIST flag = %t, want %t", frame.tree.id, frame.emission.Port, frame.decoded.TopologyChange(), topologyChangeTimerActive(p, now))
			}
		} else if frame.decoded.TopologyChange() != topologyChangeTimerActive(p, now) {
			t.Errorf("VLAN %d frame on %s flag = %t, want %t", frame.tree.vid, frame.emission.Port, frame.decoded.TopologyChange(), topologyChangeTimerActive(p, now))
		}

		for _, record := range frame.decoded.MSTIs {
			mt := l.trees[treeID(record.MSTID)]
			mp := mt.ports[frame.emission.Port]
			recordBPDU := bpdu.BPDU{Flags: record.Flags}
			wantRegionalRoot := mt.rootID
			wantRegionalRoot.Priority = (wantRegionalRoot.Priority & 0xF000) | uint16(record.MSTID)
			if record.RegionalRootID != wantRegionalRoot {
				t.Errorf("MSTI %d frame names regional root %v, want final %v", record.MSTID, record.RegionalRootID, wantRegionalRoot)
			}
			if recordBPDU.Role() != mp.role {
				t.Errorf("MSTI %d frame role %v, want final %v", record.MSTID, recordBPDU.Role(), mp.role)
			}
			if recordBPDU.TopologyChange() != topologyChangeTimerActive(mp, now) {
				t.Errorf("MSTI %d frame flag = %t, want %t", record.MSTID, recordBPDU.TopologyChange(), topologyChangeTimerActive(mp, now))
			}
		}
	}
}
