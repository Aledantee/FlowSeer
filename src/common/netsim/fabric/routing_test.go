package fabric_test

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

func TestHostIPStackSendsThroughGateway(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build switch ports: %v", err)
	}

	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)
	swMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}

	swCfg := vswitch.Config{
		MAC:   swMAC,
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{
					10: "vlan10",
					20: "vlan20",
				},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &vid20, Untagged: []vlan.ID{20}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"vlan10": {
							VLAN:     10,
							MAC:      swMAC,
							Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")},
						},
						"vlan20": {
							VLAN:     20,
							MAC:      swMAC,
							Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")},
						},
					},
					Neighbors: []routing.Neighbor{
						{
							Interface: "vlan20",
							Addr:      netip.MustParseAddr("10.0.20.7"),
							MAC:       macH2,
						},
					},
				},
			},
		},
	}

	cfg := fabric.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": swCfg,
		},
		Hosts: map[string]fabric.Host{
			"h1": {
				Address: macH1,
				IP: &fabric.HostIP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
					Gateway:   netip.MustParseAddr("10.0.10.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{
						netip.MustParseAddr("10.0.10.1"): swMAC,
					},
				},
			},
			"h2": {
				Address: macH2,
			},
		},
		Cables: []fabric.Cable{
			{
				A:            fabric.Endpoint{Node: "h1"},
				B:            fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				LengthMeters: 5,
			},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:            fabric.Endpoint{Node: "h2"},
				LengthMeters: 5,
			},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	fid, err := fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1"},
		Packet: &fabric.Packet{
			To:       netip.MustParseAddr("10.0.20.7"),
			Protocol: 17,
			Payload:  []byte("ping"),
		},
	})
	if err != nil {
		t.Fatalf("Inject packet: %v", err)
	}

	fab.Run(10)

	var journey *fabric.Journey
	for _, j := range fab.Report() {
		if j.FrameID == fid {
			journey = &j
			break
		}
	}
	if journey == nil {
		t.Fatalf("journey for frame %d not found", fid)
	}

	var swHop *fabric.Entry
	for i := range journey.Entries {
		if journey.Entries[i].Kind == fabric.EntryHop && journey.Entries[i].Device == "sw1" {
			swHop = &journey.Entries[i]
			break
		}
	}
	if swHop == nil {
		t.Fatalf("hop on sw1 not found in journey entries: %+v", journey.Entries)
	}
	if swHop.Result == nil {
		t.Fatal("sw1 hop missing Result")
	}

	wantFloodSteps := []trace.Step{
		{Layer: port.LayerVlan, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
		{Layer: port.LayerRelay, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
		{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
		{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
		{Layer: port.LayerRouting, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "vlan20"}},
		{Layer: port.LayerRelay, Op: trace.OpLookup, RuleID: "unicast-miss", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:77"}},
		{Layer: port.LayerRelay, Op: trace.OpReplicate, RuleID: "flood", Subject: trace.Subject{Kind: "vlan", Key: "20"}},
		{Layer: port.LayerVlan, Op: trace.OpRewrite, RuleID: "vlan-tag-form", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
		{Layer: port.LayerRelay, Op: trace.OpTransmit, RuleID: "transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
	}
	assertSteps(t, swHop.Result.Steps, wantFloodSteps)

	if len(journey.Deliveries) != 1 {
		t.Fatalf("len(journey.Deliveries) = %d, want 1", len(journey.Deliveries))
	}
	deliv := journey.Deliveries[0]
	if deliv.Host != "h2" {
		t.Errorf("delivery host = %q, want h2", deliv.Host)
	}

	hdr, _, err := ip.Decode(deliv.Frame.Payload)
	if err != nil {
		t.Fatalf("decode delivered IP payload: %v", err)
	}
	if hdr.HopLimit != 63 {
		t.Errorf("delivered HopLimit = %d, want 63", hdr.HopLimit)
	}

	_, err = fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1"},
		Packet: &fabric.Packet{
			To:       netip.MustParseAddr("10.0.10.9"),
			Protocol: 17,
			Payload:  []byte("ping"),
		},
	})
	if err == nil {
		t.Fatal("Inject to 10.0.10.9 with no neighbor entry succeeded, want error")
	}
	if !strings.Contains(err.Error(), "10.0.10.9") {
		t.Errorf("Inject error %q does not contain address 10.0.10.9", err.Error())
	}
	// A host stack resolves no neighbors (HostRoutingConfig writes
	// NeighborDisabled into every host VRF), so this is a definite miss, not
	// a hold nothing ever wakes.
	if !strings.Contains(err.Error(), string(routing.ReasonNeighborMiss)) {
		t.Errorf("Inject error %q does not name reason %q", err.Error(), routing.ReasonNeighborMiss)
	}
}

func TestFabricMACAssignment(t *testing.T) {
	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports1, err := b1.Build()
	if err != nil {
		t.Fatalf("build ports1: %v", err)
	}

	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports2, err := b2.Build()
	if err != nil {
		t.Fatalf("build ports2: %v", err)
	}

	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)

	baseCfg := func() fabric.Config {
		return fabric.Config{
			Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Switches: map[string]vswitch.Config{
				"sw1": {
					Ports: ports1,
					STP:   &stp.Config{},
					Bridge: &bridge.Config{
						VLAN: &bridge.VLAN{
							Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
							Switchports: map[string]bridge.Switchport{
								"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
								"1/1/2": {PVID: &vid20, Untagged: []vlan.ID{20}},
							},
						},
					},
					Routing: &routing.Config{
						VRFs: map[string]routing.VRF{
							routing.DefaultVRF: {
								Interfaces: map[string]routing.Interface{
									"vlan10": {
										VLAN:     10,
										Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")},
									},
								},
							},
						},
					},
				},
				"sw2": {
					Ports: ports2,
				},
			},
			Hosts: map[string]fabric.Host{
				"h1": {
					IP: &fabric.HostIP{
						Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
						Gateway:   netip.MustParseAddr("10.0.10.1"),
						Neighbors: map[netip.Addr]netaddr.MAC{
							netip.MustParseAddr("10.0.10.1"): netaddr.Local(1),
						},
					},
				},
				"h2": {},
			},
			Cables: []fabric.Cable{
				{
					A:            fabric.Endpoint{Node: "h1"},
					B:            fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
					LengthMeters: 5,
				},
				{
					A:            fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
					B:            fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
					LengthMeters: 10,
				},
				{
					A:            fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
					B:            fabric.Endpoint{Node: "h2"},
					LengthMeters: 5,
				},
			},
		}
	}

	cfg := baseCfg()
	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	filled := fab.Config()
	wantSW1 := netaddr.Local(1)
	wantSW2 := netaddr.Local(2)
	wantH1 := netaddr.Local(3)
	wantH2 := netaddr.Local(4)

	if filled.Switches["sw1"].MAC != wantSW1 {
		t.Errorf("sw1 MAC = %v, want %v", filled.Switches["sw1"].MAC, wantSW1)
	}
	if filled.Switches["sw2"].MAC != wantSW2 {
		t.Errorf("sw2 MAC = %v, want %v", filled.Switches["sw2"].MAC, wantSW2)
	}
	if filled.Hosts["h1"].Address != wantH1 {
		t.Errorf("h1 Address = %v, want %v", filled.Hosts["h1"].Address, wantH1)
	}
	if filled.Hosts["h2"].Address != wantH2 {
		t.Errorf("h2 Address = %v, want %v", filled.Hosts["h2"].Address, wantH2)
	}

	sw1IfaceMAC := filled.Switches["sw1"].Routing.VRFs[routing.DefaultVRF].Interfaces["vlan10"].MAC
	if sw1IfaceMAC != wantSW1 {
		t.Errorf("sw1 vlan10 MAC = %v, want %v", sw1IfaceMAC, wantSW1)
	}
	if fab.Switch("sw1").BridgeID().Address != wantSW1 {
		t.Errorf("sw1 BridgeID().Address = %v, want %v", fab.Switch("sw1").BridgeID().Address, wantSW1)
	}

	fid, err := fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1"},
		Packet: &fabric.Packet{
			To:       netip.MustParseAddr("10.0.10.1"),
			Protocol: 17,
			Payload:  []byte("probe"),
		},
	})
	if err != nil {
		t.Fatalf("Inject packet from h1: %v", err)
	}

	fab.Run(5)

	var journey *fabric.Journey
	for _, j := range fab.Report() {
		if j.FrameID == fid {
			journey = &j
			break
		}
	}
	if journey == nil {
		t.Fatalf("journey for packet of 44 not found")
	}
	var reachedSW1 bool
	for _, e := range journey.Entries {
		if e.Kind == fabric.EntryHop && e.Device == "sw1" {
			reachedSW1 = true
			break
		}
	}
	if !reachedSW1 {
		t.Errorf("packet from h1 did not reach sw1 interface: %+v", journey.Entries)
	}

	cfgShift := baseCfg()
	h2Shift := cfgShift.Hosts["h2"]
	h2Shift.Address = netaddr.Local(1)
	cfgShift.Hosts["h2"] = h2Shift

	fabShift, err := fabric.New(statedPhysical(cfgShift))
	if err != nil {
		t.Fatalf("fabric.New with explicit h2 MAC: %v", err)
	}
	filledShift := fabShift.Config()
	if filledShift.Switches["sw1"].MAC != netaddr.Local(2) {
		t.Errorf("shifted sw1 MAC = %v, want %v", filledShift.Switches["sw1"].MAC, netaddr.Local(2))
	}
	if filledShift.Switches["sw2"].MAC != netaddr.Local(3) {
		t.Errorf("shifted sw2 MAC = %v, want %v", filledShift.Switches["sw2"].MAC, netaddr.Local(3))
	}
	if filledShift.Hosts["h1"].Address != netaddr.Local(4) {
		t.Errorf("shifted h1 Address = %v, want %v", filledShift.Hosts["h1"].Address, netaddr.Local(4))
	}
	if filledShift.Hosts["h2"].Address != netaddr.Local(1) {
		t.Errorf("explicit h2 Address = %v, want %v", filledShift.Hosts["h2"].Address, netaddr.Local(1))
	}

	fabDerived, err := fabric.Derive(fab, constructionSpec(statedPhysical(baseCfg())))
	if err != nil {
		t.Fatalf("fabric.Derive: %v", err)
	}
	filledDerived := fabDerived.Config()
	if filledDerived.Switches["sw1"].MAC != wantSW1 {
		t.Errorf("derived sw1 MAC = %v, want %v", filledDerived.Switches["sw1"].MAC, wantSW1)
	}
	if filledDerived.Switches["sw2"].MAC != wantSW2 {
		t.Errorf("derived sw2 MAC = %v, want %v", filledDerived.Switches["sw2"].MAC, wantSW2)
	}
	if filledDerived.Hosts["h1"].Address != wantH1 {
		t.Errorf("derived h1 Address = %v, want %v", filledDerived.Hosts["h1"].Address, wantH1)
	}
	if filledDerived.Hosts["h2"].Address != wantH2 {
		t.Errorf("derived h2 Address = %v, want %v", filledDerived.Hosts["h2"].Address, wantH2)
	}

	cfgDupHosts := baseCfg()
	dupH1 := cfgDupHosts.Hosts["h1"]
	dupH1.Address = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	cfgDupHosts.Hosts["h1"] = dupH1
	dupH2 := cfgDupHosts.Hosts["h2"]
	dupH2.Address = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	cfgDupHosts.Hosts["h2"] = dupH2

	if err := cfgDupHosts.Validate(); err == nil {
		t.Fatal("Validate on duplicate host MACs succeeded, want error")
	}

	cfgHostSWColl := baseCfg()
	collSW := cfgHostSWColl.Switches["sw1"]
	collSW.MAC = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}
	cfgHostSWColl.Switches["sw1"] = collSW
	collH := cfgHostSWColl.Hosts["h1"]
	collH.Address = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}
	cfgHostSWColl.Hosts["h1"] = collH

	if err := cfgHostSWColl.Validate(); err == nil {
		t.Fatal("Validate on host colliding with switch base MAC succeeded, want error")
	}
}

