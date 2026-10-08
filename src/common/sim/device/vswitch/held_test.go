package vswitch_test

import (
	"bytes"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/mld"
	"go.aledante.io/FlowSeer/src/common/net/ndp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func neighborIngressSwitch(t *testing.T, ipv6 bool) *vswitch.Switch {
	t.Helper()
	cfg := baseRoutingConfig(t)
	cfg.Ports = mustTable(t, port.NewBuilder().Range("1/1/%d", 1, 3, port.Port{
		Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up,
	}))
	p10 := vlan.ID(10)
	for _, name := range []string{"1/1/2", "1/1/3"} {
		cfg.Bridge.VLAN.Switchports[name] = bridge.Switchport{PVID: &p10, Untagged: []vlan.ID{10}}
	}
	vrf := cfg.Routing.VRFs[routing.DefaultVRF]
	vrf.Neighbors = nil
	if ipv6 {
		iface := vrf.Interfaces["vlan10"]
		iface.Prefixes = []netip.Prefix{netip.MustParsePrefix("2001:db8:10::1/64")}
		vrf.Interfaces["vlan10"] = iface
	}
	cfg.Routing.VRFs[routing.DefaultVRF] = vrf
	return mustSwitch(t, cfg)
}

func TestVLANNeighborLearningPrecedesAnswer(t *testing.T) {
	concrete := 0
	for _, family := range []string{"ARP", "NDP"} {
		t.Run(family, func(t *testing.T) {
			sw := neighborIngressSwitch(t, family == "NDP")
			var frame ethernet.Frame
			if family == "ARP" {
				var err error
				frame, err = arp.Encode(arp.Message{
					HardwareType: 1, ProtocolType: 0x0800,
					Operation: arp.Request, SenderMAC: macH1, SenderAddr: ipH1,
					TargetAddr: netip.MustParseAddr("10.0.10.1"),
				}, netaddr.MAC{255, 255, 255, 255, 255, 255})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				frame = makeNDPFrame(t, netip.MustParseAddr("2001:db8:10::11"), netip.MustParseAddr("ff02::1:ff00:1"),
					macH1, netaddr.MAC{0x33, 0x33, 0xff, 0, 0, 1}, 255,
					ndp.Message{
						Type: ndp.NeighborSolicitation, Target: netip.MustParseAddr("2001:db8:10::1"),
						HasLinkLayerAddr: true, LinkLayerAddr: macH1,
					})
			}
			out := sw.Receive(fixedTime, "1/1/1", frame)
			if len(out.Emissions) != 1 || out.Emissions[0].Port != "1/1/1" || out.Emissions[0].Frame.Dst != macH1 {
				t.Fatalf("answer = %+v, want one unicast to the learned ingress", out.Emissions)
			}
			if family == "ARP" {
				answer, err := arp.Decode(out.Emissions[0].Frame)
				if err != nil || answer.Operation != arp.Reply || answer.TargetAddr != ipH1 {
					t.Fatalf("ARP answer = %+v/%v, want reply to source", answer, err)
				}
			} else {
				hdr, body, err := ip.Decode(out.Emissions[0].Frame.Payload)
				if err != nil {
					t.Fatal(err)
				}
				answer, err := ndp.Decode(hdr, body)
				if err != nil || answer.Type != ndp.NeighborAdvertisement || answer.Target != netip.MustParseAddr("2001:db8:10::1") {
					t.Fatalf("NDP answer = %+v/%v, want advertisement for interface", answer, err)
				}
			}
			if out.Outcome != trace.Flooded || len(out.Egress) != 2 || out.Egress[0].Port != "1/1/2" || out.Egress[1].Port != "1/1/3" {
				t.Fatalf("relay = %+v, want flood to both other VLAN ports", out)
			}
			if entries := sw.Neighbors(); len(entries) != 1 || entries[0].MAC != macH1 {
				t.Fatalf("neighbors = %+v, want the request source", entries)
			}
			concrete++
		})
	}
	if concrete != 2 {
		t.Fatalf("concrete families = %d, want 2", concrete)
	}
}

func TestVLANNeighborToOwnMACConsumed(t *testing.T) {
	concrete := 0
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprint(malformed), func(t *testing.T) {
			sw := neighborIngressSwitch(t, false)
			frame := makeARPReply(t, ipH1, netip.MustParseAddr("10.0.10.1"), macH1, macRouter)
			if malformed {
				frame.Payload = []byte("undecodable")
				if _, err := arp.Decode(frame); err == nil {
					t.Fatal("malformed fixture decoded")
				}
			}
			out := sw.Receive(fixedTime, "1/1/1", frame)
			if out.Outcome != trace.Consumed || len(out.Egress) != 0 {
				t.Fatalf("output = %+v, want consumed without relay egress", out)
			}
			concrete++
		})
	}
	if concrete != 2 {
		t.Fatalf("concrete payload cases = %d, want 2", concrete)
	}
}

func TestVLANNeighborForOtherStationRelayedOnly(t *testing.T) {
	sw := neighborIngressSwitch(t, false)
	held := sw.Receive(fixedTime, "1/1/2", ethernet.Frame{
		Src: macH2, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
		Payload: makeIPv4Packet(t, netip.MustParseAddr("10.0.10.22"), ipH1, 64, []byte("pending")),
	})
	if held.Outcome != trace.Held {
		t.Fatalf("setup = %s, want Held", held.Outcome)
	}
	before := sw.Neighbors()
	frame := makeARPReply(t, ipH1, netip.MustParseAddr("10.0.10.22"), macH1, macH2)
	out := sw.Receive(fixedTime, "1/1/1", frame)
	if out.Outcome != trace.Forwarded || len(out.Egress) != 1 || out.Egress[0].Port != "1/1/2" || len(out.Emissions) != 0 || !reflect.DeepEqual(sw.Neighbors(), before) {
		t.Fatalf("output/neighbors = %+v/%+v, want relay only and unchanged neighbors", out, sw.Neighbors())
	}
}

func TestHeldOutputKeepsReleaseIdentity(t *testing.T) {
	sw := buildBaseRoutingSwitch(t)
	dst := netip.MustParseAddr("10.0.20.88")
	frame := ethernet.Frame{
		Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
		Payload: makeIPv4Packet(t, ipH1, dst, 64, []byte("same-payload")),
	}
	first := sw.Receive(fixedTime, "1/1/1", frame)
	second := sw.Receive(fixedTime, "1/1/1", frame)
	if first.Outcome != trace.Held || second.Outcome != trace.Held || first.Hold == 0 || second.Hold == 0 || first.Hold == second.Hold {
		t.Fatalf("holds = %s/%d, %s/%d, want distinct nonzero tokens", first.Outcome, first.Hold, second.Outcome, second.Hold)
	}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), macH2, macRouter)
	out := sw.Receive(fixedTime.Add(time.Millisecond), "1/1/2", reply)
	if len(out.Drops) != 0 || len(out.Emissions) != 2 {
		t.Fatalf("release = %+v, want exactly two data emissions", out)
	}
	for i, token := range []routing.HoldToken{first.Hold, second.Hold} {
		if em := out.Emissions[i]; em.Protocol || em.Hold != token || em.Ingress != "vlan10" {
			t.Errorf("emission %d = %+v, want token %d and ingress vlan10", i, em, token)
		}
	}
	if out.Hold != 0 {
		t.Errorf("observing reply hold = %d, want zero", out.Hold)
	}
}

func TestTimedOutHeldOutputKeepsIdentity(t *testing.T) {
	sw := buildBaseRoutingSwitch(t)
	dst := netip.MustParseAddr("10.0.20.88")
	held := sw.Receive(fixedTime, "1/1/1", ethernet.Frame{
		Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
		Payload: makeIPv4Packet(t, ipH1, dst, 64, []byte("timeout")),
	})
	if held.Outcome != trace.Held || held.Hold == 0 {
		t.Fatalf("hold = %s/%d, want held with nonzero token", held.Outcome, held.Hold)
	}
	sw.Advance(fixedTime.Add(time.Second))
	sw.Advance(fixedTime.Add(2 * time.Second))
	out := sw.Advance(fixedTime.Add(3 * time.Second))
	if len(out.Drops) != 1 {
		t.Fatalf("drops = %+v, want exactly one timeout", out.Drops)
	}
	drop := out.Drops[0]
	if drop.Hold != held.Hold || drop.Ingress != "vlan10" || drop.Reason != routing.ReasonNeighborMiss {
		t.Fatalf("timeout = %+v, want token %d and ingress vlan10", drop, held.Hold)
	}
}

func TestRoutingSolicitationHasNoHoldToken(t *testing.T) {
	sw := buildBaseRoutingSwitch(t)
	out := sw.Receive(fixedTime, "1/1/1", ethernet.Frame{
		Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
		Payload: makeIPv4Packet(t, ipH1, netip.MustParseAddr("10.0.20.88"), 64, []byte("solicit")),
	})
	if out.Outcome != trace.Held || out.Hold == 0 || len(out.Emissions) != 1 {
		t.Fatalf("output = %+v, want a hold and one solicitation", out)
	}
	em := out.Emissions[0]
	if !em.Protocol || em.Hold != 0 || em.Ingress != "" {
		t.Fatalf("solicitation = %+v, want protocol with zero token and empty ingress", em)
	}
	msg, err := arp.Decode(em.Frame)
	if err != nil || msg.Operation != arp.Request {
		t.Fatalf("solicitation = %+v/%v, want ARP request", msg, err)
	}
}

