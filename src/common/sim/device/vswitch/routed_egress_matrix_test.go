package vswitch_test

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/ndp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestRoutedLAGEmissionMatrix(t *testing.T) {
	cells := 0
	for _, kind := range []string{"routed LAG", "sub-interface on a LAG", "VLAN interface on a LAG"} {
		for _, path := range []string{"ARP request", "Neighbor Solicitation", "unicast probe", "release"} {
			t.Run(kind+"/"+path, func(t *testing.T) {
				cfg, replyPort, replyTags, _, _, _ := routedEgressConfig(t, kind, "none")
				cfg.LAG = &lag.Config{LAGs: map[string]lag.LAG{"lag1": {
					Mode: lag.BalanceSLB, Members: map[string]lag.Member{"member-a": {}, "member-b": {}},
				}}}
				frame := ethernet.Frame{
					Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
					Tags:    []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
					Payload: makeIPv4Packet(t, ipH1, ipH2, 64, []byte("same flow")),
				}
				if path == "Neighbor Solicitation" {
					vrf := cfg.Routing.VRFs[routing.DefaultVRF]
					for name, prefix := range map[string]string{"vlan10": "2001:db8:10::1/64", "vlan20": "2001:db8:20::1/64"} {
						iface := vrf.Interfaces[name]
						iface.Prefixes = []netip.Prefix{netip.MustParsePrefix(prefix)}
						vrf.Interfaces[name] = iface
					}
					cfg.Routing.VRFs[routing.DefaultVRF] = vrf
					frame.EtherType = ethernet.EtherTypeIPv6
					frame.Payload = makeIPv6Packet(t, netip.MustParseAddr("2001:db8:10::7"), netip.MustParseAddr("2001:db8:20::7"), 64, []byte("same flow"))
				}
				sw := mustSwitch(t, cfg)
				held := sw.Receive(fixedTime, "1/1/1", frame)
				if held.Outcome != trace.Held || held.Hold == 0 || len(held.Emissions) != 1 {
					t.Fatalf("initial output = %+v, want held token and one solicitation", held)
				}
				first := held.Emissions[0]
				var second device.Emission
				switch path {
				case "ARP request", "Neighbor Solicitation":
					if !first.Protocol {
						t.Fatal("solicitation is not a protocol emission")
					}
					if path == "ARP request" {
						msg, err := arp.Decode(first.Frame)
						if err != nil || msg.Operation != arp.Request || msg.TargetAddr != ipH2 {
							t.Fatalf("ARP = %+v/%v, want request for %s", msg, err, ipH2)
						}
					} else {
						hdr, payload, err := ip.Decode(first.Frame.Payload)
						if err != nil {
							t.Fatal(err)
						}
						msg, err := ndp.Decode(hdr, payload)
						if err != nil || msg.Type != ndp.NeighborSolicitation || msg.Target != netip.MustParseAddr("2001:db8:20::7") {
							t.Fatalf("NDP = %+v/%v, want Neighbor Solicitation", msg, err)
						}
					}
					retry := sw.Advance(fixedTime.Add(time.Second))
					if len(retry.Emissions) != 1 {
						t.Fatalf("retry = %+v, want one emission", retry)
					}
					second = retry.Emissions[0]
				case "release", "unicast probe":
					reply := makeARPReply(t, ipH2, netip.MustParseAddr("10.0.20.1"), macH2, macRouter)
					reply.Tags = replyTags
					released := sw.Receive(fixedTime.Add(time.Millisecond), replyPort, reply)
					first = soleDataEmission(t, released.Emissions, macH2)
					if first.Protocol || first.Hold != held.Hold {
						t.Fatalf("release = %+v, want held token %d", first, held.Hold)
					}
					if path == "release" {
						live := sw.Receive(fixedTime.Add(2*time.Millisecond), "1/1/1", frame)
						if live.Outcome != trace.Forwarded || len(live.Egress) != 1 {
							t.Fatalf("live = %+v, want one forwarded egress", live)
						}
						second = device.Emission{Port: live.Egress[0].Port, Member: live.Egress[0].Member, Frame: live.Egress[0].Frame}
					} else {
						stale := sw.Receive(fixedTime.Add(31*time.Second), "1/1/1", frame)
						if stale.Outcome != trace.Forwarded {
							t.Fatalf("stale = %+v, want forwarded", stale)
						}
						probe := sw.Advance(fixedTime.Add(36 * time.Second))
						if len(probe.Emissions) != 1 {
							t.Fatalf("probe = %+v, want one emission", probe)
						}
						first = probe.Emissions[0]
						msg, err := arp.Decode(first.Frame)
						if err != nil || msg.Operation != arp.Request || first.Frame.Dst != macH2 || !first.Protocol {
							t.Fatalf("probe = %+v/%+v/%v, want unicast ARP", first, msg, err)
						}
						retry := sw.Advance(fixedTime.Add(37 * time.Second))
						if len(retry.Emissions) != 1 {
							t.Fatalf("probe retry = %+v, want one emission", retry)
						}
						second = retry.Emissions[0]
					}
				}
				if first.Port != "lag1" || first.Member != "member-a" || second.Port != "lag1" || second.Member != first.Member || second.Frame.Src != first.Frame.Src {
					t.Fatalf("first = %+v, second = %+v, want same flow on lag1/member-a", first, second)
				}
				cells++
			})
		}
	}
	if cells != 12 {
		t.Fatalf("emission cells = %d, want 12 reaching member expectations", cells)
	}
}

