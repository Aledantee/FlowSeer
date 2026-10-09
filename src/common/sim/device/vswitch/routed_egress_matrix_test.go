package vswitch_test

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

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
	vid := vlan.ID(20)
	fab, err := fabric.New(fabric.Config{
		Start:    fixedTime,
		Switches: map[string]vswitch.Config{"sw": cfg},
		Hosts: map[string]fabric.Host{
			"source": {Address: macH1},
			"a":      {Address: macH2, VLAN: &vid, Accept: fabric.HostAccept{Promiscuous: true}},
			"b":      {Address: netaddr.MAC{2, 0, 0, 0, 0, 3}, VLAN: &vid, Accept: fabric.HostAccept{Promiscuous: true}},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw", Port: "1/1/1"}, B: fabric.Endpoint{Node: "source"}},
			{A: fabric.Endpoint{Node: "sw", Port: "member-a"}, B: fabric.Endpoint{Node: "a"}},
			{A: fabric.Endpoint{Node: "sw", Port: "member-b"}, B: fabric.Endpoint{Node: "b"}},
		},
		PhyAssumption: &fabric.PhyAssumption{Medium: fabric.TwistedPair, Ethernet: phy.Ethernet{
			SupportedSpeedsBPS:       []uint64{1_000_000_000},
			AutoNegotiationSupported: phy.CapabilitySupported, Setting: &phy.Setting{AutoNegotiation: true},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sw := fab.Switch("sw")
	// The routed selection uses VID zero. A later selection from the wire tag
	// would use a different, deliberately primed bucket.
	for _, prime := range []struct {
		vid    vlan.ID
		member string
	}{{0, "member-a"}, {20, "member-b"}} {
		member, ok := sw.SelectMember(fixedTime, "lag1", ethernet.Frame{Src: macRouter}, prime.vid)
		if !ok || member != prime.member {
			t.Fatalf("selection for VID %d = %s/%v, want %s", prime.vid, member, ok, prime.member)
		}
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
		if journey.Origin.Kind != fabric.OriginRelease || journey.Origin.Of != heldID {
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
	case "routed LAG":
		cfg.Ports = mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "member-a", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "member-b", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}))
		delete(cfg.Bridge.VLAN.Switchports, "1/1/2")
		iface.Port = "lag1"
		replyPort, wantPort, wantMember = "member-a", "lag1", "member-a"
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