// TestObservationReleaseDoesNotChargeLAGRebalanceToObservingFrame is
// evidence for this change: a released held frame that egresses a balanced
// LAG with a stale bucket must not attribute lag-rebalance-unmodeled to the
// unrelated frame whose observation triggered the release. The switch holds
// a frame for a neighbor reachable only over a LAG-backed VLAN interface,
// with that LAG's bucket already stale; an ARP reply resolving the neighbor
// arrives on a plain access port that never touches the LAG at all, and its
// own result must carry no lag-rebalance-unmodeled issue even though
// releasing the held frame raises the same condition on lag1 itself.
func TestObservationReleaseDoesNotChargeLAGRebalanceToObservingFrame(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member-a", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member-b", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}))

	p10 := vlan.ID(10)
	p20 := vlan.ID(20)
	ten := 10 * time.Second
	staticMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x09}
	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
					"lag1":  {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		LAG: &lag.Config{LAGs: map[string]lag.LAG{
			"lag1": {
				Mode:              lag.BalanceSLB,
				Members:           map[string]lag.Member{"member-a": {}, "member-b": {}},
				RebalanceInterval: &ten,
			},
		}},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: netip.MustParseAddr("10.0.20.9"), MAC: staticMAC},
					},
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)

	mustSwitchLearn(t, sw, []bridge.Seed{
		{FID: 20, MAC: staticMAC, Port: "lag1", Lifetime: bridge.Static},
		{FID: 20, MAC: learnedMAC, Port: "lag1", Lifetime: bridge.Static},
	})

	t0 := fixedTime

	// hashSLB keys off the frame's own Src, which routing rewrites to the
	// router's MAC on every vlan20 egress, and the VLAN ID — so any routed
	// frame out vlan20 over lag1 lands in the same bucket regardless of
	// destination. This commits it at t0, using the pre-configured (already
	// Reachable) static neighbor so nothing holds.
	staticPkt := makeIPv4Packet(t, ipH1, netip.MustParseAddr("10.0.20.9"), 64, []byte("warm"))
	staticFrame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: staticPkt}
	warm := captureOutput(callOutputs, sw, sw.Receive(t0, "1/1/1", staticFrame))
	if warm.Outcome != trace.Forwarded {
		t.Fatalf("outcome = %v, want Forwarded", warm.Outcome)
	}
	if hasIssueCode(warm.Metadata.Issues(), vswitch.IssueLAGRebalanceUnmodeled) {
		t.Fatalf("issues at t0 = %+v, want none yet: this is the bucket's first assignment", warm.Metadata.Issues())
	}

	// Hold a frame for an unresolved neighbor well past the rebalance
	// interval, so the bucket the release later reuses is stale.
	dst := netip.MustParseAddr("10.0.20.77")
	pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hold"))
	held := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}
	t1 := t0.Add(11 * time.Second)
	res := captureOutput(callOutputs, sw, sw.Receive(t1, "1/1/1", held))
	if res.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", res.Outcome)
	}
	if hasIssueCode(res.Metadata.Issues(), vswitch.IssueLAGRebalanceUnmodeled) {
		t.Fatalf("held result charged for solicitation LAG selection: %+v", res.Metadata.Issues())
	}

	// The observing frame is an ARP reply arriving on a plain access port,
	// never crossing lag1 itself.
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	observing := captureOutput(callOutputs, sw, sw.Receive(t1.Add(time.Millisecond), "1/1/2", reply))

	if hasIssueCode(observing.Metadata.Issues(), vswitch.IssueLAGRebalanceUnmodeled) {
		t.Errorf("observing ARP reply's own result issues = %+v, want no lag-rebalance-unmodeled: it never traversed lag1", observing.Metadata.Issues())
	}

	emissions := dataAfterSolicitations(t, takeEmissions(callOutputs, sw), 2)
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1: the held frame must have released over lag1", len(emissions))
	}
}

// buildRoutedPortSwitch builds a switch with two routed ports and no bridge,
// for the neighbor lifecycle tests that need a routed interface bound
// directly to a port rather than to a VLAN.
func buildRoutedPortSwitch(t *testing.T) *vswitch.Switch {
	t.Helper()
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"1/1/1": {Port: "1/1/1", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"1/1/2": {Port: "1/1/2", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
				},
			},
		},
	}
	return mustSwitch(t, cfg)
}

// buildIPv6MulticastRoutingSwitch builds a two-VLAN routed switch with MLD
// snooping enabled on both VLANs, so a Neighbor Discovery test can prove
// observation reaches the neighbor path and not the multicast one on a VLAN
// where the multicast path is genuinely live, not merely absent.
func buildIPv6MulticastRoutingSwitch(t *testing.T) *vswitch.Switch {
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
		Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {}, 20: {}}},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("2001:db8:10::1/64")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("2001:db8:20::1/64")}},
					},
				},
			},
		},
	}
	return mustSwitch(t, cfg)
}

// makeNDPFrame builds a Neighbor Discovery frame at the given hop limit, so
// a test can exercise both the valid RFC 4861 value (255) and an invalid one
// a codec refuses.
func makeNDPFrame(t *testing.T, src, dst netip.Addr, srcMAC, dstMAC netaddr.MAC, hopLimit uint8, msg ndp.Message) ethernet.Frame {
	t.Helper()
	hdr := ip.Header{Src: src, Dst: dst, HopLimit: hopLimit, Protocol: 58, V6: &ip.V6{}}
	payload, err := ndp.Encode(hdr, msg)
	if err != nil {
		t.Fatalf("encode NDP: %v", err)
	}
	packet, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv6: %v", err)
	}
	return ethernet.Frame{Dst: dstMAC, Src: srcMAC, EtherType: ethernet.EtherTypeIPv6, Payload: packet}
}

// TestARPObservationReleasesHeldFrameOnRoutedPort checks neighbor reception on a
// routed port: a routed frame with no neighbor entry holds, an observed ARP
// reply moves the entry, and Wake releases the held frame as ordinary data
// (Protocol: false) rather than a protocol frame of the switch's own.
func TestARPObservationReleasesHeldFrameOnRoutedPort(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildRoutedPortSwitch(t)

	dst := netip.MustParseAddr("10.0.20.77")
	pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}

	held := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", frame))
	if held.Outcome != trace.Held || held.Reason != routing.ReasonNeighborPending {
		t.Fatalf("outcome/reason = %v/%v, want Held/%v", held.Outcome, held.Reason, routing.ReasonNeighborPending)
	}

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	observed := captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Millisecond), "1/1/2", reply))
	if observed.Outcome != trace.Consumed {
		t.Fatalf("ARP reply outcome = %s, want Consumed", observed.Outcome)
	}
	captureOutput(callOutputs, sw, sw.Advance(fixedTime.Add(time.Millisecond)))
	emissions := dataAfterSolicitations(t, takeEmissions(callOutputs, sw), 1)
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1: %+v", len(emissions), emissions)
	}
	if emissions[0].Port != "1/1/2" {
		t.Errorf("released port = %q, want 1/1/2", emissions[0].Port)
	}
	if emissions[0].Frame.Dst != learnedMAC {
		t.Errorf("released dst MAC = %v, want %v", emissions[0].Frame.Dst, learnedMAC)
	}
	if emissions[0].Protocol {
		t.Errorf("released frame Protocol = true, want false")
	}
	if failures := takeDrops(callOutputs, sw); len(failures) != 0 {
		t.Errorf("neighbor failures = %d, want 0: %+v", len(failures), failures)
	}
}

var (
	ipSubHost   = netip.MustParseAddr("10.0.10.77")
	ipSubSrc    = netip.MustParseAddr("10.0.30.7")
	ipSubHost3  = netip.MustParseAddr("10.0.30.77")
	macSubHost  = netaddr.MAC{0x02, 0, 0, 0, 0x10, 0x77}
	macSubHost3 = netaddr.MAC{0x02, 0, 0, 0, 0x30, 0x77}
)

// buildSubInterfaceSwitch builds a bridgeless routed switch carrying two C-TAG
// sub-interfaces on eth1 and a plain untagged routed port on eth3, with no
// static neighbors, so a frame routed out any of the three holds. A
// sub-interface's ordinary shape is a switch with no bridge at all, which is
// why this fixture configures none.
func buildSubInterfaceSwitch(t *testing.T) *vswitch.Switch {
	t.Helper()
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "eth1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "eth3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"eth1.10": {Port: "eth1", VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"eth1.20": {Port: "eth1", VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						"eth3":    {Port: "eth3", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
					},
				},
			},
		},
	}
	return mustSwitch(t, cfg)
}

