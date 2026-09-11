package fabric_test

import (
	"net/netip"
	"strings"
	"testing"
	"time"

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

	fab, err := fabric.New(cfg)
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
		{Layer: port.LayerVlan, Op: trace.OpClassify, Detail: "vlan 10"},
		{Layer: port.LayerRelay, Op: trace.OpLearn, Detail: "00:11:22:33:44:11 -> 1/1/1"},
		{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf default interface vlan10"},
		{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "10.0.20.0/24 connected vlan20"},
		{Layer: port.LayerRouting, Op: trace.OpRewrite, Detail: "hop limit 64 to 63, src 00:00:5e:00:01:01, dst 00:11:22:33:44:77"},
		{Layer: port.LayerRelay, Op: trace.OpLookup, Detail: "unicast miss"},
		{Layer: port.LayerRelay, Op: trace.OpReplicate, Detail: "1 candidate ports"},
		{Layer: port.LayerVlan, Op: trace.OpRewrite, Detail: "port 1/1/2 egress tag form"},
		{Layer: port.LayerRelay, Op: trace.OpTransmit, Detail: "port 1/1/2"},
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
	fab, err := fabric.New(cfg)
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

	fabShift, err := fabric.New(cfgShift)
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

	fabDerived, err := fabric.Derive(fab, baseCfg())
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

	fab, err := fabric.New(cfg)
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
		if got[i].Layer != want[i].Layer || got[i].Op != want[i].Op || got[i].Detail != want[i].Detail {
			t.Errorf("step %d mismatch:\ngot:  %+v\nwant: %+v", i, got[i], want[i])
		}
	}
}
