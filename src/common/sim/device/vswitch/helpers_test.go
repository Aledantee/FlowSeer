package vswitch_test

import (
	"fmt"
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/mld"
	"go.aledante.io/FlowSeer/src/common/net/ndp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

var fixedTime = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func mustTable(t *testing.T, b *port.Builder) port.Table {
	t.Helper()
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	return tbl
}

func mustEncode(t *testing.T, b bpdu.BPDU, src netaddr.MAC) ethernet.Frame {
	t.Helper()

	frame, err := bpdu.Encode(b, src)
	if err != nil {
		t.Fatalf("stp.Encode: %v", err)
	}
	return frame
}

func mustSwitch(t *testing.T, cfg vswitch.Config) *vswitch.Switch {
	t.Helper()
	sw, err := vswitch.New(cfg)
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}
	return sw
}

func mustSwitchLearn(t *testing.T, sw *vswitch.Switch, seeds []bridge.Seed) {
	t.Helper()
	if err := sw.Learn(seeds); err != nil {
		t.Fatalf("Learn: %v", err)
	}
}

func makeComparePair(t *testing.T) (vswitch.Config, vswitch.Config, *vswitch.Switch, *vswitch.Switch) {
	t.Helper()

	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	vlanTable := map[vlan.ID]string{
		10: "vlan10",
		20: "vlan20",
	}

	pvid10 := vlan.ID(10)
	pvid20 := vlan.ID(20)

	curCfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: vlanTable,
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &pvid10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &pvid10, Untagged: []vlan.ID{10}},
					"1/1/3": {PVID: &pvid10, Untagged: []vlan.ID{10}},
				},
			},
		},
	}

	expCfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: vlanTable,
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &pvid10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &pvid20, Untagged: []vlan.ID{20}},
					"1/1/3": {PVID: &pvid10, Untagged: []vlan.ID{10}},
				},
			},
		},
	}

	return curCfg, expCfg, mustSwitch(t, curCfg), mustSwitch(t, expCfg)
}

func loopProtectTestSwitch(t *testing.T) *vswitch.Switch {
	t.Helper()

	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	return mustSwitch(t, vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual}},
			},
		},
	})
}

// mustEncodeProbe encodes p for a frame sent from src and fails the test on
// an encode error.
func mustEncodeProbe(t *testing.T, p loopprotect.Probe, src netaddr.MAC) ethernet.Frame {
	t.Helper()

	f, err := loopprotect.Encode(p, src)
	if err != nil {
		t.Fatalf("loopprotect.Encode(%+v): %v", p, err)
	}

	return f
}

func makeIPv4Packet(t *testing.T, src, dst netip.Addr, ttl uint8, payload []byte) []byte {
	t.Helper()
	hdr := ip.Header{
		Src:      src,
		Dst:      dst,
		HopLimit: ttl,
		Protocol: 17,
		V4:       &ip.V4{},
	}
	pkt, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv4 packet: %v", err)
	}
	return pkt
}

func makeIPv6Packet(t *testing.T, src, dst netip.Addr, hopLimit uint8, payload []byte) []byte {
	t.Helper()
	hdr := ip.Header{
		Src:      src,
		Dst:      dst,
		HopLimit: hopLimit,
		Protocol: 17,
		V6:       &ip.V6{},
	}
	pkt, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv6 packet: %v", err)
	}
	return pkt
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

var (
	macRouter = netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1     = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	macH2     = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	ipH1      = netip.MustParseAddr("10.0.10.7")
	ipH2      = netip.MustParseAddr("10.0.20.7")
	ipR1      = netip.MustParseAddr("10.0.10.1")
)

func buildBaseRoutingSwitch(t *testing.T) *vswitch.Switch {
	t.Helper()
	return mustSwitch(t, baseRoutingConfig(t))
}

func baseRoutingConfig(t *testing.T) vswitch.Config {
	t.Helper()
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

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
	}
	return cfg
}