// holdOnSubInterface routes a frame in on eth3 for a host behind eth1.10, which
// has no neighbor, leaving the frame in that interface's hold queue.
func holdOnSubInterface(t *testing.T, sw *vswitch.Switch) vswitch.Output {
	t.Helper()
	pkt := makeIPv4Packet(t, ipSubSrc, ipSubHost, 64, []byte("held on a sub-interface"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}
	res := sw.Receive(fixedTime, "eth3", frame)
	if res.Outcome != trace.Held || res.Reason != routing.ReasonNeighborPending {
		t.Fatalf("outcome/reason = %v/%v, want Held/%v", res.Outcome, res.Reason, routing.ReasonNeighborPending)
	}
	return res
}

// subInterfaceARPReply builds ipSubHost's reply to eth1.10's ARP request,
// carrying the tags the caller asks for.
func subInterfaceARPReply(t *testing.T, tags []vlan.Tag) ethernet.Frame {
	t.Helper()
	reply := makeARPReply(t, ipSubHost, netip.MustParseAddr("10.0.10.1"), macSubHost, macRouter)
	reply.Tags = tags

	return reply
}

// TestSubInterfaceObservationBindsOnlyItsOuterVID pins which advertisement on a
// parent port answers a sub-interface's pending neighbor. The reply's own outer
// C-TAG names the interface, so a reply at a sibling's VID, at a VID no
// interface carries, under an S-TAG, untagged, or arriving while the port is
// operationally down binds nothing at all rather than falling back to whichever
// interface the port would carry untagged. The last subtest is the positive
// control: without it the five above would all pass against an
// observationInterface that never observes anything. The reply that does bind
// a sub-interface is TestARPObservationReleasesHeldFrameOnSubInterface, which
// needs the release path: Observe updates a pending entry and creates none, so
// binding shows only as the held frame leaving.
func TestSubInterfaceObservationBindsOnlyItsOuterVID(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	cTag := uint16(ethernet.EtherTypeDot1Q)

	for _, tc := range []struct {
		name string
		tags []vlan.Tag
		down bool
	}{
		{name: "the sibling sub-interface's VID", tags: []vlan.Tag{{TPID: cTag, VID: 20}}},
		{name: "a VID no interface carries", tags: []vlan.Tag{{TPID: cTag, VID: 30}}},
		{name: "an S-TAG at the right VID", tags: []vlan.Tag{{TPID: uint16(ethernet.EtherTypeProviderBridging), VID: 10}}},
		{name: "untagged", tags: nil},
		{name: "the right C-TAG while the parent port is down", tags: []vlan.Tag{{TPID: cTag, VID: 10}}, down: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sw := buildSubInterfaceSwitch(t)
			captureOutput(callOutputs, sw, holdOnSubInterface(t, sw))
			if tc.down {
				if _, err := sw.LinkChange(time.Time{}, "eth1", port.Down, vswitch.PointToPointTrue, 0); err != nil {
					t.Fatalf("LinkChange(eth1, Down) = %v", err)
				}
			}
			captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Second), "eth1", subInterfaceARPReply(t, tc.tags)))
			captureOutput(callOutputs, sw, sw.Advance(fixedTime.Add(time.Second)))

			requests := 2
			if tc.down {
				requests = 1
			}
			if emissions := dataAfterSolicitations(t, takeEmissions(callOutputs, sw), requests); len(emissions) != 0 {
				t.Errorf("emissions = %+v, want none: the reply answered a neighbor it does not name", emissions)
			}
			if failures := takeDrops(callOutputs, sw); len(failures) != 0 {
				t.Errorf("neighbor failures = %+v, want none", failures)
			}
		})
	}

	t.Run("an untagged reply on a plain routed port binds that port's interface", func(t *testing.T) {
		sw := buildSubInterfaceSwitch(t)

		pkt := makeIPv4Packet(t, ipSubHost, ipSubHost3, 64, []byte("held on the untagged port"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			Tags:      []vlan.Tag{{TPID: cTag, VID: 10}},
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		if res := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "eth1", frame)); res.Outcome != trace.Held {
			t.Fatalf("outcome = %v, want Held; steps=%+v", res.Outcome, res.Steps)
		}

		reply := makeARPReply(t, ipSubHost3, netip.MustParseAddr("10.0.30.1"), macSubHost3, macRouter)
		captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Second), "eth3", reply))
		captureOutput(callOutputs, sw, sw.Advance(fixedTime.Add(time.Second)))

		emissions := dataAfterSolicitations(t, takeEmissions(callOutputs, sw), 2)
		if len(emissions) != 1 || emissions[0].Port != "eth3" {
			t.Fatalf("emissions = %+v, want one on eth3", emissions)
		}
		if emissions[0].Frame.Dst != macSubHost3 {
			t.Errorf("released dst MAC = %v, want %v", emissions[0].Frame.Dst, macSubHost3)
		}
	})
}

// TestARPObservationReleasesHeldFrameOnSubInterface covers a held frame whose
// neighbor resolves on a sub-interface: it leaves by the parent port, tagged
// with the sub-interface's VLAN, the way the live path leaves the same frame.
// The fixture configures no bridge, which is a sub-interface's ordinary shape,
// so a release routed through the bridge does not merely take the wrong path
// here — it dereferences a nil bridge and panics.
func TestARPObservationReleasesHeldFrameOnSubInterface(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildSubInterfaceSwitch(t)
	captureOutput(callOutputs, sw, holdOnSubInterface(t, sw))
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Second), "eth1", subInterfaceARPReply(t, []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}})))
	captureOutput(callOutputs, sw, sw.Advance(fixedTime.Add(time.Second)))

	emission := soleDataEmission(t, takeEmissions(callOutputs, sw), macSubHost)
	if emission.Port != "eth1" {
		t.Errorf("released port = %q, want eth1, the sub-interface's parent", emission.Port)
	}
	// The held frame arrived untagged on eth3, so the egress tag carries its
	// priority: zero, and drop eligibility clear.
	wantTag := vlan.Tag{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}
	if got := emission.Frame.Tags; len(got) != 1 || got[0] != wantTag {
		t.Errorf("released tags = %+v, want [%+v]", got, wantTag)
	}
	if failures := takeDrops(callOutputs, sw); len(failures) != 0 {
		t.Errorf("neighbor failures = %+v, want none", failures)
	}
}

// TestHeldFrameOnSubInterfaceTimesOutAgainstParentPort pins HeldFrame.Port's
// contract for a sub-interface: the drop is counted against the parent port,
// which is the only port the interface has. finishHeld fills Port from the
// interface, so this pins that contract rather than the release path; a timed
// out frame never reaches releaseHeldFrame at all.
func TestHeldFrameOnSubInterfaceTimesOutAgainstParentPort(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildSubInterfaceSwitch(t)
	captureOutput(callOutputs, sw, holdOnSubInterface(t, sw))

	assertSolicitationSchedule(t, callOutputs, sw, fixedTime, true)
	if emissions := takeEmissions(callOutputs, sw); len(emissions) != 0 {
		t.Errorf("emissions at timeout = %+v, want none", emissions)
	}
	failures := takeDrops(callOutputs, sw)
	if len(failures) != 1 {
		t.Fatalf("neighbor failures = %d, want 1: %+v", len(failures), failures)
	}
	if failures[0].Port != "eth1" {
		t.Errorf("drop port = %q, want eth1, the sub-interface's parent", failures[0].Port)
	}
	if failures[0].Reason != routing.ReasonNeighborMiss {
		t.Errorf("reason = %v, want %v", failures[0].Reason, routing.ReasonNeighborMiss)
	}
}

// TestReleasedSubInterfaceFrameStaysOffTheBridge covers the same release on a
// switch that does have a bridge, where routing it through the bridge would not
// fault but would flood the frame at the FID the sub-interface's VLAN id
// happens to name. A sub-interface's parent port is outside the bridge, so the
// two VLAN 10s are unrelated namespaces and the released frame belongs on eth1
// alone.
func TestReleasedSubInterfaceFrameStaysOffTheBridge(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	p10 := vlan.ID(10)
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "eth1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "eth3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "swport", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table:       map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{"swport": {PVID: &p10, Untagged: []vlan.ID{10}}},
		}},
		Routing: &routing.Config{VRFs: map[string]routing.VRF{
			"default": {
				Interfaces: map[string]routing.Interface{
					"eth1.10": {Port: "eth1", VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
					"eth3":    {Port: "eth3", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
				},
			},
		}},
	})

	pkt := makeIPv4Packet(t, ipSubSrc, ipSubHost, 64, []byte("held beside a bridge"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}
	if res := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "eth3", frame)); res.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held; steps=%+v", res.Outcome, res.Steps)
	}
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Second), "eth1", subInterfaceARPReply(t, []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}})))
	captureOutput(callOutputs, sw, sw.Advance(fixedTime.Add(time.Second)))

	emission := soleDataEmission(t, takeEmissions(callOutputs, sw), macSubHost)
	if emission.Port != "eth1" {
		t.Errorf("released port = %q, want eth1: the bridge's VLAN 10 is a different namespace", emission.Port)
	}
}

// TestARPObservationReleasesHeldFrameOnVLANInterface covers the same release
// as TestARPObservationReleasesHeldFrameOnRoutedPort for an interface bound
// to a VLAN rather than a port, and checks the released frame egresses
// through the bridge the way a routed frame's egress already does.
func TestARPObservationReleasesHeldFrameOnVLANInterface(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildBaseRoutingSwitch(t)

	dst := netip.MustParseAddr("10.0.20.77")
	pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}

	held := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", frame))
	if held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}
	if !slices.ContainsFunc(held.Metadata.Issues(), func(issue analysis.Issue) bool {
		return issue.Code == vswitch.IssueNeighborUnresolved && issue.Status == analysis.Incomplete
	}) {
		t.Errorf("issues = %+v, want IssueNeighborUnresolved at Incomplete", held.Metadata.Issues())
	}

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Millisecond), "1/1/2", reply))
	captureOutput(callOutputs, sw, sw.Advance(fixedTime.Add(time.Millisecond)))
	emissions := dataAfterSolicitations(t, takeEmissions(callOutputs, sw), 1)
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1: %+v", len(emissions), emissions)
	}
	if emissions[0].Frame.Dst != learnedMAC {
		t.Errorf("released dst MAC = %v, want %v", emissions[0].Frame.Dst, learnedMAC)
	}
	if emissions[0].Protocol {
		t.Errorf("released frame Protocol = true, want false")
	}
}

