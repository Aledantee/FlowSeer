package fabric_test

import (
	"testing"
	"time"

	"buf.build/go/protovalidate"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func TestPortCountersForwardingAndEgressDropExport(t *testing.T) {
	fab, macH1, macH2 := newTwoSwitchTopology(t, fabric.Fault{Kind: fabric.FaultNone})

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	payload := []byte("ping-unicast")
	frame := ethernet.Frame{
		Src:     macH1,
		Dst:     macH2,
		Payload: payload,
	}

	inj := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  frame,
	}
	if _, err := fab.Inject(inj); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	steps := fab.Run(10)
	if steps != 2 {
		t.Fatalf("steps = %d, want 2", steps)
	}

	snap := fab.Snapshot()
	c1, ok := snap.Devices["sw1"].Counters["1/1/1"]
	if !ok {
		t.Fatal("sw1:1/1/1 counters missing in snapshot")
	}
	if got, want := c1.InUnicast, uint64(1); got != want {
		t.Errorf("sw1:1/1/1 InUnicast = %d, want %d", got, want)
	}

	exp1 := netmodel.InterfaceCounters(c1)
	if got, want := exp1.GetInUnicastPackets(), uint64(1); got != want {
		t.Errorf("sw1:1/1/1 GetInUnicastPackets() = %d, want %d", got, want)
	}
	if err := protovalidate.Validate(exp1); err != nil {
		t.Errorf("protovalidate.Validate(sw1:1/1/1) failed: %v", err)
	}

	c24, ok := snap.Devices["sw1"].Counters["1/1/24"]
	if !ok {
		t.Fatal("sw1:1/1/24 counters missing in snapshot")
	}
	if got, want := c24.OutUnicast, uint64(1); got != want {
		t.Errorf("sw1:1/1/24 OutUnicast = %d, want %d", got, want)
	}

	exp24 := netmodel.InterfaceCounters(c24)
	if got, want := exp24.GetOutUnicastPackets(), uint64(1); got != want {
		t.Errorf("sw1:1/1/24 GetOutUnicastPackets() = %d, want %d", got, want)
	}
	if err := protovalidate.Validate(exp24); err != nil {
		t.Errorf("protovalidate.Validate(sw1:1/1/24) failed: %v", err)
	}

	// Now test that a drop at egress counts in OutDiscards.
	bDrop := port.NewBuilder()
	bDrop.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	bDrop.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 50})
	pTableDrop, err := bDrop.Build()
	if err != nil {
		t.Fatalf("build drop ports: %v", err)
	}

	vid10 := vlan.ID(10)
	cfgDrop := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: pTableDrop,
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "VLAN10"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
							"1/1/24": {Tagged: []vlan.ID{10}},
						},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				A: fabric.Endpoint{Node: "sw1", Port: "1/1/24"},
				B: fabric.Endpoint{Node: "h2"},
			},
		},
	}

	fabDrop, err := fabric.New(cfgDrop)
	if err != nil {
		t.Fatalf("New drop fabric: %v", err)
	}

	oversized := ethernet.Frame{
		Src:     macH1,
		Dst:     macH2,
		Payload: make([]byte, 100),
	}
	if _, err := fabDrop.Inject(fabric.Injection{At: t0, Origin: fabric.Endpoint{Node: "h1"}, Frame: oversized}); err != nil {
		t.Fatalf("Inject oversized: %v", err)
	}

	fabDrop.Run(5)

	snapDrop := fabDrop.Snapshot()
	cEgressDrop := snapDrop.Devices["sw1"].Counters["1/1/24"]
	if got, want := cEgressDrop.OutDiscards, uint64(1); got != want {
		t.Errorf("sw1:1/1/24 OutDiscards = %d, want %d", got, want)
	}
	if got, want := cEgressDrop.Discards[bridge.ReasonMTUExceeded], uint64(1); got != want {
		t.Errorf("sw1:1/1/24 Discards[ReasonMTUExceeded] = %d, want %d", got, want)
	}

	expDrop := netmodel.InterfaceCounters(cEgressDrop)
	if got, want := expDrop.GetOutDiscards(), uint64(1); got != want {
		t.Errorf("exported OutDiscards = %d, want %d", got, want)
	}
	if err := protovalidate.Validate(expDrop); err != nil {
		t.Errorf("protovalidate.Validate(expDrop) failed: %v", err)
	}
}