func TestHostOnRoutedPortReachesHostOnVLAN(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	vid20 := vlan.ID(20)
	swMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x22}

	swCfg := vswitch.Config{
		MAC:   swMAC,
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/2": {PVID: &vid20, Untagged: []vlan.ID{20}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"1/1/1": {
							Port:     "1/1/1",
							MAC:      swMAC,
							Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")},
						},
						"vlan20": {
							VLAN:     20,
							MAC:      swMAC,
							Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")},
						},
					},
					Neighbors: []routing.Neighbor{
						{
							Interface: "vlan20",
							Addr:      netip.MustParseAddr("10.0.20.7"),
							MAC:       macH2,
						},
					},
				},
			},
		},
	}

	cfg := fabric.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": swCfg,
		},
		Hosts: map[string]fabric.Host{
			"h1": {
				Address: macH1,
				IP: &fabric.HostIP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.50.7/24")},
					Gateway:   netip.MustParseAddr("10.0.50.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{
						netip.MustParseAddr("10.0.50.1"): swMAC,
					},
				},
			},
			"h2": {
				Address: macH2,
			},
		},
		Cables: []fabric.Cable{
			{
				A:            fabric.Endpoint{Node: "h1"},
				B:            fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				LengthMeters: 5,
			},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:            fabric.Endpoint{Node: "h2"},
				LengthMeters: 5,
			},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	fid, err := fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1"},
		Packet: &fabric.Packet{
			To:       netip.MustParseAddr("10.0.20.7"),
			Protocol: 17,
			Payload:  []byte("data-through-router"),
		},
	})
	if err != nil {
		t.Fatalf("Inject from routed port host: %v", err)
	}

	fab.Run(10)

	var journey *fabric.Journey
	for _, j := range fab.Report() {
		if j.FrameID == fid {
			journey = &j
			break
		}
	}
	if journey == nil {
		t.Fatalf("journey for frame %d not found", fid)
	}

	if len(journey.Deliveries) != 1 {
		t.Fatalf("len(journey.Deliveries) = %d, want 1", len(journey.Deliveries))
	}
	deliv := journey.Deliveries[0]
	if deliv.Host != "h2" {
		t.Errorf("delivery host = %q, want h2", deliv.Host)
	}

	hdr, payload, err := ip.Decode(deliv.Frame.Payload)
	if err != nil {
		t.Fatalf("decode delivered payload: %v", err)
	}
	if hdr.HopLimit != 63 {
		t.Errorf("delivered HopLimit = %d, want 63", hdr.HopLimit)
	}
	if string(payload) != "data-through-router" {
		t.Errorf("delivered payload = %q, want %q", string(payload), "data-through-router")
	}
}

