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

				superior := topologyPropertyBPDU(l, topologyPropertyRootForTree(l, l.cist()), true, false)
				superior.RootPathCost = 200_000
				callNow := now.Add(31 * time.Second)
				effects := l.Receive(callNow, "p2", superior)
				assertTopologyProperty(t, l, effects, topologyPropertyOwed{tree: cistID, port: "p1"})

				if got := l.PortInfo("p1"); got.Role != bpdu.RoleRoot || got.State != StateForwarding {
					t.Fatalf("final p1 state = %v/%v, want Root/Forwarding", got.Role, got.State)
				}
				wantP2Role, wantP2State := bpdu.RoleAlternate, StateDiscarding
				if mode.name == "MST" {
					wantP2Role, wantP2State = bpdu.RoleDesignated, StateForwarding
				}
				if got := l.PortInfo("p2"); got.Role != wantP2Role || got.State != wantP2State {
					t.Fatalf("final p2 state = %v/%v, want %v/%v", got.Role, got.State, wantP2Role, wantP2State)
				}
				if root, _, port := l.Root(); root != topologyPropertyRootForTree(l, l.cist()) || port != "p1" {
					t.Fatalf("final CIST root = %v via %q, want the retained root via p1", root, port)
				}
			})

			t.Run("LinkChange", func(t *testing.T) {
				l, now := mode.new(t), topologyPropertyTime()
				prepareTopologyPropertyState(t, l, now)
				prepareTopologyPropertyFailover(t, l, now.Add(31*time.Second))

				callNow := now.Add(32 * time.Second)
				effects := l.LinkChange(callNow, "p1", false, true, 0)
				assertTopologyProperty(t, l, effects, topologyPropertyOwedForMode(mode.name, "p2")...)
				if got := l.PortInfo("p2"); got.Role != bpdu.RoleRoot || got.State != StateForwarding {
					t.Fatalf("final p2 state = %v/%v, want Root/Forwarding", got.Role, got.State)
				}
			})

			t.Run("Mcheck", func(t *testing.T) {
				l, now := newTopologyPropertyMcheckLayer(t, mode.name), topologyPropertyTime()
				prepareTopologyPropertyMcheck(t, l, now)
				beforeState := l.PortInfo("p1")
				if beforeState.Role != bpdu.RoleRoot || beforeState.State != StateDiscarding {
					t.Fatalf("Mcheck prerequisite p1 = %v/%v, want Root/Discarding", beforeState.Role, beforeState.State)
				}

				callNow := now.Add(32 * time.Second)
				effects := l.Mcheck(callNow, "p1")
				assertTopologyProperty(t, l, effects, topologyPropertyMcheckOwedForMode(mode.name, "p1")...)
				if got := l.PortInfo("p1"); got.Role != bpdu.RoleRoot || got.State != StateForwarding {
					t.Fatalf("Mcheck final p1 = %v/%v, want Root/Forwarding", got.Role, got.State)
				}
			})

			t.Run("Advance", func(t *testing.T) {
				l, now := mode.new(t), topologyPropertyTime()
				prepareTopologyPropertyAdvanceState(l, now)
				assertTopologyPropertyAdvanceState(t, l)

				callNow := now.Add(30*time.Second + 500*time.Millisecond)
				effects := l.Advance(callNow)
				assertTopologyProperty(t, l, effects, topologyPropertyOwedForMode(mode.name, "p1")...)
				if got := l.PortInfo("p2"); got.Role != bpdu.RoleDesignated || got.State != StateForwarding {
					t.Fatalf("final p2 state = %v/%v, want Designated/Forwarding", got.Role, got.State)
				}
			})

			t.Run("DesignatedRestartRootReturn", func(t *testing.T) {
				l, now := mode.new(t), topologyPropertyTime()
				prepareTopologyPropertyState(t, l, now)
				if got := l.PortInfo("p2"); got.Role != bpdu.RoleDesignated {
					t.Fatalf("restart prerequisite p2 = %v, want Designated", got.Role)
				}
				l.Advance(now.Add(34 * time.Second))

				b := topologyPropertyBPDU(l, topologyPropertyRootForTree(l, l.cist()), false, true)
				b.RootPathCost = 0
				b.SetAgreement(true)
				effects := l.Receive(now.Add(35*time.Second), "p2", b)
				if got := l.PortInfo("p2"); got.Role != bpdu.RoleRoot || got.State != StateForwarding {
					t.Fatalf("final p2 = %v/%v, want Root/Forwarding", got.Role, got.State)
				}
				unflagged := false
				for _, emission := range effects.Emissions {
					decoded, _ := decodeTopologyPropertyEmission(t, l, emission)
					unflagged = unflagged || !decoded.TopologyChange()
				}
				if !unflagged {
					t.Fatal("role-change case emitted no unflagged frame")
				}
				assertTopologyProperty(t, l, effects)
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

		callNow := now.Add(31 * time.Second)
		sstp := topologyPropertyBPDU(l, topologyPropertyRootForTree(l, l.trees[treeID(10)]), true, false)
		sstp.RootPathCost = 200_000
		effects, outcome := l.ReceiveSSTP(callNow, "p2", SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}, sstp)
		if outcome != SSTPApplied {
			t.Fatalf("ReceiveSSTP outcome = %v, want %v", outcome, SSTPApplied)
		}
		assertTopologyProperty(t, l, effects, topologyPropertyOwed{tree: treeID(10), port: "p1"})
		if got := l.VLANPortInfo(10, "p1"); got.Role != bpdu.RoleRoot || got.State != StateForwarding {
			t.Fatalf("VLAN 10 final p1 state = %v/%v, want Root/Forwarding", got.Role, got.State)
		}
	})
}