func TestStormGrowthAcrossSimulationRuns(t *testing.T) {
	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable1, err := b1.Build()
	if err != nil {
		t.Fatalf("build sw1: %v", err)
	}

	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable2, err := b2.Build()
	if err != nil {
		t.Fatalf("build sw2: %v", err)
	}

	vid10 := vlan.ID(10)
	newBridgeCfg := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "VLAN10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/2": {Tagged: []vlan.ID{10}},
					"1/1/3": {Tagged: []vlan.ID{10}},
				},
			},
		}
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macUnknown := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}

	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: pTable1, Bridge: newBridgeCfg()},
			"sw2": {Ports: pTable2, Bridge: newBridgeCfg()},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
				LengthMeters: 100,
			},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/3"},
				LengthMeters: 200,
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	inj := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Src:     macH1,
			Dst:     macUnknown,
			Payload: []byte("storm-probe"),
		},
	}
	if _, err := fab.Inject(inj); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	steps1 := fab.Run(5)
	if steps1 != 5 {
		t.Fatalf("steps1 = %d, want 5", steps1)
	}

	snap1 := fab.Snapshot()
	c1Sw1Port2 := snap1.Devices["sw1"].Counters["1/1/2"]
	c1Sw2Port2 := snap1.Devices["sw2"].Counters["1/1/2"]

	octets1 := c1Sw1Port2.InOctets + c1Sw1Port2.OutOctets + c1Sw2Port2.InOctets + c1Sw2Port2.OutOctets
	if octets1 == 0 {
		t.Fatal("expected non-zero traffic on uplinks after first run")
	}

	steps2 := fab.Run(5)
	if steps2 != 5 {
		t.Fatalf("steps2 = %d, want 5", steps2)
	}

	snap2 := fab.Snapshot()
	c2Sw1Port2 := snap2.Devices["sw1"].Counters["1/1/2"]
	c2Sw2Port2 := snap2.Devices["sw2"].Counters["1/1/2"]

	octets2 := c2Sw1Port2.InOctets + c2Sw1Port2.OutOctets + c2Sw2Port2.InOctets + c2Sw2Port2.OutOctets
	if octets2 <= octets1 {
		t.Errorf("traffic did not grow across runs: octets1=%d octets2=%d", octets1, octets2)
	}

	// Verify snapshot 1 remained untouched by the subsequent run.
	c1Sw1Port2After := snap1.Devices["sw1"].Counters["1/1/2"]
	if c1Sw1Port2After.InOctets != c1Sw1Port2.InOctets || c1Sw1Port2After.OutOctets != c1Sw1Port2.OutOctets {
		t.Errorf("snapshot 1 was mutated by second run")
	}
}