func assertSteps(t *testing.T, got []trace.Step, want []trace.Step) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("step count mismatch: got %d, want %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i].Layer != want[i].Layer || got[i].Op != want[i].Op || got[i].RuleID != want[i].RuleID || got[i].Subject != want[i].Subject {
			t.Errorf("step %d mismatch:\ngot:  %+v\nwant: %+v", i, got[i], want[i])
		}
	}
}

// TestDeriveDoesNotCarryAnAddressTheNewConfigurationClaims is evidence that
// an assignment carried over from the current fabric yields to an explicit
// address in the new configuration, so no two nodes share one.
func TestDeriveDoesNotCarryAnAddressTheNewConfigurationClaims(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}
	cfgFor := func(h1 netaddr.MAC) fabric.Config {
		return fabric.Config{
			Start:    time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Switches: map[string]vswitch.Config{"sw1": {Ports: ports}},
			Hosts:    map[string]fabric.Host{"h1": {Address: h1}},
			Cables: []fabric.Cable{{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			}},
		}
	}

	cur, err := fabric.New(statedPhysical(cfgFor(netaddr.MAC{})))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}
	if got := cur.Config().Switches["sw1"].MAC; got != netaddr.Local(1) {
		t.Fatalf("sw1 = %s, want %s", got, netaddr.Local(1))
	}

	next, err := fabric.Derive(cur, constructionSpec(statedPhysical(cfgFor(netaddr.Local(1)))))
	if err != nil {
		t.Fatalf("fabric.Derive: %v", err)
	}
	sw1 := next.Config().Switches["sw1"].MAC
	h1 := next.Config().Hosts["h1"].Address
	if h1 != netaddr.Local(1) {
		t.Errorf("h1 = %s, want the explicit %s", h1, netaddr.Local(1))
	}
	if sw1 == h1 {
		t.Errorf("sw1 and h1 both carry %s", sw1)
	}
	if sw1 != netaddr.Local(2) {
		t.Errorf("sw1 = %s, want the next free %s", sw1, netaddr.Local(2))
	}
}