func TestHeldLAGEgressUsesSelectedMemberOnWire(t *testing.T) {
	cfg, _, _, _, _, _ := routedEgressConfig(t, "routed LAG", "none")
	vrf := cfg.Routing.VRFs[routing.DefaultVRF]
	iface := vrf.Interfaces["vlan20"]
	iface.VLAN = 20
	vrf.Interfaces["vlan20"] = iface
	cfg.Routing.VRFs[routing.DefaultVRF] = vrf
	cfg.LAG = &lag.Config{LAGs: map[string]lag.LAG{
		"lag1": {Mode: lag.BalanceSLB, Members: map[string]lag.Member{"member-a": {}, "member-b": {}}},
	}}
	target := cfg.Clone()
	bridgeCfg := cfg.Clone()
	cfg.Bridge = nil
	cfg.Routing = nil
	vrf = bridgeCfg.Routing.VRFs[routing.DefaultVRF]
	delete(vrf.Interfaces, "vlan20")
	bridgeCfg.Routing.VRFs[routing.DefaultVRF] = vrf
	bridgeCfg.Bridge.VLAN.Switchports["1/1/1"] = bridge.Switchport{Tagged: []vlan.ID{10, 20}}
	bridgeCfg.Bridge.VLAN.Switchports["lag1"] = bridge.Switchport{Tagged: []vlan.ID{20}}
	vid := vlan.ID(20)
	fab, err := assembly.New(assembly.Config{
		Start:    fixedTime,
		Switches: map[string]vswitch.Config{"sw": cfg},
		Hosts: map[string]hostmodel.Config{
			"source": {Address: macH1},
			"a":      {Address: macH2, VLAN: &vid, Accept: hostmodel.Accept{Promiscuous: true}},
			"b":      {Address: netaddr.MAC{2, 0, 0, 0, 0, 3}, VLAN: &vid, Accept: hostmodel.Accept{Promiscuous: true}},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw", Port: "1/1/1"}, B: fabric.Endpoint{Node: "source"}},
			{A: fabric.Endpoint{Node: "sw", Port: "member-a"}, B: fabric.Endpoint{Node: "a"}},
			{A: fabric.Endpoint{Node: "sw", Port: "member-b"}, B: fabric.Endpoint{Node: "b"}},
		},
		PhyAssumption: &cablemodel.PhyAssumption{Medium: cablemodel.TwistedPair, Ethernet: phy.Ethernet{
			SupportedSpeedsBPS:       []uint64{1_000_000_000},
			AutoNegotiationSupported: phy.CapabilitySupported, Setting: &phy.Setting{AutoNegotiation: true},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The routed selection uses VID zero. A later selection from the wire tag
	// would use a different, deliberately primed bucket.
	primeFrame := ethernet.Frame{Src: macRouter, Dst: netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, Payload: []byte("prime")}
	zero := fab.Device("sw").(*vswitch.Switch).Receive(fixedTime, "1/1/1", primeFrame)
	if len(zero.Egress) != 1 || zero.Egress[0].Member != "member-a" {
		t.Fatalf("VID zero egress = %+v, want member-a", zero.Egress)
	}
	if err := assembly.Configure(fab, "sw", bridgeCfg); err != nil {
		t.Fatal(err)
	}
	primeFrame.Tags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20}}
	tagged := fab.Device("sw").(*vswitch.Switch).Receive(fixedTime, "1/1/1", primeFrame)
	if len(tagged.Egress) != 1 || tagged.Egress[0].Member != "member-b" {
		t.Fatalf("VID 20 egress = %+v, want member-b", tagged.Egress)
	}
	if err := assembly.Configure(fab, "sw", target); err != nil {
		t.Fatal(err)
	}
	frame := ethernet.Frame{
		Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
		Tags:    []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10, PCP: 5}},
		Payload: makeIPv4Packet(t, ipH1, ipH2, 64, []byte("held member")),
	}
	heldID, err := fab.Inject(fabric.Injection{At: fixedTime, Origin: fabric.Endpoint{Node: "sw", Port: "1/1/1"}, Frame: frame})
	if err != nil {
		t.Fatal(err)
	}
	reply := makeARPReply(t, ipH2, netip.MustParseAddr("10.0.20.1"), macH2, macRouter)
	reply.Tags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20}}
	if _, err := fab.Inject(fabric.Injection{At: fixedTime.Add(time.Millisecond), Origin: fabric.Endpoint{Node: "sw", Port: "member-a"}, Frame: reply}); err != nil {
		t.Fatal(err)
	}
	fab.Run(50)
	if err := fab.Err(); err != nil {
		t.Fatal(err)
	}
	releases := 0
	for _, journey := range fab.Report() {
		if journey.FrameID != heldID {
			continue
		}
		releases++
		if len(journey.Deliveries) != 1 || journey.Deliveries[0].Host != "a" {
			t.Fatalf("release deliveries = %+v, want exactly one on member-a's host", journey.Deliveries)
		}
	}
	if releases != 1 {
		t.Fatalf("release journeys = %d, want 1", releases)
	}
}

