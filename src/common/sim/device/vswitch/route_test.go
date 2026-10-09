package vswitch_test

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestVLANToVLANRouting(t *testing.T) {
	sw := buildBaseRoutingSwitch(t)

	// Pre-learn neighbor's MAC on 1/1/2 so the relay hits.
	sw.Receive(fixedTime, "1/1/2", ethernet.Frame{
		Src:       macH2,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   makeIPv4Packet(t, ipH2, ipH1, 64, []byte("learn")),
	})

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("hello"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Receive(fixedTime, "1/1/1", frame)

	wantSteps := []trace.Step{
		{Layer: bridge.LayerNameVLAN, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
		{Layer: bridge.LayerName, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
		{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
		{Layer: routing.LayerName, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
		{Layer: routing.LayerName, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "vlan20"}},
		{Layer: bridge.LayerName, Op: trace.OpLookup, RuleID: "unicast-hit", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:77"}},
		{Layer: bridge.LayerNameVLAN, Op: trace.OpRewrite, RuleID: "vlan-tag-form", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
		{Layer: bridge.LayerName, Op: trace.OpTransmit, RuleID: "transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
	}
	assertSteps(t, res.Steps, wantSteps)

	if res.Outcome != trace.Forwarded {
		t.Errorf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
	}
	if res.FID != 20 {
		t.Errorf("res.FID = %d, want 20", res.FID)
	}
	if res.Ingress != "1/1/1" {
		t.Errorf("res.Ingress = %q, want 1/1/1", res.Ingress)
	}
	if len(res.Egress) != 1 {
		t.Fatalf("len(res.Egress) = %d, want 1", len(res.Egress))
	}
	egress := res.Egress[0]
	if egress.Port != "1/1/2" {
		t.Errorf("egress.Port = %q, want 1/1/2", egress.Port)
	}
	if egress.Frame.Src != macRouter {
		t.Errorf("egress.Frame.Src = %v, want %v", egress.Frame.Src, macRouter)
	}
	if egress.Frame.Dst != macH2 {
		t.Errorf("egress.Frame.Dst = %v, want %v", egress.Frame.Dst, macH2)
	}
	if len(egress.Frame.Tags) != 0 {
		t.Errorf("egress.Frame.Tags = %v, want untagged", egress.Frame.Tags)
	}

	hdr, _, err := ip.Decode(egress.Frame.Payload)
	if err != nil {
		t.Fatalf("decode routed payload: %v", err)
	}
	if hdr.HopLimit != 63 {
		t.Errorf("hdr.HopLimit = %d, want 63", hdr.HopLimit)
	}
	if hdr.Src != ipH1 || hdr.Dst != ipH2 {
		t.Errorf("hdr.Src=%v, Dst=%v, want %v, %v", hdr.Src, hdr.Dst, ipH1, ipH2)
	}

	// Flooded variant when FDB has not learned the neighbor MAC on 1/1/2.
	swEmpty := buildBaseRoutingSwitch(t)
	resFlood := swEmpty.Receive(fixedTime, "1/1/1", frame)
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
	assertSteps(t, resFlood.Steps, wantFloodSteps)
	if resFlood.Outcome != trace.Flooded {
		t.Errorf("resFlood.Outcome = %v, want %v", resFlood.Outcome, trace.Flooded)
	}
	if resFlood.FID != 20 {
		t.Errorf("resFlood.FID = %d, want 20", resFlood.FID)
	}
}

// TestNeighborPendingHolds forwards to a next hop with no configured or
// observed neighbor. Under the default NeighborObserved policy this is a
// hold, not a drop: netsim has not asked about the address yet, which is a
// different claim from the definite failure TestNeighborDisabledMisses
// covers on the same topology.
func TestNeighborPendingHolds(t *testing.T) {
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
					// No neighbors configured.
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
		Payload:   pkt,
	}

	res := sw.Receive(fixedTime, "1/1/1", frame)

	wantSteps := []trace.Step{
		{Layer: bridge.LayerNameVLAN, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
		{Layer: bridge.LayerName, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
		{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
		{Layer: routing.LayerName, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
		{Layer: routing.LayerName, Op: trace.OpLookup, RuleID: "neighbor-pending", Subject: trace.Subject{Kind: "ip", Key: "10.0.20.7"}},
	}
	assertSteps(t, res.Steps, wantSteps)

	if res.Outcome != trace.Held {
		t.Errorf("outcome = %v, want %v", res.Outcome, trace.Held)
	}
	if res.Reason != routing.ReasonNeighborPending {
		t.Errorf("reason = %v, want %v", res.Reason, routing.ReasonNeighborPending)
	}
	if len(res.Egress) != 0 {
		t.Errorf("len(res.Egress) = %d, want 0", len(res.Egress))
	}
	if res.FID != 10 {
		t.Errorf("res.FID = %d, want 10", res.FID)
	}
	if res.Ingress != "1/1/1" {
		t.Errorf("res.Ingress = %q, want 1/1/1", res.Ingress)
	}
	if !slices.ContainsFunc(res.Metadata.Issues(), func(issue analysis.Issue) bool {
		return issue.Code == vswitch.IssueNeighborUnresolved && issue.Status == analysis.Incomplete
	}) {
		t.Errorf("issues = %+v, want IssueNeighborUnresolved at Incomplete", res.Metadata.Issues())
	}
}

// TestNeighborDisabledMisses forwards the same unresolved next hop as
// TestNeighborPendingHolds, but with the VRF's neighbor policy set to
// NeighborDisabled: there netsim does know the outcome, so the frame is a
// definite drop rather than a hold.
func TestNeighborDisabledMisses(t *testing.T) {
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
					NeighborPolicy: routing.NeighborPolicy{Mode: routing.NeighborDisabled},
					// No neighbors configured.
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
		Payload:   pkt,
	}

	res := sw.Receive(fixedTime, "1/1/1", frame)

	wantSteps := []trace.Step{
		{Layer: bridge.LayerNameVLAN, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
		{Layer: bridge.LayerName, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
		{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
		{Layer: routing.LayerName, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
		{Layer: routing.LayerName, Op: trace.OpDrop, RuleID: "neighbor-miss", Subject: trace.Subject{Kind: "ip", Key: "10.0.20.7"}},
	}
	assertSteps(t, res.Steps, wantSteps)

	if res.Outcome != trace.Dropped {
		t.Errorf("outcome = %v, want %v", res.Outcome, trace.Dropped)
	}
	if res.Reason != routing.ReasonNeighborMiss {
		t.Errorf("reason = %v, want %v", res.Reason, routing.ReasonNeighborMiss)
	}
	if res.Metadata.Status() != analysis.Complete {
		t.Errorf("status = %v, want %v", res.Metadata.Status(), analysis.Complete)
	}
}

// TestNeighborDisabledDoesNotObserve checks that receipt preserves the
// configured binding. TestReceiveAnswerUsesOptionThenCacheAndDisabledNeverLearns
// in routing/receive_test.go pins the disabled policy's refusal to learn a
// solicitation's source when no configured binding exists.
func TestNeighborDisabledDoesNotObserve(t *testing.T) {
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
					NeighborPolicy: routing.NeighborPolicy{Mode: routing.NeighborDisabled},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
					},
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)

	spoofedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x99, 0x99}
	reply := makeARPReply(t, ipH2, netip.MustParseAddr("10.0.20.1"), spoofedMAC, macRouter)
	sw.Receive(fixedTime, "1/1/2", reply)

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}
	res := sw.Receive(fixedTime.Add(time.Millisecond), "1/1/1", frame)

	if len(res.Egress) != 1 {
		t.Fatalf("len(res.Egress) = %d, want 1", len(res.Egress))
	}
	if got := res.Egress[0].Frame.Dst; got != macH2 {
		t.Errorf("egress.Frame.Dst = %v, want %v: the observed ARP reply must not have rewritten the static neighbor", got, macH2)
	}
}

func TestRoutingTTLLocalBadHeaderAndNoRouteDrops(t *testing.T) {
	sw := buildBaseRoutingSwitch(t)

	t.Run("ttl expired", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipH2, 1, []byte("hello"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Receive(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: bridge.LayerNameVLAN, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
			{Layer: bridge.LayerName, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
			{Layer: routing.LayerName, Op: trace.OpDrop, RuleID: "ttl-expired", Subject: trace.Subject{Kind: "ip", Key: "10.0.20.7"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonTTLExpired {
			t.Errorf("got outcome=%v reason=%v, want Dropped/ttl-expired", res.Outcome, res.Reason)
		}
		if len(res.Egress) != 0 {
			t.Errorf("egress count = %d, want 0", len(res.Egress))
		}
	})

	t.Run("packet to local device address consumed", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipR1, 64, []byte("hello"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Receive(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: bridge.LayerNameVLAN, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
			{Layer: bridge.LayerName, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
			{Layer: routing.LayerName, Op: trace.OpLookup, RuleID: "local-delivery", Subject: trace.Subject{Kind: "ip", Key: "10.0.10.1"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Consumed || res.Reason != routing.ReasonNotRouted {
			t.Errorf("got outcome=%v reason=%v, want Consumed/not-routed", res.Outcome, res.Reason)
		}
	})

	t.Run("bad header checksum", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("hello"))
		pkt[11] ^= 0x01
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Receive(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: bridge.LayerNameVLAN, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
			{Layer: bridge.LayerName, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
			{Layer: routing.LayerName, Op: trace.OpDrop, RuleID: "bad-header", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonBadHeader {
			t.Errorf("got outcome=%v reason=%v, want Dropped/bad-header", res.Outcome, res.Reason)
		}
	})

	t.Run("no route", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, netip.MustParseAddr("10.0.30.7"), 64, []byte("hello"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Receive(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: bridge.LayerNameVLAN, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
			{Layer: bridge.LayerName, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
			{Layer: routing.LayerName, Op: trace.OpLookup, RuleID: "no-route", Subject: trace.Subject{Kind: "ip", Key: "10.0.30.7"}},
			{Layer: routing.LayerName, Op: trace.OpDrop, RuleID: "no-route", Subject: trace.Subject{Kind: "vrf", Key: "default"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNoRoute {
			t.Errorf("got outcome=%v reason=%v, want Dropped/no-route", res.Outcome, res.Reason)
		}
	})
}

func TestIPv6Routing(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	ip6Src := netip.MustParseAddr("2001:db8:10::7")
	ip6Dst := netip.MustParseAddr("2001:db8:20::7")
	ip6Off := netip.MustParseAddr("2001:db8:30::7")

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
						"vlan10": {
							VLAN: 10,
							MAC:  macRouter,
							Prefixes: []netip.Prefix{
								netip.MustParsePrefix("10.0.10.1/24"),
								netip.MustParsePrefix("2001:db8:10::1/64"),
							},
						},
						"vlan20": {
							VLAN: 20,
							MAC:  macRouter,
							Prefixes: []netip.Prefix{
								netip.MustParsePrefix("10.0.20.1/24"),
								netip.MustParsePrefix("2001:db8:20::1/64"),
							},
						},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
						{Interface: "vlan20", Addr: ip6Dst, MAC: macH2},
					},
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)

	// Pre-learn on 1/1/2
	sw.Receive(fixedTime, "1/1/2", ethernet.Frame{
		Src:       macH2,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv6,
		Payload:   makeIPv6Packet(t, ip6Dst, ip6Src, 64, []byte("learn")),
	})

	t.Run("forwarded on vlan 20 with hop limit 63", func(t *testing.T) {
		pkt := makeIPv6Packet(t, ip6Src, ip6Dst, 64, []byte("ipv6 hello"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv6,
			Payload:   pkt,
		}

		res := sw.Receive(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: bridge.LayerNameVLAN, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
			{Layer: bridge.LayerName, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
			{Layer: routing.LayerName, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "2001:db8:20::/64"}},
			{Layer: routing.LayerName, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "vlan20"}},
			{Layer: bridge.LayerName, Op: trace.OpLookup, RuleID: "unicast-hit", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:77"}},
			{Layer: bridge.LayerNameVLAN, Op: trace.OpRewrite, RuleID: "vlan-tag-form", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
			{Layer: bridge.LayerName, Op: trace.OpTransmit, RuleID: "transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Forwarded {
			t.Errorf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}
		if res.FID != 20 {
			t.Errorf("FID = %d, want 20", res.FID)
		}

		hdr, _, err := ip.Decode(res.Egress[0].Frame.Payload)
		if err != nil {
			t.Fatalf("decode egress IPv6: %v", err)
		}
		if hdr.HopLimit != 63 {
			t.Errorf("hdr.HopLimit = %d, want 63", hdr.HopLimit)
		}
	})

	t.Run("off-link destination ends no-route and IPv4 is unaffected", func(t *testing.T) {
		pkt := makeIPv6Packet(t, ip6Src, ip6Off, 64, []byte("off"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv6,
			Payload:   pkt,
		}
		res := sw.Receive(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: bridge.LayerNameVLAN, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
			{Layer: bridge.LayerName, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
			{Layer: routing.LayerName, Op: trace.OpLookup, RuleID: "no-route", Subject: trace.Subject{Kind: "ip", Key: "2001:db8:30::7"}},
			{Layer: routing.LayerName, Op: trace.OpDrop, RuleID: "no-route", Subject: trace.Subject{Kind: "vrf", Key: "default"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNoRoute {
			t.Errorf("outcome=%v reason=%v, want Dropped/no-route", res.Outcome, res.Reason)
		}

		// IPv4 to unrouted destination still ends no-route unaffected by IPv6 routes.
		v4Pkt := makeIPv4Packet(t, ipH1, netip.MustParseAddr("10.0.30.7"), 64, []byte("test"))
		v4Frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   v4Pkt,
		}
		v4Res := sw.Receive(fixedTime, "1/1/1", v4Frame)
		if v4Res.Outcome != trace.Dropped || v4Res.Reason != routing.ReasonNoRoute {
			t.Errorf("v4Res outcome=%v reason=%v, want Dropped/no-route", v4Res.Outcome, v4Res.Reason)
		}
	})
}

func TestVRFIsolation(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)
	p30 := vlan.ID(30)
	p40 := vlan.ID(40)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macTenantRouter := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x02}
	macTenantH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20", 30: "vlan30", 40: "vlan40"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
					"1/1/3": {PVID: &p30, Untagged: []vlan.ID{30}},
					"1/1/4": {PVID: &p40, Untagged: []vlan.ID{40}},
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
					Routes: []routing.Route{
						{Prefix: netip.MustParsePrefix("10.0.88.0/24"), Interface: "vlan20"},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
					},
				},
				"tenant": {
					Interfaces: map[string]routing.Interface{
						"vlan30": {VLAN: 30, MAC: macTenantRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan40": {VLAN: 40, MAC: macTenantRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan40", Addr: ipH2, MAC: macTenantH2},
					},
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)

	// Pre-learn on 1/1/2 and 1/1/4
	sw.Receive(fixedTime, "1/1/2", ethernet.Frame{
		Src:       macH2,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   makeIPv4Packet(t, ipH2, ipH1, 64, []byte("learn")),
	})
	sw.Receive(fixedTime, "1/1/4", ethernet.Frame{
		Src:       macTenantH2,
		Dst:       macTenantRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   makeIPv4Packet(t, ipH2, ipH1, 64, []byte("learn")),
	})

	t.Run("tenant routes in vlan 40 to tenant neighbor", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("tenant hello"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macTenantRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Receive(fixedTime, "1/1/3", frame)
		wantSteps := []trace.Step{
			{Layer: bridge.LayerNameVLAN, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "30"}},
			{Layer: bridge.LayerName, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan30"}},
			{Layer: routing.LayerName, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
			{Layer: routing.LayerName, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "vlan40"}},
			{Layer: bridge.LayerName, Op: trace.OpLookup, RuleID: "unicast-hit", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:99"}},
			{Layer: bridge.LayerNameVLAN, Op: trace.OpRewrite, RuleID: "vlan-tag-form", Subject: trace.Subject{Kind: "port", Key: "1/1/4"}},
			{Layer: bridge.LayerName, Op: trace.OpTransmit, RuleID: "transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/4"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Forwarded {
			t.Errorf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}
		if res.FID != 40 {
			t.Errorf("FID = %d, want 40", res.FID)
		}
		if res.Egress[0].Frame.Dst != macTenantH2 {
			t.Errorf("egress destination = %v, want %v", res.Egress[0].Frame.Dst, macTenantH2)
		}
	})

	t.Run("default still routes to default neighbor", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("default hello"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Receive(fixedTime, "1/1/1", frame)
		if res.FID != 20 || res.Egress[0].Frame.Dst != macH2 {
			t.Errorf("default routed frame FID=%d Dst=%v, want 20, %v", res.FID, res.Egress[0].Frame.Dst, macH2)
		}
	})

	t.Run("packet in tenant VRF to address only default has route for ends no-route", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, netip.MustParseAddr("10.0.88.7"), 64, []byte("exclusive"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macTenantRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Receive(fixedTime, "1/1/3", frame)
		wantSteps := []trace.Step{
			{Layer: bridge.LayerNameVLAN, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "30"}},
			{Layer: bridge.LayerName, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan30"}},
			{Layer: routing.LayerName, Op: trace.OpLookup, RuleID: "no-route", Subject: trace.Subject{Kind: "ip", Key: "10.0.88.7"}},
			{Layer: routing.LayerName, Op: trace.OpDrop, RuleID: "no-route", Subject: trace.Subject{Kind: "vrf", Key: "tenant"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNoRoute {
			t.Errorf("got outcome=%v reason=%v, want Dropped/no-route", res.Outcome, res.Reason)
		}
	})

	t.Run("duplicate VLAN across VRFs fails Validate", func(t *testing.T) {
		badCfg := cfg.Clone()
		tenantVRF := badCfg.Routing.VRFs["tenant"]
		tenantVRF.Interfaces["dup"] = routing.Interface{VLAN: 10, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.99.1/24")}}
		badCfg.Routing.VRFs["tenant"] = tenantVRF
		if err := badCfg.Validate(); err == nil {
			t.Error("Validate succeeded, want error for duplicate VLAN across VRFs")
		}
	})
}

func TestRoutedPortForwardingAndBypassRelay(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/5", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macPort5Neighbor := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	ipPort5Dst := netip.MustParseAddr("10.0.50.7")

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
						"1/1/5":  {Port: "1/1/5", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
						{Interface: "1/1/5", Addr: ipPort5Dst, MAC: macPort5Neighbor},
					},
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)
	baseMAC := sw.Config().MAC

	t.Run("packet from VLAN to routed port egresses untagged with transmit step", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipPort5Dst, 64, []byte("to port 5"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Receive(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: bridge.LayerNameVLAN, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
			{Layer: bridge.LayerName, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
			{Layer: routing.LayerName, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.50.0/24"}},
			{Layer: routing.LayerName, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "1/1/5"}},
			{Layer: routing.LayerName, Op: trace.OpTransmit, RuleID: "routing.transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/5"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Forwarded {
			t.Errorf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}
		if res.FID != 0 {
			t.Errorf("res.FID = %d, want 0 on routed port", res.FID)
		}
		if res.Ingress != "1/1/1" {
			t.Errorf("res.Ingress = %q, want 1/1/1", res.Ingress)
		}
		if len(res.Egress) != 1 || res.Egress[0].Port != "1/1/5" {
			t.Fatalf("res.Egress = %+v, want 1 entry on 1/1/5", res.Egress)
		}
		if len(res.Egress[0].Frame.Tags) != 0 {
			t.Errorf("res.Egress[0].Frame.Tags = %v, want untagged", res.Egress[0].Frame.Tags)
		}
	})

	t.Run("packet on routed port to base MAC routes to VLAN with no relay ingress step", func(t *testing.T) {
		// Pre-learn on 1/1/2
		sw.Receive(fixedTime, "1/1/2", ethernet.Frame{
			Src:       macH2,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   makeIPv4Packet(t, ipH2, ipH1, 64, []byte("learn")),
		})

		pkt := makeIPv4Packet(t, ipPort5Dst, ipH2, 64, []byte("from port 5"))
		frame := ethernet.Frame{
			Src:       macPort5Neighbor,
			Dst:       baseMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Receive(fixedTime, "1/1/5", frame)
		wantSteps := []trace.Step{
			{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "1/1/5"}},
			{Layer: routing.LayerName, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
			{Layer: routing.LayerName, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "vlan20"}},
			{Layer: bridge.LayerName, Op: trace.OpLookup, RuleID: "unicast-hit", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:77"}},
			{Layer: bridge.LayerNameVLAN, Op: trace.OpRewrite, RuleID: "vlan-tag-form", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
			{Layer: bridge.LayerName, Op: trace.OpTransmit, RuleID: "transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Forwarded {
			t.Errorf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}
		if res.FID != 20 {
			t.Errorf("res.FID = %d, want 20", res.FID)
		}
		if res.Ingress != "1/1/5" {
			t.Errorf("res.Ingress = %q, want 1/1/5", res.Ingress)
		}
	})

	t.Run("frame on routed port to other MAC drops with not-bridged", func(t *testing.T) {
		otherMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}
		frame := ethernet.Frame{
			Src:       macPort5Neighbor,
			Dst:       otherMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("test"),
		}
		res := sw.Receive(fixedTime, "1/1/5", frame)
		wantSteps := []trace.Step{
			{Layer: routing.LayerName, Op: trace.OpDrop, RuleID: "routing.not_bridged", Subject: trace.Subject{Kind: "port", Key: "1/1/5"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNotBridged {
			t.Errorf("got outcome=%v reason=%v, want Dropped/not-bridged", res.Outcome, res.Reason)
		}
		if res.FID != 0 {
			t.Errorf("res.FID = %d, want 0", res.FID)
		}
	})

	t.Run("down routed port drops with port-down under routing", func(t *testing.T) {
		if _, err := sw.LinkChange(time.Time{}, "1/1/5", port.Down, device.PointToPointTrue, 0); err != nil {
			t.Fatalf("LinkChange down: %v", err)
		}
		frame := ethernet.Frame{
			Src:       macPort5Neighbor,
			Dst:       baseMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   makeIPv4Packet(t, ipPort5Dst, ipH2, 64, []byte("down")),
		}
		res := sw.Receive(fixedTime, "1/1/5", frame)
		wantSteps := []trace.Step{
			{Layer: routing.LayerName, Op: trace.OpDrop, RuleID: "port.status.down", Subject: trace.Subject{Kind: "port", Key: "1/1/5"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != port.ReasonPortDown {
			t.Errorf("got outcome=%v reason=%v, want Dropped/port-down", res.Outcome, res.Reason)
		}
		if _, err := sw.LinkChange(time.Time{}, "1/1/5", port.Up, device.PointToPointTrue, 0); err != nil {
			t.Fatalf("LinkChange up: %v", err)
		}
	})

	t.Run("router without bridge routes packet between two ports", func(t *testing.T) {
		rPorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

		rCfg := vswitch.Config{
			Ports: rPorts,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
							"1/1/2": {Port: "1/1/2", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
						Neighbors: []routing.Neighbor{
							{Interface: "1/1/2", Addr: ipH2, MAC: macH2},
						},
					},
				},
			},
		}
		rSw := mustSwitch(t, rCfg)
		rBaseMAC := rSw.Config().MAC

		pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("router hello"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       rBaseMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := rSw.Receive(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: routing.LayerName, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "1/1/1"}},
			{Layer: routing.LayerName, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
			{Layer: routing.LayerName, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "1/1/2"}},
			{Layer: routing.LayerName, Op: trace.OpTransmit, RuleID: "routing.transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Forwarded {
			t.Errorf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}
	})
}

// TestRoutedSubInterfaceForwardingAndTagMiss covers a firewall cabled to a trunk with no
// bridge at all: the port carries two routed sub-interfaces classified by outer VLAN tag,
// beside a plain untagged routed port and, in a second switch, a bridge switchport sharing
// one sub-interface's VID. Sub-interface egress carries the routed VLAN's tag with the
// ingress priority; a routed port with no interface at the arriving VID or TPID drops rather
// than falling through to a bridge that does not exist for it.
func TestRoutedSubInterfaceForwardingAndTagMiss(t *testing.T) {
	neighbor20 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88}

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
					Neighbors: []routing.Neighbor{
						{Interface: "eth1.20", Addr: ipH2, MAC: neighbor20},
					},
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)

	t.Run("frame tagged 10 leaves tagged 20 carrying the ingress priority", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("sub-interface routing"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10, PCP: 3, DEI: true}},
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Receive(fixedTime, "eth1", frame)
		if res.Outcome != trace.Forwarded {
			t.Fatalf("outcome = %v, want %v; steps=%+v", res.Outcome, trace.Forwarded, res.Steps)
		}
		if len(res.Egress) != 1 || res.Egress[0].Port != "eth1" {
			t.Fatalf("egress = %+v, want one entry on eth1", res.Egress)
		}
		wantTag := vlan.Tag{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20, PCP: 3, DEI: true}
		if gotTags := res.Egress[0].Frame.Tags; len(gotTags) != 1 || gotTags[0] != wantTag {
			t.Errorf("egress tags = %+v, want [%+v]", gotTags, wantTag)
		}
	})

	t.Run("frame tagged 30 on the sub-interface port drops as not-bridged", func(t *testing.T) {
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 30}},
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("no sub-interface at vid 30"),
		}
		res := sw.Receive(fixedTime, "eth1", frame)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNotBridged {
			t.Fatalf("got outcome=%v reason=%v, want Dropped/not-bridged", res.Outcome, res.Reason)
		}
		if len(res.Steps) != 1 || res.Steps[0].RuleID != "routing.tag_miss" {
			t.Fatalf("steps = %+v, want a single routing.tag_miss drop", res.Steps)
		}
		wantFacts := []trace.Fact{routing.PortFact("eth1"), routing.VLANFact(30)}
		gotFacts := res.Steps[0].Inputs
		if !slices.EqualFunc(gotFacts, wantFacts, func(a, b trace.Fact) bool {
			return a.TypeID() == b.TypeID() && a.Canonical() == b.Canonical()
		}) {
			t.Errorf("fact inputs = %+v, want %+v", gotFacts, wantFacts)
		}
	})

	t.Run("untagged frame drops as not-bridged when no untagged sub-interface exists", func(t *testing.T) {
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("no untagged sub-interface here"),
		}
		res := sw.Receive(fixedTime, "eth1", frame)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNotBridged {
			t.Fatalf("got outcome=%v reason=%v, want Dropped/not-bridged", res.Outcome, res.Reason)
		}
		if len(res.Steps) != 1 || res.Steps[0].RuleID != "routing.tag_miss" {
			t.Fatalf("steps = %+v, want a single routing.tag_miss drop", res.Steps)
		}
	})

	t.Run("S-tagged frame on the sub-interface port drops as not-bridged", func(t *testing.T) {
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeProviderBridging), VID: 10}},
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("s-tagged, no C-TAG sub-interface can match"),
		}
		res := sw.Receive(fixedTime, "eth1", frame)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNotBridged {
			t.Fatalf("got outcome=%v reason=%v, want Dropped/not-bridged", res.Outcome, res.Reason)
		}
		if len(res.Steps) != 1 || res.Steps[0].RuleID != "routing.tag_protocol_miss" {
			t.Fatalf("steps = %+v, want a single routing.tag_protocol_miss drop", res.Steps)
		}
	})

	t.Run("tagged frame on a plain untagged routed port drops as not-bridged", func(t *testing.T) {
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("tagged on a plain routed port"),
		}
		res := sw.Receive(fixedTime, "eth3", frame)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNotBridged {
			t.Fatalf("got outcome=%v reason=%v, want Dropped/not-bridged", res.Outcome, res.Reason)
		}
		if len(res.Steps) != 1 || res.Steps[0].RuleID != "routing.tag_miss" {
			t.Fatalf("steps = %+v, want a single routing.tag_miss drop", res.Steps)
		}
	})

	t.Run("the plain untagged routed port still routes beside the sub-interface's port", func(t *testing.T) {
		pkt := makeIPv4Packet(t, netip.MustParseAddr("10.0.30.7"), ipH2, 64, []byte("plain routed port"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Receive(fixedTime, "eth3", frame)
		if res.Outcome != trace.Forwarded {
			t.Fatalf("outcome = %v, want %v; steps=%+v", res.Outcome, trace.Forwarded, res.Steps)
		}
		if len(res.Egress) != 1 || res.Egress[0].Port != "eth1" || len(res.Egress[0].Frame.Tags) != 1 {
			t.Fatalf("egress = %+v, want one tagged entry on eth1", res.Egress)
		}
	})

	t.Run("sub-interface egress refused for MTU still carries the tag on the egress record", func(t *testing.T) {
		// The egress tag is applied before the transmit and LAG checks, because a refused
		// egress record still embeds the frame those checks were given. Moving the tag
		// assignment below the transmit check would leave this refusal's egress record
		// untagged instead, and a comparison that matches egress records by tag equality
		// would not catch that silently.
		mtuPorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "ethA", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "ethB", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 40}))

		mtuCfg := vswitch.Config{
			Ports: mtuPorts,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"ethA":    {Port: "ethA", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.40.1/24")}},
							"ethB.10": {Port: "ethB", VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
						Neighbors: []routing.Neighbor{
							{Interface: "ethB.10", Addr: ipH2, MAC: macH2},
						},
					},
				},
			},
		}
		mtuSw := mustSwitch(t, mtuCfg)

		oversizedPayload := make([]byte, 50)
		pkt := makeIPv4Packet(t, netip.MustParseAddr("10.0.40.7"), ipH2, 64, oversizedPayload)
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := mtuSw.Receive(fixedTime, "ethA", frame)
		if res.Outcome != trace.Dropped || res.Reason != port.ReasonMTUExceeded {
			t.Fatalf("outcome=%v reason=%v, want Dropped/mtu-exceeded", res.Outcome, res.Reason)
		}
		step, ok := findStep(res.Steps, trace.RuleID("port.status.mtu-exceeded"))
		if !ok {
			t.Fatalf("no literal port.status.mtu-exceeded step in trace: %+v", res.Steps)
		}
		if step.Layer != routing.LayerName {
			t.Errorf("step layer = %v, want %v", step.Layer, routing.LayerName)
		}
		if len(res.Egress) != 1 || res.Egress[0].Port != "ethB" || res.Egress[0].Dropped != port.ReasonMTUExceeded {
			t.Fatalf("egress = %+v, want one refused entry on ethB", res.Egress)
		}
		wantTag := vlan.Tag{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}
		if gotTags := res.Egress[0].Frame.Tags; len(gotTags) != 1 || gotTags[0] != wantTag {
			t.Errorf("egress tags = %+v, want [%+v] even though transmit refused", gotTags, wantTag)
		}
	})

	p10 := vlan.ID(10)
	bridgeSw := mustSwitch(t, vswitch.Config{
		Ports: mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "eth1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "swport", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table:       map[vlan.ID]string{10: "ten"},
				Switchports: map[string]bridge.Switchport{"swport": {PVID: &p10, Untagged: []vlan.ID{10}}},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"eth1.10": {Port: "eth1", VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
					},
				},
			},
		},
	})

	t.Run("a bridge VLAN sharing the sub-interface's VID still floods rather than routing to it", func(t *testing.T) {
		// A bridge VLAN lookup must exclude the sub-interface. Otherwise Owns
		// would match the frame's router-MAC destination and it would leave
		// tagged out eth1 instead of taking the ordinary no-egress flood drop on swport alone.
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("stay on the bridge"),
		}
		res := bridgeSw.Receive(fixedTime, "swport", frame)
		if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonNoEgress {
			t.Fatalf("got outcome=%v reason=%v, want Dropped/no-egress", res.Outcome, res.Reason)
		}
	})
}

func TestSourceMACLearnedOnVLANAndNotOnRoutedPort(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/5", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macPort5Src := netaddr.MAC{0x00, 0x55, 0x55, 0x55, 0x55, 0x55}

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
						"1/1/5":  {Port: "1/1/5", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
					},
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)

	// Ingress on VLAN 10 learns source MAC on VLAN 10.
	vlanPkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("vlan learn"))
	sw.Receive(fixedTime, "1/1/1", ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   vlanPkt,
	})
	entries := sw.Entries()
	var learnedVLAN10 bool
	for _, e := range entries {
		if e.FID == 10 && e.MAC == macH1 && e.Port == "1/1/1" {
			learnedVLAN10 = true
		}
	}
	if !learnedVLAN10 {
		t.Errorf("FDB entries %+v did not contain macH1 on FID 10", entries)
	}

	// Ingress on routed port 1/1/5 must NOT learn anything in FDB.
	portPkt := makeIPv4Packet(t, netip.MustParseAddr("10.0.50.5"), ipH2, 64, []byte("port no learn"))
	sw.Receive(fixedTime, "1/1/5", ethernet.Frame{
		Src:       macPort5Src,
		Dst:       sw.Config().MAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   portPkt,
	})
	for _, e := range sw.Entries() {
		if e.MAC == macPort5Src {
			t.Errorf("routed port source MAC %v unexpectedly learned in FDB: %+v", macPort5Src, e)
		}
	}
}

func TestRoutedFrameLeavesOnSameTrunkPort(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {Tagged: []vlan.ID{10, 20}},
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
	sw := mustSwitch(t, cfg)

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("trunk reflection"))
	frame := ethernet.Frame{
		Tags:      []vlan.Tag{{VID: 10}},
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Receive(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("outcome = %v, want Flooded", res.Outcome)
	}
	if len(res.Egress) != 1 {
		t.Fatalf("len(res.Egress) = %d, want 1", len(res.Egress))
	}
	if res.Egress[0].Port != "1/1/1" {
		t.Errorf("egress port = %q, want 1/1/1", res.Egress[0].Port)
	}
	if len(res.Egress[0].Frame.Tags) != 1 || res.Egress[0].Frame.Tags[0].VID != 20 {
		t.Errorf("egress tags = %v, want tag with VID 20", res.Egress[0].Frame.Tags)
	}
	if res.Ingress != "1/1/1" {
		t.Errorf("res.Ingress = %q, want 1/1/1", res.Ingress)
	}
}

func TestRoutedFrameIngressLAGResolution(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/1a", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/1b", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"lag1":  {PVID: &p10, Untagged: []vlan.ID{10}},
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
	sw := mustSwitch(t, cfg)

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("lag hello"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Receive(fixedTime, "1/1/1a", frame)
	if res.Ingress != "lag1" {
		t.Errorf("res.Ingress = %q, want lag1", res.Ingress)
	}
}

func TestRoutedPortLAGLowestForwardingMember(t *testing.T) {
	p10 := vlan.ID(10)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag2", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/5b", Kind: port.Physical, LagParent: "lag2", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/5a", Kind: port.Physical, LagParent: "lag2", AdminStatus: port.Up, OperStatus: port.Up}))

	ipPort5Dst := netip.MustParseAddr("10.0.50.7")
	macPort5Neighbor := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"lag2":   {Port: "lag2", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "lag2", Addr: ipPort5Dst, MAC: macPort5Neighbor},
					},
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)

	pkt := makeIPv4Packet(t, ipH1, ipPort5Dst, 64, []byte("lag routed egress"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Receive(fixedTime, "1/1/1", frame)
	if len(res.Egress) != 1 {
		t.Fatalf("len(res.Egress) = %d, want 1", len(res.Egress))
	}
	if res.Egress[0].Port != "lag2" {
		t.Errorf("egress port = %q, want lag2", res.Egress[0].Port)
	}
	if res.Egress[0].Member != "1/1/5a" {
		t.Errorf("egress member = %q, want lowest-named 1/1/5a", res.Egress[0].Member)
	}
	if !traceHasFactType(res.Steps, "lag.selection") {
		t.Errorf("routed LAG trace has no member selection fact: %+v", res.Steps)
	}
	if !traceHasFactType(res.Steps, "routing.lookup_decision") {
		t.Errorf("routed LAG trace has no route lookup fact: %+v", res.Steps)
	}
}

func TestRoutedFrameMTUExceededVLANAndPort(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 40}).
		Add(port.Port{Name: "1/1/5", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 40}))

	ipPort5Dst := netip.MustParseAddr("10.0.50.7")
	macPort5Neighbor := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

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
						"1/1/5":  {Port: "1/1/5", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
						{Interface: "1/1/5", Addr: ipPort5Dst, MAC: macPort5Neighbor},
					},
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)

	// Pre-learn on 1/1/2
	sw.Receive(fixedTime, "1/1/2", ethernet.Frame{
		Src:       macH2,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   makeIPv4Packet(t, ipH2, ipH1, 64, []byte("x")),
	})

	// 50 bytes payload exceeds port MTU of 40.
	oversizedPayload := make([]byte, 50)

	t.Run("VLAN egress MTU exceeded", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipH2, 64, oversizedPayload)
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Receive(fixedTime, "1/1/1", frame)
		if res.Outcome != trace.Dropped || res.Reason != port.ReasonMTUExceeded {
			t.Errorf("outcome=%v reason=%v, want Dropped/mtu-exceeded", res.Outcome, res.Reason)
		}
		if len(res.Egress) != 1 || res.Egress[0].Dropped != port.ReasonMTUExceeded {
			t.Errorf("egress = %+v, want dropped for mtu-exceeded", res.Egress)
		}
	})

	t.Run("routed port egress MTU exceeded", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipPort5Dst, 64, oversizedPayload)
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Receive(fixedTime, "1/1/1", frame)
		if res.Outcome != trace.Dropped || res.Reason != port.ReasonMTUExceeded {
			t.Errorf("outcome=%v reason=%v, want Dropped/mtu-exceeded", res.Outcome, res.Reason)
		}
		step, ok := findStep(res.Steps, trace.RuleID("port.status.mtu-exceeded"))
		if !ok {
			t.Fatalf("no literal port.status.mtu-exceeded step in trace: %+v", res.Steps)
		}
		if step.Layer != routing.LayerName {
			t.Errorf("step layer = %v, want %v", step.Layer, routing.LayerName)
		}
		if len(res.Egress) != 1 || res.Egress[0].Dropped != port.ReasonMTUExceeded {
			t.Errorf("egress = %+v, want dropped for mtu-exceeded", res.Egress)
		}
	})
}

func TestRoutedFrameEgressLAGNoMember(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
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
					Neighbors: []routing.Neighbor{
						{Interface: "rp2", Addr: ipH2, MAC: macH2},
					},
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("hello lag"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}
	res := sw.Receive(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonNoMember {
		t.Fatalf("outcome=%v reason=%v, want Dropped/no-member", res.Outcome, res.Reason)
	}
	step, ok := findStep(res.Steps, trace.RuleID("lag.egress.no_member"))
	if !ok {
		t.Fatalf("no literal lag.egress.no_member step in trace: %+v", res.Steps)
	}
	if step.Layer != routing.LayerName {
		t.Errorf("step layer = %v, want %v", step.Layer, routing.LayerName)
	}
}

func TestRoutedFrameEgressPortBlocked(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
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
		STP: &stp.Config{
			Priority: 32768,
			Address:  macRouter,
			Ports: map[string]stp.Port{
				"1/1/1": {AdminEdge: true},
				"1/1/2": {AdminEdge: false},
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
	sw := mustSwitch(t, cfg)
	captureOutput(callOutputs, sw, sw.Start(fixedTime))
	takeEmissions(callOutputs, sw)

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("blocked egress"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", frame))
	if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonPortBlocked {
		t.Errorf("outcome=%v reason=%v, want Dropped/port-blocked", res.Outcome, res.Reason)
	}
	if len(res.Egress) != 1 || res.Egress[0].Dropped != bridge.ReasonPortBlocked {
		t.Errorf("egress = %+v, want dropped for port-blocked", res.Egress)
	}
}

func TestPeekLeavesFDBUnchangedWithRouting(t *testing.T) {
	sw := buildBaseRoutingSwitch(t)
	entriesBefore := sw.Entries()

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("peek hello"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Peek(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Errorf("Peek outcome = %v, want Flooded", res.Outcome)
	}
	entriesAfter := sw.Entries()
	if len(entriesBefore) != len(entriesAfter) {
		t.Errorf("FDB entry count before=%d, after=%d", len(entriesBefore), len(entriesAfter))
	}
}

func TestUndecodableARPToVLANInterfaceConsumed(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
					"1/1/3": {PVID: &p10, Untagged: []vlan.ID{10}},
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

	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeARP,
		Payload:   []byte("who has 10.0.10.1?"),
	}

	res := sw.Receive(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Consumed {
		t.Errorf("outcome = %v, want Consumed", res.Outcome)
	}
	if res.FID != 10 {
		t.Errorf("res.FID = %d, want 10", res.FID)
	}
	if len(res.Egress) != 0 {
		t.Errorf("egress = %+v, want none", res.Egress)
	}
}

func TestRoutedPortReceivesNeighborFrames(t *testing.T) {
	concrete := 0
	for _, dst := range []netaddr.MAC{macRouter, {255, 255, 255, 255, 255, 255}, macH2} {
		t.Run(dst.String(), func(t *testing.T) {
			sw := buildRoutedPortSwitch(t)
			frame := makeARPReply(t, ipH1, netip.MustParseAddr("10.0.10.1"), macH1, dst)
			out := sw.Receive(fixedTime, "1/1/1", frame)
			if dst == macH2 {
				if out.Outcome != trace.Dropped || out.Reason != routing.ReasonNotBridged {
					t.Fatalf("other receiver = %+v, want not-bridged", out)
				}
			} else if out.Outcome != trace.Consumed || len(out.Egress) != 0 {
				t.Fatalf("neighbor reception = %+v, want consumed", out)
			}
			concrete++
		})
	}
	if concrete != 3 {
		t.Fatalf("concrete destinations = %d, want 3", concrete)
	}
}

// routeLookupFact returns the canonical route-lookup fact carried by a forwarding
// result, which is where the equal-cost candidate set reaches a journey.
func routeLookupFact(t *testing.T, res device.Output) string {
	t.Helper()
	for _, step := range res.Steps {
		if step.Layer != routing.LayerName || step.Op != trace.OpLookup {
			continue
		}
		for _, f := range step.Outputs {
			if f.TypeID() == "routing.lookup_decision" && strings.Contains(f.Canonical(), ";matched=true;") {
				return f.Canonical()
			}
		}
	}
	t.Fatalf("no matched route lookup fact in steps: %+v", res.Steps)
	return ""
}

// routeCandidates splits the candidate list out of a route-lookup fact, each member
// rendered as "configured next hop|interface|forwarding next hop".
func routeCandidates(t *testing.T, fact string) (candidates []string, chosen int) {
	t.Helper()
	open := strings.Index(fact, ";candidates=[")
	if open < 0 || !strings.HasSuffix(fact, "]") {
		t.Fatalf("route fact carries no candidate set: %s", fact)
	}
	list := fact[open+len(";candidates=[") : len(fact)-1]
	if list != "" {
		candidates = strings.Split(list, ",")
	}

	chosenAt := strings.Index(fact, ";chosen=")
	if chosenAt < 0 {
		t.Fatalf("route fact names no chosen candidate: %s", fact)
	}
	rest := fact[chosenAt+len(";chosen="):]
	if end := strings.Index(rest, ";"); end >= 0 {
		rest = rest[:end]
	}
	chosen, err := strconv.Atoi(rest)
	if err != nil {
		t.Fatalf("chosen index %q: %v", rest, err)
	}
	if chosen < 0 || chosen >= len(candidates) {
		t.Fatalf("chosen index %d out of range for %v", chosen, candidates)
	}

	return candidates, chosen
}

func TestRoutedEgressNamesTheEqualCostCandidateSet(t *testing.T) {
	macH3 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88}
	nextHopA := netip.MustParseAddr("10.0.20.7")
	nextHopB := netip.MustParseAddr("10.0.30.7")
	dst := netip.MustParseAddr("10.0.99.5")

	routes := []routing.Route{
		{Prefix: netip.MustParsePrefix("10.0.99.0/24"), NextHop: nextHopA, Preference: 1, Metric: 10},
		{Prefix: netip.MustParsePrefix("10.0.99.0/24"), NextHop: nextHopB, Preference: 1, Metric: 10},
	}
	candidateSet := func(ifaceA, ifaceB string) []string {
		return []string{
			nextHopA.String() + "|" + ifaceA + "|" + nextHopA.String(),
			nextHopB.String() + "|" + ifaceB + "|" + nextHopB.String(),
		}
	}

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   makeIPv4Packet(t, ipH1, dst, 64, []byte("ecmp")),
	}

	t.Run("routed port", func(t *testing.T) {
		sw := mustSwitch(t, vswitch.Config{
			Ports: ports,
			Routing: &routing.Config{VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"1/1/1": {Port: "1/1/1", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"1/1/2": {Port: "1/1/2", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						"1/1/3": {Port: "1/1/3", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
					},
					Routes: routes,
					Neighbors: []routing.Neighbor{
						{Interface: "1/1/2", Addr: nextHopA, MAC: macH2},
						{Interface: "1/1/3", Addr: nextHopB, MAC: macH3},
					},
				},
			}},
		})

		res := sw.Receive(fixedTime, "1/1/1", frame)
		if res.Outcome != trace.Forwarded {
			t.Fatalf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}

		got, chosen := routeCandidates(t, routeLookupFact(t, res))
		if want := candidateSet("1/1/2", "1/1/3"); !slices.Equal(got, want) {
			t.Fatalf("candidates = %v, want %v", got, want)
		}

		wantPort := []string{"1/1/2", "1/1/3"}[chosen]
		wantMAC := []netaddr.MAC{macH2, macH3}[chosen]
		if len(res.Egress) != 1 || res.Egress[0].Port != wantPort {
			t.Fatalf("egress = %+v, want one on %s", res.Egress, wantPort)
		}
		if res.Egress[0].Frame.Dst != wantMAC {
			t.Errorf("egress destination MAC = %s, want %s for candidate %d", res.Egress[0].Frame.Dst, wantMAC, chosen)
		}
	})

	t.Run("VLAN interface", func(t *testing.T) {
		p10, p20, p30 := vlan.ID(10), vlan.ID(20), vlan.ID(30)
		sw := mustSwitch(t, vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20", 30: "vlan30"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
					"1/1/3": {PVID: &p30, Untagged: []vlan.ID{30}},
				},
			}},
			Routing: &routing.Config{VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						"vlan30": {VLAN: 30, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
					},
					Routes: routes,
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: nextHopA, MAC: macH2},
						{Interface: "vlan30", Addr: nextHopB, MAC: macH3},
					},
				},
			}},
		})
		mustSwitchLearn(t, sw, []bridge.Seed{
			{FID: 20, MAC: macH2, Port: "1/1/2", Lifetime: bridge.Static},
			{FID: 30, MAC: macH3, Port: "1/1/3", Lifetime: bridge.Static},
		})

		res := sw.Receive(fixedTime, "1/1/1", frame)
		if res.Outcome != trace.Forwarded {
			t.Fatalf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}

		got, chosen := routeCandidates(t, routeLookupFact(t, res))
		if want := candidateSet("vlan20", "vlan30"); !slices.Equal(got, want) {
			t.Fatalf("candidates = %v, want %v", got, want)
		}

		wantFID := []vlan.ID{20, 30}[chosen]
		wantPort := []string{"1/1/2", "1/1/3"}[chosen]
		wantMAC := []netaddr.MAC{macH2, macH3}[chosen]
		if res.FID != wantFID {
			t.Errorf("FID = %d, want %d for candidate %d", res.FID, wantFID, chosen)
		}
		if len(res.Egress) != 1 || res.Egress[0].Port != wantPort {
			t.Fatalf("egress = %+v, want one on %s", res.Egress, wantPort)
		}
		if res.Egress[0].Frame.Dst != wantMAC {
			t.Errorf("egress destination MAC = %s, want %s for candidate %d", res.Egress[0].Frame.Dst, wantMAC, chosen)
		}
	})
}

func TestConsumedRoutedNeighborRecordsMatchedVLAN(t *testing.T) {
	for _, vid := range []vlan.ID{0, 10} {
		t.Run(fmt.Sprint(vid), func(t *testing.T) {
			cfg := buildRoutedPortSwitch(t).Spec().Config
			vrf := cfg.Routing.VRFs[routing.DefaultVRF]
			iface := vrf.Interfaces["1/1/1"]
			iface.VLAN = vid
			vrf.Interfaces["1/1/1"] = iface
			cfg.Routing.VRFs[routing.DefaultVRF] = vrf
			sw := mustSwitch(t, cfg)
			frame := makeARPReply(t, ipH1, netip.MustParseAddr("10.0.10.1"), macH1, macRouter)
			if vid != 0 {
				frame.Tags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: vid}}
			}
			for _, out := range []device.Output{sw.Peek(fixedTime, "1/1/1", frame), sw.Receive(fixedTime, "1/1/1", frame)} {
				if out.Outcome != trace.Consumed || out.FID != 0 {
					t.Fatalf("output = %+v, want consumed with FID 0", out)
				}
				found := false
				for _, step := range out.Steps {
					if step.RuleID != routing.RuleClassify {
						continue
					}
					for _, fact := range step.Inputs {
						if fact.TypeID() == "routing.vlan" {
							found = true
							if got, want := fact.Canonical(), fmt.Sprint(vid); got != want {
								t.Errorf("VLAN fact = %q, want %q", got, want)
							}
						}
					}
				}
				if !found {
					t.Fatal("consumed step has no VLAN fact")
				}
			}
		})
	}
}