// TestFabricReleasesHeldFrameOnObservedARPReply covers R21a and R21b across
// two hosts and a switch: h1's packet to h2 holds at the switch because
// nothing has been observed for h2's address, an ARP reply injected directly
// at the switch's port to h2 moves the entry, and the switch releases the
// held frame without anything else nudging it. The released frame travels as
// its own injected journey (linking it back to the journey that held it is a
// later phase's job), so the switch's own release must itself carry the
// frame all the way to a delivery and must not be marked Protocol: the
// released frame is h1's ordinary data, not a frame of the switch's own.
func TestFabricReleasesHeldFrameOnObservedARPReply(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build switch ports: %v", err)
	}

	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)
	swMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	addrH2 := netip.MustParseAddr("10.0.20.7")

	swCfg := vswitch.Config{
		MAC:   swMAC,
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &vid20, Untagged: []vlan.ID{20}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					// No neighbor configured for h2: the switch has never
					// been told, and observes it below instead.
				},
			},
		},
	}

	cfg := fabric.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": swCfg,
		},
		Hosts: map[string]fabric.Host{
			"h1": {
				Address: macH1,
				IP: &fabric.HostIP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
					Gateway:   netip.MustParseAddr("10.0.10.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{
						netip.MustParseAddr("10.0.10.1"): swMAC,
					},
				},
			},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, LengthMeters: 5},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}, LengthMeters: 5},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	heldID, err := fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1"},
		Packet: &fabric.Packet{
			To:       addrH2,
			Protocol: 17,
			Payload:  []byte("ping"),
		},
	})
	if err != nil {
		t.Fatalf("Inject packet: %v", err)
	}

	msg := arp.Message{
		HardwareType: 1,
		ProtocolType: 0x0800,
		Operation:    arp.Reply,
		SenderMAC:    macH2,
		SenderAddr:   addrH2,
		TargetMAC:    swMAC,
		TargetAddr:   netip.MustParseAddr("10.0.20.1"),
	}
	replyFrame, err := arp.Encode(msg, swMAC)
	if err != nil {
		t.Fatalf("encode ARP reply: %v", err)
	}

	replyID, err := fab.Inject(fabric.Injection{
		At:     cfg.Start.Add(time.Second),
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
		Frame:  replyFrame,
	})
	if err != nil {
		t.Fatalf("Inject ARP reply: %v", err)
	}

	fab.Run(20)

	heldJourney := findJourney(t, fab, heldID)
	if len(heldJourney.Entries) == 0 {
		t.Fatalf("held journey has no entries")
	}
	if last := heldJourney.Entries[len(heldJourney.Entries)-1]; last.Result == nil || last.Result.Outcome != trace.Held {
		t.Fatalf("held journey's last entry = %+v, want outcome Held", last)
	}

	// The released frame is a new injected journey at the switch, distinct
	// from both the held journey and the ARP reply's own: linking it back to
	// the journey it was held from belongs to a later phase.
	var released *fabric.Journey
	for _, j := range fab.Report() {
		if j.FrameID == heldID || j.FrameID == replyID {
			continue
		}
		if len(j.Deliveries) == 1 && j.Deliveries[0].Host == "h2" {
			jj := j
			released = &jj
			break
		}
	}
	if released == nil {
		t.Fatalf("no released journey delivered to h2 among: %+v", fab.Report())
	}
	if released.Protocol {
		t.Errorf("released journey Protocol = true, want false: it carries h1's ordinary data, not a frame of the switch's own")
	}

	hdr, _, err := ip.Decode(released.Deliveries[0].Frame.Payload)
	if err != nil {
		t.Fatalf("decode delivered IP payload: %v", err)
	}
	if hdr.Src != netip.MustParseAddr("10.0.10.7") || hdr.Dst != addrH2 {
		t.Errorf("delivered src/dst = %s/%s, want 10.0.10.7/%s", hdr.Src, hdr.Dst, addrH2)
	}
}