// TestReleasedFrameCarriesIngressPCPAndDEI covers a held frame's egress
// priority: a tagged frame arrives on a tagged vlan10 port with PCP 5, holds
// on an unresolved vlan20 next hop, and once the neighbor resolves the frame
// this releases onto a tagged vlan20 port carries the same PCP 5 the live,
// non-held path would have used. Before the item 1 fix, releaseHeldFrame
// built its synthetic bridge.Ingress with PCP and DEI left at zero, so the
// released frame always egressed at priority 0 regardless of what it
// arrived with.
func TestReleasedFrameCarriesIngressPCPAndDEI(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
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
					"1/1/1": {PVID: &p10, Tagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Tagged: []vlan.ID{20}},
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
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("hello"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), PCP: 5, VID: 10}},
		Payload:   pkt,
	}

	held := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", frame))
	if held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	learnedMAC := macH2
	reply := makeARPReply(t, ipH2, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Millisecond), "1/1/2", reply))
	captureOutput(callOutputs, sw, sw.Advance(fixedTime.Add(time.Millisecond)))
	emissions := dataAfterSolicitations(t, takeEmissions(callOutputs, sw), 1)
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1: %+v", len(emissions), emissions)
	}
	released := emissions[0]
	if released.Frame.Dst != learnedMAC {
		t.Fatalf("released dst MAC = %v, want %v", released.Frame.Dst, learnedMAC)
	}
	if len(released.Frame.Tags) != 1 {
		t.Fatalf("released tags = %+v, want exactly one tag", released.Frame.Tags)
	}
	if released.Frame.Tags[0].VID != p20 {
		t.Errorf("released VID = %v, want %v", released.Frame.Tags[0].VID, p20)
	}
	if released.Frame.Tags[0].PCP != 5 {
		t.Errorf("released PCP = %v, want 5: the ingress priority must survive the hold", released.Frame.Tags[0].PCP)
	}
	if released.Frame.Tags[0].DEI {
		t.Errorf("released DEI = true, want false")
	}
}

// TestARPReplyUnderPeekLeavesTableUntouched checks that Peek observes
// nothing, so the same reply that releases a held frame under Forward
// changes nothing under Peek, and two consecutive Peeks over the pending
// destination agree.
func TestARPReplyUnderPeekLeavesTableUntouched(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildBaseRoutingSwitch(t)

	dst := netip.MustParseAddr("10.0.20.77")
	pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}

	first := sw.Peek(fixedTime, "1/1/1", frame)
	if first.Outcome != trace.Held || first.Reason != routing.ReasonNeighborPending {
		t.Fatalf("outcome/reason = %v/%v, want Held/%v", first.Outcome, first.Reason, routing.ReasonNeighborPending)
	}
	second := sw.Peek(fixedTime, "1/1/1", frame)
	if second.Outcome != first.Outcome || second.Reason != first.Reason {
		t.Errorf("second peek disagreed with first: %v/%v vs %v/%v", second.Outcome, second.Reason, first.Outcome, first.Reason)
	}

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	sw.Peek(fixedTime.Add(time.Second), "1/1/2", reply)

	if _, hasTimer := sw.NextWake(); hasTimer {
		t.Errorf("NextWake reports a timer after Peek alone, want none: a preview must not create an entry")
	}
	captureOutput(callOutputs, sw, sw.Advance(fixedTime.Add(10*time.Second)))
	if emissions := takeEmissions(callOutputs, sw); len(emissions) != 0 {
		t.Errorf("emissions after Wake with only Peeks = %d, want 0: %+v", len(emissions), emissions)
	}
	if failures := takeDrops(callOutputs, sw); len(failures) != 0 {
		t.Errorf("neighbor failures after Wake with only Peeks = %d, want 0: %+v", len(failures), failures)
	}
}

// TestHeldFrameTimesOutWithDropStep checks each request and the final drop
// when resolution gets no reply.
func TestHeldFrameTimesOutWithDropStep(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildBaseRoutingSwitch(t)

	dst := netip.MustParseAddr("10.0.20.88")
	pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}

	held := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", frame))
	if held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	assertSolicitationSchedule(t, callOutputs, sw, fixedTime, true)
	if emissions := takeEmissions(callOutputs, sw); len(emissions) != 0 {
		t.Errorf("emissions at timeout = %d, want 0: %+v", len(emissions), emissions)
	}
	failures := takeDrops(callOutputs, sw)
	if len(failures) != 1 {
		t.Fatalf("neighbor failures = %d, want 1: %+v", len(failures), failures)
	}
	if failures[0].Step.Op != trace.OpDrop {
		t.Errorf("op = %v, want %v", failures[0].Step.Op, trace.OpDrop)
	}
	if failures[0].Reason != routing.ReasonNeighborMiss {
		t.Errorf("reason = %v, want %v", failures[0].Reason, routing.ReasonNeighborMiss)
	}
	if failures[0].Step.RuleID != trace.RuleID(routing.ReasonNeighborMiss) {
		t.Errorf("rule = %v, want %v", failures[0].Step.RuleID, routing.ReasonNeighborMiss)
	}
	if got := failures[0].Step.Subject; got != (trace.Subject{Kind: "interface", Key: "vlan20"}) {
		t.Errorf("drop subject = %+v, want interface vlan20", got)
	}
	outputs := failures[0].Step.Outputs
	if len(outputs) != 1 || outputs[0].TypeID() != "routing.egress_decision" ||
		outputs[0].Canonical() != `interface="vlan20";port="";member="";reason="neighbor-miss"` {
		t.Errorf("drop outputs = %+v, want egress decision naming vlan20 and neighbor-miss", outputs)
	}
}

// TestNDPInvalidHopLimitDoesNotObserve covers a Neighbor Advertisement whose
// hop limit is not 255: routing.Layer.Receive ignores the frame, so the held
// frame still times out on schedule.
func TestNDPInvalidHopLimitDoesNotObserve(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildIPv6MulticastRoutingSwitch(t)

	src := netip.MustParseAddr("2001:db8:10::7")
	dst := netip.MustParseAddr("2001:db8:20::77")
	pkt := makeIPv6Packet(t, src, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv6, Payload: pkt}

	if held := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", frame)); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	na := makeNDPFrame(t, dst, netip.MustParseAddr("2001:db8:20::1"), learnedMAC, macRouter, 254,
		ndp.Message{Type: ndp.NeighborAdvertisement, Target: dst, Solicited: true, Override: true, LinkLayerAddr: learnedMAC, HasLinkLayerAddr: true})
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(500*time.Millisecond), "1/1/2", na))

	assertSolicitationSchedule(t, callOutputs, sw, fixedTime, true)
	if emissions := takeEmissions(callOutputs, sw); len(emissions) != 0 {
		t.Errorf("emissions = %d, want 0: the hop-limit-254 advertisement must not have resolved the entry", len(emissions))
	}
	if failures := takeDrops(callOutputs, sw); len(failures) != 1 {
		t.Fatalf("neighbor failures = %d, want 1", len(failures))
	}
}

func TestRoutingSolicitationDoesNotChargeMulticastQueryToHeldFrame(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildIPv6MulticastRoutingSwitch(t)
	group := netip.MustParseAddr("ff02::1:ff00:77")
	groupMAC := netaddr.MAC{0x33, 0x33, 0xff, 0, 0, 0x77}
	peer := netip.MustParseAddr("fe80::2")
	captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/2", makeMLDControlFrame(t,
		peer, netip.MustParseAddr("ff02::1"), mcastRouterMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, 1, true,
		mld.Message{Type: mld.Query, Version: mld.V1})))
	captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/2", makeMLDControlFrame(t,
		peer, group, macH1, groupMAC, 1, true,
		mld.Message{Type: mld.ReportV1, Group: group})))
	captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/2", makeMLDControlFrame(t,
		peer, netip.MustParseAddr("ff02::2"), macH1, netaddr.MAC{0x33, 0x33, 0, 0, 0, 2}, 1, true,
		mld.Message{Type: mld.Done, Group: group})))
	now := fixedTime.Add(3 * time.Second)
	probe := sw.Peek(now, "1/1/2", ethernet.Frame{
		Src: macH1, Dst: groupMAC, EtherType: ethernet.EtherTypeIPv6,
		Payload: makeIPv6Packet(t, peer, group, 64, []byte("multicast")),
	})
	if !hasIssueCode(probe.Metadata.Issues(), vswitch.IssueMcastQueryUnobserved) {
		t.Fatalf("multicast probe issues = %+v, want mcast-query-unobserved", probe.Metadata.Issues())
	}
	takeEmissions(callOutputs, sw)
	dst := netip.MustParseAddr("2001:db8:20::77")
	held := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", ethernet.Frame{
		Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv6,
		Payload: makeIPv6Packet(t, netip.MustParseAddr("2001:db8:10::7"), dst, 64, []byte("hold")),
	}))
	if held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}
	if hasIssueCode(held.Metadata.Issues(), vswitch.IssueMcastQueryUnobserved) {
		t.Errorf("held frame issues = %+v, want no mcast-query-unobserved", held.Metadata.Issues())
	}
	emissions := takeEmissions(callOutputs, sw)
	if len(emissions) != 1 || emissions[0].Port != "1/1/2" || !emissions[0].Protocol {
		t.Fatalf("solicitation emissions = %+v, want one protocol frame on 1/1/2", emissions)
	}
	solicitationPayload := emissions[0].Frame.Payload
	hdr, payload, err := ip.Decode(solicitationPayload)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := ndp.Decode(hdr, payload)
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Dst != group || msg.Type != ndp.NeighborSolicitation || msg.Target != dst {
		t.Errorf("emitted destination/message = %s/%+v, want %s solicitation for %s", hdr.Dst, msg, group, dst)
	}
	observing := captureOutput(callOutputs, sw, sw.Receive(now.Add(time.Second), "1/1/2", makeARPReply(t,
		netip.MustParseAddr("10.0.20.2"), netip.MustParseAddr("10.0.20.1"), macH1, macRouter)))
	if hasIssueCode(observing.Metadata.Issues(), vswitch.IssueMcastQueryUnobserved) {
		t.Errorf("observing frame issues = %+v, want no mcast-query-unobserved", observing.Metadata.Issues())
	}
	emissions = takeEmissions(callOutputs, sw)
	if len(emissions) != 1 || emissions[0].Port != "1/1/2" || !emissions[0].Protocol || !bytes.Equal(emissions[0].Frame.Payload, solicitationPayload) {
		t.Fatalf("retransmission emissions = %+v, want the same solicitation on 1/1/2", emissions)
	}
}

