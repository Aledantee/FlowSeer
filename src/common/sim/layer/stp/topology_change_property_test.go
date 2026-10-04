package stp

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestTopologyChangeEmissionProperty(t *testing.T) {
	t.Parallel()

	modes := []struct {
		name string
		new  func(*testing.T) *Layer
	}{
		{name: "RSTP", new: func(t *testing.T) *Layer { return newTopologyPropertyLayer(t, "rstp") }},
		{name: "MST", new: func(t *testing.T) *Layer { return newTopologyPropertyLayer(t, "mst") }},
		{name: "PVST", new: func(t *testing.T) *Layer { return newTopologyPropertyLayer(t, "pvst") }},
	}

	for _, mode := range modes {
		mode := mode
		t.Run(mode.name, func(t *testing.T) {
			t.Run("Receive", func(t *testing.T) {
				l, now := mode.new(t), topologyPropertyTime()
				prepareTopologyPropertyState(t, l, now)
				assertTopologyPropertyState(t, l, bpdu.RoleRoot, bpdu.RoleDesignated)
				before := snapshotTopologyPropertyTimers(l)

				superior := topologyPropertyBPDU(l, topologyPropertyRootForTree(l, l.cist()), true, false)
				superior.RootPathCost = 0
				callNow := now.Add(31 * time.Second)
				effects := l.Receive(callNow, "p2", superior)
				assertTopologyProperty(t, l, callNow, before, effects)

				if got := l.PortInfo("p1"); got.Role != bpdu.RoleAlternate || got.State != StateDiscarding {
					t.Fatalf("final p1 state = %v/%v, want Alternate/Discarding", got.Role, got.State)
				}
				if got := l.PortInfo("p2"); got.Role != bpdu.RoleRoot || got.State != StateForwarding {
					t.Fatalf("final p2 state = %v/%v, want Root/Forwarding", got.Role, got.State)
				}
				if root, _, port := l.Root(); root != topologyPropertyRootForTree(l, l.cist()) || port != "p2" {
					t.Fatalf("final CIST root = %v via %q, want the superior path via p2", root, port)
				}
			})

			t.Run("LinkChange", func(t *testing.T) {
				l, now := mode.new(t), topologyPropertyTime()
				prepareTopologyPropertyState(t, l, now)
				prepareTopologyPropertyFailover(t, l, now.Add(31*time.Second))
				before := snapshotTopologyPropertyTimers(l)

				callNow := now.Add(32 * time.Second)
				effects := l.LinkChange(callNow, "p1", false, true, 0)
				assertTopologyProperty(t, l, callNow, before, effects)
				if got := l.PortInfo("p2"); got.Role != bpdu.RoleRoot || got.State != StateForwarding {
					t.Fatalf("final p2 state = %v/%v, want Root/Forwarding", got.Role, got.State)
				}
			})

			t.Run("Mcheck", func(t *testing.T) {
				l, now := mode.new(t), topologyPropertyTime()
				prepareTopologyPropertyState(t, l, now)
				beforeState := l.PortInfo("p1")
				before := snapshotTopologyPropertyTimers(l)

				callNow := now.Add(31 * time.Second)
				effects := l.Mcheck(callNow, "p1")
				assertTopologyProperty(t, l, callNow, before, effects)
				if got := l.PortInfo("p1"); got.Role != beforeState.Role || got.State != beforeState.State {
					t.Fatalf("Mcheck changed p1 from %v/%v to %v/%v", beforeState.Role, beforeState.State, got.Role, got.State)
				}
			})

			t.Run("Advance", func(t *testing.T) {
				l, now := mode.new(t), topologyPropertyTime()
				prepareTopologyPropertyAdvanceState(l, now)
				before := snapshotTopologyPropertyTimers(l)

				effects := l.Advance(now.Add(30 * time.Second))
				assertTopologyProperty(t, l, now.Add(30*time.Second), before, effects)
				if got := l.PortInfo("p2"); got.Role != bpdu.RoleDesignated || got.State != StateForwarding {
					t.Fatalf("final p2 state = %v/%v, want Designated/Forwarding", got.Role, got.State)
				}
			})
		})
	}

	t.Run("ReceiveSSTP", func(t *testing.T) {
		l, now := newTopologyPropertyLayer(t, "pvst"), topologyPropertyTime()
		prepareTopologyPropertyState(t, l, now)
		beforeState := l.VLANPortInfo(10, "p2")
		if beforeState.Role != bpdu.RoleDesignated || beforeState.State != StateForwarding {
			t.Fatalf("VLAN 10 before ReceiveSSTP = %v/%v, want Designated/Forwarding", beforeState.Role, beforeState.State)
		}
		before := snapshotTopologyPropertyTimers(l)

		callNow := now.Add(31 * time.Second)
		sstp := topologyPropertyBPDU(l, topologyPropertyRootForTree(l, l.trees[treeID(10)]), true, false)
		sstp.RootPathCost = 200_000
		effects, outcome := l.ReceiveSSTP(callNow, "p2", SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}, sstp)
		if outcome != SSTPApplied {
			t.Fatalf("ReceiveSSTP outcome = %v, want %v", outcome, SSTPApplied)
		}
		assertTopologyProperty(t, l, callNow, before, effects)
		if got := l.VLANPortInfo(10, "p1"); got.Role != bpdu.RoleRoot || got.State != StateForwarding {
			t.Fatalf("VLAN 10 final p1 state = %v/%v, want Root/Forwarding", got.Role, got.State)
		}
	})
}