// TestFabricRecordsNeighborFailureOnHeldFrameTimeout is evidence that the
// fabric drains [vswitch.Switch.DrainNeighborFailures] alongside
// [vswitch.Switch.Drain]: a held frame whose neighbor never resolves must
// leave a drop entry and a discard counter behind, not vanish after a held
// journey entry with nothing following it.
func TestFabricRecordsNeighborFailureOnHeldFrameTimeout(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build switch ports: %v", err)
	}

	swMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}

	swCfg := vswitch.Config{
		MAC:   swMAC,
		Ports: ports,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"1/1/1": {
							Port:     "1/1/1",
							MAC:      swMAC,
							Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")},
						},
						"1/1/2": {
							Port:     "1/1/2",
							MAC:      swMAC,
							Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.60.1/24")},
						},
					},
					// No neighbor configured for 10.0.60.77 on 1/1/2: the
					// switch holds and never resolves it.
				},
			},
		},
	}

	cfg := fabric.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": swCfg,
		},
		Hosts: map[string]fabric.Host{
			"h1": {
				Address: macH1,
				IP: &fabric.HostIP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.50.7/24")},
					Gateway:   netip.MustParseAddr("10.0.50.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{
						netip.MustParseAddr("10.0.50.1"): swMAC,
					},
				},
			},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, LengthMeters: 5},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	heldID, err := fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1"},
		Packet: &fabric.Packet{
			To:       netip.MustParseAddr("10.0.60.77"),
			Protocol: 17,
			Payload:  []byte("ping"),
		},
	})
	if err != nil {
		t.Fatalf("Inject packet: %v", err)
	}

	fab.Run(50)

	heldJourney := findJourney(t, fab, heldID)
	if len(heldJourney.Entries) == 0 {
		t.Fatalf("held journey has no entries")
	}
	if last := heldJourney.Entries[len(heldJourney.Entries)-1]; last.Result == nil || last.Result.Outcome != trace.Held {
		t.Fatalf("held journey's last entry = %+v, want outcome Held", last)
	}

	var dropEntry *fabric.Entry
	for _, j := range fab.Report() {
		if j.FrameID == heldID {
			continue
		}
		for _, e := range j.Entries {
			if e.Kind == fabric.EntryDrop && e.Reason == routing.ReasonNeighborMiss {
				ee := e
				dropEntry = &ee
				break
			}
		}
	}
	if dropEntry == nil {
		t.Fatalf("no neighbor-miss drop entry found among: %+v", fab.Report())
	}
	if dropEntry.Device != "sw1" {
		t.Errorf("drop entry device = %q, want sw1", dropEntry.Device)
	}

	snap := fab.Snapshot()
	dev, ok := snap.Devices["sw1"]
	if !ok {
		t.Fatalf("no device sw1 in snapshot")
	}
	counters, ok := dev.Counters["1/1/2"]
	if !ok {
		t.Fatalf("no counters for port 1/1/2")
	}
	if got := counters.Discards[routing.ReasonNeighborMiss]; got != 1 {
		t.Errorf("Discards[neighbor-miss] = %d, want 1", got)
	}
}