func TestAdvanceCoalescesHeldRootAgreementWithHello(t *testing.T) {
	t.Parallel()

	now := topologyPropertyTime()
	l := newTopologyPropertyLayer(t, "rstp")
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)
	b := topologyPropertyBPDU(l, topologyPropertyRootForTree(l, l.cist()), false, true)
	for i := 0; i < 8; i++ {
		b.PortID++
		l.Receive(now.Add(time.Duration(100+i*100)*time.Millisecond), "p1", b)
	}
	if !l.tx(l.cist(), "p1").pendingAgreement {
		t.Fatal("setup did not hold a Root-port agreement")
	}

	effects := l.Advance(now.Add(2 * time.Second))
	assertTopologyProperty(t, l, effects)
	p1Emissions := 0
	for _, emission := range effects.Emissions {
		if emission.Port == "p1" {
			p1Emissions++
		}
	}
	if p1Emissions != 1 {
		t.Fatalf("Advance emitted %d Root-port frames, want one held agreement or hello", p1Emissions)
	}
	if tx := l.tx(l.cist(), "p1"); tx.pendingAgreement || tx.pendingTCN {
		t.Fatalf("Advance retained a duplicate Root-port transmission: %+v", *tx)
	}
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

			if l.pvst != nil {
				refreshTopologyPropertyVLANs(l, now.Add(34*time.Second))
			}
			tcnNow := now.Add(35 * time.Second)
			effects := l.Receive(tcnNow, "p2", bpdu.BPDU{Version: 0, Type: bpdu.TypeTopologyChangeNotification})
			want := []topologyPropertyOwed{{tree: cistID, port: "p1"}}
			if l.pvst != nil {
				want = topologyPropertyOwedForMode("PVST", "p1")
			}
			assertTopologyProperty(t, l, effects, want...)
			for _, id := range l.treeOrder {
				mt := l.trees[id]
				if got := l.VLANPortInfo(topologyPropertyVID(mt), "p1"); got.Role != bpdu.RoleRoot || got.State != StateForwarding {
					t.Fatalf("legacy tree %d p1 = %v/%v, want Root/Forwarding", id, got.Role, got.State)
				}
			}

			var cistFrames []layer.Emission
			for _, emission := range effects.Emissions {
				if emission.Port != "p1" {
					continue
				}
				if emission.VID == 0 {
					cistFrames = append(cistFrames, emission)
				}
			}
			if len(cistFrames) != 1 {
				t.Fatalf("legacy root-port frames = %d, want one untagged TCN", len(cistFrames))
			}
			decoded, err := bpdu.Decode(cistFrames[0].Frame)
			if err != nil || decoded.Type != bpdu.TypeTopologyChangeNotification {
				t.Fatalf("legacy root-port frame = type %v, decode error %v, want one TCN", decoded.Type, err)
			}
			for _, emission := range effects.Emissions {
				if emission.Port == "p1" && emission.VID != 0 {
					t.Fatalf("legacy root-port transmission used VID %d, want only untagged VID 0", emission.VID)
				}
			}
			ladderEffects := l.Advance(tcnNow)
			assertTopologyProperty(t, l, ladderEffects, topologyPropertyOwed{tree: cistID, port: "p1"})

			superior := topologyPropertyBPDU(l, topologyPropertyRootForTree(l, l.cist()), true, false)
			superior.RootPathCost = 0
			rootMoveNow := now.Add(36 * time.Second)
			effects = l.Receive(rootMoveNow, "p2", superior)
			assertTopologyProperty(t, l, effects)
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
	return newTopologyPropertyLayerWithOptions(t, mode, false)
}