func TestLegacyTopologyChangeEmissionProperty(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"rstp", "pvst"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			l, now := newTopologyPropertyLayer(t, mode), topologyPropertyTime()
			prepareTopologyPropertyState(t, l, now)

			legacy := topologyPropertyBPDU(l, topologyPropertyRootForTree(l, l.cist()), false, false)
			legacy.Version = 0
			legacy.Type = bpdu.TypeConfiguration
			legacyNow := now.Add(34 * time.Second)
			l.Receive(legacyNow, "p1", legacy)
			if l.PortInfo("p1").SendRSTP {
				t.Fatal("p1 did not migrate to legacy STP")
			}

			before := snapshotTopologyPropertyTimers(l)
			tcnNow := now.Add(35 * time.Second)
			effects := l.Receive(tcnNow, "p2", bpdu.BPDU{Version: 0, Type: bpdu.TypeTopologyChangeNotification})
			assertTopologyProperty(t, l, tcnNow, before, effects)

			rootFrames := topologyPropertyFramesFor(t, l, effects)
			var cist []topologyPropertyFrame
			for _, frame := range rootFrames {
				if frame.tree == cistID && frame.emission.Port == "p1" {
					cist = append(cist, frame)
				}
			}
			if len(cist) != 1 || cist[0].emission.VID != 0 {
				t.Fatalf("legacy root-port frames = %+v, want one untagged TCN", cist)
			}
			for _, emission := range effects.Emissions {
				if emission.Port == "p1" && emission.VID != 0 {
					t.Fatalf("legacy root-port transmission used VID %d, want only untagged VID 0", emission.VID)
				}
			}

			before = snapshotTopologyPropertyTimers(l)
			superior := topologyPropertyBPDU(l, topologyPropertyRootForTree(l, l.cist()), true, false)
			superior.RootPathCost = 0
			rootMoveNow := now.Add(36 * time.Second)
			effects = l.Receive(rootMoveNow, "p2", superior)
			assertTopologyProperty(t, l, rootMoveNow, before, effects)
			if got := l.PortInfo("p1"); got.Role != bpdu.RoleAlternate {
				t.Fatalf("legacy p1 after root move = %v, want Alternate", got.Role)
			}
		})
	}
}

func topologyPropertyTime() time.Time {
	return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
}

func newTopologyPropertyLayer(t *testing.T, mode string) *Layer {
	t.Helper()
	cfg := Config{
		Priority: 32768,
		Address:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		Ports:    map[string]Port{"p1": {PathCost: 200_000}, "p2": {}},
	}
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

	portBuilder := port.NewBuilder()
	portBuilder.Add(port.Port{Name: "p1", Kind: port.Physical})
	portBuilder.Add(port.Port{Name: "p2", Kind: port.Physical})
	ports, err := portBuilder.Build()
	if err != nil {
		t.Fatalf("build topology property ports: %v", err)
	}
	l, err := New(cfg, layer.Env{Ports: ports})
	if err != nil {
		t.Fatalf("New(%s) = %v", mode, err)
	}

	return l
}

func prepareTopologyPropertyState(t *testing.T, l *Layer, now time.Time) {
	t.Helper()
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)

	receiveTopologyPropertyRoots(l, now, true)
	receiveTopologyPropertyRoots(l, now.Add(14*time.Second), false)
	l.Advance(now.Add(15 * time.Second))
	receiveTopologyPropertyRoots(l, now.Add(29*time.Second), false)
	l.Advance(now.Add(30 * time.Second))
	assertTopologyPropertyState(t, l, bpdu.RoleRoot, bpdu.RoleDesignated)
}

