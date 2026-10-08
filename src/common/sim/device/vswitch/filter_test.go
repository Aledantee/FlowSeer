package vswitch

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/tcp"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

var (
	fixedTime = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	macRouter = netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1     = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	macH2     = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	ipH1      = netip.MustParseAddr("10.0.10.7")
	ipH2      = netip.MustParseAddr("10.0.20.7")
)

func makeTCPPayload(srcPort, dstPort uint16, flags tcp.Flags, data []byte) []byte {
	seg := make([]byte, 20+len(data))
	binary.BigEndian.PutUint16(seg[0:2], srcPort)
	binary.BigEndian.PutUint16(seg[2:4], dstPort)
	seg[12] = 0x50
	seg[13] = byte(flags)
	copy(seg[20:], data)
	return seg
}

func heldFilterConfig(t *testing.T, policy *filter.Config) Config {
	t.Helper()
	p10, p20 := vlan.ID(10), vlan.ID(20)
	return Config{
		Ports: mustInternalTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
			},
		}},
		Routing: &routing.Config{VRFs: map[string]routing.VRF{
			routing.DefaultVRF: {Interfaces: map[string]routing.Interface{
				"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
				"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
			}},
		}},
		Filter: policy,
	}
}

func heldFilterFrame(t *testing.T) ethernet.Frame {
	t.Helper()
	return ethernet.Frame{
		Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
		Payload: makeIPv4PacketWithProto(t, ipH1, ipH2, 6, makeTCPPayload(40000, 443, tcp.SYN, []byte("held"))),
	}
}

