package fabric_test

import (
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestSwitchResolvesHostWithoutStaticNeighbor(t *testing.T) {
	var cases int
	for _, family := range []string{"IPv4", "IPv6"} {
		t.Run(family, func(t *testing.T) {
			srcPrefix, dstPrefix := netip.MustParsePrefix("192.0.2.1/24"), netip.MustParsePrefix("198.51.100.1/24")
			hostPrefix := netip.MustParsePrefix("198.51.100.10/24")
			src := netip.MustParseAddr("192.0.2.10")
			etherType := ethernet.EtherTypeIPv4
			if family == "IPv6" {
				srcPrefix, dstPrefix = netip.MustParsePrefix("2001:db8:1::1/64"), netip.MustParsePrefix("2001:db8:2::1/64")
				hostPrefix, src = netip.MustParsePrefix("2001:db8:2::10/64"), netip.MustParseAddr("2001:db8:1::10")
				etherType = ethernet.EtherTypeIPv6
			}
			swMAC, hostMAC := netaddr.MAC{2, 0, 0, 0, 0, 1}, netaddr.MAC{2, 0, 0, 0, 0, 10}
			cfg := assembly.Config{
				Start: fixedTime,
				Switches: map[string]vswitch.Config{"sw": {MAC: swMAC, Ports: mustTable(t, port.NewBuilder().Range("p%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})), Routing: &routing.Config{VRFs: map[string]routing.VRF{routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{"source": {Port: "p1", MAC: swMAC, Prefixes: []netip.Prefix{srcPrefix}}, "target": {Port: "p2", MAC: swMAC, Prefixes: []netip.Prefix{dstPrefix}}},
				}}}}},
				Hosts:  map[string]hostmodel.Config{"source": {Address: netaddr.MAC{2, 0, 0, 0, 0, 2}}, "target": {Address: hostMAC, IP: &hostmodel.IP{Addresses: []netip.Prefix{hostPrefix}}}},
				Cables: []fabric.Cable{{A: fabric.Endpoint{Node: "source"}, B: fabric.Endpoint{Node: "sw", Port: "p1"}}, {A: fabric.Endpoint{Node: "sw", Port: "p2"}, B: fabric.Endpoint{Node: "target"}}},
			}
			fab, err := assembly.New(statedPhysical(cfg))
			if err != nil {
				t.Fatal(err)
			}
			header := ip.Header{Src: src, Dst: hostPrefix.Addr(), Protocol: 17, HopLimit: 64}
			if family == "IPv4" {
				header.V4 = &ip.V4{}
			} else {
				header.V6 = &ip.V6{}
			}
			payload, err := header.Encode([]byte("host-resolution"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fab.Inject(fabric.Injection{At: fixedTime, Origin: fabric.Endpoint{Node: "sw", Port: "p1"}, Frame: ethernet.Frame{Src: cfg.Hosts["source"].Address, Dst: swMAC, EtherType: etherType, Payload: payload}}); err != nil {
				t.Fatal(err)
			}
			observed, _ := observeJourneys(t, fab, 50)
			if len(fab.Snapshot().Queue) != 0 {
				t.Fatal("queue did not drain")
			}
			var held, answers, delivered int
			for _, j := range observed {
				for _, e := range j.Entries {
					if e.Result != nil && e.Result.Outcome == trace.Held {
						held++
					}
				}
				if j.Protocol && j.Injection.Origin.Node == "target" {
					answers++
				}
				for _, d := range j.Deliveries {
					if d.Host != "target" || d.Frame.EtherType != etherType {
						continue
					}
					hdr, body, err := ip.Decode(d.Frame.Payload)
					if err != nil {
						t.Fatal(err)
					}
					if hdr.Protocol != 17 {
						continue
					}
					if hdr.Dst != hostPrefix.Addr() || hdr.Src != src || hdr.HopLimit != 63 || string(body) != "host-resolution" {
						t.Fatalf("delivery = %+v/%q, want routed original", hdr, body)
					}
					delivered++
				}
			}
			if held != 1 || answers != 1 || delivered != 1 {
				t.Fatalf("held/answers/delivered = %d/%d/%d, want 1/1/1", held, answers, delivered)
			}
			cases++
		})
	}
	if cases != 2 {
		t.Fatalf("checked cases = %d, want 2", cases)
	}
}

func TestFabricRoutersResolveNextHopAndDeliverHeldFrame(t *testing.T) {
	ports, err := port.NewBuilder().Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
	if err != nil {
		t.Fatal(err)
	}
	sw1MAC := netaddr.MAC{2, 0, 0, 0, 0, 1}
	sw2MAC := netaddr.MAC{2, 0, 0, 0, 0, 2}
	h1MAC := netaddr.MAC{2, 0, 0, 0, 0, 7}
	h2MAC := netaddr.MAC{2, 0, 0, 0, 0, 8}
	h2 := netip.MustParseAddr("10.0.20.7")
	cfg := assembly.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": {MAC: sw1MAC, Ports: ports, Routing: &routing.Config{VRFs: map[string]routing.VRF{routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"host": {Port: "1/1/1", MAC: sw1MAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
					"link": {Port: "1/1/2", MAC: sw1MAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.99.1/24")}},
				},
				Routes: []routing.Route{{Prefix: netip.MustParsePrefix("10.0.20.0/24"), NextHop: netip.MustParseAddr("10.0.99.2"), Interface: "link"}},
			}}}},
			"sw2": {MAC: sw2MAC, Ports: ports, Routing: &routing.Config{VRFs: map[string]routing.VRF{routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"link": {Port: "1/1/1", MAC: sw2MAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.99.2/24")}},
					"host": {Port: "1/1/2", MAC: sw2MAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
				},
				Neighbors: []routing.Neighbor{{Interface: "host", Addr: h2, MAC: h2MAC}},
			}}}},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: h1MAC, IP: &hostmodel.IP{Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")}, Gateway: netip.MustParseAddr("10.0.10.1"), Neighbors: map[netip.Addr]netaddr.MAC{netip.MustParseAddr("10.0.10.1"): sw1MAC}}},
			"h2": {Address: h2MAC},
		},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}},
		},
	}
	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatal(err)
	}
	_, err = fab.Inject(fabric.Injection{At: cfg.Start, Origin: fabric.Endpoint{Node: "h1"}, Packet: &fabric.Packet{To: h2, Protocol: 17, Payload: []byte("held-through-two-routers")}})
	if err != nil {
		t.Fatal(err)
	}
	observed, _ := observeJourneys(t, fab, 50)
	var held, request, reply, delivered bool
	for _, journey := range observed {
		for _, entry := range journey.Entries {
			if entry.Result != nil && entry.Result.Outcome == trace.Held {
				held = true
			}
		}
		if journey.Protocol && journey.Injection.Frame.EtherType == ethernet.EtherTypeARP {
			msg, err := arp.Decode(journey.Injection.Frame)
			if err != nil {
				t.Fatal(err)
			}
			if msg.Operation == arp.Request && msg.TargetAddr == netip.MustParseAddr("10.0.99.2") {
				request = true
			}
			if msg.Operation == arp.Reply && msg.SenderAddr == netip.MustParseAddr("10.0.99.2") {
				reply = true
			}
		}
		for _, delivery := range journey.Deliveries {
			if delivery.Host != "h2" || delivery.Frame.EtherType != ethernet.EtherTypeIPv4 {
				continue
			}
			hdr, payload, err := ip.Decode(delivery.Frame.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if hdr.Src != netip.MustParseAddr("10.0.10.7") || hdr.Dst != h2 || hdr.HopLimit != 62 || string(payload) != "held-through-two-routers" {
				t.Fatalf("delivery = %+v/%q, want two routed hops", hdr, payload)
			}
			delivered = true
		}
	}
	if !held || !request || !reply || !delivered {
		t.Fatalf("held/request/reply/delivered = %t/%t/%t/%t, want all true", held, request, reply, delivered)
	}
}

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

	cfg := assembly.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": swCfg,
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {
				Address: macH1,
				IP: &hostmodel.IP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.5.7/24"), netip.MustParsePrefix("10.0.10.7/24")},
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
				Cable: cablemodel.Cable{LengthMeters: 5},
				A:     fabric.Endpoint{Node: "h1"},
				B:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				Cable: cablemodel.Cable{LengthMeters: 5},
				A:     fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:     fabric.Endpoint{Node: "h2"},
			},
		},
	}

	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
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
		{Layer: bridge.LayerNameVLAN, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
		{Layer: bridge.LayerName, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
		{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
		{Layer: routing.LayerName, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
		{Layer: routing.LayerName, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "vlan20"}},
		{Layer: bridge.LayerName, Op: trace.OpLookup, RuleID: "unicast-miss", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:77"}},
		{Layer: bridge.LayerName, Op: trace.OpReplicate, RuleID: "flood", Subject: trace.Subject{Kind: "vlan", Key: "20"}},
		{Layer: bridge.LayerNameVLAN, Op: trace.OpRewrite, RuleID: "vlan-tag-form", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
		{Layer: bridge.LayerName, Op: trace.OpTransmit, RuleID: "transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
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
	if hdr.Src != netip.MustParseAddr("10.0.10.7") {
		t.Errorf("delivered source = %s, want 10.0.10.7 on the gateway subnet", hdr.Src)
	}

	if hdr.HopLimit != 63 {
		t.Errorf("delivered HopLimit = %d, want 63", hdr.HopLimit)
	}

	_, err = fab.Inject(fabric.Injection{
		At:     fab.Snapshot().Clock,
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
	// A host stack resolves no neighbors (host.RoutingConfig writes
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

	baseCfg := func() assembly.Config {
		return assembly.Config{
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
			Hosts: map[string]hostmodel.Config{
				"h1": {
					IP: &hostmodel.IP{
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
					Cable: cablemodel.Cable{LengthMeters: 5},
					A:     fabric.Endpoint{Node: "h1"},
					B:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				},
				{
					Cable: cablemodel.Cable{LengthMeters: 10},
					A:     fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
					B:     fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
				},
				{
					Cable: cablemodel.Cable{LengthMeters: 5},
					A:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
					B:     fabric.Endpoint{Node: "h2"},
				},
			},
		}
	}

	cfg := baseCfg()
	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
	}

	filled := assembly.Spec(fab).Config()
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
	if fab.Device("sw1").(*vswitch.Switch).BridgeID().Address != wantSW1 {
		t.Errorf("sw1 BridgeID().Address = %v, want %v", fab.Device("sw1").(*vswitch.Switch).BridgeID().Address, wantSW1)
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

	fabShift, err := assembly.New(statedPhysical(cfgShift))
	if err != nil {
		t.Fatalf("assembly.New with explicit h2 MAC: %v", err)
	}
	filledShift := assembly.Spec(fabShift).Config()
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

	fabDerived, err := assembly.Derive(fab, constructionSpec(statedPhysical(baseCfg())))
	if err != nil {
		t.Fatalf("assembly.Derive: %v", err)
	}
	filledDerived := assembly.Spec(fabDerived).Config()
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

	cfg := assembly.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": swCfg,
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {
				Address: macH1,
				IP: &hostmodel.IP{
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
				Cable: cablemodel.Cable{LengthMeters: 5},
				A:     fabric.Endpoint{Node: "h1"},
				B:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				Cable: cablemodel.Cable{LengthMeters: 5},
				A:     fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:     fabric.Endpoint{Node: "h2"},
			},
		},
	}

	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
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
	cfgFor := func(h1 netaddr.MAC) assembly.Config {
		return assembly.Config{
			Start:    time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			Switches: map[string]vswitch.Config{"sw1": {Ports: ports}},
			Hosts:    map[string]hostmodel.Config{"h1": {Address: h1}},
			Cables: []fabric.Cable{{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			}},
		}
	}

	cur, err := assembly.New(statedPhysical(cfgFor(netaddr.MAC{})))
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
	}
	if got := assembly.Spec(cur).Config().Switches["sw1"].MAC; got != netaddr.Local(1) {
		t.Fatalf("sw1 = %s, want %s", got, netaddr.Local(1))
	}

	next, err := assembly.Derive(cur, constructionSpec(statedPhysical(cfgFor(netaddr.Local(1)))))
	if err != nil {
		t.Fatalf("assembly.Derive: %v", err)
	}
	sw1 := assembly.Spec(next).Config().Switches["sw1"].MAC
	h1 := assembly.Spec(next).Config().Hosts["h1"].Address
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
// its own injected journey without a link to the journey that held it, so the
// switch's own release must itself carry the
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

	cfg := assembly.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": swCfg,
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {
				Address: macH1,
				IP: &hostmodel.IP{
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
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}},
		},
	}

	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
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

	_, err = fab.Inject(fabric.Injection{
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
	if !slices.ContainsFunc(heldJourney.Entries, func(e fabric.Entry) bool { return e.Result != nil && e.Result.Outcome == trace.Held }) {
		t.Fatal("continued journey has no Held hop")
	}

	// The original journey continues through the release to the host.
	var released *fabric.Journey
	for _, j := range fab.Report() {
		if j.FrameID != heldID {
			continue
		}
		if !j.Protocol && len(j.Deliveries) == 1 && j.Deliveries[0].Host == "h2" {
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

// TestFabricReleasesHeldFrameOnSubInterfaceToVLANHost is the whole-fabric
// counterpart of the switch's sub-interface release: a packet held for a
// neighbor behind a sub-interface reaches the host once that host answers, and
// reaches it tagged. The switch keeps its own hold queue, so nothing here is a
// second copy of that test — what it adds is the wire on either side of the
// switch, where a frame released untagged, or flooded onto a bridge, would
// never arrive at a host that only accepts its own VLAN.
//
// There is no separate assertion that no device.Drop was recorded.
// Every way the held frame could leave its queue without reaching a wire ends
// with no delivery to h2, which the released == nil check below already fails
// on, so such an assertion would have no discriminating power here.
func TestFabricReleasesHeldFrameOnSubInterfaceToVLANHost(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "eth1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "eth3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build switch ports: %v", err)
	}

	vid10 := vlan.ID(10)
	swMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	addrH1 := netip.MustParseAddr("10.0.30.7")
	addrH2 := netip.MustParseAddr("10.0.10.7")

	cfg := assembly.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": {
				MAC:   swMAC,
				Ports: ports,
				Routing: &routing.Config{
					VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"eth1.10": {Port: "eth1", VLAN: 10, MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
								"eth3":    {Port: "eth3", MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
							},
							// No neighbor for h2: the reply below is the only
							// thing that resolves it.
						},
					},
				},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {
				Address: macH1,
				IP: &hostmodel.IP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.30.7/24")},
					Gateway:   netip.MustParseAddr("10.0.30.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{netip.MustParseAddr("10.0.30.1"): swMAC},
				},
			},
			"h2": {Address: macH2, VLAN: &vid10},
		},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "eth3"}},
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "eth1"}, B: fabric.Endpoint{Node: "h2"}},
		},
	}

	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
	}

	heldID, err := fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1"},
		Packet: &fabric.Packet{To: addrH2, Protocol: 17, Payload: []byte("ping")},
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
		TargetAddr:   netip.MustParseAddr("10.0.10.1"),
	}
	replyFrame, err := arp.Encode(msg, swMAC)
	if err != nil {
		t.Fatalf("encode ARP reply: %v", err)
	}

	// h2 carries a VLAN tag form, so the fabric stamps VID 10 on what it sends,
	// which is the only tag that names eth1.10 on the parent port.
	_, err = fab.Inject(fabric.Injection{
		At:     cfg.Start.Add(time.Second),
		Origin: fabric.Endpoint{Node: "h2"},
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
	if !slices.ContainsFunc(heldJourney.Entries, func(e fabric.Entry) bool { return e.Result != nil && e.Result.Outcome == trace.Held }) {
		t.Fatal("continued journey has no Held hop")
	}

	var released *fabric.Journey
	for _, j := range fab.Report() {
		if j.FrameID != heldID {
			continue
		}
		if !j.Protocol && len(j.Deliveries) == 1 && j.Deliveries[0].Host == "h2" {
			jj := j
			released = &jj
			break
		}
	}
	if released == nil {
		t.Fatalf("no released journey delivered to h2 among: %+v", fab.Report())
	}

	wantTag := vlan.Tag{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}
	if got := released.Deliveries[0].Frame.Tags; len(got) != 1 || got[0] != wantTag {
		t.Errorf("delivered tags = %+v, want [%+v]", got, wantTag)
	}
	hdr, _, err := ip.Decode(released.Deliveries[0].Frame.Payload)
	if err != nil {
		t.Fatalf("decode delivered IP payload: %v", err)
	}
	if hdr.Src != addrH1 || hdr.Dst != addrH2 {
		t.Errorf("delivered src/dst = %s/%s, want %s/%s", hdr.Src, hdr.Dst, addrH1, addrH2)
	}

	snap := fab.Snapshot()
	if got := snap.Devices["sw1"].Counters["eth1"].OutUnicast; got == 0 {
		t.Errorf("sw1 eth1 OutUnicast = 0, want the released frame counted against the parent port")
	}
}

// TestFabricRecordsNeighborFailureOnHeldFrameTimeout checks that the fabric
// records the [device.Output] drops of a call alongside its emissions: a held
// frame whose neighbor never resolves must leave a drop entry and a discard
// counter behind, not vanish after a held journey entry with nothing
// following it.
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

	cfg := assembly.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": swCfg,
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {
				Address: macH1,
				IP: &hostmodel.IP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.50.7/24")},
					Gateway:   netip.MustParseAddr("10.0.50.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{
						netip.MustParseAddr("10.0.50.1"): swMAC,
					},
				},
			},
		},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
		},
	}

	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
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
	if !slices.ContainsFunc(heldJourney.Entries, func(e fabric.Entry) bool { return e.Result != nil && e.Result.Outcome == trace.Held }) {
		t.Fatal("continued journey has no Held hop")
	}

	var dropEntry *fabric.Entry
	for _, j := range fab.Report() {
		if j.FrameID != heldID {
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

// TestFabricNeighborFailureOnAnSVIIsCountedAgainstNoPort is the case
// TestFabricRecordsNeighborFailureOnHeldFrameTimeout cannot see: it names its routed interfaces
// after the ports they sit on, so recovering a port from the step's subject happened to work
// there. A VLAN interface has no single port, and a discard counted against the string "vlan20"
// lands on an Endpoint no Snapshot can ever show.
func TestFabricNeighborFailureOnAnSVIIsCountedAgainstNoPort(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build switch ports: %v", err)
	}

	swMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	pvid10 := vlan.ID(10)
	pvid20 := vlan.ID(20)

	cfg := assembly.Config{
		Start: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": {
				MAC:   swMAC,
				Ports: ports,
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &pvid10, Untagged: []vlan.ID{10}},
						"1/1/2": {PVID: &pvid20, Untagged: []vlan.ID{20}},
					},
				}},
				Routing: &routing.Config{VRFs: map[string]routing.VRF{
					routing.DefaultVRF: {Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						// Nothing is configured for 10.0.20.77: the switch
						// holds a frame for it and never resolves it.
						"vlan20": {VLAN: 20, MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					}},
				}},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {
				Address: macH1,
				VLAN:    nil,
				IP: &hostmodel.IP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
					Gateway:   netip.MustParseAddr("10.0.10.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{netip.MustParseAddr("10.0.10.1"): swMAC},
				},
			},
		},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
		},
	}

	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
	}

	heldID, err := fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1"},
		Packet: &fabric.Packet{To: netip.MustParseAddr("10.0.20.77"), Protocol: 17, Payload: []byte("ping")},
	})
	if err != nil {
		t.Fatalf("Inject packet: %v", err)
	}

	fab.Run(50)

	var dropEntry *fabric.Entry
	for _, j := range fab.Report() {
		if j.FrameID != heldID {
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
	if dropEntry.Port != "" {
		t.Errorf("drop entry port = %q, want empty: vlan20 is not a port", dropEntry.Port)
	}
	if dropEntry.Step == nil {
		t.Fatal("drop entry carries no step")
	}
	if dropEntry.Step.RuleID != trace.RuleID(dropEntry.Reason) {
		t.Errorf("step rule = %q, reason = %q, want them to agree", dropEntry.Step.RuleID, dropEntry.Reason)
	}

	snap := fab.Snapshot()
	dev, ok := snap.Devices["sw1"]
	if !ok {
		t.Fatalf("no device sw1 in snapshot")
	}
	for name, counters := range dev.Counters {
		if got := counters.Discards[routing.ReasonNeighborMiss]; got != 0 {
			t.Errorf("port %q counted %d neighbor-miss discards, want 0: no port carried this frame", name, got)
		}
	}
}

// TestFabricNeighborFailureOnAnSVIDoesNotWidenToTheSwitch checks that a
// portless SVI drop on the held journey does not inherit an unrelated port's
// issue on the same switch.
func TestFabricNeighborFailureOnAnSVIDoesNotWidenToTheSwitch(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up})
	// 1/1/3 carries no traffic in this test: it exists only to put a real issue on sw1, at its own
	// port scope. It is configured Up but listed Uncabled, so its derived status is Down and the
	// mismatch raises an oper-status-conflict issue on PortScope("sw1", "1/1/3").
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build switch ports: %v", err)
	}

	swMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	pvid10 := vlan.ID(10)
	pvid20 := vlan.ID(20)

	cfg := assembly.Config{
		Start: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": {
				MAC:   swMAC,
				Ports: ports,
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &pvid10, Untagged: []vlan.ID{10}},
						"1/1/2": {PVID: &pvid20, Untagged: []vlan.ID{20}},
					},
				}},
				Routing: &routing.Config{VRFs: map[string]routing.VRF{
					routing.DefaultVRF: {Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						// Nothing is configured for 10.0.20.77: the switch
						// holds a frame for it and never resolves it.
						"vlan20": {VLAN: 20, MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					}},
				}},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {
				Address: macH1,
				IP: &hostmodel.IP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
					Gateway:   netip.MustParseAddr("10.0.10.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{netip.MustParseAddr("10.0.10.1"): swMAC},
				},
			},
		},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
		},
		Uncabled: []fabric.Uncabled{
			{Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
			{Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}},
		},
		PhyAssumption: gigabitCopper(),
	}

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
	}

	fabMeta := fab.Metadata()
	issueScope := analysis.PortScope("sw1", "1/1/3")
	found := false
	for _, issue := range fabMeta.Issues() {
		if issue.Code == fabric.IssueOperStatusConflict && issue.Scope.Compare(issueScope) == 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("fabric metadata has no oper-status-conflict issue on 1/1/3: %+v", fabMeta.Issues())
	}

	heldID, err := fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1"},
		Packet: &fabric.Packet{To: netip.MustParseAddr("10.0.20.77"), Protocol: 17, Payload: []byte("ping")},
	})
	if err != nil {
		t.Fatalf("Inject packet: %v", err)
	}

	fab.Run(50)

	var dropJourney *fabric.Journey
	for _, j := range fab.Report() {
		if j.FrameID != heldID {
			continue
		}
		for _, e := range j.Entries {
			if e.Kind == fabric.EntryDrop && e.Reason == routing.ReasonNeighborMiss {
				jj := j
				dropJourney = &jj

				break
			}
		}
	}
	if dropJourney == nil {
		t.Fatalf("no neighbor-miss drop journey found among: %+v", fab.Report())
	}

	for _, issue := range dropJourney.Metadata.Issues() {
		if issue.Code == fabric.IssueOperStatusConflict && issue.Scope.Compare(issueScope) == 0 {
			t.Errorf("drop journey metadata carries 1/1/3's oper-status-conflict issue, want none: %+v", issue)
		}
	}
}