func prepareTopologyPropertyAdvanceState(l *Layer, now time.Time) {
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)
	receiveTopologyPropertyRoots(l, now, true)
	receiveTopologyPropertyRoots(l, now.Add(14*time.Second), false)
	l.Advance(now.Add(15 * time.Second))
	receiveTopologyPropertyRoots(l, now.Add(29*time.Second), false)
}

func receiveTopologyPropertyRoots(l *Layer, now time.Time, proposal bool) {
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		b := topologyPropertyBPDU(l, topologyPropertyRootForTree(l, mt), false, proposal)
		if l.pvst != nil && id != cistID {
			l.ReceiveSSTP(now, "p1", SSTPArrival{ArrivalVID: mt.vid, TLVVID: mt.vid, Admitted: true}, b)
		} else if id == cistID {
			l.Receive(now, "p1", b)
		}
	}
}

func prepareTopologyPropertyFailover(t *testing.T, l *Layer, now time.Time) {
	t.Helper()
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		b := topologyPropertyBPDU(l, topologyPropertyRootForTree(l, mt), false, false)
		if l.mst != nil {
			b.RootPathCost = 0
			b.InternalRootPathCost = 200_000
		} else {
			b.RootPathCost = 200_000
		}
		for i := range b.MSTIs {
			if l.mst != nil {
				b.MSTIs[i].InternalRootPathCost = 200_000
			} else {
				b.MSTIs[i].InternalRootPathCost = 0
			}
		}
		if l.pvst != nil && id != cistID {
			l.ReceiveSSTP(now, "p2", SSTPArrival{ArrivalVID: mt.vid, TLVVID: mt.vid, Admitted: true}, b)
		} else if id == cistID {
			l.Receive(now, "p2", b)
		}
	}
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		vid := topologyPropertyVID(mt)
		p1 := l.VLANPortInfo(vid, "p1")
		p2 := l.VLANPortInfo(vid, "p2")
		if p1.Role != bpdu.RoleRoot || p1.State != StateForwarding || p2.Role != bpdu.RoleAlternate || p2.State != StateDiscarding {
			t.Fatalf("tree %d failover setup = p1 %v/%v p2 %v/%v, want Root/Forwarding and Alternate/Discarding", id, p1.Role, p1.State, p2.Role, p2.State)
		}
	}
}

func assertTopologyPropertyState(t *testing.T, l *Layer, p1Role, p2Role bpdu.Role) {
	t.Helper()
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		vid := topologyPropertyVID(mt)
		p1 := l.VLANPortInfo(vid, "p1")
		p2 := l.VLANPortInfo(vid, "p2")
		if p1.Role != p1Role || p1.State != StateForwarding {
			t.Fatalf("tree %d p1 = %v/%v, want %v/Forwarding", id, p1.Role, p1.State, p1Role)
		}
		if p2.Role != p2Role || p2.State != StateForwarding {
			t.Fatalf("tree %d p2 = %v/%v, want %v/Forwarding", id, p2.Role, p2.State, p2Role)
		}
		if p2.DesignatedRoot != mt.rootID {
			t.Fatalf("tree %d p2 designated root = %v (tree=%+v), want %v (tree=%+v)", id, p2.DesignatedRoot, p2.Tree, mt.rootID, mt)
		}
	}
}

func topologyPropertyVID(t *tree) vlan.ID {
	if t.vid != 0 {
		return t.vid
	}
	if t.id != cistID {
		return vlan.ID(t.id * 10)
	}

	return 1
}

func topologyPropertyRootForTree(l *Layer, t *tree) bpdu.BridgeID {
	root := bpdu.BridgeID{
		Priority: 4096,
		Address:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
	}
	if l.pvst != nil {
		root.Priority |= uint16(t.vid)
	} else if l.mst != nil && t.id != cistID {
		root.Priority |= uint16(t.id)
	}

	return root
}