func heldFilterReply(t *testing.T) ethernet.Frame {
	t.Helper()
	frame, err := arp.Encode(arp.Message{
		HardwareType: 1, ProtocolType: 0x0800, Operation: arp.Reply,
		SenderMAC: macH2, SenderAddr: ipH2, TargetMAC: macRouter,
		TargetAddr: netip.MustParseAddr("10.0.20.1"),
	}, macRouter)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func assertHeldFilterDrop(t *testing.T, out Output, iface string) {
	t.Helper()
	if len(out.Drops) != 1 || out.Drops[0].Reason != filter.ReasonFilterDrop {
		t.Fatalf("drops = %+v, want one filter refusal", out.Drops)
	}
	if out.Drops[0].Step.Layer != filter.LayerName || out.Drops[0].Step.Subject.Key != iface {
		t.Fatalf("drop step = %+v, want filter on %s", out.Drops[0].Step, iface)
	}
	if steps := out.Drops[0].Steps; len(steps) != 1 || steps[0].Layer != filter.LayerName || steps[0].Subject.Key != iface || len(steps[0].Inputs) == 0 || len(steps[0].Outputs) == 0 {
		t.Fatalf("drop steps = %+v, want the filter's complete refusal", steps)
	}
	for _, em := range out.Emissions {
		if !em.Protocol {
			t.Fatalf("refused held frame emitted: %+v", em)
		}
	}
}

func TestHeldFrameFiltersAtRelease(t *testing.T) {
	for _, direction := range []filter.Direction{filter.In, filter.Out} {
		t.Run(string(direction), func(t *testing.T) {
			policy := &filter.Config{
				Sets: map[string]filter.RuleSet{
					"deferred": {Stateful: true, Default: filter.Drop},
					"refuse": {Stateful: true, Default: filter.Drop, Rules: []filter.Rule{{
						Name: "unrelated-reverse", Action: filter.Accept,
						Match: filter.Match{DstPorts: []filter.PortRange{{Start: 123, End: 123}}},
					}}},
				},
				Bindings: []filter.Binding{{Interface: "vlan20", Direction: direction, Set: "refuse"}},
			}
			if direction == filter.In {
				policy.Bindings = append(policy.Bindings, filter.Binding{Interface: "vlan10", Direction: filter.In, Set: "deferred"})
			}
			sw := mustInternalSwitch(t, heldFilterConfig(t, policy))
			if out := sw.Receive(fixedTime, "1/1/1", heldFilterFrame(t)); out.Outcome != trace.Held {
				t.Fatalf("outcome = %s, want held", out.Outcome)
			}
			out := sw.Receive(fixedTime.Add(time.Millisecond), "1/1/2", heldFilterReply(t))
			iface := "vlan20"
			if direction == filter.In {
				iface = "vlan10"
			}
			assertHeldFilterDrop(t, out, iface)
		})
	}
}

func TestHeldFrameMeetsDerivedIngressFilter(t *testing.T) {
	policy := &filter.Config{
		Sets:     map[string]filter.RuleSet{"inbound": {Default: filter.Accept}},
		Bindings: []filter.Binding{{Interface: "vlan10", Direction: filter.In, Set: "inbound"}},
	}
	sw := mustInternalSwitch(t, heldFilterConfig(t, policy))
	if out := sw.Receive(fixedTime, "1/1/1", heldFilterFrame(t)); out.Outcome != trace.Held {
		t.Fatalf("outcome = %s, want held", out.Outcome)
	}
	cfg := sw.Config()
	cfg.Filter.Sets["inbound"] = filter.RuleSet{Default: filter.Drop}
	next, out, err := Derive(sw, ConstructionSpec{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if !next.Retention().Routing.Kept || len(out.Drops) != 0 || len(out.Emissions) != 0 {
		t.Fatalf("routing retention = %+v, output = %+v", next.Retention().Routing, out)
	}
	assertHeldFilterDrop(t, next.Receive(fixedTime.Add(time.Millisecond), "1/1/2", heldFilterReply(t)), "vlan10")
}

func TestOriginatedHeldFrameMeetsEgressFilterAlone(t *testing.T) {
	for _, action := range []filter.Action{filter.Accept, filter.Drop} {
		t.Run(string(action), func(t *testing.T) {
			policy := &filter.Config{
				Sets: map[string]filter.RuleSet{
					"inbound": {Default: filter.Drop}, "outbound": {Default: action},
				},
				Bindings: []filter.Binding{
					{Interface: "vlan20", Direction: filter.In, Set: "inbound"},
					{Interface: "vlan20", Direction: filter.Out, Set: "outbound"},
				},
			}
			sw := mustInternalSwitch(t, heldFilterConfig(t, policy))
			held := sw.routing.Originate(fixedTime, routing.DefaultVRF, ipH2, 17, []byte("originated"), true)
			if held.Outcome() != trace.Held {
				t.Fatalf("outcome = %s, want held", held.Outcome())
			}
			// Setup uses the switch-owned layer. Only the switch's public output is asserted.
			out := sw.Receive(fixedTime.Add(time.Millisecond), "1/1/2", heldFilterReply(t))
			if action == filter.Drop {
				assertHeldFilterDrop(t, out, "vlan20")
				return
			}
			if len(out.Drops) != 0 || len(out.Emissions) != 1 || out.Emissions[0].Protocol || out.Emissions[0].Port != "1/1/2" {
				t.Fatalf("originated output = %+v, want one data emission without ingress refusal", out)
			}
		})
	}
}

func makeIPv4PacketWithProto(t *testing.T, src, dst netip.Addr, proto uint8, payload []byte) []byte {
	t.Helper()
	hdr := ip.Header{
		Src:      src,
		Dst:      dst,
		HopLimit: 64,
		Protocol: proto,
		V4:       &ip.V4{},
	}
	pkt, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv4 packet: %v", err)
	}
	return pkt
}

func TestFilterIngressDropIsCompleteOutcomeAndSkipsRouting(t *testing.T) {
	ports := mustInternalTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	cfg := Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
					},
				},
			},
		},
		Filter: &filter.Config{
			Sets: map[string]filter.RuleSet{
				"drop-all": {
					Default: filter.Drop,
				},
			},
			Bindings: []filter.Binding{
				{Interface: "vlan10", Direction: filter.In, Set: "drop-all"},
			},
		},
	}

	sw := mustInternalSwitch(t, cfg)
	pkt := makeIPv4PacketWithProto(t, ipH1, ipH2, 17, []byte("payload"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Receive(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Dropped {
		t.Fatalf("Outcome = %v, want %v", res.Outcome, trace.Dropped)
	}
	if res.Reason != filter.ReasonFilterDrop {
		t.Fatalf("Reason = %v, want %v", res.Reason, filter.ReasonFilterDrop)
	}
	if res.Metadata.Status() != analysis.Complete {
		t.Fatalf("Status = %v, want Complete", res.Metadata.Status())
	}
	for _, step := range res.Steps {
		if step.Layer == routing.LayerName {
			t.Errorf("found routing step %v in trace; want routing to be skipped", step)
		}
	}
}

func TestFilterEgressBindingAppliesToForwardedFramesOnly(t *testing.T) {
	ports := mustInternalTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	cfg := Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
					},
				},
			},
		},
		Filter: &filter.Config{
			Sets: map[string]filter.RuleSet{
				"egress-drop": {
					Default: filter.Drop,
				},
			},
			Bindings: []filter.Binding{
				{Interface: "vlan10", Direction: filter.Out, Set: "egress-drop"},
				{Interface: "vlan20", Direction: filter.Out, Set: "egress-drop"},
			},
		},
	}

	sw := mustInternalSwitch(t, cfg)

	// 1. Packet arriving on vlan10 destined to router's own IP on vlan10 (10.0.10.1) -> Consumed, NOT dropped by vlan10 Out!
	routerIP := netip.MustParseAddr("10.0.10.1")
	localPkt := makeIPv4PacketWithProto(t, ipH1, routerIP, 17, []byte("to router"))
	localFrame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   localPkt,
	}
	resLocal := sw.Receive(fixedTime, "1/1/1", localFrame)
	if resLocal.Outcome != trace.Consumed {
		t.Fatalf("local frame Outcome = %v, want %v", resLocal.Outcome, trace.Consumed)
	}

	// 2. Forwarded packet arriving on vlan10 destined to 10.0.20.7 on vlan20 -> Egress binding on vlan20 Out drops it!
	fwdPkt := makeIPv4PacketWithProto(t, ipH1, ipH2, 17, []byte("to h2"))
	fwdFrame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   fwdPkt,
	}
	resFwd := sw.Receive(fixedTime, "1/1/1", fwdFrame)
	if resFwd.Outcome != trace.Dropped {
		t.Fatalf("forwarded frame Outcome = %v, want %v", resFwd.Outcome, trace.Dropped)
	}
	if resFwd.Reason != filter.ReasonFilterDrop {
		t.Fatalf("forwarded frame Reason = %v, want %v", resFwd.Reason, filter.ReasonFilterDrop)
	}
}