var (
	mcastHostMAC   = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	mcastRouterMAC = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0xfe}
	mcastGroupMAC  = netaddr.MAC{0x01, 0x00, 0x5e, 0x01, 0x01, 0x01}
	allHostsMAC    = netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x01}
)

func mcastSwitchConfig(t *testing.T, floodUnregistered *bool) vswitch.Config {
	t.Helper()

	pvid := vlan.ID(10)
	ports := mustTable(t, port.NewBuilder().Range("1/1/%d", 1, 4, port.Port{
		Kind:        port.Physical,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
	}))
	switchports := make(map[string]bridge.Switchport, 4)
	for i := 1; i <= 4; i++ {
		switchports[fmt.Sprintf("1/1/%d", i)] = bridge.Switchport{PVID: &pvid, Untagged: []vlan.ID{10}}
	}

	return vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table:       map[vlan.ID]string{10: "ten"},
			Switchports: switchports,
		}},
		Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			10: {FloodUnregistered: floodUnregistered},
		}},
	}
}

func makeIGMPControlFrame(t *testing.T, src, dst netip.Addr, srcMAC, dstMAC netaddr.MAC, ttl uint8, message igmp.Message) ethernet.Frame {
	t.Helper()

	payload, err := igmp.Encode(message)
	if err != nil {
		t.Fatalf("encode IGMP: %v", err)
	}
	hdr := ip.Header{Src: src, Dst: dst, HopLimit: ttl, Protocol: 2, V4: &ip.V4{}}
	packet, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv4: %v", err)
	}

	return ethernet.Frame{Dst: dstMAC, Src: srcMAC, EtherType: ethernet.EtherTypeIPv4, Payload: packet}
}

func makeMLDControlFrame(t *testing.T, src, dst netip.Addr, srcMAC, dstMAC netaddr.MAC, hopLimit uint8, routerAlert bool, message mld.Message) ethernet.Frame {
	t.Helper()

	hdr := ip.Header{Src: src, Dst: dst, HopLimit: hopLimit, Protocol: 0, V6: &ip.V6{}}
	payload, err := mld.Encode(hdr, message)
	if err != nil {
		t.Fatalf("encode MLD: %v", err)
	}
	hopByHop := []byte{58, 0, 0, 0, 0, 0, 0, 0}
	if routerAlert {
		hopByHop = []byte{58, 0, 5, 2, 0, 0, 1, 0}
	}
	packet, err := hdr.Encode(append(hopByHop, payload...))
	if err != nil {
		t.Fatalf("encode IPv6: %v", err)
	}

	return ethernet.Frame{Dst: dstMAC, Src: srcMAC, EtherType: ethernet.EtherTypeIPv6, Payload: packet}
}

func hasIssueCode(issues []analysis.Issue, code analysis.IssueCode) bool {
	return slices.ContainsFunc(issues, func(issue analysis.Issue) bool { return issue.Code == code })
}

func findStep(steps []trace.Step, ruleID trace.RuleID) (trace.Step, bool) {
	for _, step := range steps {
		if step.RuleID == ruleID {
			return step, true
		}
	}

	return trace.Step{}, false
}

// makeARPReply builds an ARP reply mapping senderIP to senderMAC, addressed
// to dstMAC the way a reply confirming a forward path is: the frame goes
// straight to the requester rather than to the broadcast address a request
// would use.
func makeARPReply(t *testing.T, senderIP, targetIP netip.Addr, senderMAC, dstMAC netaddr.MAC) ethernet.Frame {
	t.Helper()
	msg := arp.Message{
		HardwareType: 1,
		ProtocolType: 0x0800,
		Operation:    arp.Reply,
		SenderMAC:    senderMAC,
		SenderAddr:   senderIP,
		TargetMAC:    dstMAC,
		TargetAddr:   targetIP,
	}
	frame, err := arp.Encode(msg, dstMAC)
	if err != nil {
		t.Fatalf("encode ARP reply: %v", err)
	}
	return frame
}