func topologyPropertyBPDU(l *Layer, root bpdu.BridgeID, flagged, proposal bool) bpdu.BPDU {
	b := bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		RootID:       root,
		BridgeID:     root,
		PortID:       0x8001,
		HelloTime:    DefaultHelloTime,
		MaxAge:       DefaultMaxAge,
		ForwardDelay: DefaultForwardDelay,
	}
	b.SetRole(bpdu.RoleDesignated)
	b.SetTopologyChange(flagged)
	b.SetProposal(proposal)
	if l.mst == nil {
		return b
	}

	cid := *l.configID
	b.Version = 3
	b.ConfigID = &cid
	b.RegionalRootID = topologyPropertyRootForTree(l, l.cist())
	b.RemainingHops = 20
	for _, id := range l.treeOrder {
		if id == cistID {
			continue
		}
		var flags bpdu.BPDU
		flags.SetRole(bpdu.RoleDesignated)
		flags.SetTopologyChange(flagged)
		flags.SetProposal(proposal)
		b.MSTIs = append(b.MSTIs, bpdu.MSTIRecord{
			MSTID:          bpdu.MSTID(id),
			Flags:          flags.Flags,
			RegionalRootID: topologyPropertyRootForTree(l, l.trees[id]),
			BridgePriority: uint8(root.Priority >> 8),
			PortPriority:   0x80,
			RemainingHops:  20,
		})
	}

	return b
}

type topologyPropertyTimerKey struct {
	tree treeID
	port string
}

func snapshotTopologyPropertyTimers(l *Layer) map[topologyPropertyTimerKey]time.Time {
	timers := make(map[topologyPropertyTimerKey]time.Time)
	for _, id := range l.treeOrder {
		for _, name := range l.portNames {
			if p := l.trees[id].ports[name]; p != nil {
				timers[topologyPropertyTimerKey{tree: id, port: name}] = p.tcWhile
			}
		}
	}

	return timers
}

type topologyPropertyFrame struct {
	tree      treeID
	emission  layer.Emission
	decoded   bpdu.BPDU
	record    *bpdu.MSTIRecord
	emissions []int
	vids      []vlan.ID
}

func assertTopologyProperty(t *testing.T, l *Layer, now time.Time, before map[topologyPropertyTimerKey]time.Time, effects layer.Effects) {
	t.Helper()
	expected := make(map[topologyPropertyTimerKey]struct{})
	for _, id := range l.treeOrder {
		for _, name := range l.portNames {
			p := l.trees[id].ports[name]
			key := topologyPropertyTimerKey{tree: id, port: name}
			if p.tcWhile.IsZero() || p.tcWhile.Equal(before[key]) || !p.tcWhile.After(now) {
				continue
			}
			if l.links[name].up && p.role == bpdu.RoleRoot {
				expected[key] = struct{}{}
			}
		}
	}

	frames := topologyPropertyFramesFor(t, l, effects)
	observed := make(map[topologyPropertyTimerKey]*topologyPropertyFrame)
	var order []topologyPropertyTimerKey
	for _, frame := range frames {
		key := topologyPropertyTimerKey{tree: frame.tree, port: frame.emission.Port}
		if previous, ok := observed[key]; ok {
			previous.emissions = append(previous.emissions, len(previous.emissions))
			previous.vids = append(previous.vids, frame.emission.VID)
			continue
		}
		frameCopy := frame
		observed[key] = &frameCopy
		order = append(order, key)

		p := l.trees[key.tree].ports[key.port]
		pInfo := l.VLANPortInfo(topologyPropertyVID(l.trees[key.tree]), key.port)
		if pInfo.Role != bpdu.RoleRoot {
			t.Errorf("topology-change frame on tree %d port %s has final role %v, want Root", key.tree, key.port, p.role)
		}
		if frame.record == nil && frame.decoded.Type != bpdu.TypeTopologyChangeNotification {
			if frame.decoded.RootID != l.trees[key.tree].rootID {
				t.Errorf("tree %d frame on %s names root %v, want final %v", key.tree, key.port, frame.decoded.RootID, l.trees[key.tree].rootID)
			}
			if frame.decoded.Role() != pInfo.Role {
				t.Errorf("tree %d frame on %s role %v, want final %v", key.tree, key.port, frame.decoded.Role(), pInfo.Role)
			}
		}
		if frame.record != nil {
			recordFlags := bpdu.BPDU{Flags: frame.record.Flags}
			if frame.record.RegionalRootID != l.trees[key.tree].rootID {
				t.Errorf("MSTI %d frame on %s names root %v, want final %v", key.tree, key.port, frame.record.RegionalRootID, l.trees[key.tree].rootID)
			}
			if recordFlags.Role() != pInfo.Role {
				t.Errorf("MSTI %d frame on %s role %v, want final %v", key.tree, key.port, recordFlags.Role(), pInfo.Role)
			}
		}
	}

	for key := range expected {
		frame, ok := observed[key]
		if !ok {
			t.Errorf("tree %d Root port %s restarted its topology-change timer without a frame", key.tree, key.port)
			continue
		}
		pvstCopies := l.pvst != nil && key.tree == cistID && l.links[key.port].sendRSTP && len(frame.emissions) == 2
		if len(frame.emissions) > 1 && !pvstCopies {
			t.Errorf("topology-change frame repeated for tree %d port %s", key.tree, key.port)
		}
		if l.pvst != nil {
			if key.tree == cistID {
				if l.links[key.port].sendRSTP {
					if !containsTopologyPropertyVID(frame.vids, 1) {
						t.Errorf("PVST VLAN 1 topology-change transmission on %s lacks VID 1", key.port)
					}
				} else if len(frame.vids) != 1 || frame.vids[0] != 0 {
					t.Errorf("legacy PVST VLAN 1 topology-change VIDs = %v, want [0]", frame.vids)
				}
			} else if len(frame.vids) != 1 || frame.vids[0] != l.trees[key.tree].vid {
				t.Errorf("PVST VLAN %d topology-change VIDs = %v, want [%d]", key.tree, frame.vids, l.trees[key.tree].vid)
			}
		}
	}

	lastTree := -1
	lastPort := ""
	for _, key := range order {
		treeIndex := 0
		for i, id := range l.treeOrder {
			if id == key.tree {
				treeIndex = i
				break
			}
		}
		if treeIndex < lastTree || treeIndex == lastTree && key.port < lastPort {
			t.Fatalf("topology-change frames are not ordered by tree then port: %v/%s before %v/%s", lastTree, lastPort, treeIndex, key.port)
		}
		lastTree = treeIndex
		lastPort = key.port
	}
}