func newTopologyPropertyMcheckLayer(t *testing.T, mode string) *Layer {
	return newTopologyPropertyLayerWithOptions(t, mode, true)
}

func newTopologyPropertyLayerWithOptions(t *testing.T, mode string, p2AdminEdge bool) *Layer {
	t.Helper()
	switch mode {
	case "RSTP":
		mode = "rstp"
	case "MST":
		mode = "mst"
	case "PVST":
		mode = "pvst"
	}
	cfg := Config{
		Priority: 32768,
		Address:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		Ports:    map[string]Port{"p1": {PathCost: 200_000}, "p2": {AdminEdge: p2AdminEdge}},
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
	l.Advance(now.Add(29 * time.Second))
}

func prepareTopologyPropertyMcheck(t *testing.T, l *Layer, now time.Time) {
	t.Helper()
	l.LinkChange(now, "p1", true, true, 1_000_000_000)
	l.LinkChange(now, "p2", true, true, 1_000_000_000)

	legacy := topologyPropertyBPDU(l, topologyPropertyRootForTree(l, l.cist()), false, false)
	legacy.Version = 0
	legacy.Type = bpdu.TypeConfiguration
	l.Receive(now.Add(31*time.Second), "p1", legacy)
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

func refreshTopologyPropertyVLANs(l *Layer, now time.Time) {
	if l.pvst == nil {
		return
	}
	for _, id := range l.treeOrder {
		if id == cistID {
			continue
		}
		mt := l.trees[id]
		b := topologyPropertyBPDU(l, topologyPropertyRootForTree(l, mt), false, false)
		l.ReceiveSSTP(now, "p1", SSTPArrival{ArrivalVID: mt.vid, TLVVID: mt.vid, Admitted: true}, b)
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
		wantRoot := topologyPropertyRootForTree(l, mt)
		if p2.DesignatedRoot != wantRoot {
			t.Fatalf("tree %d p2 designated root = %v, want literal root %v", id, p2.DesignatedRoot, wantRoot)
		}
	}
}

func assertTopologyPropertyAdvanceState(t *testing.T, l *Layer) {
	t.Helper()
	for _, id := range l.treeOrder {
		mt := l.trees[id]
		vid := topologyPropertyVID(mt)
		p1 := l.VLANPortInfo(vid, "p1")
		p2 := l.VLANPortInfo(vid, "p2")
		if p1.Role != bpdu.RoleRoot || p1.State != StateForwarding {
			t.Fatalf("tree %d advance start p1 = %v/%v, want Root/Forwarding", id, p1.Role, p1.State)
		}
		if p2.Role != bpdu.RoleDesignated || p2.State != StateLearning {
			t.Fatalf("tree %d advance start p2 = %v/%v, want Designated/Learning", id, p2.Role, p2.State)
		}
		wantRoot := topologyPropertyRootForTree(l, mt)
		if mt.rootID != wantRoot {
			t.Fatalf("tree %d advance start root = %v, want literal root %v", id, mt.rootID, wantRoot)
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

type topologyPropertyOwed struct {
	tree treeID
	port string
}

func topologyPropertyOwedForMode(mode, port string) []topologyPropertyOwed {
	var trees []treeID
	switch mode {
	case "RSTP":
		trees = []treeID{cistID}
	case "MST":
		trees = []treeID{cistID, 1, 2, 3, 4}
	case "PVST":
		trees = []treeID{cistID, 10, 20, 30, 40}
	default:
		return nil
	}
	owed := make([]topologyPropertyOwed, 0, len(trees))
	for _, tree := range trees {
		owed = append(owed, topologyPropertyOwed{tree: tree, port: port})
	}

	return owed
}

func topologyPropertyMcheckOwedForMode(mode, port string) []topologyPropertyOwed {
	if mode == "MST" {
		return topologyPropertyOwedForMode(mode, port)
	}

	return []topologyPropertyOwed{{tree: cistID, port: port}}
}

func assertTopologyProperty(t *testing.T, l *Layer, effects layer.Effects, want ...topologyPropertyOwed) {
	t.Helper()
	expected := make(map[topologyPropertyOwed]struct{}, len(want))
	for _, key := range want {
		expected[key] = struct{}{}
	}
	observed := make(map[topologyPropertyOwed]struct{})
	frameOrder := make([]topologyPropertyOwed, 0, len(effects.Emissions))
	transmissions := make(map[txKey]int)
	emissionVIDs := make(map[txKey][]vlan.ID)

	for _, emission := range effects.Emissions {
		decoded, emissionTree := decodeTopologyPropertyEmission(t, l, emission)
		key := topologyPropertyOwed{tree: emissionTree, port: emission.Port}
		budget := l.txKeyFor(l.trees[emissionTree], emission.Port)
		transmissions[budget]++
		if transmissions[budget] > 1 {
			if l.pvst == nil || emissionTree != cistID || !l.links[emission.Port].sendRSTP || transmissions[budget] > 2 {
				t.Errorf("transmit budget tree %d port %s used %d times", budget.tree, budget.port, transmissions[budget])
			}
		}
		emissionVIDs[budget] = append(emissionVIDs[budget], emission.VID)

		if decoded.Type == bpdu.TypeTopologyChangeNotification {
			observed[key] = struct{}{}
			frameOrder = append(frameOrder, key)
			continue
		}

		p := l.trees[emissionTree].ports[emission.Port]
		if decoded.RootID != l.trees[emissionTree].rootID {
			t.Errorf("tree %d frame on %s names root %v, want final %v", emissionTree, emission.Port, decoded.RootID, l.trees[emissionTree].rootID)
		}
		if decoded.Type != bpdu.TypeConfiguration && decoded.Role() != p.role {
			t.Errorf("tree %d frame on %s role %v, want final %v", emissionTree, emission.Port, decoded.Role(), p.role)
		}
		if decoded.TopologyChange() {
			observed[key] = struct{}{}
			frameOrder = append(frameOrder, key)
		}
		frameHasTopologyChange := decoded.TopologyChange()
		for _, record := range decoded.MSTIs {
			recordTree := treeID(record.MSTID)
			mp := l.trees[recordTree].ports[emission.Port]
			recordFlags := bpdu.BPDU{Flags: record.Flags}
			if record.RegionalRootID != l.trees[recordTree].rootID {
				t.Errorf("MSTI %d frame on %s names root %v, want final %v", recordTree, emission.Port, record.RegionalRootID, l.trees[recordTree].rootID)
			}
			if recordFlags.Role() != mp.role {
				t.Errorf("MSTI %d frame on %s role %v, want final %v", recordTree, emission.Port, recordFlags.Role(), mp.role)
			}
			if recordFlags.TopologyChange() {
				recordKey := topologyPropertyOwed{tree: recordTree, port: emission.Port}
				observed[recordKey] = struct{}{}
				frameHasTopologyChange = true
			}
		}
		if frameHasTopologyChange && !decoded.TopologyChange() {
			frameOrder = append(frameOrder, key)
		}
	}
	for budget, vids := range emissionVIDs {
		if l.pvst == nil {
			continue
		}
		link := l.links[budget.port]
		switch {
		case budget.tree == cistID && link.sendRSTP:
			if len(vids) != 2 || vids[0] != 1 || vids[1] != 0 {
				t.Errorf("PVST VLAN 1 budget tree %d port %s used VIDs %v, want [1 0]", budget.tree, budget.port, vids)
			}
		case budget.tree == cistID:
			if len(vids) != 1 || vids[0] != 0 {
				t.Errorf("legacy PVST VLAN 1 budget tree %d port %s used VIDs %v, want [0]", budget.tree, budget.port, vids)
			}
		default:
			if len(vids) != 1 || vids[0] != vlan.ID(budget.tree) {
				t.Errorf("PVST VLAN %d budget port %s used VIDs %v, want [%d]", budget.tree, budget.port, vids, budget.tree)
			}
		}
	}

	for _, key := range want {
		if _, ok := observed[key]; ok {
			tx := l.tx(l.trees[key.tree], key.port)
			if tx.pendingAgreement || tx.pendingTCN {
				t.Errorf("tree %d Root port %s sent a flagged frame and retained a duplicate", key.tree, key.port)
			}
			continue
		}
		tx := l.tx(l.trees[key.tree], key.port)
		if tx.pendingAgreement || tx.pendingTCN {
			continue
		}
		if l.pvst != nil && key.tree != cistID && !l.links[key.port].sendRSTP {
			continue
		}
		t.Errorf("tree %d Root port %s owed a flagged frame or held transmission", key.tree, key.port)
	}

	lastTree := -1
	lastPort := ""
	for _, key := range frameOrder {
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