func TestFilterStatefulReplyForwardsEndToEnd(t *testing.T) {
	ports := mustInternalTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ipClient := netip.MustParseAddr("10.0.10.7")
	ipServer := netip.MustParseAddr("10.0.20.5")

	protoTCP := uint8(6)
	cfg := Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan10", Addr: ipClient, MAC: macH1},
						{Interface: "vlan20", Addr: ipServer, MAC: macH2},
					},
				},
			},
		},
		Filter: &filter.Config{
			Sets: map[string]filter.RuleSet{
				"lan-in": {
					Stateful: true,
					Default:  filter.Drop,
					Rules: []filter.Rule{
						{
							Name: "allow-https",
							Match: filter.Match{
								Protocol: &protoTCP,
								Src:      []netip.Prefix{netip.MustParsePrefix("10.0.10.0/24")},
								Dst:      []netip.Prefix{netip.MustParsePrefix("10.0.20.5/32")},
								DstPorts: []filter.PortRange{{Start: 443, End: 443}},
							},
							Action: filter.Accept,
						},
					},
				},
				"srv-in": {
					Stateful: true,
					Default:  filter.Drop,
					Rules:    nil,
				},
			},
			Bindings: []filter.Binding{
				{Interface: "vlan10", Direction: filter.In, Set: "lan-in"},
				{Interface: "vlan20", Direction: filter.In, Set: "srv-in"},
			},
		},
	}

	sw := mustInternalSwitch(t, cfg)
	if err := sw.Learn([]bridge.Seed{
		{FID: 10, MAC: macH1, Port: "1/1/1", Lifetime: bridge.Static},
		{FID: 20, MAC: macH2, Port: "1/1/2", Lifetime: bridge.Static},
	}); err != nil {
		t.Fatalf("Learn: %v", err)
	}

	// Server sends reply arriving on vlan20 (port 1/1/2): 10.0.20.5:443 -> 10.0.10.7:40000
	tcpPayload := makeTCPPayload(443, 40000, tcp.ACK, []byte("https response"))
	replyPkt := makeIPv4PacketWithProto(t, ipServer, ipClient, 6, tcpPayload)
	replyFrame := ethernet.Frame{
		Src:       macH2,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   replyPkt,
	}

	res := sw.Receive(fixedTime, "1/1/2", replyFrame)
	if res.Outcome != trace.Forwarded {
		t.Fatalf("reply Outcome = %v (reason: %v), want Forwarded", res.Outcome, res.Reason)
	}

	var stateStep *trace.Step
	for _, st := range res.Steps {
		if st.RuleID == filter.RuleState {
			stateStep = &st
			break
		}
	}
	if stateStep == nil {
		t.Fatalf("trace did not contain filter.state step; steps were: %+v", res.Steps)
	}
	if stateStep.Subject.Key != "vlan20" {
		t.Errorf("state step Subject.Key = %q, want vlan20", stateStep.Subject.Key)
	}
}