func TestDeadDirectionCableLossAndDelivery(t *testing.T) {
	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable1, err := b1.Build()
	if err != nil {
		t.Fatalf("build sw1: %v", err)
	}

	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable2, err := b2.Build()
	if err != nil {
		t.Fatalf("build sw2: %v", err)
	}

	vid10 := vlan.ID(10)
	newBridgeCfg := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "VLAN10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/24": {Tagged: []vlan.ID{10}},
				},
			},
		}
	}

	forcedPhy := func() *phy.Config {
		return &phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/24": {
					SupportedSpeedsBPS: []uint64{1_000_000_000},
					Setting: &phy.Setting{
						SpeedBPS:        1_000_000_000,
						AutoNegotiation: false,
					},
				},
			},
		}
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: pTable1, Bridge: newBridgeCfg(), Phy: forcedPhy()},
			"sw2": {Ports: pTable2, Bridge: newBridgeCfg(), Phy: forcedPhy()},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/24"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/24"},
				LengthMeters: 100,
				Fault:        fabric.Fault{Kind: fabric.FaultDeadAToB},
			},
			{
				A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				B: fabric.Endpoint{Node: "h2"},
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	// Two forced ends remain Oper Up even with DeadAToB.
	links := fab.Links()
	var trunkLink *fabric.Link
	for i := range links {
		if links[i].Cable.A.Node == "sw1" && links[i].Cable.B.Node == "sw2" {
			trunkLink = &links[i]
			break
		}
	}
	if trunkLink == nil {
		t.Fatal("trunk link not found")
	}
	if trunkLink.A.Oper != port.Up || trunkLink.B.Oper != port.Up {
		t.Fatalf("trunk oper status A=%v B=%v, want both Up", trunkLink.A.Oper, trunkLink.B.Oper)
	}

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	// Direction A to B (sw1 -> sw2): should be lost at cable with ReasonCableLoss.
	fid1, err := fab.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Src: macH1, Dst: macH2, Payload: []byte("ping-a-to-b")},
	})
	if err != nil {
		t.Fatalf("Inject A to B: %v", err)
	}

	fab.Run(10)

	journeys := fab.Report()
	var j1 *fabric.Journey
	for i := range journeys {
		if journeys[i].FrameID == fid1 {
			j1 = &journeys[i]
			break
		}
	}
	if j1 == nil {
		t.Fatal("journey 1 not found")
	}
	if len(j1.Deliveries) != 0 {
		t.Errorf("frame A to B delivered %d times, want 0", len(j1.Deliveries))
	}

	foundLoss := false
	for _, entry := range j1.Entries {
		if entry.Kind == fabric.EntryLoss && entry.Reason == fabric.ReasonCableLoss {
			foundLoss = true
			break
		}
	}
	if !foundLoss {
		t.Errorf("frame A to B missing EntryLoss with ReasonCableLoss, entries: %+v", j1.Entries)
	}

	// Direction B to A (sw2 -> sw1): cable is not dead in reverse, should deliver to h1.
	t1 := t0.Add(time.Second)
	fid2, err := fab.Inject(fabric.Injection{
		At:     t1,
		Origin: fabric.Endpoint{Node: "h2"},
		Frame:  ethernet.Frame{Src: macH2, Dst: macH1, Payload: []byte("ping-b-to-a")},
	})
	if err != nil {
		t.Fatalf("Inject B to A: %v", err)
	}

	fab.Run(10)

	journeys = fab.Report()
	var j2 *fabric.Journey
	for i := range journeys {
		if journeys[i].FrameID == fid2 {
			j2 = &journeys[i]
			break
		}
	}
	if j2 == nil {
		t.Fatal("journey 2 not found")
	}
	if len(j2.Deliveries) != 1 {
		t.Fatalf("frame B to A delivered %d times, want 1", len(j2.Deliveries))
	}
	if j2.Deliveries[0].Host != "h1" {
		t.Errorf("delivery host = %q, want h1", j2.Deliveries[0].Host)
	}
}