// TestNDPReachesNeighborPathNotMulticastPath checks IPv6 neighbor reception: a valid
// Neighbor Advertisement releases the held frame even on a VLAN where MLD
// snooping is live, because an NDP frame carries no Hop-by-Hop header and so
// is never a multicast control candidate — it never reaches
// forwardMulticastControl, and its observation never reaches mcast group
// state either.
func TestNDPReachesNeighborPathNotMulticastPath(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildIPv6MulticastRoutingSwitch(t)

	src := netip.MustParseAddr("2001:db8:10::7")
	dst := netip.MustParseAddr("2001:db8:20::77")
	pkt := makeIPv6Packet(t, src, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv6, Payload: pkt}

	if held := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", frame)); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	na := makeNDPFrame(t, dst, netip.MustParseAddr("2001:db8:20::1"), learnedMAC, macRouter, 255,
		ndp.Message{Type: ndp.NeighborAdvertisement, Target: dst, Solicited: true, Override: true, LinkLayerAddr: learnedMAC, HasLinkLayerAddr: true})
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Millisecond), "1/1/2", na))

	if got := sw.Groups(20, fixedTime.Add(time.Millisecond)); len(got) != 0 {
		t.Errorf("mcast groups on vlan20 after NDP = %+v, want none: NDP must not reach multicast control", got)
	}
	captureOutput(callOutputs, sw, sw.Advance(fixedTime.Add(time.Millisecond)))
	emissions := dataAfterSolicitations(t, takeEmissions(callOutputs, sw), 1)
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1: NDP observation must have released the held frame", len(emissions))
	}
	if emissions[0].Frame.Dst != learnedMAC {
		t.Errorf("released dst MAC = %v, want %v", emissions[0].Frame.Dst, learnedMAC)
	}
	if emissions[0].Protocol {
		t.Errorf("released frame Protocol = true, want false")
	}
}

// TestMLDReportReachesMulticastPathNotNeighborPath covers the converse of
// TestNDPReachesNeighborPathNotMulticastPath: an MLD report is a multicast
// control candidate carrying a Hop-by-Hop header, so it is learned as
// multicast group state and never reaches [routing.Layer.Receive] — a
// separately held frame on the same switch still times out on schedule,
// proving the report never touched it.
func TestMLDReportReachesMulticastPathNotNeighborPath(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildIPv6MulticastRoutingSwitch(t)

	src := netip.MustParseAddr("2001:db8:10::7")
	dst := netip.MustParseAddr("2001:db8:20::77")
	pkt := makeIPv6Packet(t, src, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv6, Payload: pkt}

	if held := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", frame)); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	group := netip.MustParseAddr("ff05::1")
	report := makeMLDControlFrame(t, netip.MustParseAddr("fe80::1"), group,
		mcastHostMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, 1, true,
		mld.Message{Type: mld.ReportV1, Group: group})
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(500*time.Millisecond), "1/1/1", report))

	if got := sw.Groups(10, fixedTime.Add(time.Second)); len(got) != 1 {
		t.Fatalf("mcast groups on vlan10 after MLD report = %+v, want the reported group learned", got)
	}

	assertSolicitationSchedule(t, callOutputs, sw, fixedTime, true)
	if emissions := takeEmissions(callOutputs, sw); len(emissions) != 0 {
		t.Errorf("emissions = %d, want 0", len(emissions))
	}
	failures := takeDrops(callOutputs, sw)
	if len(failures) != 1 {
		t.Fatalf("neighbor failures = %d, want 1: the MLD report must not have resolved the held entry", len(failures))
	}
}

// TestARPReplyForOtherStationLeavesStaleEntryStale proves a reply addressed
// to another station does not confirm a stale neighbor's reachability.
func TestARPReplyForOtherStationLeavesStaleEntryStale(t *testing.T) {
	sw := buildHeldEgressSwitch(t, 3)
	addr := netip.MustParseAddr("10.0.20.77")
	mac := netaddr.MAC{2, 0, 0, 0, 0x20, 0x77}
	holdFrameToward(t, sw, fixedTime, addr, []byte("held"))
	request, err := arp.Encode(arp.Message{
		HardwareType: 1, ProtocolType: 0x0800, Operation: arp.Request,
		SenderAddr: addr, SenderMAC: mac, TargetAddr: netip.MustParseAddr("10.0.20.88"),
	}, netaddr.MAC{255, 255, 255, 255, 255, 255})
	if err != nil {
		t.Fatal(err)
	}
	sw.Receive(fixedTime.Add(time.Millisecond), "1/1/2", request)
	before := sw.Neighbors()
	if len(before) != 1 || before[0].State != routing.NeighborStale {
		t.Fatalf("setup = %+v, want stale neighbor", before)
	}
	reply := makeARPReply(t, addr, netip.MustParseAddr("10.0.20.88"), mac, netaddr.MAC{2, 0, 0, 0, 0x20, 0x88})
	sw.Receive(fixedTime.Add(2*time.Millisecond), "1/1/2", reply)
	if after := sw.Neighbors(); !reflect.DeepEqual(after, before) {
		t.Fatalf("neighbors = %+v, want stale unchanged %+v", after, before)
	}
}

// TestNDPNonLocalSolicitationTeachesNothingAndKeepsBothFramesHeld proves a
// non-local solicitation is discarded before learning its source, as RFC 4861
// section 7.2.3 requires.
func TestNDPNonLocalSolicitationTeachesNothingAndKeepsBothFramesHeld(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildIPv6MulticastRoutingSwitch(t)
	src := netip.MustParseAddr("2001:db8:10::7")
	other := netip.MustParseAddr("2001:db8:20::77")
	solicitor := netip.MustParseAddr("2001:db8:20::7")
	mac := netaddr.MAC{0x02, 0, 0, 0, 0x10, 0x07}
	for _, dst := range []netip.Addr{other, solicitor} {
		frame := ethernet.Frame{
			Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv6,
			Payload: makeIPv6Packet(t, src, dst, 64, []byte("held")),
		}
		if res := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", frame)); res.Outcome != trace.Held {
			t.Fatalf("outcome = %v, want Held", res.Outcome)
		}
	}
	before := sw.Neighbors()
	if len(before) != 2 {
		t.Fatalf("neighbors = %+v, want two pending entries", before)
	}
	ns := makeNDPFrame(t, solicitor, netip.MustParseAddr("2001:db8:20::1"), mac, macRouter, 255,
		ndp.Message{Type: ndp.NeighborSolicitation, Target: other, LinkLayerAddr: mac, HasLinkLayerAddr: true})
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(2*time.Millisecond), "1/1/2", ns))
	if emissions := dataAfterSolicitations(t, takeEmissions(callOutputs, sw), 2); len(emissions) != 0 {
		t.Fatalf("emissions = %d, want no advertisement or release", len(emissions))
	}
	if after := sw.Neighbors(); !reflect.DeepEqual(after, before) {
		t.Fatalf("neighbors = %+v, want unchanged pending entries %+v", after, before)
	}
	for _, entry := range sw.Neighbors() {
		if entry.State != routing.NeighborIncomplete || entry.HoldDepth != 1 || entry.MAC != (netaddr.MAC{}) {
			t.Fatalf("neighbor = %+v, want no learned binding and one pending frame", entry)
		}
	}
	// A source without a pending entry must not be added either.
	ns = makeNDPFrame(t, netip.MustParseAddr("2001:db8:20::8"), netip.MustParseAddr("2001:db8:20::1"), mac, macRouter, 255,
		ndp.Message{Type: ndp.NeighborSolicitation, Target: other, LinkLayerAddr: mac, HasLinkLayerAddr: true})
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(3*time.Millisecond), "1/1/2", ns))
	if len(sw.Neighbors()) != 2 || len(takeEmissions(callOutputs, sw)) != 0 {
		t.Fatal("non-local solicitation created an entry or answer")
	}
}