// holdFrameToward forwards a frame from 1/1/1 toward dst and asserts it was held.
func holdFrameToward(t *testing.T, sw *vswitch.Switch, at time.Time, dst netip.Addr, payload []byte) device.Output {
	t.Helper()
	pkt := makeIPv4Packet(t, ipH1, dst, 64, payload)
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}
	res := sw.Receive(at, "1/1/1", frame)
	if res.Outcome != trace.Held {
		t.Fatalf("outcome for %s = %v, want Held", dst, res.Outcome)
	}
	return res
}

// soleDataEmission returns the one emission addressed to dst, failing when there is not exactly
// one: a Receive that observes an advertisement also relays the advertisement itself.
func soleDataEmission(t *testing.T, emissions []device.Emission, dst netaddr.MAC) device.Emission {
	t.Helper()
	var found []device.Emission
	for _, em := range emissions {
		if em.Frame.Dst == dst {
			found = append(found, em)
		}
	}
	if len(found) != 1 {
		t.Fatalf("emissions to %s = %d, want exactly 1: %+v", dst, len(found), emissions)
	}
	return found[0]
}

// dataAfterSolicitations checks protocol frames separately from ordinary releases.
func dataAfterSolicitations(t *testing.T, emissions []device.Emission, count int) []device.Emission {
	t.Helper()
	var data []device.Emission
	requests := 0
	for _, em := range emissions {
		if !em.Protocol {
			data = append(data, em)
			continue
		}
		if em.PCP != 0 {
			t.Errorf("solicitation PCP = %d, want 0", em.PCP)
		}
		switch em.Frame.EtherType {
		case ethernet.EtherTypeARP:
			msg, err := arp.Decode(em.Frame)
			if err != nil || msg.Operation != arp.Request {
				t.Fatalf("protocol frame = %+v, error = %v, want ARP request", msg, err)
			}
		case ethernet.EtherTypeIPv6:
			hdr, body, err := ip.Decode(em.Frame.Payload)
			if err != nil {
				t.Fatal(err)
			}
			msg, err := ndp.Decode(hdr, body)
			if err != nil || msg.Type != ndp.NeighborSolicitation {
				t.Fatalf("protocol frame = %+v, error = %v, want solicitation", msg, err)
			}
		default:
			t.Fatalf("unexpected protocol frame: %+v", em)
		}
		requests++
	}
	if requests != count {
		t.Fatalf("solicitations = %d, want %d: %+v", requests, count, emissions)
	}
	return data
}

// captureOutput keeps the outputs a fixture needs to inspect across several calls.
func captureOutput(callOutputs map[*vswitch.Switch]device.Output, sw *vswitch.Switch, out device.Output) device.Output {
	prior := callOutputs[sw]
	prior.Emissions = append(prior.Emissions, out.Emissions...)
	prior.Drops = append(prior.Drops, out.Drops...)
	prior.Copies = out.Copies
	callOutputs[sw] = prior
	return out
}

func captureLinkOutput(t *testing.T, callOutputs map[*vswitch.Switch]device.Output, sw *vswitch.Switch, now time.Time, name string, state port.LinkState) {
	t.Helper()
	out, err := sw.LinkChange(now, name, device.Link{State: state, Duplex: device.DuplexUnknown, PeerRelays: false, SpeedBPS: 0})
	if err != nil {
		t.Fatal(err)
	}
	captureOutput(callOutputs, sw, out)
}

func takeEmissions(callOutputs map[*vswitch.Switch]device.Output, sw *vswitch.Switch) []device.Emission {
	out := callOutputs[sw]
	emissions := out.Emissions
	out.Emissions = nil
	callOutputs[sw] = out
	return emissions
}

func takeCopies(callOutputs map[*vswitch.Switch]device.Output, sw *vswitch.Switch) []device.Copy {
	out := callOutputs[sw]
	copies := out.Copies
	out.Copies = nil
	callOutputs[sw] = out
	return copies
}

func takeDrops(callOutputs map[*vswitch.Switch]device.Output, sw *vswitch.Switch) []device.Drop {
	out := callOutputs[sw]
	drops := out.Drops
	out.Drops = nil
	callOutputs[sw] = out
	return drops
}