func TestFrameClassesAndCorruptArrivalCounters(t *testing.T) {
	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable, err := b1.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	vid10 := vlan.ID(10)
	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: pTable,
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "VLAN10"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
							"1/1/2": {Tagged: []vlan.ID{10}},
						},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	macSrc := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macBcast := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	macMcast := netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x01}

	// 1. Broadcast frame
	bcastFrame := ethernet.Frame{Src: macSrc, Dst: macBcast, Payload: []byte("bcast")}
	rawBcast, _ := bcastFrame.Encode()
	if _, err := fab.Inject(fabric.Injection{At: t0, Origin: fabric.Endpoint{Node: "h1"}, Frame: bcastFrame}); err != nil {
		t.Fatalf("Inject bcast: %v", err)
	}
	fab.Run(5)

	snapBcast := fab.Snapshot()
	cBcast := snapBcast.Devices["sw1"].Counters["1/1/1"]
	if got, want := cBcast.InBroadcast, uint64(1); got != want {
		t.Errorf("InBroadcast = %d, want %d", got, want)
	}
	if got, want := cBcast.InOctets, uint64(len(rawBcast)); got != want {
		t.Errorf("InOctets = %d, want %d", got, want)
	}

	// 2. Multicast frame
	mcastFrame := ethernet.Frame{Src: macSrc, Dst: macMcast, Payload: []byte("mcast")}
	rawMcast, _ := mcastFrame.Encode()
	if _, err := fab.Inject(fabric.Injection{At: t0.Add(time.Second), Origin: fabric.Endpoint{Node: "h1"}, Frame: mcastFrame}); err != nil {
		t.Fatalf("Inject mcast: %v", err)
	}
	fab.Run(5)

	snapMcast := fab.Snapshot()
	cMcast := snapMcast.Devices["sw1"].Counters["1/1/1"]
	if got, want := cMcast.InMulticast, uint64(1); got != want {
		t.Errorf("InMulticast = %d, want %d", got, want)
	}
	if got, want := cMcast.InOctets, uint64(len(rawBcast)+len(rawMcast)); got != want {
		t.Errorf("InOctets = %d, want %d", got, want)
	}

	// 3. Corrupt arrival across a cable with FaultCorruptEveryNth: 1.
	// Connect two switches so the cable crossing applies the fault.
	cfgCorrupt := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: pTable, Bridge: &bridge.Config{VLAN: &bridge.VLAN{Table: map[vlan.ID]string{10: "VLAN10"}, Switchports: map[string]bridge.Switchport{"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}}, "1/1/2": {Tagged: []vlan.ID{10}}}}}},
			"sw2": {Ports: pTable, Bridge: &bridge.Config{VLAN: &bridge.VLAN{Table: map[vlan.ID]string{10: "VLAN10"}, Switchports: map[string]bridge.Switchport{"1/1/2": {Tagged: []vlan.ID{10}}}}}},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macSrc},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}, LengthMeters: 100, Fault: fabric.Fault{Kind: fabric.FaultCorruptEveryNth, N: 1}},
		},
	}
	fabCorrupt, err := fabric.New(cfgCorrupt)
	if err != nil {
		t.Fatalf("New corrupt fabric: %v", err)
	}

	if _, err := fabCorrupt.Inject(fabric.Injection{At: t0, Origin: fabric.Endpoint{Node: "h1"}, Frame: bcastFrame}); err != nil {
		t.Fatalf("Inject into corrupt fab: %v", err)
	}
	fabCorrupt.Run(5)

	snapCorrupt := fabCorrupt.Snapshot()
	cSw2Port2 := snapCorrupt.Devices["sw2"].Counters["1/1/2"]
	if got, want := cSw2Port2.InErrors, uint64(1); got != want {
		t.Errorf("sw2:1/1/2 InErrors = %d, want %d", got, want)
	}
	if got, want := cSw2Port2.InDiscards, uint64(1); got != want {
		t.Errorf("sw2:1/1/2 InDiscards = %d, want %d", got, want)
	}
	if got, want := cSw2Port2.Discards[fabric.ReasonBadFrame], uint64(1); got != want {
		t.Errorf("sw2:1/1/2 Discards[ReasonBadFrame] = %d, want %d", got, want)
	}
	if cSw2Port2.InBroadcast != 0 || cSw2Port2.InMulticast != 0 || cSw2Port2.InUnicast != 0 {
		t.Errorf("corrupt arrival classified as frame class: bcast=%d mcast=%d ucast=%d", cSw2Port2.InBroadcast, cSw2Port2.InMulticast, cSw2Port2.InUnicast)
	}
}