// TestNDPLocalSolicitationResolvesItsOwnSourceNotTheTarget proves a local
// solicitation learns its source and leaves the other next hop unresolved,
// as RFC 4861 section 7.2.3 requires.
func TestNDPLocalSolicitationResolvesItsOwnSourceNotTheTarget(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildIPv6MulticastRoutingSwitch(t)

	src := netip.MustParseAddr("2001:db8:10::7")
	heldDst := netip.MustParseAddr("2001:db8:20::77")
	heldPkt := makeIPv6Packet(t, src, heldDst, 64, []byte("target"))
	heldFrame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv6, Payload: heldPkt}
	if held := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", heldFrame)); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	solicitorAddr := netip.MustParseAddr("2001:db8:20::7")
	solicitorMAC := netaddr.MAC{0x02, 0, 0, 0, 0x10, 0x07}
	solicitorPkt := makeIPv6Packet(t, src, solicitorAddr, 64, []byte("solicitor"))
	solicitorFrame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv6, Payload: solicitorPkt}
	if held := captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Millisecond), "1/1/1", solicitorFrame)); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	// A local IP destination keeps the solicitation's ordinary forwarding
	// path from adding another frame to either neighbor's hold queue.
	if data := dataAfterSolicitations(t, takeEmissions(callOutputs, sw), 2); len(data) != 0 {
		t.Fatalf("setup released %d frames, want both held", len(data))
	}
	ns := makeNDPFrame(t, solicitorAddr, netip.MustParseAddr("2001:db8:20::1"), solicitorMAC, macRouter, 255,
		ndp.Message{Type: ndp.NeighborSolicitation, Target: netip.MustParseAddr("2001:db8:20::1"), LinkLayerAddr: solicitorMAC, HasLinkLayerAddr: true})
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(2*time.Millisecond), "1/1/2", ns))

	all := takeEmissions(callOutputs, sw)
	if len(all) != 2 {
		t.Fatalf("emissions = %d, want advertisement and released frame", len(all))
	}
	hdrAnswer, body, err := ip.Decode(all[0].Frame.Payload)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := ndp.Decode(hdrAnswer, body)
	if err != nil || answer.Type != ndp.NeighborAdvertisement || answer.Target != netip.MustParseAddr("2001:db8:20::1") {
		t.Fatalf("answer = %+v, error = %v, want local-target advertisement", answer, err)
	}
	emissions := all[1:]
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1: the solicitation must resolve its own source address, not the target it asked about", len(emissions))
	}
	if emissions[0].Frame.Dst != solicitorMAC {
		t.Errorf("released dst MAC = %v, want %v: the solicitor's own held frame must be the one released", emissions[0].Frame.Dst, solicitorMAC)
	}
	hdr, _, err := ip.Decode(emissions[0].Frame.Payload)
	if err != nil {
		t.Fatalf("decode released IP payload: %v", err)
	}
	if hdr.Dst != solicitorAddr {
		t.Errorf("released packet dst = %s, want %s: the target's held frame must not have been the one released", hdr.Dst, solicitorAddr)
	}
	for _, entry := range sw.Neighbors() {
		if entry.Addr == heldDst && (entry.State != routing.NeighborIncomplete || entry.HoldDepth != 1) {
			t.Fatalf("other neighbor = %+v, want incomplete with one held frame", entry)
		}
	}

	assertSolicitationSchedule(t, callOutputs, sw, fixedTime, false)
	if emissions := takeEmissions(callOutputs, sw); len(emissions) != 0 {
		t.Errorf("emissions at timeout = %d, want 0", len(emissions))
	}
	failures := takeDrops(callOutputs, sw)
	if len(failures) != 1 {
		t.Fatalf("neighbor failures = %d, want 1: the frame held for the solicitation's target must never have resolved", len(failures))
	}
}

// buildHeldEgressSwitch builds a switch with both egress shapes a released held frame can take:
// vlan20, an SVI over 1/1/2, and rp1, a routed port on 1/1/3 whose MTU is small enough that a
// test can build a frame the port refuses. depth bounds each neighbor's hold queue.
// Nothing is configured for the next hops on purpose — every test below needs the frame held
// first, which only an unresolved neighbor produces.
func buildHeldEgressSwitch(t *testing.T, depth int) *vswitch.Switch {
	t.Helper()
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 600}))

	return mustSwitch(t, vswitch.Config{
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
						"rp1":    {Port: "1/1/3", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
					},
					NeighborPolicy: routing.NeighborPolicy{HoldDepth: depth},
				},
			},
		},
	})
}

// TestReleaseOntoRefusingRoutedPortRecordsThePortsOwnReason covers a released held frame the
// port table refuses: the record must carry the refusal the port gave, not neighbor-miss, which
// would say the next hop never answered when it answered a moment ago. The frame is too large
// for 1/1/3 rather than the port being down, because a down port could not have carried the ARP
// reply that resolved the neighbor in the first place — the observing Forward is the call that
// releases, so the egress has to be refusing at that moment.
func TestReleaseOntoRefusingRoutedPortRecordsThePortsOwnReason(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildHeldEgressSwitch(t, 3)

	dst := netip.MustParseAddr("10.0.30.77")
	captureOutput(callOutputs, sw, holdFrameToward(t, sw, fixedTime, dst, make([]byte, 1000)))

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x30, 0x77}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.30.1"), learnedMAC, macRouter)
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Millisecond), "1/1/3", reply))

	if emissions := dataAfterSolicitations(t, takeEmissions(callOutputs, sw), 1); len(emissions) != 0 {
		t.Fatalf("emissions = %d, want 0: 1/1/3 has an MTU of 600", len(emissions))
	}
	drops := takeDrops(callOutputs, sw)
	if len(drops) != 1 {
		t.Fatalf("neighbor drops = %d, want 1: %+v", len(drops), drops)
	}
	if drops[0].Reason != port.ReasonMTUExceeded {
		t.Errorf("reason = %v, want %v, the port table's own refusal", drops[0].Reason, port.ReasonMTUExceeded)
	}
	if drops[0].Port != "1/1/3" {
		t.Errorf("port = %q, want 1/1/3", drops[0].Port)
	}
	if drops[0].Step.Layer != routing.LayerName {
		t.Errorf("step layer = %v, want %v", drops[0].Step.Layer, routing.LayerName)
	}
	if want := trace.RuleID("port.status.mtu-exceeded"); drops[0].Step.RuleID != want {
		t.Errorf("rule = %v, want %v: the step and the reason must agree", drops[0].Step.RuleID, want)
	}
}

// TestReleaseOntoSVIWithNoSelectableMemberRecordsTheBridgesReason covers a released frame whose
// SVI egress is a LAG that fails member selection: the bridge's selectMember records its own
// [bridge.ReasonNoMember] entry, naming the LAG, and releaseHeldFrame carries that reason and
// that port name onto the [NeighborDrop] unchanged, rather than reporting a reason of its own
// invention.
//
// The shape: vlan20's member is a LAG whose LACP never converged, so the aggregation forwards —
// its member links are up — but distributes to nothing. The advertisement arrives on 1/1/5, a
// second vlan20 port, and carries a different Ethernet source from the address it advertises, so
// the forwarding database keeps pointing the destination at the LAG. Nothing simpler reaches
// this branch: the observing Forward is the call that releases, so the neighbor has to resolve
// over a live port while the egress the frame would take is already refusing.
func TestReleaseOntoSVIWithNoSelectableMemberRecordsTheBridgesReason(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/5", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"lag1":  {PVID: &p20, Untagged: []vlan.ID{20}},
					"1/1/5": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		LAG: &lag.Config{LAGs: map[string]lag.LAG{
			// LACP Active with no partner ever answering: the members stay out of the
			// distributing set, so Select finds nothing to hash onto.
			"lag1": {LACP: lag.LACPConfig{Mode: lag.Active}, Members: map[string]lag.Member{"1/1/2": {}}},
		}},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
				},
			},
		},
	})

	dst := netip.MustParseAddr("10.0.20.77")
	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	if err := sw.Learn([]bridge.Seed{{FID: 20, MAC: learnedMAC, Port: "lag1", Lifetime: bridge.Static}}); err != nil {
		t.Fatalf("Learn: %v", err)
	}

	captureOutput(callOutputs, sw, holdFrameToward(t, sw, fixedTime, dst, []byte("svi")))

	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	reply.Src = netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x01}
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Second), "1/1/5", reply))

	for _, em := range takeEmissions(callOutputs, sw) {
		if em.Frame.Dst == learnedMAC {
			t.Fatalf("the held frame egressed on %s, want no emission: lag1 distributes to nothing", em.Port)
		}
	}
	drops := takeDrops(callOutputs, sw)
	if len(drops) != 1 {
		t.Fatalf("neighbor drops = %d, want 1: %+v", len(drops), drops)
	}
	if drops[0].Reason != bridge.ReasonNoMember {
		t.Errorf("reason = %v, want %v, the bridge's own answer", drops[0].Reason, bridge.ReasonNoMember)
	}
	if drops[0].Port != "lag1" {
		t.Errorf("port = %q, want lag1, the aggregation that refused it", drops[0].Port)
	}
	if drops[0].Step.RuleID != trace.RuleID(drops[0].Reason) {
		t.Errorf("rule = %v, want %v: the step and the reason must agree", drops[0].Step.RuleID, drops[0].Reason)
	}
}

func TestReleaseOntoLAGWithNoSelectableMemberRecordsRoutingReason(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: ports,
		LAG: &lag.Config{LAGs: map[string]lag.LAG{
			"lag1": {LACP: lag.LACPConfig{Mode: lag.Active}, Members: map[string]lag.Member{"1/1/2": {}}},
		}},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"rp1": {Port: "1/1/1", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"rp2": {Port: "lag1", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					NeighborPolicy: routing.NeighborPolicy{HoldDepth: 3},
				},
			},
		},
	})

	dst := netip.MustParseAddr("10.0.20.77")
	captureOutput(callOutputs, sw, holdFrameToward(t, sw, fixedTime, dst, []byte("held toward lag")))

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Second), "1/1/2", reply))

	for _, em := range takeEmissions(callOutputs, sw) {
		if em.Frame.Dst == learnedMAC {
			t.Fatalf("the held frame egressed on %s, want no emission: lag1 distributes to nothing", em.Port)
		}
	}
	drops := takeDrops(callOutputs, sw)
	if len(drops) != 1 {
		t.Fatalf("neighbor drops = %d, want 1: %+v", len(drops), drops)
	}
	if drops[0].Reason != bridge.ReasonNoMember {
		t.Errorf("reason = %v, want %v", drops[0].Reason, bridge.ReasonNoMember)
	}
	if drops[0].Port != "lag1" {
		t.Errorf("port = %q, want lag1", drops[0].Port)
	}
	if drops[0].Step.Layer != routing.LayerName {
		t.Errorf("step layer = %v, want %v", drops[0].Step.Layer, routing.LayerName)
	}
	if want := trace.RuleID("lag.egress.no_member"); drops[0].Step.RuleID != want {
		t.Errorf("rule = %v, want %v", drops[0].Step.RuleID, want)
	}
}

