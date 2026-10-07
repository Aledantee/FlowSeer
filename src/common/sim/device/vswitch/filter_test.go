package vswitch_test

import (
	"encoding/binary"
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/tcp"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
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

func makeIPv4PacketWithProto(t *testing.T, src, dst netip.Addr, proto uint8, ttl uint8, payload []byte) []byte {
	t.Helper()
	hdr := ip.Header{
		Src:      src,
		Dst:      dst,
		HopLimit: ttl,
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
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	cfg := vswitch.Config{
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

	sw := mustSwitch(t, cfg)
	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("payload"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Forward(fixedTime, "1/1/1", frame)
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
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	cfg := vswitch.Config{
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

	sw := mustSwitch(t, cfg)

	// 1. Packet arriving on vlan10 destined to router's own IP on vlan10 (10.0.10.1) -> Consumed, NOT dropped by vlan10 Out!
	routerIP := netip.MustParseAddr("10.0.10.1")
	localPkt := makeIPv4Packet(t, ipH1, routerIP, 64, []byte("to router"))
	localFrame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   localPkt,
	}
	resLocal := sw.Forward(fixedTime, "1/1/1", localFrame)
	if resLocal.Outcome != trace.Consumed {
		t.Fatalf("local frame Outcome = %v, want %v", resLocal.Outcome, trace.Consumed)
	}

	// 2. Forwarded packet arriving on vlan10 destined to 10.0.20.7 on vlan20 -> Egress binding on vlan20 Out drops it!
	fwdPkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("to h2"))
	fwdFrame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   fwdPkt,
	}
	resFwd := sw.Forward(fixedTime, "1/1/1", fwdFrame)
	if resFwd.Outcome != trace.Dropped {
		t.Fatalf("forwarded frame Outcome = %v, want %v", resFwd.Outcome, trace.Dropped)
	}
	if resFwd.Reason != filter.ReasonFilterDrop {
		t.Fatalf("forwarded frame Reason = %v, want %v", resFwd.Reason, filter.ReasonFilterDrop)
	}
}

func TestFilterStatefulReplyForwardsEndToEnd(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ipClient := netip.MustParseAddr("10.0.10.7")
	ipServer := netip.MustParseAddr("10.0.20.5")

	protoTCP := uint8(6)
	cfg := vswitch.Config{
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

	sw := mustSwitch(t, cfg)
	mustSwitchLearn(t, sw, []bridge.Seed{
		{FID: 10, MAC: macH1, Port: "1/1/1", Lifetime: bridge.Static},
		{FID: 20, MAC: macH2, Port: "1/1/2", Lifetime: bridge.Static},
	})

	// Server sends reply arriving on vlan20 (port 1/1/2): 10.0.20.5:443 -> 10.0.10.7:40000
	tcpPayload := makeTCPPayload(443, 40000, tcp.ACK, []byte("https response"))
	replyPkt := makeIPv4PacketWithProto(t, ipServer, ipClient, 6, 64, tcpPayload)
	replyFrame := ethernet.Frame{
		Src:       macH2,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   replyPkt,
	}

	res := sw.Forward(fixedTime, "1/1/2", replyFrame)
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