func TestLagPortAndMemberCounters(t *testing.T) {
	lagParent := "lag1"
	b := port.NewBuilder()
	b.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/5", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: lagParent})
	b.Add(port.Port{Name: "1/1/6", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: lagParent})
	pTable, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	vid10 := vlan.ID(10)
	brCfg := &bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "VLAN10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
				"lag1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
			},
		},
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: pTable, Bridge: brCfg},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/5"}, B: fabric.Endpoint{Node: "h2"}},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	frame := ethernet.Frame{
		Src:     macH1,
		Dst:     macH2,
		Payload: []byte("lag-test"),
	}

	if _, err := fab.Inject(fabric.Injection{At: t0, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	fab.Run(5)

	snap := fab.Snapshot()
	cLag := snap.Devices["sw1"].Counters["lag1"]
	cMem := snap.Devices["sw1"].Counters["1/1/5"]

	if got, want := cLag.OutUnicast, uint64(1); got != want {
		t.Errorf("lag1 OutUnicast = %d, want %d", got, want)
	}
	if got, want := cMem.OutUnicast, uint64(1); got != want {
		t.Errorf("member 1/1/5 OutUnicast = %d, want %d", got, want)
	}
	if cLag.OutOctets == 0 || cMem.OutOctets == 0 {
		t.Errorf("expected non-zero OutOctets: lag1=%d 1/1/5=%d", cLag.OutOctets, cMem.OutOctets)
	}
	if cLag.OutOctets != cMem.OutOctets {
		t.Errorf("expected equal OutOctets: lag1=%d 1/1/5=%d", cLag.OutOctets, cMem.OutOctets)
	}

	// Reverse traffic ingresses on LAG member 1/1/5 towards 1/1/1.
	reverseFrame := ethernet.Frame{
		Src:     macH2,
		Dst:     macH1,
		Payload: []byte("lag-reverse"),
	}
	if _, err := fab.Inject(fabric.Injection{At: t0.Add(time.Second), Origin: fabric.Endpoint{Node: "h2"}, Frame: reverseFrame}); err != nil {
		t.Fatalf("Inject reverse: %v", err)
	}

	fab.Run(5)

	snapReverse := fab.Snapshot()
	cLagRev := snapReverse.Devices["sw1"].Counters["lag1"]
	cMemRev := snapReverse.Devices["sw1"].Counters["1/1/5"]
	cP1Rev := snapReverse.Devices["sw1"].Counters["1/1/1"]

	if got, want := cLagRev.InUnicast, uint64(1); got != want {
		t.Errorf("lag1 InUnicast = %d, want %d", got, want)
	}
	if got, want := cMemRev.InUnicast, uint64(1); got != want {
		t.Errorf("member 1/1/5 InUnicast = %d, want %d", got, want)
	}
	if got, want := cP1Rev.OutUnicast, uint64(1); got != want {
		t.Errorf("1/1/1 OutUnicast = %d, want %d", got, want)
	}
}

func TestWholeFrameDropCounters(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: pTable,
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "VLAN10"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {Admission: bridge.TaggedOnly, Tagged: []vlan.ID{10}},
							"1/1/2": {Tagged: []vlan.ID{10}},
						},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	untagged := ethernet.Frame{
		Src:     netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		Dst:     netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		Payload: []byte("rejected-admission"),
	}

	if _, err := fab.Inject(fabric.Injection{At: t0, Origin: fabric.Endpoint{Node: "h1"}, Frame: untagged}); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	fab.Run(5)

	snap := fab.Snapshot()
	c := snap.Devices["sw1"].Counters["1/1/1"]

	if got, want := c.InDiscards, uint64(1); got != want {
		t.Errorf("InDiscards = %d, want %d", got, want)
	}
	if got, want := c.Discards[bridge.ReasonAdmission], uint64(1); got != want {
		t.Errorf("Discards[ReasonAdmission] = %d, want %d", got, want)
	}
	if got, want := c.InUnicast, uint64(1); got != want {
		t.Errorf("InUnicast = %d, want %d", got, want)
	}
}