func twoPorts(t *testing.T) port.Table {
	t.Helper()
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}
	return ports
}

// TestFabricReleasedFrameQueuesAtItsIngressPriority is the end of the priority's journey: the
// switch carries a released frame's priority on the Emission, and the fabric has to queue on
// that rather than re-derive one from a frame whose egress tag is gone. The baseline is the same
// frame over the same fabric with the binding configured, so it never holds.
func TestFabricReleasedFrameQueuesAtItsIngressPriority(t *testing.T) {
	sw1MAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	sw2MAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x02}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	nextHop := netip.MustParseAddr("10.0.60.7")
	vid10 := vlan.ID(10)
	start := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)

	// sw1's 1/1/1 is tagged, so a frame arrives carrying a priority; 1/1/2 is a routed port, so
	// the frame leaves with no tag at all and nothing but the emission can carry it. The far end
	// is a second switch rather than a host, because only a crossing into a device records the
	// priority the frame queued at.
	build := func(t *testing.T, neighbors []routing.Neighbor) *fabric.Fabric {
		t.Helper()
		cfg := assembly.Config{
			Start: start,
			Switches: map[string]vswitch.Config{
				"sw1": {
					MAC:   sw1MAC,
					Ports: twoPorts(t),
					Bridge: &bridge.Config{VLAN: &bridge.VLAN{
						Table:       map[vlan.ID]string{10: "vlan10"},
						Switchports: map[string]bridge.Switchport{"1/1/1": {PVID: &vid10, Tagged: []vlan.ID{10}}},
					}},
					Routing: &routing.Config{VRFs: map[string]routing.VRF{routing.DefaultVRF: {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, MAC: sw1MAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
							"rp2":    {Port: "1/1/2", MAC: sw1MAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.60.1/24")}},
						},
						Neighbors: neighbors,
					}}},
				},
				"sw2": {MAC: sw2MAC, Ports: twoPorts(t)},
			},
			// h1 exists only to give 1/1/1 a link: an unconnected port comes up Down, and a
			// frame injected onto a down port never reaches the switch at all.
			Hosts: map[string]hostmodel.Config{"h1": {Address: macH1}},
			Cables: []fabric.Cable{
				{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
				{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
			},
		}

		fab, err := assembly.New(statedPhysical(cfg))
		if err != nil {
			t.Fatalf("assembly.New: %v", err)
		}
		return fab
	}

	// The frame is injected at sw1's port rather than from a host: a host injection replaces the
	// frame's tag with the host's own VLAN form, which carries priority 0, and this test is
	// about a frame that arrived at priority 5.
	packet, err := ip.Header{
		Src:      netip.MustParseAddr("10.0.50.7"),
		Dst:      nextHop,
		HopLimit: 64,
		Protocol: 17,
		V4:       &ip.V4{},
	}.Encode([]byte("priority"))
	if err != nil {
		t.Fatalf("encode IPv4 packet: %v", err)
	}
	taggedFrame := ethernet.Frame{
		Src:       macH1,
		Dst:       sw1MAC,
		EtherType: ethernet.EtherTypeIPv4,
		Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), PCP: 5, VID: 10}},
		Payload:   packet,
	}

	replyFrame, err := arp.Encode(arp.Message{
		HardwareType: 1,
		ProtocolType: 0x0800,
		Operation:    arp.Reply,
		SenderMAC:    sw2MAC,
		SenderAddr:   nextHop,
		TargetMAC:    sw1MAC,
		TargetAddr:   netip.MustParseAddr("10.0.60.1"),
	}, sw1MAC)
	if err != nil {
		t.Fatalf("encode ARP reply: %v", err)
	}

	// Both runs take the same injections, so the only difference between them is whether the
	// binding was configured up front. The injected reply resolves the held run's next hop.
	// The configured run ignores it, because Receive never overwrites a
	// configured binding.
	run := func(t *testing.T, neighbors []routing.Neighbor) vlan.PCP {
		t.Helper()
		fab := build(t, neighbors)
		if _, err := fab.Inject(fabric.Injection{
			At:     start,
			Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			Frame:  taggedFrame,
		}); err != nil {
			t.Fatalf("Inject frame: %v", err)
		}
		if _, err := fab.Inject(fabric.Injection{
			At:     start.Add(time.Second),
			Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
			Frame:  replyFrame,
		}); err != nil {
			t.Fatalf("Inject ARP reply: %v", err)
		}
		fab.Run(200)

		if neighbors == nil {
			held := false
			for _, j := range fab.Report() {
				for _, e := range j.Entries {
					if e.Result != nil && e.Result.Outcome == trace.Held {
						held = true
					}
				}
			}
			if !held {
				t.Fatalf("no journey entry took the hold path: %+v", fab.Report())
			}
		}

		var found []vlan.PCP
		for _, j := range fab.Report() {
			for _, e := range j.Entries {
				if !j.Protocol && j.Injection.Frame.EtherType == ethernet.EtherTypeIPv4 && e.Kind == fabric.EntryCrossing && e.Device == "sw2" {
					found = append(found, e.PCP)
				}
			}
		}
		if len(found) != 1 {
			t.Fatalf("crossings into sw2 = %d, want exactly 1: %+v", len(found), fab.Report())
		}
		return found[0]
	}

	heldPCP := run(t, nil)
	livePCP := run(t, []routing.Neighbor{{Interface: "rp2", Addr: nextHop, MAC: sw2MAC}})

	if heldPCP != livePCP {
		t.Errorf("held frame queued at PCP %d, the same frame unheld at %d: a hold must not change the priority", heldPCP, livePCP)
	}
	if heldPCP != 5 {
		t.Errorf("released frame queued at PCP %d, want 5, the priority it arrived with", heldPCP)
	}
}