// TestReleaseOntoFloodVLANWithNoMemberRecordsTheBridgesReason covers the arm
// releaseHeldFrame's len(res.Egress) == 0 guard exists for: vlan20 is a
// flood VLAN with no switchport carrying it at all, so [bridge.Layer.Egress]
// never reaches a candidate port and returns with no Egress entry, naming
// the reason on the result instead — the same shape
// TestReleaseOntoSVIWithNoSelectableMemberRecordsTheBridgesReason covers for
// a LAG that fails member selection, but reached through the
// candidates-stay-empty branch of replicate rather than through
// selectMember, which always records an Egress entry of its own even when
// it fails.
//
// The ARP reply that resolves the neighbor arrives tagged for VID 20 on
// 1/1/2, a port with no switchport entry at all: a tagged frame classifies
// by its own VID regardless of switchport membership, which is the only way
// to attribute the observation to vlan20's interface without also handing
// the release a live vlan20 port to flood onto.
func TestReleaseOntoFloodVLANWithNoMemberRecordsTheBridgesReason(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	p10 := vlan.ID(10)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
				},
			},
			FloodVLANs: []vlan.ID{20},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
				},
			},
		},
	})

	dst := netip.MustParseAddr("10.0.20.77")
	captureOutput(callOutputs, sw, holdFrameToward(t, sw, fixedTime, dst, []byte("flood-vlan")))

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	reply.Tags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20}}
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Second), "1/1/2", reply))

	for _, em := range takeEmissions(callOutputs, sw) {
		if em.Frame.Dst == learnedMAC {
			t.Fatalf("the held frame egressed on %s, want no emission: vlan20 has no member port", em.Port)
		}
	}
	drops := takeDrops(callOutputs, sw)
	if len(drops) != 1 {
		t.Fatalf("neighbor drops = %d, want 1: %+v", len(drops), drops)
	}
	if drops[0].Reason != bridge.ReasonNoEgress {
		t.Errorf("reason = %v, want %v, the bridge's own answer", drops[0].Reason, bridge.ReasonNoEgress)
	}
	if drops[0].Port != "" {
		t.Errorf("port = %q, want empty: no port was chosen", drops[0].Port)
	}
	if drops[0].Step.RuleID != trace.RuleID(drops[0].Reason) {
		t.Errorf("rule = %v, want %v: the step and the reason must agree", drops[0].Step.RuleID, drops[0].Reason)
	}
}

// TestEvictedHeldFrameIsNotReportedAsANeighborMiss covers a frame the hold queue pushed out to
// make room. It is not a neighbor that failed to answer — in this very run the neighbor answers,
// and the two frames behind it release on the same Wake.
func TestEvictedHeldFrameIsNotReportedAsANeighborMiss(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildHeldEgressSwitch(t, 2)

	dst := netip.MustParseAddr("10.0.20.77")
	for i, payload := range [][]byte{[]byte("first"), []byte("second"), []byte("third")} {
		captureOutput(callOutputs, sw, holdFrameToward(t, sw, fixedTime.Add(time.Duration(i)*time.Millisecond), dst, payload))
	}

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Second), "1/1/2", reply))

	var released int
	for _, em := range takeEmissions(callOutputs, sw) {
		if em.Frame.Dst == learnedMAC {
			released++
		}
	}
	if released != 2 {
		t.Fatalf("released frames = %d, want 2: the two frames the queue kept", released)
	}
	drops := takeDrops(callOutputs, sw)
	if len(drops) != 1 {
		t.Fatalf("neighbor drops = %d, want 1: %+v", len(drops), drops)
	}
	if drops[0].Reason != routing.ReasonNeighborHoldOverflow {
		t.Errorf("reason = %v, want %v", drops[0].Reason, routing.ReasonNeighborHoldOverflow)
	}
	if drops[0].Step.RuleID != trace.RuleID(routing.ReasonNeighborHoldOverflow) {
		t.Errorf("rule = %v, want %v", drops[0].Step.RuleID, routing.ReasonNeighborHoldOverflow)
	}
	if drops[0].Port != "" {
		t.Errorf("port = %q, want empty: vlan20 is an SVI, which owns no single port", drops[0].Port)
	}
}

func TestFrameEvictedFromHoldQueueDrainedAfterAgeWithNoWake(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildHeldEgressSwitch(t, 2)

	dst := netip.MustParseAddr("10.0.20.77")
	for i, payload := range [][]byte{[]byte("first"), []byte("second"), []byte("third")} {
		captureOutput(callOutputs, sw, holdFrameToward(t, sw, fixedTime.Add(time.Duration(i)*time.Millisecond), dst, payload))
	}
	captureOutput(

		// Age with no Wake between drains the evicted frame into neighborFailures.
		callOutputs, sw, sw.Advance(fixedTime.Add(time.Second)))

	drops := takeDrops(callOutputs, sw)
	if len(drops) != 1 {
		t.Fatalf("neighbor drops = %d, want 1: %+v", len(drops), drops)
	}
	if drops[0].Reason != routing.ReasonNeighborHoldOverflow {
		t.Errorf("reason = %v, want %v", drops[0].Reason, routing.ReasonNeighborHoldOverflow)
	}
	if drops[0].Step.RuleID != trace.RuleID(routing.ReasonNeighborHoldOverflow) {
		t.Errorf("rule = %v, want %v", drops[0].Step.RuleID, routing.ReasonNeighborHoldOverflow)
	}
}

func TestWakeAgeForwardStopConditionInvariance(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	dst := netip.MustParseAddr("10.0.20.77")
	setup := func() (*vswitch.Switch, ethernet.Frame, time.Time) {
		sw := buildBaseRoutingSwitch(t)
		learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
		pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hello"))
		frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}

		if held := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", frame)); held.Outcome != trace.Held {
			t.Fatalf("outcome = %v, want Held", held.Outcome)
		}
		reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
		captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(time.Millisecond), "1/1/2", reply))
		captureOutput(callOutputs, sw, sw.Advance(fixedTime.Add(time.Millisecond)))
		takeEmissions(callOutputs, sw)

		expT := fixedTime.Add(time.Millisecond + 30*time.Second)
		return sw, frame, expT
	}

	neighborState := func(sw *vswitch.Switch, addr netip.Addr) routing.NeighborState {
		for _, n := range sw.Neighbors() {
			if n.Addr == addr {
				return n.State
			}
		}
		t.Fatalf("neighbor %v not found", addr)
		return ""
	}

	sw1, frame1, expT := setup()
	sw2, frame2, _ := setup()

	if st := neighborState(sw1, dst); st != routing.NeighborReachable {
		t.Fatalf("sw1 neighbor state before = %v, want %v", st, routing.NeighborReachable)
	}
	if st := neighborState(sw2, dst); st != routing.NeighborReachable {
		t.Fatalf("sw2 neighbor state before = %v, want %v", st, routing.NeighborReachable)
	}
	captureOutput(callOutputs, sw1, sw1.Advance(expT))
	captureOutput(callOutputs, sw1, sw1.Advance(expT))
	res1 := captureOutput(callOutputs, sw1, sw1.Receive(expT, "1/1/1", frame1))
	em1 := takeEmissions(callOutputs, sw1)
	captureOutput(callOutputs, sw2, sw2.Advance(expT))
	res2 := captureOutput(callOutputs, sw2, sw2.Receive(expT, "1/1/1", frame2))
	em2 := takeEmissions(callOutputs, sw2)

	if st := neighborState(sw1, dst); st != routing.NeighborDelay {
		t.Fatalf("sw1 neighbor state after = %v, want %v", st, routing.NeighborDelay)
	}
	if st := neighborState(sw2, dst); st != routing.NeighborDelay {
		t.Fatalf("sw2 neighbor state after = %v, want %v", st, routing.NeighborDelay)
	}

	if res1.Outcome != res2.Outcome {
		t.Fatalf("res1 outcome = %v, res2 outcome = %v, want equal", res1.Outcome, res2.Outcome)
	}
	if res1.Reason != res2.Reason {
		t.Fatalf("res1 reason = %v, res2 reason = %v, want equal", res1.Reason, res2.Reason)
	}
	if !reflect.DeepEqual(res1.Egress, res2.Egress) {
		t.Fatalf("res1 egress = %+v, res2 egress = %+v, want equal", res1.Egress, res2.Egress)
	}
	if !reflect.DeepEqual(sw1.Neighbors(), sw2.Neighbors()) {
		t.Fatalf("sw1 neighbors = %+v, sw2 neighbors = %+v, want equal", sw1.Neighbors(), sw2.Neighbors())
	}
	if len(em1) != len(em2) {
		t.Fatalf("em1 count = %d, em2 count = %d, want equal", len(em1), len(em2))
	}
}