func TestRoutedEgressMatrix(t *testing.T) {
	cells := 0
	for _, kind := range []string{"VLAN interface", "routed port", "sub-interface", "routed LAG"} {
		for _, binding := range []string{"none", "deferred ingress drop", "egress drop"} {
			t.Run(kind+"/"+binding, func(t *testing.T) {
				cfg, replyPort, replyTags, wantPort, wantMember, wantTags := routedEgressConfig(t, kind, binding)
				frame := ethernet.Frame{
					Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
					Tags:    []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10, PCP: 5, DEI: true}},
					Payload: makeIPv4Packet(t, ipH1, ipH2, 64, []byte{0x9c, 0x40, 0x01, 0xbb, 0, 8, 0, 0}),
				}
				held := mustSwitch(t, cfg)
				if kind == "VLAN interface" {
					mustSwitchLearn(t, held, []bridge.Seed{{FID: 20, MAC: macH2, Port: "1/1/2", Lifetime: bridge.Static}})
				}
				heldOut := held.Receive(fixedTime, "1/1/1", frame)
				if heldOut.Outcome != trace.Held {
					t.Fatalf("outcome = %s/%s, want held", heldOut.Outcome, heldOut.Reason)
				}
				reply := makeARPReply(t, ipH2, netip.MustParseAddr("10.0.20.1"), macH2, macRouter)
				reply.Tags = replyTags
				released := held.Receive(fixedTime.Add(time.Millisecond), replyPort, reply)

				liveCfg := cfg.Clone()
				vrf := liveCfg.Routing.VRFs[routing.DefaultVRF]
				vrf.Neighbors = []routing.Neighbor{{Interface: "vlan20", Addr: ipH2, MAC: macH2}}
				liveCfg.Routing.VRFs[routing.DefaultVRF] = vrf
				live := mustSwitch(t, liveCfg)
				if kind == "VLAN interface" {
					mustSwitchLearn(t, live, []bridge.Seed{{FID: 20, MAC: macH2, Port: "1/1/2", Lifetime: bridge.Static}})
				}
				out := live.Receive(fixedTime, "1/1/1", frame)
				if binding != "none" {
					if out.Outcome != trace.Dropped || out.Reason != filter.ReasonFilterDrop {
						t.Fatalf("live = %s/%s, want filter drop", out.Outcome, out.Reason)
					}
					if len(released.Drops) != 1 || len(released.Emissions) != 0 {
						t.Fatalf("release = %+v, want one drop and no emissions", released)
					}
					drop := released.Drops[0]
					if drop.Reason != out.Reason || drop.Port != "" || len(out.Egress) != 0 {
						t.Fatalf("released drop = %+v, live = %+v", drop, out)
					}
					if drop.Hold != heldOut.Hold || drop.Ingress != "vlan10" {
						t.Fatalf("drop identity = %d/%s, want %d/vlan10", drop.Hold, drop.Ingress, heldOut.Hold)
					}
					cells++
					return
				}
				if out.Outcome != trace.Forwarded || len(out.Egress) != 1 || len(released.Emissions) != 1 || len(released.Drops) != 0 {
					t.Fatalf("live = %+v, release = %+v, want one egress on both paths", out, released)
				}
				eg, em := out.Egress[0], released.Emissions[0]
				if em.Port != eg.Port || em.Member != eg.Member || em.PCP != eg.PCP || !slices.Equal(em.Frame.Tags, eg.Frame.Tags) || eg.Dropped != out.Reason {
					t.Fatalf("live egress = %+v, released = %+v", eg, em)
				}
				if eg.Port != wantPort || eg.Member != wantMember || eg.PCP != 5 || !slices.Equal(eg.Frame.Tags, wantTags) {
					t.Fatalf("egress = %+v, want port %s, member %s, PCP 5, tags %+v", eg, wantPort, wantMember, wantTags)
				}
				if em.Protocol || em.Hold != heldOut.Hold || em.Ingress != "vlan10" {
					t.Fatalf("release identity = %+v, want token %d and ingress vlan10", em, heldOut.Hold)
				}
				cells++
			})
		}
	}
	if cells != 12 {
		t.Fatalf("routed egress cells = %d, want 12", cells)
	}
	t.Logf("routed egress cells = %d", cells)
}