// A held frame survives Derive over an unchanged routing configuration and
// releases on the next Wake once resolved.
func TestFabricDeriveRetainsHeldFrameUnderUnchangedRoutingKey(t *testing.T) {
	start := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	sw1MAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	sw2MAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x02}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	nextHop := netip.MustParseAddr("10.0.60.2")

	vid10 := vlan.ID(10)

	cfg := assembly.Config{
		Start: start,
		Switches: map[string]vswitch.Config{
			"sw1": {
				MAC:   sw1MAC,
				Ports: twoPorts(t),
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table:       map[vlan.ID]string{10: "vlan10"},
						Switchports: map[string]bridge.Switchport{"1/1/1": {PVID: &vid10, Tagged: []vlan.ID{10}}},
					},
				},
				Routing: &routing.Config{
					VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"vlan10": {VLAN: 10, MAC: sw1MAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
								"rp2":    {Port: "1/1/2", MAC: sw1MAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.60.1/24")}},
							},
						},
					},
				},
			},
			"sw2": {MAC: sw2MAC, Ports: twoPorts(t)},
		},
		Hosts: map[string]hostmodel.Config{"h1": {Address: macH1}},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
		},
	}

	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
	}

	packet, _ := ip.Header{
		Src:      netip.MustParseAddr("10.0.50.7"),
		Dst:      nextHop,
		HopLimit: 64,
		Protocol: 17,
		V4:       &ip.V4{},
	}.Encode([]byte("payload"))
	taggedFrame := ethernet.Frame{
		Src:       macH1,
		Dst:       sw1MAC,
		EtherType: ethernet.EtherTypeIPv4,
		Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
		Payload:   packet,
	}

	if _, err := fab.Inject(fabric.Injection{
		At:     start,
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		Frame:  taggedFrame,
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if _, ok := fab.Step(); !ok {
		t.Fatal("fab.Step failed")
	}

	snap := fab.Snapshot()
	if len(snap.Devices["sw1"].Neighbors) != 1 || snap.Devices["sw1"].Neighbors[0].HoldDepth != 1 {
		t.Fatalf("sw1 neighbors before derive = %+v, want one neighbor with HoldDepth 1", snap.Devices["sw1"].Neighbors)
	}

	nextCfg := cfg.Clone()
	derived, err := assembly.Derive(fab, constructionSpec(statedPhysical(nextCfg)))
	if err != nil {
		t.Fatalf("assembly.Derive: %v", err)
	}
	if !assembly.Retention(derived)["sw1"].Routing.Kept {
		t.Fatalf("sw1 routing retention = %+v, want Kept: true", assembly.Retention(derived)["sw1"].Routing)
	}
	snapDerived := derived.Snapshot()
	if len(snapDerived.Devices["sw1"].Neighbors) != 1 || snapDerived.Devices["sw1"].Neighbors[0].HoldDepth != 1 {
		t.Errorf("sw1 neighbors on derived = %+v, want HoldDepth 1", snapDerived.Devices["sw1"].Neighbors)
	}

	// Resolve neighbor via ARP reply on derived fabric
	replyFrame, _ := arp.Encode(arp.Message{
		HardwareType: 1,
		ProtocolType: 0x0800,
		Operation:    arp.Reply,
		SenderMAC:    sw2MAC,
		SenderAddr:   nextHop,
		TargetMAC:    sw1MAC,
		TargetAddr:   netip.MustParseAddr("10.0.60.1"),
	}, sw1MAC)
	if _, err := derived.Inject(fabric.Injection{
		At:     snapDerived.Clock.Add(time.Millisecond),
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
		Frame:  replyFrame,
	}); err != nil {
		t.Fatalf("Inject ARP reply: %v", err)
	}
	derived.Run(200)

	// Held frame was released and crossed into sw2
	var released bool
	for _, j := range derived.Report() {
		for _, e := range j.Entries {
			if !j.Protocol && j.Injection.Frame.EtherType == ethernet.EtherTypeIPv4 && e.Kind == fabric.EntryCrossing && e.Device == "sw2" {
				released = true
			}
		}
	}
	if !released {
		t.Error("held frame was not released across derive")
	}
}

// Changing NeighborPolicy.RetransTimer rebuilds the routing layer and
// reports held frames as failed.
func TestFabricDeriveFailsHeldFrameOnNeighborPolicyEdit(t *testing.T) {
	start := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	sw1MAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	sw2MAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x02}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	nextHop := netip.MustParseAddr("10.0.60.2")

	vid10 := vlan.ID(10)

	cfg := assembly.Config{
		Start: start,
		Switches: map[string]vswitch.Config{
			"sw1": {
				MAC:   sw1MAC,
				Ports: twoPorts(t),
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table:       map[vlan.ID]string{10: "vlan10"},
						Switchports: map[string]bridge.Switchport{"1/1/1": {PVID: &vid10, Tagged: []vlan.ID{10}}},
					},
				},
				Routing: &routing.Config{
					VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"vlan10": {VLAN: 10, MAC: sw1MAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
								"rp2":    {Port: "1/1/2", MAC: sw1MAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.60.1/24")}},
							},
						},
					},
				},
			},
			"sw2": {MAC: sw2MAC, Ports: twoPorts(t)},
		},
		Hosts: map[string]hostmodel.Config{"h1": {Address: macH1}},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
		},
	}

	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
	}

	packet, _ := ip.Header{
		Src:      netip.MustParseAddr("10.0.50.7"),
		Dst:      nextHop,
		HopLimit: 64,
		Protocol: 17,
		V4:       &ip.V4{},
	}.Encode([]byte("payload"))
	taggedFrame := ethernet.Frame{
		Src:       macH1,
		Dst:       sw1MAC,
		EtherType: ethernet.EtherTypeIPv4,
		Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
		Payload:   packet,
	}

	if _, err := fab.Inject(fabric.Injection{
		At:     start,
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		Frame:  taggedFrame,
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if _, ok := fab.Step(); !ok {
		t.Fatal("fab.Step failed")
	}

	targetCfg := cfg.Clone()
	sw1Cfg := targetCfg.Switches["sw1"]
	vrf := sw1Cfg.Routing.VRFs[routing.DefaultVRF]
	vrf.NeighborPolicy.RetransTimer = 10 * time.Second
	sw1Cfg.Routing.VRFs[routing.DefaultVRF] = vrf
	targetCfg.Switches["sw1"] = sw1Cfg

	derived, err := assembly.Derive(fab, constructionSpec(statedPhysical(targetCfg)))
	if err != nil {
		t.Fatalf("assembly.Derive: %v", err)
	}
	if assembly.Retention(derived)["sw1"].Routing.Kept {
		t.Errorf("sw1 routing retention = %+v, want Kept: false", assembly.Retention(derived)["sw1"].Routing)
	}
	var failures []fabric.Entry
	for _, journey := range derived.Report() {
		for _, entry := range journey.Entries {
			if entry.Kind == fabric.EntryDrop && entry.Device == "sw1" {
				failures = append(failures, entry)
			}
		}
	}
	if len(failures) != 1 {
		t.Fatalf("sw1 drop journeys = %d, want 1 failed held frame", len(failures))
	}
	if failures[0].At != derived.Snapshot().Clock || failures[0].Reason != routing.ReasonNeighborMiss {
		t.Errorf("derive drop = %+v, want neighbor-miss at derived clock", failures[0])
	}
}

func TestFabricDeriveOrdersHeldFrameDropsBySwitchName(t *testing.T) {
	start := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	nextHop := netip.MustParseAddr("10.0.60.2")
	cfg := assembly.Config{Start: start, Switches: make(map[string]vswitch.Config), Hosts: make(map[string]hostmodel.Config)}
	for i, name := range []string{"sw1", "sw2"} {
		mac := netaddr.MAC{2, 0, 0, 0, 0, byte(i + 1)}
		cfg.Switches[name] = vswitch.Config{
			MAC: mac, Ports: twoPorts(t),
			Routing: &routing.Config{VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {Interfaces: map[string]routing.Interface{
					"in":  {Port: "1/1/1", MAC: mac, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
					"out": {Port: "1/1/2", MAC: mac, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.60.1/24")}},
				}},
			}},
		}
	}
	for i, name := range []string{"h1", "h2", "h3", "h4"} {
		cfg.Hosts[name] = hostmodel.Config{Address: netaddr.MAC{2, 0, 0, 0, 1, byte(i + 1)}}
	}
	cfg.Cables = []fabric.Cable{
		{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
		{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
		{A: fabric.Endpoint{Node: "h3"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
		{A: fabric.Endpoint{Node: "h4"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}},
	}
	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatal(err)
	}
	packet, err := (ip.Header{
		Src: netip.MustParseAddr("10.0.50.7"), Dst: nextHop,
		HopLimit: 64, Protocol: 17, V4: &ip.V4{},
	}).Encode([]byte("held"))
	if err != nil {
		t.Fatal(err)
	}
	var heldIDs []fabric.FrameID
	for _, name := range []string{"sw1", "sw2"} {
		id, err := fab.Inject(fabric.Injection{
			At: start, Origin: fabric.Endpoint{Node: name, Port: "1/1/1"},
			Frame: ethernet.Frame{
				Src: cfg.Hosts["h1"].Address, Dst: cfg.Switches[name].MAC,
				EtherType: ethernet.EtherTypeIPv4, Payload: packet,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		heldIDs = append(heldIDs, id)
	}
	for range 2 {
		if _, ok := fab.Step(); !ok {
			t.Fatal("fabric.Step failed")
		}
	}
	for _, id := range heldIDs {
		journey := findJourney(t, fab, id)
		last := journey.Entries[len(journey.Entries)-1]
		if last.Result == nil || last.Result.Outcome != trace.Held {
			t.Fatalf("frame %d last entry = %+v, want Held", id, last)
		}
	}
	targetCfg := cfg.Clone()
	for _, name := range []string{"sw1", "sw2"} {
		vrf := targetCfg.Switches[name].Routing.VRFs[routing.DefaultVRF]
		vrf.NeighborPolicy.RetransTimer = 10 * time.Second
		targetCfg.Switches[name].Routing.VRFs[routing.DefaultVRF] = vrf
	}
	const runs = 128
	var baseline []fabric.Journey
	var reversed, different int
	for run := range runs {
		derived, err := assembly.Derive(fab, constructionSpec(statedPhysical(targetCfg)))
		if err != nil {
			t.Fatal(err)
		}
		report := derived.Report()
		ids := make(map[string]fabric.FrameID)
		for _, journey := range report {
			for _, entry := range journey.Entries {
				if entry.Kind == fabric.EntryDrop && entry.Reason == routing.ReasonNeighborMiss {
					if _, exists := ids[entry.Device]; exists {
						t.Fatalf("run %d: duplicate drop for %s", run, entry.Device)
					}
					ids[entry.Device] = journey.FrameID
				}
			}
		}
		if len(ids) != 2 || ids["sw1"] == 0 || ids["sw2"] == 0 {
			t.Fatalf("run %d: drop IDs = %v, want one per switch", run, ids)
		}
		if ids["sw1"] >= ids["sw2"] {
			reversed++
		}
		if run == 0 {
			baseline = report
		} else if !reflect.DeepEqual(report, baseline) {
			different++
		}
	}
	if reversed != 0 {
		t.Errorf("sw1 drop FrameID >= sw2 drop FrameID in %d of %d derives, want 0", reversed, runs)
	}
	if different != 0 {
		t.Errorf("reports differ from first derive in %d of %d derives, want 0", different, runs)
	}
}

// A frame released onto a routed LAG leaves by the member the switch selected
// for it. The fabric's own selection would hash the frame's sub-interface tag
// into the choice and could pick the other member.
func TestFabricReleasedFrameLeavesByTheMemberTheSwitchSelected(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build switch ports: %v", err)
	}

	vid10 := vlan.ID(10)
	swMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	macH3 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x78}
	addrH2 := netip.MustParseAddr("10.0.10.7")

	cfg := assembly.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": {
				MAC:   swMAC,
				Ports: ports,
				LAG:   &lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: lag.BalanceSLB}}},
				Routing: &routing.Config{
					VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"lag1.10": {Port: "lag1", VLAN: 10, MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
								"1/1/3":   {Port: "1/1/3", MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
							},
						},
					},
				},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {
				Address: macH1,
				IP: &hostmodel.IP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.30.7/24")},
					Gateway:   netip.MustParseAddr("10.0.30.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{netip.MustParseAddr("10.0.30.1"): swMAC},
				},
			},
			"h2": {Address: macH2, VLAN: &vid10},
			"h3": {Address: macH3, VLAN: &vid10},
		},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}},
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h2"}},
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h3"}},
		},
	}

	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
	}

	heldID, err := fab.Inject(fabric.Injection{
		At:     cfg.Start,
		Origin: fabric.Endpoint{Node: "h1"},
		Packet: &fabric.Packet{To: addrH2, Protocol: 17, Payload: []byte("ping")},
	})
	if err != nil {
		t.Fatalf("Inject packet: %v", err)
	}
	replyFrame, err := arp.Encode(arp.Message{
		HardwareType: 1, ProtocolType: 0x0800, Operation: arp.Reply,
		SenderMAC: macH2, SenderAddr: addrH2,
		TargetMAC: swMAC, TargetAddr: netip.MustParseAddr("10.0.10.1"),
	}, swMAC)
	if err != nil {
		t.Fatalf("encode ARP reply: %v", err)
	}
	if _, err := fab.Inject(fabric.Injection{
		At:     cfg.Start.Add(time.Second),
		Origin: fabric.Endpoint{Node: "h2"},
		Frame:  replyFrame,
	}); err != nil {
		t.Fatalf("Inject ARP reply: %v", err)
	}
	fab.Run(40)

	var released *fabric.Journey
	for _, j := range fab.Report() {
		if j.FrameID == heldID {
			released = &j
			break
		}
	}
	if released == nil {
		t.Fatalf("no release of frame %d among %+v", heldID, fab.Report())
	}
	// The switch selects member 1/1/1, the cable to h2, when it releases the
	// frame. Selecting again at the fabric with the sub-interface's VID 10
	// hashes to another bucket and takes 1/1/2, the cable to h3.
	if len(released.Deliveries) != 1 || released.Deliveries[0].Host != "h2" {
		t.Errorf("released frame deliveries = %+v, want one to h2 through member 1/1/1", released.Deliveries)
	}
	counters := fab.Snapshot().Devices["sw1"].Counters
	if got := counters["1/1/1"].OutUnicast; got != 1 {
		t.Errorf("sw1 1/1/1 OutUnicast = %d, want the released frame", got)
	}
	if got := counters["1/1/2"].OutUnicast; got != 0 {
		t.Errorf("sw1 1/1/2 OutUnicast = %d, want 0", got)
	}
}