func topologyPropertyFramesFor(t *testing.T, l *Layer, effects layer.Effects) []topologyPropertyFrame {
	t.Helper()
	var frames []topologyPropertyFrame
	for emissionIndex, emission := range effects.Emissions {
		decoded, emissionTree := decodeTopologyPropertyEmission(t, l, emission)
		if decoded.Type == bpdu.TypeTopologyChangeNotification {
			frames = append(frames, topologyPropertyFrame{
				tree:      emissionTree,
				emission:  emission,
				decoded:   decoded,
				emissions: []int{emissionIndex},
				vids:      []vlan.ID{emission.VID},
			})
			continue
		}
		if decoded.TopologyChange() && decoded.Role() != bpdu.RoleDesignated {
			frames = append(frames, topologyPropertyFrame{
				tree:      emissionTree,
				emission:  emission,
				decoded:   decoded,
				emissions: []int{emissionIndex},
				vids:      []vlan.ID{emission.VID},
			})
		}
		for i := range decoded.MSTIs {
			record := &decoded.MSTIs[i]
			recordFlags := bpdu.BPDU{Flags: record.Flags}
			if !recordFlags.TopologyChange() || recordFlags.Role() == bpdu.RoleDesignated {
				continue
			}
			frames = append(frames, topologyPropertyFrame{
				tree:      treeID(record.MSTID),
				emission:  emission,
				decoded:   decoded,
				record:    record,
				emissions: []int{emissionIndex},
				vids:      []vlan.ID{emission.VID},
			})
		}
	}

	return frames
}

func containsTopologyPropertyVID(vids []vlan.ID, want vlan.ID) bool {
	for _, vid := range vids {
		if vid == want {
			return true
		}
	}

	return false
}

func decodeTopologyPropertyEmission(t *testing.T, l *Layer, emission layer.Emission) (bpdu.BPDU, treeID) {
	t.Helper()
	if l.pvst != nil && emission.VID != 0 {
		decoded, _, err := bpdu.DecodeSSTP(emission.Frame)
		if err != nil {
			t.Fatalf("decode SSTP emission on %s: %v", emission.Port, err)
		}
		tree, ok := l.vidToTree[emission.VID]
		if !ok {
			t.Fatalf("emission on unknown PVST VLAN %d", emission.VID)
		}

		return decoded, tree
	}
	decoded, err := bpdu.Decode(emission.Frame)
	if err != nil {
		t.Fatalf("decode emission on %s: %v", emission.Port, err)
	}

	return decoded, cistID
}