// TestReleasedFrameCarriesIngressPriorityOntoEgress covers the half
// TestReleasedFrameCarriesIngressPCPAndDEI could not see. That test releases onto a tagged port,
// where the priority survives in the egress tag. On an untagged access port and on a routed port
// there is no tag to carry it, so the priority has to travel on the Emission itself — a reader
// deriving it from the frame gets 0, and the frame queues behind traffic the live, non-held path
// would have overtaken. The sub-interface row has it both ways, on the tag it prepends and on
// the Emission, and each release is compared against the live path through the same interface.
func TestReleasedFrameCarriesIngressPriorityOntoEgress(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	newSwitch := func(t *testing.T, neighbors []routing.Neighbor) *vswitch.Switch {
		t.Helper()
		return mustSwitch(t, vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Tagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			}},
			Routing: &routing.Config{VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						"rp1":    {Port: "1/1/3", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
						"rp2.40": {Port: "1/1/4", VLAN: 40, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.40.1/24")}},
					},
					Neighbors: neighbors,
				},
			}},
		})
	}

	// The held and the live frame take the same route to the same next hop; only the order of
	// the advertisement and the frame differs, so any divergence is the hold.
	taggedFrame := func(t *testing.T, dst netip.Addr) ethernet.Frame {
		t.Helper()
		return ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), PCP: 5, DEI: true, VID: 10}},
			Payload:   makeIPv4Packet(t, ipH1, dst, 64, []byte("priority")),
		}
	}

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x30, 0x77}
	subTag := vlan.Tag{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 40, PCP: 5, DEI: true}
	for _, tc := range []struct {
		name      string
		iface     string
		dst       netip.Addr
		gateway   netip.Addr
		replyOn   string
		wantEgres string
		wantTags  []vlan.Tag
		replyTags []vlan.Tag
	}{
		{"untagged access port", "vlan20", netip.MustParseAddr("10.0.20.77"), netip.MustParseAddr("10.0.20.1"), "1/1/2", "1/1/2", nil, nil},
		{"routed port", "rp1", netip.MustParseAddr("10.0.30.77"), netip.MustParseAddr("10.0.30.1"), "1/1/3", "1/1/3", nil, nil},
		// The sub-interface's reply arrives on its parent port, where only its
		// own C-TAG names it.
		{"sub-interface", "rp2.40", netip.MustParseAddr("10.0.40.77"), netip.MustParseAddr("10.0.40.1"), "1/1/4", "1/1/4", []vlan.Tag{subTag}, []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 40}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := newSwitch(t, nil)
			if res := captureOutput(callOutputs, held, held.Receive(fixedTime, "1/1/1", taggedFrame(t, tc.dst))); res.Outcome != trace.Held {
				t.Fatalf("outcome = %v (%v), want Held; steps=%+v", res.Outcome, res.Reason, res.Steps)
			}
			reply := makeARPReply(t, tc.dst, tc.gateway, learnedMAC, macRouter)
			reply.Tags = tc.replyTags
			captureOutput(callOutputs, held, held.Receive(fixedTime.Add(time.Second), tc.replyOn, reply))
			heldEmission := soleDataEmission(t, takeEmissions(callOutputs, held), learnedMAC)

			// The baseline is a switch that never held: the binding is configured, so the same
			// frame forwards straight through and its priority rides res.Egress, which is what
			// the fabric transmits on the live path. Receive alone would not do — an
			// advertisement for an address nothing routed to creates no entry.
			live := newSwitch(t, []routing.Neighbor{{Interface: tc.iface, Addr: tc.dst, MAC: learnedMAC}})
			res := captureOutput(callOutputs, live, live.Receive(fixedTime, "1/1/1", taggedFrame(t, tc.dst)))
			if res.Outcome == trace.Held || res.Outcome == trace.Dropped {
				t.Fatalf("live outcome = %v (%v), want the frame on its way: the binding is configured", res.Outcome, res.Reason)
			}
			if len(res.Egress) != 1 {
				t.Fatalf("live egress = %+v, want exactly one entry", res.Egress)
			}

			if heldEmission.PCP != res.Egress[0].PCP {
				t.Errorf("held PCP = %d, live PCP = %d: a hold must not change the priority", heldEmission.PCP, res.Egress[0].PCP)
			}
			if heldEmission.PCP != 5 {
				t.Errorf("released PCP = %d, want 5, the priority the frame arrived with", heldEmission.PCP)
			}
			if !slices.Equal(heldEmission.Frame.Tags, tc.wantTags) {
				t.Errorf("released tags = %+v, want %+v", heldEmission.Frame.Tags, tc.wantTags)
			}
			if !slices.Equal(heldEmission.Frame.Tags, res.Egress[0].Frame.Tags) {
				t.Errorf("held tags = %+v, live tags = %+v: a hold must not change what reaches the wire",
					heldEmission.Frame.Tags, res.Egress[0].Frame.Tags)
			}
			if heldEmission.Port != tc.wantEgres {
				t.Errorf("released port = %q, want %q", heldEmission.Port, tc.wantEgres)
			}
		})
	}
}

func TestRoutedFrameToDownPortEmitsPortStatusDownRule(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"rp1": {Port: "1/1/1", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"rp2": {Port: "1/1/2", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "rp2", Addr: ipH2, MAC: macH2},
					},
				},
			},
		},
	})

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("routed-data"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}

	res := sw.Receive(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Dropped {
		t.Fatalf("Outcome = %v, want Dropped", res.Outcome)
	}
	if res.Reason != port.ReasonPortDown {
		t.Fatalf("Reason = %v, want %v", res.Reason, port.ReasonPortDown)
	}

	step, ok := findStep(res.Steps, trace.RuleID("port.status.port-down"))
	if !ok {
		t.Fatalf("no literal port.status.port-down step in trace: %+v", res.Steps)
	}
	if step.Layer != routing.LayerName {
		t.Errorf("step layer = %v, want %v", step.Layer, routing.LayerName)
	}
}

// assertSolicitationSchedule drains the first request when initial is set,
// then checks each retransmission instant and the final failure boundary.
func assertSolicitationSchedule(t *testing.T, callOutputs map[*vswitch.Switch]vswitch.Output, sw *vswitch.Switch, start time.Time, initial bool) {
	t.Helper()
	if initial {
		if data := dataAfterSolicitations(t, takeEmissions(callOutputs, sw), 1); len(data) != 0 {
			t.Fatalf("data before resolution = %+v", data)
		}
	}
	for second := 1; second <= 2; second++ {
		before := start.Add(time.Duration(second)*time.Second - time.Nanosecond)
		captureOutput(callOutputs, sw, sw.Advance(before))
		if em := takeEmissions(callOutputs, sw); len(em) != 0 {
			t.Fatalf("early emission at %s: %+v", before, em)
		}
		captureOutput(callOutputs, sw, sw.Advance(start.Add(time.Duration(second)*time.Second)))
		if data := dataAfterSolicitations(t, takeEmissions(callOutputs, sw), 1); len(data) != 0 {
			t.Fatalf("data at retransmission %d = %+v", second, data)
		}
		if failures := takeDrops(callOutputs, sw); len(failures) != 0 {
			t.Fatalf("early failures at second %d: %+v", second, failures)
		}
	}
	captureOutput(callOutputs, sw, sw.Advance(start.Add(3*time.Second-time.Nanosecond)))
	if failures := takeDrops(callOutputs, sw); len(failures) != 0 {
		t.Fatalf("failures before deadline: %+v", failures)
	}
	captureOutput(callOutputs, sw, sw.Advance(start.Add(3*time.Second)))
}

func TestRoutingSolicitationsLeaveEachInterfaceShape(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	for _, tc := range []struct {
		name          string
		build         func(*testing.T) *vswitch.Switch
		ingress, port string
		src, dst      string
		vid           vlan.ID
	}{
		{"VLAN", buildBaseRoutingSwitch, "1/1/1", "1/1/2", "10.0.10.7", "10.0.20.77", 0},
		{"routed port", buildRoutedPortSwitch, "1/1/1", "1/1/2", "10.0.10.7", "10.0.20.77", 0},
		{"sub-interface", buildSubInterfaceSwitch, "eth3", "eth1", "10.0.30.7", "10.0.10.77", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sw := tc.build(t)
			target := netip.MustParseAddr(tc.dst)
			frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: makeIPv4Packet(t, netip.MustParseAddr(tc.src), target, 64, []byte("hold"))}
			if res := captureOutput(callOutputs, sw, sw.Receive(fixedTime, tc.ingress, frame)); res.Outcome != trace.Held {
				t.Fatalf("outcome = %v, want held", res.Outcome)
			}
			em := takeEmissions(callOutputs, sw)
			if len(em) != 1 || em[0].Port != tc.port || !em[0].Protocol || em[0].PCP != 0 {
				t.Fatalf("emissions = %+v, want one priority-zero protocol frame on %s", em, tc.port)
			}
			msg, err := arp.Decode(em[0].Frame)
			if err != nil || msg.Operation != arp.Request || msg.TargetAddr != target || em[0].Frame.Dst != (netaddr.MAC{255, 255, 255, 255, 255, 255}) {
				t.Fatalf("solicitation = %+v, ARP = %+v/%v", em[0], msg, err)
			}
			tags := em[0].Frame.Tags
			if tc.vid == 0 && len(tags) != 0 || tc.vid != 0 && (len(tags) != 1 || tags[0].VID != tc.vid || tags[0].PCP != 0 || tags[0].DEI) {
				t.Fatalf("tags = %+v, want VID %d at priority zero", tags, tc.vid)
			}
			if drops := takeDrops(callOutputs, sw); len(drops) != 0 {
				t.Fatalf("drops for solicitation = %+v", drops)
			}
		})
	}
}