func routedEgressConfig(t *testing.T, kind, binding string) (vswitch.Config, string, []vlan.Tag, string, string, []vlan.Tag) {
	t.Helper()
	cfg := baseRoutingConfig(t)
	cfg.Bridge.VLAN.Switchports["1/1/1"] = bridge.Switchport{Tagged: []vlan.ID{10}}
	iface := routing.Interface{MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}}
	replyPort, wantPort, wantMember := "1/1/2", "1/1/2", ""
	var replyTags, wantTags []vlan.Tag
	switch kind {
	case "VLAN interface":
		iface.VLAN = 20
		cfg.Bridge.VLAN.Switchports["1/1/2"] = bridge.Switchport{Tagged: []vlan.ID{20}}
		replyTags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20}}
		wantTags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20, PCP: 5, DEI: true}}
	case "routed port":
		iface.Port = "1/1/2"
		delete(cfg.Bridge.VLAN.Switchports, "1/1/2")
	case "sub-interface":
		iface.Port, iface.VLAN = "1/1/2", 20
		delete(cfg.Bridge.VLAN.Switchports, "1/1/2")
		replyTags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20}}
		wantTags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20, PCP: 5, DEI: true}}
	case "routed LAG", "sub-interface on a LAG", "VLAN interface on a LAG":
		cfg.Ports = mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "member-a", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "member-b", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}))
		delete(cfg.Bridge.VLAN.Switchports, "1/1/2")
		iface.Port = "lag1"
		replyPort, wantPort, wantMember = "member-a", "lag1", "member-a"
		if kind != "routed LAG" {
			iface.VLAN = 20
			replyTags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20}}
			wantTags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20, PCP: 5, DEI: true}}
		}
		if kind == "VLAN interface on a LAG" {
			iface.Port = ""
			cfg.Bridge.VLAN.Switchports["lag1"] = bridge.Switchport{Tagged: []vlan.ID{20}}
		}
	default:
		t.Fatalf("unknown egress kind %s", kind)
	}
	vrf := cfg.Routing.VRFs[routing.DefaultVRF]
	vrf.Neighbors = nil
	vrf.Interfaces["vlan20"] = iface
	cfg.Routing.VRFs[routing.DefaultVRF] = vrf
	if binding != "none" {
		cfg.Filter = &filter.Config{
			Sets: map[string]filter.RuleSet{
				"deferred": {Stateful: true, Default: filter.Drop},
				"refuse":   {Stateful: true, Default: filter.Drop},
			},
		}
		if binding == "deferred ingress drop" {
			cfg.Filter.Bindings = []filter.Binding{
				{Interface: "vlan10", Direction: filter.In, Set: "deferred"},
				{Interface: "vlan20", Direction: filter.In, Set: "refuse"},
			}
		} else {
			cfg.Filter.Bindings = []filter.Binding{{Interface: "vlan20", Direction: filter.Out, Set: "refuse"}}
		}
	}
	return cfg, replyPort, replyTags, wantPort, wantMember, wantTags
}