func TestRoutingOnlySwitchWakesAfterDerive(t *testing.T) {
	start := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	sw1MAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	sw2MAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x02}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	nextHop := netip.MustParseAddr("10.0.60.2")

	vid10 := vlan.ID(10)

	cfg := assembly.Config{
		Start: start,
		Switches: map[string]vswitch.Config{
			"sw1": {
				MAC:   sw1MAC,
				Ports: twoPorts(t),
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table:       map[vlan.ID]string{10: "vlan10"},
						Switchports: map[string]bridge.Switchport{"1/1/1": {PVID: &vid10, Tagged: []vlan.ID{10}}},
					},
				},
				Routing: &routing.Config{
					VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"vlan10": {VLAN: 10, MAC: sw1MAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
								"rp2":    {Port: "1/1/2", MAC: sw1MAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.60.1/24")}},
							},
						},
					},
				},
			},
			"sw2": {MAC: sw2MAC, Ports: twoPorts(t)},
		},
		Hosts: map[string]hostmodel.Config{"h1": {Address: macH1}},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
		},
	}

	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("assembly.New: %v", err)
	}

	packet, _ := ip.Header{
		Src:      netip.MustParseAddr("10.0.50.7"),
		Dst:      nextHop,
		HopLimit: 64,
		Protocol: 17,
		V4:       &ip.V4{},
	}.Encode([]byte("payload"))
	taggedFrame := ethernet.Frame{
		Src:       macH1,
		Dst:       sw1MAC,
		EtherType: ethernet.EtherTypeIPv4,
		Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
		Payload:   packet,
	}

	if _, err := fab.Inject(fabric.Injection{
		At:     start,
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		Frame:  taggedFrame,
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if _, ok := fab.Step(); !ok {
		t.Fatal("fab.Step failed")
	}

	snap := fab.Snapshot()
	if len(snap.Devices["sw1"].Neighbors) != 1 || snap.Devices["sw1"].Neighbors[0].HoldDepth != 1 {
		t.Fatalf("sw1 neighbors before derive = %+v, want one neighbor with HoldDepth 1", snap.Devices["sw1"].Neighbors)
	}

	// Unrelated edit: derive with unchanged routing
	nextCfg := cfg.Clone()
	derived, err := assembly.Derive(fab, constructionSpec(statedPhysical(nextCfg)))
	if err != nil {
		t.Fatalf("assembly.Derive: %v", err)
	}
	if !assembly.Retention(derived)["sw1"].Routing.Kept {
		t.Fatalf("sw1 routing retention = %+v, want Kept: true", assembly.Retention(derived)["sw1"].Routing)
	}
	snapDerived := derived.Snapshot()
	if len(snapDerived.Devices["sw1"].Neighbors) != 1 || snapDerived.Devices["sw1"].Neighbors[0].HoldDepth != 1 {
		t.Errorf("sw1 neighbors on derived = %+v, want HoldDepth 1", snapDerived.Devices["sw1"].Neighbors)
	}

	derived.Run(200)
	misses := 0
	for _, journey := range derived.Report() {
		for _, entry := range journey.Entries {
			if entry.Reason == routing.ReasonNeighborMiss {
				misses++
			}
		}
	}
	if misses != 1 {
		t.Fatalf("neighbor-miss drops = %d, want 1: %+v", misses, derived.Report())
	}
}
