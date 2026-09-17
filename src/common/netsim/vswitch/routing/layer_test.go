package routing_test

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

func standardSwitchConfig() routing.Config {
	deviceMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	neighborMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}

	return routing.Config{
		VRFs: map[string]routing.VRF{
			routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"vlan10": {
						VLAN: 10,
						MAC:  deviceMAC,
						Prefixes: []netip.Prefix{
							netip.MustParsePrefix("10.0.10.1/24"),
							netip.MustParsePrefix("2001:db8:10::1/64"),
						},
					},
					"vlan20": {
						VLAN: 20,
						MAC:  deviceMAC,
						Prefixes: []netip.Prefix{
							netip.MustParsePrefix("10.0.20.1/24"),
							netip.MustParsePrefix("2001:db8:20::1/64"),
						},
					},
				},
				Neighbors: []routing.Neighbor{
					{
						Interface: "vlan20",
						Addr:      netip.MustParseAddr("10.0.20.7"),
						MAC:       neighborMAC,
					},
					{
						Interface: "vlan20",
						Addr:      netip.MustParseAddr("2001:db8:20::7"),
						MAC:       neighborMAC,
					},
				},
			},
		},
	}
}

func encodeIPv4Packet(t *testing.T, src, dst netip.Addr, hopLimit uint8, payload []byte) []byte {
	t.Helper()
	hdr := ip.Header{
		Src:      src,
		Dst:      dst,
		HopLimit: hopLimit,
		Protocol: 17, // UDP
		V4:       &ip.V4{},
	}
	b, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv4 packet: %v", err)
	}
	return b
}

// encodeIPv6Packet fixes the hop limit at 64, unlike encodeIPv4Packet: hop
// limit expiry is exercised over IPv4 only, and the routing layer decrements
// both families in the same code path.
func encodeIPv6Packet(t *testing.T, src, dst netip.Addr, payload []byte) []byte {
	t.Helper()
	hdr := ip.Header{
		Src:      src,
		Dst:      dst,
		HopLimit: 64,
		Protocol: 17, // UDP
		V6:       &ip.V6{},
	}
	b, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv6 packet: %v", err)
	}
	return b
}

// testNow is the fixed instant most tests route or originate against; only the tests
// exercising the neighbor lifecycle's timers advance it themselves.
var testNow = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

func mustNewRouting(t *testing.T, cfg routing.Config) *routing.Layer {
	t.Helper()
	return mustNewRoutingWithPorts(t, cfg, port.Table{})
}

func mustNewRoutingWithPorts(t *testing.T, cfg routing.Config, ports port.Table) *routing.Layer {
	t.Helper()
	l, err := routing.New(cfg, ports, "sw1")
	if err != nil {
		t.Fatalf("routing.New: %v", err)
	}
	return l
}

func TestRouteIPv4Connected(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, standardSwitchConfig())

	ifaceName, ok := l.ByVLAN(10)
	if !ok || ifaceName != "vlan10" {
		t.Fatalf("ByVLAN(10) = %q, %v; want vlan10, true", ifaceName, ok)
	}

	srcMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	dstMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	pktPayload := []byte("hello network")
	pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.20.7"), 64, pktPayload)

	frame := ethernet.Frame{
		Src:       srcMAC,
		Dst:       dstMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	if !l.Owns(ifaceName, frame) {
		t.Fatalf("Owns(%q) = false, want true", ifaceName)
	}

	res := l.Route(testNow, ifaceName, frame, true)
	if res.Reason != "" {
		t.Fatalf("Route reason = %q, want empty", res.Reason)
	}
	if res.Interface != "vlan20" {
		t.Errorf("Route interface = %q, want vlan20", res.Interface)
	}

	wantNeighborMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	if res.Frame.Src != dstMAC {
		t.Errorf("egress frame src = %s, want %s", res.Frame.Src, dstMAC)
	}
	if res.Frame.Dst != wantNeighborMAC {
		t.Errorf("egress frame dst = %s, want %s", res.Frame.Dst, wantNeighborMAC)
	}
	if res.Frame.Tags != nil {
		t.Errorf("egress frame tags = %v, want nil", res.Frame.Tags)
	}

	decodedHdr, decodedPayload, err := ip.Decode(res.Frame.Payload)
	if err != nil {
		t.Fatalf("ip.Decode egress payload: %v", err)
	}
	if decodedHdr.HopLimit != 63 {
		t.Errorf("egress hop limit = %d, want 63", decodedHdr.HopLimit)
	}
	if string(decodedPayload) != string(pktPayload) {
		t.Errorf("egress payload = %q, want %q", decodedPayload, pktPayload)
	}

	expectedSteps := []trace.Step{
		{
			Layer:   port.LayerRouting,
			Op:      trace.OpClassify,
			RuleID:  "classify",
			Subject: trace.Subject{Kind: "interface", Key: "vlan10"},
		},
		{
			Layer:   port.LayerRouting,
			Op:      trace.OpLookup,
			RuleID:  "connected",
			Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"},
		},
		{
			Layer:   port.LayerRouting,
			Op:      trace.OpRewrite,
			RuleID:  "decrement-ttl",
			Subject: trace.Subject{Kind: "interface", Key: "vlan20"},
		},
	}
	if len(res.Steps) != len(expectedSteps) {
		t.Fatalf("steps count = %d, want %d: %+v", len(res.Steps), len(expectedSteps), res.Steps)
	}
	for i, step := range expectedSteps {
		if !sameStepIdentity(res.Steps[i], step) {
			t.Errorf("step %d = %+v, want %+v", i, res.Steps[i], step)
		}
	}
}

// TestRouteNeighborMiss covers the terminal miss a VRF that never resolves neighbors gives an
// absent entry; TestRouteNeighborLifecycle covers the pending answer NeighborObserved gives the
// same absent entry.
func TestRouteNeighborMiss(t *testing.T) {
	t.Parallel()
	cfg := standardSwitchConfig()
	vrf := cfg.VRFs[routing.DefaultVRF]
	vrf.Neighbors = nil
	vrf.NeighborPolicy = routing.NeighborPolicy{Mode: routing.NeighborDisabled}
	cfg.VRFs[routing.DefaultVRF] = vrf

	l := mustNewRouting(t, cfg)
	pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.20.7"), 64, []byte("payload"))
	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := l.Route(testNow, "vlan10", frame, true)
	if res.Reason != routing.ReasonNeighborMiss {
		t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonNeighborMiss)
	}
	if res.Interface != "vlan20" {
		t.Errorf("interface = %q, want matched target vlan20", res.Interface)
	}
	if len(res.Frame.Payload) != 0 {
		t.Errorf("expected no egress frame, got payload len %d", len(res.Frame.Payload))
	}
	wantScope := analysis.FieldScope(
		analysis.ProtocolScope("sw1", string(port.LayerRouting), routing.DefaultVRF),
		"interfaces", "vlan20", "neighbors", "10.0.20.7",
	)
	if !slices.ContainsFunc(res.ConsultedScopes(), func(scope analysis.Scope) bool {
		return scope.Compare(wantScope) == 0
	}) {
		t.Errorf("consulted scopes = %v, want exact neighbor lookup %s", res.ConsultedScopes(), wantScope)
	}

	expectedSteps := []trace.Step{
		{
			Layer:   port.LayerRouting,
			Op:      trace.OpClassify,
			RuleID:  "classify",
			Subject: trace.Subject{Kind: "interface", Key: "vlan10"},
		},
		{
			Layer:   port.LayerRouting,
			Op:      trace.OpLookup,
			RuleID:  "connected",
			Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"},
		},
		{
			Layer:   port.LayerRouting,
			Op:      trace.OpDrop,
			RuleID:  "neighbor-miss",
			Subject: trace.Subject{Kind: "ip", Key: "10.0.20.7"},
		},
	}
	if len(res.Steps) != len(expectedSteps) {
		t.Fatalf("steps count = %d, want %d: %+v", len(res.Steps), len(expectedSteps), res.Steps)
	}
	for i, step := range expectedSteps {
		if !sameStepIdentity(res.Steps[i], step) {
			t.Errorf("step %d = %+v, want %+v", i, res.Steps[i], step)
		}
	}
}

// TestRouteNeighborLifecycle covers the three answers an unresolved next hop can now give
// (pending, miss, disabled) beside each other, and that a peek changes nothing a later commit
// can see.
func TestRouteNeighborLifecycle(t *testing.T) {
	t.Parallel()

	cfgWithPolicy := func(policy routing.NeighborPolicy) routing.Config {
		return routing.Config{VRFs: map[string]routing.VRF{
			routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"vlan10": {
						VLAN:     10,
						MAC:      netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01},
						Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.99.0/24")},
					},
				},
				NeighborPolicy: policy,
			},
		}}
	}
	pendingFrame := func(t *testing.T) ethernet.Frame {
		t.Helper()
		return ethernet.Frame{
			Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
			Dst:       netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01},
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   encodeIPv4Packet(t, netip.MustParseAddr("10.0.99.1"), netip.MustParseAddr("10.0.99.7"), 64, []byte("data")),
		}
	}

	t.Run("pending under NeighborObserved", func(t *testing.T) {
		t.Parallel()
		l := mustNewRouting(t, cfgWithPolicy(routing.NeighborPolicy{}))
		res := l.Route(testNow, "vlan10", pendingFrame(t), true)
		if res.Reason != routing.ReasonNeighborPending {
			t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonNeighborPending)
		}
		if len(res.Frame.Payload) != 0 {
			t.Errorf("expected no egress frame while pending, got payload len %d", len(res.Frame.Payload))
		}
	})

	t.Run("miss under NeighborDisabled", func(t *testing.T) {
		t.Parallel()
		l := mustNewRouting(t, cfgWithPolicy(routing.NeighborPolicy{Mode: routing.NeighborDisabled}))
		res := l.Route(testNow, "vlan10", pendingFrame(t), true)
		if res.Reason != routing.ReasonNeighborMiss {
			t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonNeighborMiss)
		}
	})

	t.Run("commit false changes nothing", func(t *testing.T) {
		t.Parallel()
		l := mustNewRouting(t, cfgWithPolicy(routing.NeighborPolicy{}))
		first := l.Route(testNow, "vlan10", pendingFrame(t), false)
		second := l.Route(testNow, "vlan10", pendingFrame(t), false)
		if first.Reason != routing.ReasonNeighborPending || second.Reason != routing.ReasonNeighborPending {
			t.Fatalf("reasons = %q, %q, want both %q", first.Reason, second.Reason, routing.ReasonNeighborPending)
		}
		// If either peek had created an entry, NextWake would name its resolution deadline.
		if _, ok := l.NextWake(); ok {
			t.Fatal("NextWake reports a timer after two peeks, want none: a peek must not create an entry")
		}
		// If either peek had created an entry or queued a frame, Wake would report it.
		eff := l.Wake(testNow.Add(time.Hour))
		if len(eff.Failed) != 0 || len(eff.Released) != 0 {
			t.Fatalf("effects = %+v, want none: a peek must not create an entry or queue a frame", eff)
		}
	})
}

func TestRouteDropsAndLocalAddress(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, standardSwitchConfig())
	deviceMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}

	t.Run("hop limit 1 drops with ttl-expired", func(t *testing.T) {
		t.Parallel()
		pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.20.7"), 1, []byte("payload"))
		frame := ethernet.Frame{
			Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
			Dst:       deviceMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := l.Route(testNow, "vlan10", frame, true)
		if res.Reason != routing.ReasonTTLExpired {
			t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonTTLExpired)
		}
		if len(res.Frame.Payload) != 0 {
			t.Errorf("expected no egress payload, got %d bytes", len(res.Frame.Payload))
		}
		if last := res.Steps[len(res.Steps)-1]; last.Op != trace.OpDrop || last.RuleID != trace.RuleID(routing.ReasonTTLExpired) {
			t.Errorf("last step = %+v, want drop ttl-expired", last)
		}
	})

	t.Run("packet to device interface address marked not-routed without drop step", func(t *testing.T) {
		t.Parallel()
		pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.10.1"), 64, []byte("ping"))
		frame := ethernet.Frame{
			Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
			Dst:       deviceMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := l.Route(testNow, "vlan10", frame, true)
		if res.Reason != routing.ReasonNotRouted {
			t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonNotRouted)
		}
		for _, s := range res.Steps {
			if s.Op == trace.OpDrop {
				t.Errorf("unexpected drop step in not-routed result: %+v", s)
			}
		}
		if last := res.Steps[len(res.Steps)-1]; last.Op != trace.OpLookup || last.RuleID != "local-delivery" || last.Subject.Key != "10.0.10.1" {
			t.Errorf("last step = %+v, want lookup local-delivery 10.0.10.1", last)
		}
	})

	t.Run("corrupt checksum drops with bad-header", func(t *testing.T) {
		t.Parallel()
		pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.20.7"), 64, []byte("payload"))
		// Corrupt checksum in header octet 10.
		corruptPkt := append([]byte(nil), pkt...)
		corruptPkt[10] ^= 0x01

		frame := ethernet.Frame{
			Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
			Dst:       deviceMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   corruptPkt,
		}
		res := l.Route(testNow, "vlan10", frame, true)
		if res.Reason != routing.ReasonBadHeader {
			t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonBadHeader)
		}
		if last := res.Steps[len(res.Steps)-1]; last.Op != trace.OpDrop || last.RuleID != trace.RuleID(routing.ReasonBadHeader) {
			t.Errorf("last step = %+v, want drop bad-header", last)
		}
	})

	t.Run("packet to unrouted destination drops with no-route", func(t *testing.T) {
		t.Parallel()
		pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.30.7"), 64, []byte("payload"))
		frame := ethernet.Frame{
			Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
			Dst:       deviceMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := l.Route(testNow, "vlan10", frame, true)
		if res.Reason != routing.ReasonNoRoute {
			t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonNoRoute)
		}
		if last := res.Steps[len(res.Steps)-1]; last.Op != trace.OpDrop || last.RuleID != "no-route" || last.Subject.Key != "default" {
			t.Errorf("last step = %+v, want drop vrf default", last)
		}
	})
}

func TestRouteIPv6(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, standardSwitchConfig())
	deviceMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}

	pkt := encodeIPv6Packet(t, netip.MustParseAddr("2001:db8:10::7"), netip.MustParseAddr("2001:db8:20::7"), []byte("ipv6 data"))
	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       deviceMAC,
		EtherType: ethernet.EtherTypeIPv6,
		Payload:   pkt,
	}

	res := l.Route(testNow, "vlan10", frame, true)
	if res.Reason != "" {
		t.Fatalf("route reason = %q, want empty", res.Reason)
	}
	if res.Interface != "vlan20" {
		t.Errorf("interface = %q, want vlan20", res.Interface)
	}

	decodedHdr, _, err := ip.Decode(res.Frame.Payload)
	if err != nil {
		t.Fatalf("ip.Decode egress payload: %v", err)
	}
	if decodedHdr.HopLimit != 63 {
		t.Errorf("hop limit = %d, want 63", decodedHdr.HopLimit)
	}

	// Unrouted IPv6 prefix.
	unroutedPkt := encodeIPv6Packet(t, netip.MustParseAddr("2001:db8:10::7"), netip.MustParseAddr("2001:db8:30::7"), []byte("ipv6 data"))
	unroutedFrame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       deviceMAC,
		EtherType: ethernet.EtherTypeIPv6,
		Payload:   unroutedPkt,
	}
	unroutedRes := l.Route(testNow, "vlan10", unroutedFrame, true)
	if unroutedRes.Reason != routing.ReasonNoRoute {
		t.Fatalf("unrouted reason = %q, want %q", unroutedRes.Reason, routing.ReasonNoRoute)
	}
}

func TestVRFIsolation(t *testing.T) {
	t.Parallel()
	cfg := standardSwitchConfig()
	tenantMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x02}
	tenantNeighborMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}

	cfg.VRFs["tenant"] = routing.VRF{
		Interfaces: map[string]routing.Interface{
			"vlan30": {
				VLAN:     30,
				MAC:      tenantMAC,
				Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")},
			},
			"vlan40": {
				VLAN:     40,
				MAC:      tenantMAC,
				Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")},
			},
		},
		Neighbors: []routing.Neighbor{
			{
				Interface: "vlan40",
				Addr:      netip.MustParseAddr("10.0.20.7"),
				MAC:       tenantNeighborMAC,
			},
		},
	}

	l := mustNewRouting(t, cfg)

	// Injected on vlan30 to tenant MAC.
	pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.20.7"), 64, []byte("tenant traffic"))
	frameTenant := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       tenantMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	resTenant := l.Route(testNow, "vlan30", frameTenant, true)
	if resTenant.Reason != "" {
		t.Fatalf("tenant route reason = %q, want empty", resTenant.Reason)
	}
	if resTenant.Interface != "vlan40" {
		t.Errorf("tenant interface = %q, want vlan40", resTenant.Interface)
	}
	if resTenant.Frame.Dst != tenantNeighborMAC {
		t.Errorf("tenant frame dst = %s, want %s", resTenant.Frame.Dst, tenantNeighborMAC)
	}
	if resTenant.Steps[0].RuleID != "classify" || resTenant.Steps[0].Subject.Key != "vlan30" {
		t.Errorf("tenant classify step = %+v, want classify vlan30", resTenant.Steps[0])
	}

	// Packet in default VRF still routes to default VRF neighbor.
	defaultMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	defaultNeighborMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	frameDefault := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       defaultMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}
	resDefault := l.Route(testNow, "vlan10", frameDefault, true)
	if resDefault.Interface != "vlan20" || resDefault.Frame.Dst != defaultNeighborMAC {
		t.Errorf("default route mismatch: iface %q, dst %s", resDefault.Interface, resDefault.Frame.Dst)
	}

	// Packet on vlan30 to an address only default VRF holds a route for.
	cfgWithDefaultRoute := cfg.Clone()
	vrfDef := cfgWithDefaultRoute.VRFs[routing.DefaultVRF]
	vrfDef.Routes = append(vrfDef.Routes, routing.Route{
		Prefix:    netip.MustParsePrefix("10.0.99.0/24"),
		NextHop:   netip.MustParseAddr("10.0.10.7"),
		Interface: "vlan10",
	})
	cfgWithDefaultRoute.VRFs[routing.DefaultVRF] = vrfDef
	l2 := mustNewRouting(t, cfgWithDefaultRoute)

	pkt99 := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.99.7"), 64, []byte("data"))
	frameTenant99 := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       tenantMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt99,
	}
	resTenant99 := l2.Route(testNow, "vlan30", frameTenant99, true)
	if resTenant99.Reason != routing.ReasonNoRoute {
		t.Fatalf("tenant99 reason = %q, want %q", resTenant99.Reason, routing.ReasonNoRoute)
	}
	if last := resTenant99.Steps[len(resTenant99.Steps)-1]; last.Op != trace.OpDrop || last.RuleID != "no-route" || last.Subject.Key != "tenant" {
		t.Errorf("tenant99 drop step = %+v, want drop naming vrf tenant", last)
	}
}

func TestRoutedPort(t *testing.T) {
	t.Parallel()
	baseMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	neighbor55 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	cfg := standardSwitchConfig()
	vrf := cfg.VRFs[routing.DefaultVRF]
	vrf.Interfaces["1/1/5"] = routing.Interface{
		Port:     "1/1/5",
		MAC:      baseMAC,
		Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")},
	}
	vrf.Neighbors = append(vrf.Neighbors, routing.Neighbor{
		Interface: "1/1/5",
		Addr:      netip.MustParseAddr("10.0.50.7"),
		MAC:       neighbor55,
	})
	cfg.VRFs[routing.DefaultVRF] = vrf

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/5", Kind: port.Physical}).
		Build()
	if err != nil {
		t.Fatalf("port.NewBuilder: %v", err)
	}
	l := mustNewRoutingWithPorts(t, cfg, ports)

	ifName, ok := l.ByPort("1/1/5")
	if !ok || ifName != "1/1/5" {
		t.Fatalf("ByPort(1/1/5) = %q, %v; want 1/1/5, true", ifName, ok)
	}

	// Packet from vlan10 to 10.0.50.7 egresses on 1/1/5.
	pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.50.7"), 64, []byte("data"))
	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       baseMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}
	res := l.Route(testNow, "vlan10", frame, true)
	if res.Reason != "" {
		t.Fatalf("route reason = %q, want empty", res.Reason)
	}
	if res.Interface != "1/1/5" {
		t.Errorf("interface = %q, want 1/1/5", res.Interface)
	}
	if res.Frame.Dst != neighbor55 {
		t.Errorf("destination MAC = %s, want %s", res.Frame.Dst, neighbor55)
	}

	// Packet arriving on 1/1/5 to baseMAC for 10.0.20.7 routes to vlan20.
	pkt20 := encodeIPv4Packet(t, netip.MustParseAddr("10.0.50.7"), netip.MustParseAddr("10.0.20.7"), 64, []byte("data"))
	frameOnPort := ethernet.Frame{
		Src:       neighbor55,
		Dst:       baseMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt20,
	}
	if !l.Owns("1/1/5", frameOnPort) {
		t.Fatalf("Owns(1/1/5) = false, want true")
	}
	resFromPort := l.Route(testNow, "1/1/5", frameOnPort, true)
	if resFromPort.Reason != "" {
		t.Fatalf("route from port reason = %q, want empty", resFromPort.Reason)
	}
	if resFromPort.Interface != "vlan20" {
		t.Errorf("interface = %q, want vlan20", resFromPort.Interface)
	}
	if resFromPort.Steps[0].RuleID != "classify" || resFromPort.Steps[0].Subject.Key != "1/1/5" {
		t.Errorf("classify step = %+v, want classify 1/1/5", resFromPort.Steps[0])
	}
}

func TestStaticRoutes(t *testing.T) {
	t.Parallel()
	baseMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	gwMAC := netaddr.MAC{0x00, 0x22, 0x33, 0x44, 0x55, 0x66}
	hostMAC := netaddr.MAC{0x00, 0x22, 0x33, 0x44, 0x55, 0x77}

	cfg := routing.Config{
		VRFs: map[string]routing.VRF{
			routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"vlan10": {
						VLAN:     10,
						MAC:      baseMAC,
						Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")},
					},
					"vlan20": {
						VLAN:     20,
						MAC:      baseMAC,
						Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")},
					},
				},
				Routes: []routing.Route{
					// Default route via vlan10 gateway.
					{
						Prefix:    netip.MustParsePrefix("0.0.0.0/0"),
						NextHop:   netip.MustParseAddr("10.0.10.254"),
						Interface: "vlan10",
					},
					// Route with next hop alone; interface resolved from prefix containing it (vlan20).
					{
						Prefix:  netip.MustParsePrefix("192.168.1.0/24"),
						NextHop: netip.MustParseAddr("10.0.20.254"),
					},
					// Route with interface alone; resolves destination as neighbor on that interface.
					{
						Prefix:    netip.MustParsePrefix("192.168.2.0/24"),
						Interface: "vlan20",
					},
				},
				Neighbors: []routing.Neighbor{
					{
						Interface: "vlan10",
						Addr:      netip.MustParseAddr("10.0.10.254"),
						MAC:       gwMAC,
					},
					{
						Interface: "vlan20",
						Addr:      netip.MustParseAddr("10.0.20.254"),
						MAC:       gwMAC,
					},
					{
						Interface: "vlan20",
						Addr:      netip.MustParseAddr("192.168.2.5"),
						MAC:       hostMAC,
					},
				},
			},
		},
	}

	l := mustNewRouting(t, cfg)

	t.Run("static route to next hop resolved on interface whose prefix contains it", func(t *testing.T) {
		t.Parallel()
		pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("192.168.1.5"), 64, []byte("data"))
		frame := ethernet.Frame{
			Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
			Dst:       baseMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := l.Route(testNow, "vlan10", frame, true)
		if res.Reason != "" {
			t.Fatalf("reason = %q, want empty", res.Reason)
		}
		if res.Interface != "vlan20" {
			t.Errorf("interface = %q, want vlan20", res.Interface)
		}
		if res.Frame.Dst != gwMAC {
			t.Errorf("destination MAC = %s, want %s", res.Frame.Dst, gwMAC)
		}
		if res.Steps[1].RuleID != "static" || res.Steps[1].Subject.Key != "192.168.1.0/24" {
			t.Errorf("lookup step = %+v, want static 192.168.1.0/24", res.Steps[1])
		}
	})

	t.Run("static route with interface alone resolving destination as neighbor", func(t *testing.T) {
		t.Parallel()
		pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("192.168.2.5"), 64, []byte("data"))
		frame := ethernet.Frame{
			Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
			Dst:       baseMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := l.Route(testNow, "vlan10", frame, true)
		if res.Reason != "" {
			t.Fatalf("reason = %q, want empty", res.Reason)
		}
		if res.Interface != "vlan20" {
			t.Errorf("interface = %q, want vlan20", res.Interface)
		}
		if res.Frame.Dst != hostMAC {
			t.Errorf("destination MAC = %s, want %s", res.Frame.Dst, hostMAC)
		}
	})

	t.Run("longest prefix winning over shorter static default", func(t *testing.T) {
		t.Parallel()
		// 192.168.1.5 matches 192.168.1.0/24 (vlan20) rather than 0.0.0.0/0 (vlan10).
		pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("192.168.1.5"), 64, []byte("data"))
		frame := ethernet.Frame{
			Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
			Dst:       baseMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := l.Route(testNow, "vlan10", frame, true)
		if res.Interface != "vlan20" {
			t.Errorf("interface = %q, want vlan20", res.Interface)
		}

		// 8.8.8.8 matches 0.0.0.0/0 and egresses on vlan10.
		pktDefault := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("8.8.8.8"), 64, []byte("data"))
		frameDefault := ethernet.Frame{
			Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
			Dst:       baseMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pktDefault,
		}
		resDefault := l.Route(testNow, "vlan10", frameDefault, true)
		if resDefault.Interface != "vlan10" {
			t.Errorf("default route interface = %q, want vlan10", resDefault.Interface)
		}
	})
}

func TestStaticRouteVRFBoundaries(t *testing.T) {
	t.Parallel()
	ports := newTestPortTable(t)

	// A next hop only another VRF can reach is valid configuration whose route is
	// withdrawn at build, because resolution walks its own VRF's table alone.
	isolatedCfg := routing.Config{
		VRFs: map[string]routing.VRF{
			"vrf1": {
				Interfaces: map[string]routing.Interface{
					"vlan10": {
						VLAN:     10,
						Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")},
					},
				},
				Routes: []routing.Route{
					{
						Prefix:  netip.MustParsePrefix("192.168.1.0/24"),
						NextHop: netip.MustParseAddr("10.0.20.254"), // only in vrf2
					},
				},
			},
			"vrf2": {
				Interfaces: map[string]routing.Interface{
					"vlan20": {
						VLAN:     20,
						Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")},
					},
				},
			},
		},
	}
	isolated := mustNewRoutingWithPorts(t, isolatedCfg, ports)
	withdrawn := isolated.WithdrawnRoutes("vrf1")
	if len(withdrawn) != 1 || withdrawn[0].Reason != routing.WithdrawnUnresolved {
		t.Fatalf("vrf1 withdrawals = %+v, want the route to 192.168.1.0/24 as %q", withdrawn, routing.WithdrawnUnresolved)
	}
	if got := isolated.WithdrawnRoutes("vrf2"); len(got) != 0 {
		t.Errorf("vrf2 withdrawals = %+v, want none", got)
	}

	// When route explicitly names interface, Validate accepts it and neighbor resolution
	// searches only within its own VRF.
	validExplicitCfg := routing.Config{
		VRFs: map[string]routing.VRF{
			"vrf1": {
				Interfaces: map[string]routing.Interface{
					"vlan10": {
						VLAN:     10,
						MAC:      netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01},
						Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")},
					},
				},
				Routes: []routing.Route{
					{
						Prefix:    netip.MustParsePrefix("192.168.1.0/24"),
						NextHop:   netip.MustParseAddr("10.0.20.254"),
						Interface: "vlan10",
					},
				},
				Neighbors: []routing.Neighbor{
					{
						Interface: "vlan10",
						Addr:      netip.MustParseAddr("10.0.20.254"),
						MAC:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
					},
				},
			},
			"vrf2": {
				Interfaces: map[string]routing.Interface{
					"vlan20": {
						VLAN:     20,
						MAC:      netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x02},
						Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")},
					},
				},
				Neighbors: []routing.Neighbor{
					{
						Interface: "vlan20",
						Addr:      netip.MustParseAddr("10.0.20.254"),
						MAC:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x22},
					},
				},
			},
		},
	}
	if err := validExplicitCfg.Validate(ports); err != nil {
		t.Fatalf("expected valid configuration, got: %v", err)
	}

	l := mustNewRoutingWithPorts(t, validExplicitCfg, ports)
	pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("192.168.1.5"), 64, []byte("data"))
	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77},
		Dst:       netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}
	res := l.Route(testNow, "vlan10", frame, true)
	if res.Reason != "" {
		t.Fatalf("reason = %q, want empty", res.Reason)
	}
	// Resolved in VRF 1 neighbor table alone.
	wantMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	if res.Frame.Dst != wantMAC {
		t.Errorf("destination MAC = %s, want %s", res.Frame.Dst, wantMAC)
	}
}

func TestOwns(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, standardSwitchConfig())
	deviceMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	wrongMAC := netaddr.MAC{0x00, 0x99, 0x99, 0x99, 0x99, 0x99}

	t.Run("right MAC on unrouted VLAN", func(t *testing.T) {
		t.Parallel()
		frame := ethernet.Frame{
			Dst:       deviceMAC,
			EtherType: ethernet.EtherTypeIPv4,
		}
		if l.Owns("vlan99", frame) {
			t.Errorf("Owns(vlan99) = true, want false")
		}
	})

	t.Run("wrong MAC on routed VLAN", func(t *testing.T) {
		t.Parallel()
		frame := ethernet.Frame{
			Dst:       wrongMAC,
			EtherType: ethernet.EtherTypeIPv4,
		}
		if l.Owns("vlan10", frame) {
			t.Errorf("Owns with wrong MAC = true, want false")
		}
	})

	t.Run("right MAC with ARP EtherType", func(t *testing.T) {
		t.Parallel()
		frame := ethernet.Frame{
			Dst:       deviceMAC,
			EtherType: ethernet.EtherTypeARP,
		}
		if l.Owns("vlan10", frame) {
			t.Errorf("Owns with ARP = true, want false")
		}
	})
}

func TestOriginate(t *testing.T) {
	t.Parallel()
	baseMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	gwMAC := netaddr.MAC{0x00, 0x22, 0x33, 0x44, 0x55, 0x66}
	hostMAC := netaddr.MAC{0x00, 0x22, 0x33, 0x44, 0x55, 0x77}

	cfg := routing.Config{
		VRFs: map[string]routing.VRF{
			routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"vlan10": {
						VLAN: 10,
						MAC:  baseMAC,
						Prefixes: []netip.Prefix{
							netip.MustParsePrefix("10.0.0.1/16"),
							netip.MustParsePrefix("10.0.10.1/24"),
						},
					},
					"vlan20": {
						VLAN: 20,
						MAC:  baseMAC,
						Prefixes: []netip.Prefix{
							netip.MustParsePrefix("10.0.20.1/24"),
						},
					},
				},
				Routes: []routing.Route{
					{
						Prefix:    netip.MustParsePrefix("0.0.0.0/0"),
						NextHop:   netip.MustParseAddr("10.0.10.254"),
						Interface: "vlan10",
					},
				},
				Neighbors: []routing.Neighbor{
					{
						Interface: "vlan10",
						Addr:      netip.MustParseAddr("10.0.10.254"),
						MAC:       gwMAC,
					},
					{
						Interface: "vlan10",
						Addr:      netip.MustParseAddr("10.0.10.7"),
						MAC:       hostMAC,
					},
					{
						Interface: "vlan10",
						Addr:      netip.MustParseAddr("10.0.50.7"),
						MAC:       hostMAC,
					},
				},
				// Disabled so the unresolved-destination subtest below still exercises the
				// terminal miss; TestRouteNeighborLifecycle covers the pending answer.
				NeighborPolicy: routing.NeighborPolicy{Mode: routing.NeighborDisabled},
			},
		},
	}

	l := mustNewRouting(t, cfg)

	t.Run("longest matching source selection", func(t *testing.T) {
		t.Parallel()
		// 10.0.10.7 matches 10.0.10.1/24 (/24 > /16), so source should be 10.0.10.1.
		res := l.Originate(testNow, routing.DefaultVRF, netip.MustParseAddr("10.0.10.7"), 17, []byte("originate payload"), true)
		if res.Reason != "" {
			t.Fatalf("reason = %q, want empty", res.Reason)
		}
		decodedHdr, _, err := ip.Decode(res.Frame.Payload)
		if err != nil {
			t.Fatalf("ip.Decode: %v", err)
		}
		if decodedHdr.Src != netip.MustParseAddr("10.0.10.1") {
			t.Errorf("src addr = %s, want 10.0.10.1", decodedHdr.Src)
		}
		if decodedHdr.HopLimit != 64 {
			t.Errorf("hop limit = %d, want 64", decodedHdr.HopLimit)
		}
		if res.Frame.Src != baseMAC || res.Frame.Dst != hostMAC {
			t.Errorf("frame MACs: src %s, dst %s", res.Frame.Src, res.Frame.Dst)
		}
	})

	t.Run("direct on connected prefix", func(t *testing.T) {
		t.Parallel()
		// 10.0.50.7 matches 10.0.0.1/16 (connected prefix).
		res := l.Originate(testNow, routing.DefaultVRF, netip.MustParseAddr("10.0.50.7"), 17, []byte("direct payload"), true)
		if res.Reason != "" {
			t.Fatalf("reason = %q, want empty", res.Reason)
		}
		if res.Frame.Dst != hostMAC {
			t.Errorf("dst MAC = %s, want host MAC %s", res.Frame.Dst, hostMAC)
		}
	})

	t.Run("default route gateway for off-prefix destination", func(t *testing.T) {
		t.Parallel()
		res := l.Originate(testNow, routing.DefaultVRF, netip.MustParseAddr("198.51.100.1"), 17, []byte("wan payload"), true)
		if res.Reason != "" {
			t.Fatalf("reason = %q, want empty", res.Reason)
		}
		if res.Frame.Dst != gwMAC {
			t.Errorf("dst MAC = %s, want gateway MAC %s", res.Frame.Dst, gwMAC)
		}
		decodedHdr, _, err := ip.Decode(res.Frame.Payload)
		if err != nil {
			t.Fatalf("ip.Decode: %v", err)
		}
		// First address of IPv4 family in sorted order is 10.0.0.1.
		if decodedHdr.Src != netip.MustParseAddr("10.0.0.1") {
			t.Errorf("src addr = %s, want 10.0.0.1", decodedHdr.Src)
		}
		if decodedHdr.HopLimit != 64 {
			t.Errorf("hop limit = %d, want 64", decodedHdr.HopLimit)
		}
	})

	t.Run("neighbor miss and no route", func(t *testing.T) {
		t.Parallel()
		// Neighbor miss: 10.0.20.7 has no neighbor entry on vlan20.
		resMiss := l.Originate(testNow, routing.DefaultVRF, netip.MustParseAddr("10.0.20.7"), 17, []byte("data"), true)
		if resMiss.Reason != routing.ReasonNeighborMiss {
			t.Fatalf("reason = %q, want %q", resMiss.Reason, routing.ReasonNeighborMiss)
		}
		if resMiss.Interface != "vlan20" {
			t.Errorf("interface = %q, want matched target vlan20", resMiss.Interface)
		}

		// No route in unknown VRF or family with no route.
		resNoRoute := l.Originate(testNow, "nonexistent", netip.MustParseAddr("10.0.10.7"), 17, []byte("data"), true)
		if resNoRoute.Reason != routing.ReasonNoRoute {
			t.Fatalf("reason = %q, want %q", resNoRoute.Reason, routing.ReasonNoRoute)
		}
	})
}

// TestRouteRefusesEtherTypeFamilyMismatch is evidence that the header family
// the version nibble names must agree with the frame's EtherType, since the
// egress frame keeps that EtherType.
func TestRouteRefusesEtherTypeFamilyMismatch(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, standardSwitchConfig())
	deviceMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}

	pkt := encodeIPv6Packet(t, netip.MustParseAddr("2001:db8:10::7"), netip.MustParseAddr("2001:db8:20::7"), []byte("ipv6 data"))
	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       deviceMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := l.Route(testNow, "vlan10", frame, true)
	if res.Reason != routing.ReasonBadHeader {
		t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonBadHeader)
	}
	last := res.Steps[len(res.Steps)-1]
	if last.Op != trace.OpDrop || last.RuleID != trace.RuleID(routing.ReasonBadHeader) {
		t.Errorf("last step = %+v, want drop bad-header", last)
	}
}

// TestRouteLeavesInputUntouched is evidence that Route works on a copy: the
// input frame's payload bytes are the same after the call, and writing to the
// egress payload does not reach them.
func TestRouteLeavesInputUntouched(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, standardSwitchConfig())
	deviceMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}

	pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.20.7"), 64, []byte("data"))
	before := append([]byte(nil), pkt...)
	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       deviceMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := l.Route(testNow, "vlan10", frame, true)
	if res.Reason != "" {
		t.Fatalf("reason = %q, want empty", res.Reason)
	}
	if string(frame.Payload) != string(before) {
		t.Fatal("Route changed the input payload")
	}
	res.Frame.Payload[len(res.Frame.Payload)-1] ^= 0xff
	if string(frame.Payload) != string(before) {
		t.Fatal("the egress payload aliases the input payload")
	}
}

// TestRouteUnknownInterface is evidence that a name the layer does not hold
// gets the same step shape as a table miss: classify, lookup, drop.
func TestRouteUnknownInterface(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, standardSwitchConfig())

	res := l.Route(testNow, "vlan99", ethernet.Frame{EtherType: ethernet.EtherTypeIPv4}, true)
	if res.Reason != routing.ReasonNoRoute {
		t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonNoRoute)
	}
	want := []trace.Step{
		{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: trace.RuleID("classify"), Subject: trace.Subject{Kind: "interface", Key: "vlan99"}},
		{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: trace.RuleID("no-route"), Subject: trace.Subject{Kind: "interface", Key: "vlan99"}},
		{Layer: port.LayerRouting, Op: trace.OpDrop, RuleID: trace.RuleID("unknown-interface"), Subject: trace.Subject{Kind: "interface", Key: "vlan99"}},
	}
	if len(res.Steps) != len(want) {
		t.Fatalf("steps = %+v, want %+v", res.Steps, want)
	}
	for i := range want {
		if !sameStepIdentity(res.Steps[i], want[i]) {
			t.Errorf("step %d = %+v, want %+v", i, res.Steps[i], want[i])
		}
	}
}

func TestConstructorsNormalizeRoutePrefixesBeforeValidation(t *testing.T) {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "wan", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}
	cfg := routing.Config{VRFs: map[string]routing.VRF{
		routing.DefaultVRF: {
			Interfaces: map[string]routing.Interface{
				"wan": {Port: "wan", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.1/24")}},
			},
			Routes: []routing.Route{{
				Prefix:    netip.MustParsePrefix("192.0.2.99/24"),
				Interface: "wan",
			}},
		},
	}}

	if _, err := routing.New(cfg, ports, "sw1"); err != nil {
		t.Errorf("routing.New: %v", err)
	}
}

func sameStepIdentity(got, want trace.Step) bool {
	return got.Layer == want.Layer && got.Op == want.Op && got.RuleID == want.RuleID && got.Subject == want.Subject
}

var (
	selectionDeviceMAC = netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	gatewayA           = netip.MustParseAddr("10.0.10.254") // on vlan10
	gatewayB           = netip.MustParseAddr("10.0.20.254") // on vlan20
	gatewayC           = netip.MustParseAddr("10.0.30.254") // on vlan30

	// Each gateway carries its own MAC, so an egress frame names the next hop it
	// was actually built for and not merely the interface it left by.
	gatewayMACA = netaddr.MAC{0x00, 0x22, 0x33, 0x44, 0x55, 0x0a}
	gatewayMACB = netaddr.MAC{0x00, 0x22, 0x33, 0x44, 0x55, 0x0b}
	gatewayMACC = netaddr.MAC{0x00, 0x22, 0x33, 0x44, 0x55, 0x0c}
)

// selectionConfig builds a three-interface VRF whose gateways all resolve, so a forwarding
// result names the interface of the route that won.
func selectionConfig(routes ...routing.Route) routing.Config {
	return routing.Config{
		VRFs: map[string]routing.VRF{
			routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"vlan10": {VLAN: 10, MAC: selectionDeviceMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
					"vlan20": {VLAN: 20, MAC: selectionDeviceMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					"vlan30": {VLAN: 30, MAC: selectionDeviceMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
				},
				Routes: routes,
				Neighbors: []routing.Neighbor{
					{Interface: "vlan10", Addr: gatewayA, MAC: gatewayMACA},
					{Interface: "vlan20", Addr: gatewayB, MAC: gatewayMACB},
					{Interface: "vlan30", Addr: gatewayC, MAC: gatewayMACC},
					{Interface: "vlan10", Addr: netip.MustParseAddr("10.0.10.7"), MAC: gatewayMACA},
				},
			},
		},
	}
}

func routeFromVLAN10(t *testing.T, l *routing.Layer, dst netip.Addr) routing.Result {
	t.Helper()
	return l.Route(testNow, "vlan10", ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       selectionDeviceMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), dst, 64, []byte("data")),
	}, true)
}

func TestRouteSelectionOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		routes        []routing.Route
		wantInterface string
	}{
		{
			name: "longer prefix wins over lower preference",
			routes: []routing.Route{
				{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: gatewayA, Preference: 1},
				{Prefix: netip.MustParsePrefix("10.0.0.0/16"), NextHop: gatewayB, Preference: 250},
			},
			wantInterface: "vlan20",
		},
		{
			name: "lower preference wins at equal prefix length",
			routes: []routing.Route{
				{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: gatewayA, Preference: 1},
				{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: gatewayB, Preference: 250},
			},
			wantInterface: "vlan10",
		},
		{
			name: "lower metric wins at equal preference",
			routes: []routing.Route{
				{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: gatewayA, Preference: 1, Metric: 20},
				{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: gatewayB, Preference: 1, Metric: 10},
			},
			wantInterface: "vlan20",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := mustNewRouting(t, selectionConfig(tc.routes...))
			res := routeFromVLAN10(t, l, netip.MustParseAddr("10.0.1.1"))
			if res.Reason != "" {
				t.Fatalf("reason = %q, want empty", res.Reason)
			}
			if res.Interface != tc.wantInterface {
				t.Errorf("interface = %q, want %q", res.Interface, tc.wantInterface)
			}
		})
	}
}

func TestRouteCandidateSet(t *testing.T) {
	t.Parallel()

	t.Run("every equal-cost route is a candidate in canonical order", func(t *testing.T) {
		t.Parallel()
		l := mustNewRouting(t, selectionConfig(
			routing.Route{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: gatewayC, Preference: 1, Metric: 10},
			routing.Route{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: gatewayA, Preference: 1, Metric: 10},
			routing.Route{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: gatewayB, Preference: 1, Metric: 10},
		))

		res := routeFromVLAN10(t, l, netip.MustParseAddr("10.0.1.1"))
		wantNextHops := []netip.Addr{gatewayA, gatewayB, gatewayC}
		if len(res.Candidates) != len(wantNextHops) {
			t.Fatalf("candidates = %+v, want %d", res.Candidates, len(wantNextHops))
		}
		for i, want := range wantNextHops {
			if res.Candidates[i].NextHop != want {
				t.Errorf("candidate %d next hop = %s, want %s", i, res.Candidates[i].NextHop, want)
			}
		}
		if res.Interface != "vlan20" {
			t.Errorf("interface = %q, want vlan20, the candidate this flow's hash lands on", res.Interface)
		}
	})

	t.Run("equal-length prefix not containing the destination is no candidate", func(t *testing.T) {
		t.Parallel()
		l := mustNewRouting(t, selectionConfig(
			routing.Route{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: gatewayA, Preference: 1, Metric: 10},
			routing.Route{Prefix: netip.MustParsePrefix("11.0.0.0/8"), NextHop: gatewayB, Preference: 1, Metric: 10},
		))

		res := routeFromVLAN10(t, l, netip.MustParseAddr("10.0.1.1"))
		if len(res.Candidates) != 1 || res.Candidates[0].NextHop != gatewayA {
			t.Fatalf("candidates = %+v, want the 10.0.0.0/8 route alone", res.Candidates)
		}
	})

	t.Run("single candidate keeps the plain route shape", func(t *testing.T) {
		t.Parallel()
		l := mustNewRouting(t, selectionConfig(
			routing.Route{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: gatewayB, Preference: 1, Metric: 10},
		))

		res := routeFromVLAN10(t, l, netip.MustParseAddr("10.0.1.1"))
		want := routing.Candidate{
			Prefix:     netip.MustParsePrefix("10.0.0.0/8"),
			NextHop:    gatewayB,
			Interface:  "vlan20",
			Preference: 1,
			Metric:     10,
		}
		if len(res.Candidates) != 1 || res.Candidates[0] != want {
			t.Fatalf("candidates = %+v, want [%+v]", res.Candidates, want)
		}
		if res.Interface != "vlan20" {
			t.Errorf("interface = %q, want vlan20", res.Interface)
		}
	})
}

func TestConnectedRouteWinsThroughPreference(t *testing.T) {
	t.Parallel()

	for _, preference := range []uint8{0, 1} {
		t.Run(fmt.Sprintf("static route at preference %d", preference), func(t *testing.T) {
			t.Parallel()
			l := mustNewRouting(t, selectionConfig(routing.Route{
				Prefix:     netip.MustParsePrefix("10.0.10.0/24"),
				NextHop:    gatewayB,
				Preference: preference,
			}))

			res := routeFromVLAN10(t, l, netip.MustParseAddr("10.0.10.7"))
			if res.Reason != "" {
				t.Fatalf("reason = %q, want empty", res.Reason)
			}
			if len(res.Candidates) != 1 {
				t.Fatalf("candidates = %+v, want the connected route alone", res.Candidates)
			}
			if res.Candidates[0].Preference != 0 || res.Candidates[0].Interface != "vlan10" {
				t.Errorf("candidate = %+v, want the connected route on vlan10 at preference 0", res.Candidates[0])
			}
		})
	}
}

// The recursion tests chain through addresses that lie in no connected prefix of
// selectionConfig, so each one needs a route of its own to resolve.
var (
	recursiveDst  = netip.MustParseAddr("10.1.1.1")
	recursivePfx  = netip.MustParsePrefix("10.0.0.0/8")
	viaPrefix     = netip.MustParsePrefix("192.0.2.0/24")
	viaAddr       = netip.MustParseAddr("192.0.2.1")
	defaultPrefix = netip.MustParsePrefix("0.0.0.0/0")
)

func TestRecursiveRouteResolvesForwardingNextHop(t *testing.T) {
	t.Parallel()

	cfg := selectionConfig(
		routing.Route{Prefix: recursivePfx, NextHop: viaAddr},
		routing.Route{Prefix: viaPrefix, NextHop: gatewayC},
	)
	if err := cfg.Normalize().Validate(port.Table{}); err != nil {
		t.Fatalf("Validate rejected an off-link next hop that resolves: %v", err)
	}

	res := routeFromVLAN10(t, mustNewRouting(t, cfg), recursiveDst)
	if res.Reason != "" {
		t.Fatalf("reason = %q, want empty", res.Reason)
	}
	if res.Interface != "vlan30" {
		t.Errorf("interface = %q, want vlan30", res.Interface)
	}
	if res.Frame.Dst != gatewayMACC {
		t.Errorf("destination MAC = %s, want %s, the neighbor of the resolved next hop %s", res.Frame.Dst, gatewayMACC, gatewayC)
	}
}

func TestRecursiveRouteNextHopInsideOwnPrefix(t *testing.T) {
	t.Parallel()

	// gatewayA lies inside 10.0.0.0/8, but resolution leaves the route through the
	// connected 10.0.10.0/24 rather than returning to it.
	res := routeFromVLAN10(t, mustNewRouting(t, selectionConfig(
		routing.Route{Prefix: recursivePfx, NextHop: gatewayA},
	)), recursiveDst)
	if res.Reason != "" {
		t.Fatalf("reason = %q, want empty", res.Reason)
	}
	if res.Interface != "vlan10" || res.Frame.Dst != gatewayMACA {
		t.Errorf("egress = %q via %s, want vlan10 via %s", res.Interface, res.Frame.Dst, gatewayMACA)
	}
}

func TestSelfRecursiveRouteIsWithdrawn(t *testing.T) {
	t.Parallel()

	selfHop := netip.MustParseAddr("10.0.0.1")
	l := mustNewRouting(t, selectionConfig(
		routing.Route{Prefix: recursivePfx, NextHop: selfHop},
		routing.Route{Prefix: defaultPrefix, NextHop: gatewayA},
	))

	res := routeFromVLAN10(t, l, recursiveDst)
	if res.Reason != "" {
		t.Fatalf("reason = %q, want the packet to fall through to the default route", res.Reason)
	}
	if res.Interface != "vlan10" || res.Frame.Dst != gatewayMACA {
		t.Errorf("egress = %q via %s, want the default route on vlan10 via %s", res.Interface, res.Frame.Dst, gatewayMACA)
	}

	withdrawn := l.WithdrawnRoutes(routing.DefaultVRF)
	if len(withdrawn) != 1 {
		t.Fatalf("withdrawn = %+v, want exactly the self-recursive route", withdrawn)
	}
	if withdrawn[0].Prefix != recursivePfx || withdrawn[0].NextHop != selfHop {
		t.Errorf("withdrawn route = %+v, want %s via %s", withdrawn[0], recursivePfx, selfHop)
	}
	if withdrawn[0].Reason != routing.WithdrawnSelfRecursive {
		t.Errorf("reason = %q, want %q", withdrawn[0].Reason, routing.WithdrawnSelfRecursive)
	}
	if !slices.Equal(withdrawn[0].Chain, []netip.Prefix{recursivePfx, recursivePfx}) {
		t.Errorf("chain = %v, want the route reached from itself", withdrawn[0].Chain)
	}
}

func TestMutuallyRecursiveRoutesAreBothWithdrawn(t *testing.T) {
	t.Parallel()

	l := mustNewRouting(t, selectionConfig(
		routing.Route{Prefix: recursivePfx, NextHop: viaAddr},
		routing.Route{Prefix: viaPrefix, NextHop: netip.MustParseAddr("10.0.0.1")},
	))

	if res := routeFromVLAN10(t, l, recursiveDst); res.Reason != routing.ReasonNoRoute {
		t.Errorf("reason = %q, want %q", res.Reason, routing.ReasonNoRoute)
	}

	withdrawn := l.WithdrawnRoutes(routing.DefaultVRF)
	if len(withdrawn) != 2 {
		t.Fatalf("withdrawn = %+v, want both routes of the cycle", withdrawn)
	}
	if withdrawn[0].Prefix != recursivePfx || withdrawn[1].Prefix != viaPrefix {
		t.Errorf("withdrawn = %+v, want %s before %s", withdrawn, recursivePfx, viaPrefix)
	}
	for _, w := range withdrawn {
		if w.Reason != routing.WithdrawnSelfRecursive {
			t.Errorf("reason for %s = %q, want %q", w.Prefix, w.Reason, routing.WithdrawnSelfRecursive)
		}
	}
	if !slices.Equal(withdrawn[0].Chain, []netip.Prefix{recursivePfx, viaPrefix, recursivePfx}) {
		t.Errorf("chain = %v, want the walk back to %s", withdrawn[0].Chain, recursivePfx)
	}
}

// chainRoutes builds length static routes where each one resolves through the previous,
// and the first through the connected prefix holding gatewayA.
func chainRoutes(length int) []routing.Route {
	routes := make([]routing.Route, 0, length)
	for i := 1; i <= length; i++ {
		hop := gatewayA
		if i > 1 {
			hop = netip.MustParseAddr(fmt.Sprintf("172.16.%d.9", i-1))
		}
		routes = append(routes, routing.Route{
			Prefix:  netip.MustParsePrefix(fmt.Sprintf("172.16.%d.0/24", i)),
			NextHop: hop,
		})
	}
	return routes
}

func TestRecursionDepthBound(t *testing.T) {
	t.Parallel()

	l := mustNewRouting(t, selectionConfig(chainRoutes(9)...))

	res := routeFromVLAN10(t, l, netip.MustParseAddr("172.16.8.1"))
	if res.Reason != "" || res.Interface != "vlan10" || res.Frame.Dst != gatewayMACA {
		t.Errorf("chain of 8: reason %q egress %q via %s, want a forward on vlan10 via %s", res.Reason, res.Interface, res.Frame.Dst, gatewayMACA)
	}

	if res := routeFromVLAN10(t, l, netip.MustParseAddr("172.16.9.1")); res.Reason != routing.ReasonNoRoute {
		t.Errorf("chain of 9: reason = %q, want %q", res.Reason, routing.ReasonNoRoute)
	}

	withdrawn := l.WithdrawnRoutes(routing.DefaultVRF)
	if len(withdrawn) != 1 {
		t.Fatalf("withdrawn = %+v, want only the chain of 9", withdrawn)
	}
	if withdrawn[0].Prefix != netip.MustParsePrefix("172.16.9.0/24") || withdrawn[0].Reason != routing.WithdrawnDepthExceeded {
		t.Errorf("withdrawn = %+v, want 172.16.9.0/24 as %q", withdrawn[0], routing.WithdrawnDepthExceeded)
	}
	if len(withdrawn[0].Chain) != 9 {
		t.Errorf("chain = %v, want the nine prefixes walked", withdrawn[0].Chain)
	}
}

func TestRecursiveRouteInheritsCandidateSet(t *testing.T) {
	t.Parallel()

	res := routeFromVLAN10(t, mustNewRouting(t, selectionConfig(
		routing.Route{Prefix: recursivePfx, NextHop: viaAddr},
		routing.Route{Prefix: viaPrefix, NextHop: gatewayB},
		routing.Route{Prefix: viaPrefix, NextHop: gatewayC},
	)), recursiveDst)

	if len(res.Candidates) != 2 {
		t.Fatalf("candidates = %+v, want both members of the resolving set", res.Candidates)
	}
	for _, c := range res.Candidates {
		if c.Prefix != recursivePfx || c.NextHop != viaAddr {
			t.Errorf("candidate = %+v, want the configured %s via %s", c, recursivePfx, viaAddr)
		}
	}
	if res.Candidates[0].Interface != "vlan20" || res.Candidates[1].Interface != "vlan30" {
		t.Errorf("candidate egresses = %q and %q, want vlan20 and vlan30", res.Candidates[0].Interface, res.Candidates[1].Interface)
	}
	if res.Interface != "vlan20" || res.Frame.Dst != gatewayMACB {
		t.Errorf("egress = %q via %s, want the first candidate on vlan20 via %s", res.Interface, res.Frame.Dst, gatewayMACB)
	}
}

func TestNextHopDoesNotResolveThroughDefaultRoute(t *testing.T) {
	t.Parallel()

	l := mustNewRouting(t, selectionConfig(
		routing.Route{Prefix: defaultPrefix, NextHop: gatewayA},
		routing.Route{Prefix: recursivePfx, NextHop: viaAddr},
	))

	res := routeFromVLAN10(t, l, recursiveDst)
	if res.Reason != "" || res.Interface != "vlan10" {
		t.Errorf("reason = %q on %q, want the default route on vlan10", res.Reason, res.Interface)
	}

	withdrawn := l.WithdrawnRoutes(routing.DefaultVRF)
	if len(withdrawn) != 1 {
		t.Fatalf("withdrawn = %+v, want the route whose next hop matches only the default route", withdrawn)
	}
	if withdrawn[0].Prefix != recursivePfx || withdrawn[0].Reason != routing.WithdrawnUnresolved {
		t.Errorf("withdrawn = %+v, want %s as %q", withdrawn[0], recursivePfx, routing.WithdrawnUnresolved)
	}
}

func TestWithdrawnRoutesSortByPrefixThenNextHop(t *testing.T) {
	t.Parallel()

	lowHop := netip.MustParseAddr("192.0.2.1")
	highHop := netip.MustParseAddr("192.0.2.2")
	l := mustNewRouting(t, selectionConfig(
		routing.Route{Prefix: netip.MustParsePrefix("172.16.0.0/16"), NextHop: highHop},
		routing.Route{Prefix: recursivePfx, NextHop: highHop},
		routing.Route{Prefix: recursivePfx, NextHop: lowHop},
	))

	withdrawn := l.WithdrawnRoutes(routing.DefaultVRF)
	want := []struct {
		prefix  netip.Prefix
		nextHop netip.Addr
	}{
		{recursivePfx, lowHop},
		{recursivePfx, highHop},
		{netip.MustParsePrefix("172.16.0.0/16"), highHop},
	}
	if len(withdrawn) != len(want) {
		t.Fatalf("withdrawn = %+v, want %d entries", withdrawn, len(want))
	}
	for i, w := range want {
		if withdrawn[i].Prefix != w.prefix || withdrawn[i].NextHop != w.nextHop {
			t.Errorf("withdrawal %d = %s via %s, want %s via %s", i, withdrawn[i].Prefix, withdrawn[i].NextHop, w.prefix, w.nextHop)
		}
		if withdrawn[i].Reason != routing.WithdrawnUnresolved {
			t.Errorf("withdrawal %d reason = %q, want %q", i, withdrawn[i].Reason, routing.WithdrawnUnresolved)
		}
	}
}

func TestRoutingTableBuildIsDeterministic(t *testing.T) {
	t.Parallel()

	cfg := selectionConfig(
		routing.Route{Prefix: recursivePfx, NextHop: gatewayC, Preference: 1, Metric: 10},
		routing.Route{Prefix: recursivePfx, NextHop: gatewayA, Preference: 1, Metric: 10},
		routing.Route{Prefix: recursivePfx, NextHop: gatewayB, Preference: 1, Metric: 10},
		routing.Route{Prefix: netip.MustParsePrefix("172.16.0.0/16"), NextHop: viaAddr},
	)

	var wantWithdrawn, wantFacts string
	for i := range 10 {
		l := mustNewRouting(t, cfg)
		gotWithdrawn := fmt.Sprintf("%+v", l.WithdrawnRoutes(routing.DefaultVRF))
		gotFacts := stepFacts(routeFromVLAN10(t, l, recursiveDst))
		if i == 0 {
			wantWithdrawn, wantFacts = gotWithdrawn, gotFacts
			continue
		}
		if gotWithdrawn != wantWithdrawn {
			t.Fatalf("construction %d withdrawals = %s, want %s", i, gotWithdrawn, wantWithdrawn)
		}
		if gotFacts != wantFacts {
			t.Fatalf("construction %d facts = %s, want %s", i, gotFacts, wantFacts)
		}
	}
}

func TestRecursiveRouteCapsInheritedCandidates(t *testing.T) {
	t.Parallel()

	const paths = 65
	ifaces := make(map[string]routing.Interface, paths)
	neighbors := make([]routing.Neighbor, 0, paths)
	routes := []routing.Route{{Prefix: recursivePfx, NextHop: viaAddr}}
	for i := 1; i <= paths; i++ {
		name := fmt.Sprintf("vlan%d", i)
		hop := netip.MustParseAddr(fmt.Sprintf("10.%d.0.254", i))
		ifaces[name] = routing.Interface{
			VLAN:     vlan.ID(i),
			MAC:      selectionDeviceMAC,
			Prefixes: []netip.Prefix{netip.MustParsePrefix(fmt.Sprintf("10.%d.0.1/24", i))},
		}
		neighbors = append(neighbors, routing.Neighbor{Interface: name, Addr: hop, MAC: gatewayMACA})
		routes = append(routes, routing.Route{Prefix: viaPrefix, NextHop: hop})
	}

	l := mustNewRouting(t, routing.Config{VRFs: map[string]routing.VRF{
		routing.DefaultVRF: {Interfaces: ifaces, Routes: routes, Neighbors: neighbors},
	}})

	res := l.Route(testNow, "vlan1", ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       selectionDeviceMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   encodeIPv4Packet(t, netip.MustParseAddr("10.1.0.7"), netip.MustParseAddr("10.200.1.1"), 64, []byte("data")),
	}, true)
	if len(res.Candidates) != 64 {
		t.Fatalf("candidates = %d, want the cap of 64", len(res.Candidates))
	}

	// Both caps fire on this configuration: the recursive route inherits 65 paths, and the
	// 65 routes resolving them are themselves equal-cost on one prefix.
	withdrawn := l.WithdrawnRoutes(routing.DefaultVRF)
	if len(withdrawn) != 2 {
		t.Fatalf("withdrawn = %+v, want the inherited path and the equal route past the cap", withdrawn)
	}
	if withdrawn[0].Prefix != recursivePfx || withdrawn[0].Reason != routing.WithdrawnMaxPaths {
		t.Errorf("withdrawn = %+v, want %s as %q", withdrawn[0], recursivePfx, routing.WithdrawnMaxPaths)
	}
	if withdrawn[1].Prefix != viaPrefix || withdrawn[1].Reason != routing.WithdrawnMaxPaths {
		t.Errorf("withdrawn = %+v, want %s as %q", withdrawn[1], viaPrefix, routing.WithdrawnMaxPaths)
	}
	for _, w := range withdrawn {
		if w.Interface != fmt.Sprintf("vlan%d", paths) {
			t.Errorf("withdrawn egress = %q, want the last path in canonical order", w.Interface)
		}
	}
}

func stepFacts(res routing.Result) string {
	var b strings.Builder
	for _, step := range res.Steps {
		fmt.Fprintf(&b, "%s/%s/%s/%s|", step.Layer, step.Op, step.RuleID, step.Subject.Key)
		for _, f := range slices.Concat(step.Inputs, step.Outputs) {
			fmt.Fprintf(&b, "%s=%s;", f.TypeID(), f.Canonical())
		}
		b.WriteString("\n")
	}
	for _, c := range res.Candidates {
		fmt.Fprintf(&b, "candidate %+v\n", c)
	}
	return b.String()
}

// ecmpConfig builds the three-candidate set the flow-hash tests select over: one prefix
// reachable through all three gateways at the same preference and metric.
func ecmpConfig(gateways ...netip.Addr) routing.Config {
	routes := make([]routing.Route, 0, len(gateways))
	for _, gw := range gateways {
		routes = append(routes, routing.Route{Prefix: ecmpPrefix, NextHop: gw, Preference: 1, Metric: 10})
	}
	return selectionConfig(routes...)
}

// routeFlow forwards one IPv4 flow in at vlan10 and returns the routing result.
func routeFlow(t *testing.T, l *routing.Layer, src, dst netip.Addr, payload []byte) routing.Result {
	t.Helper()
	return l.Route(testNow, "vlan10", ethernet.Frame{
		Src:       hostMAC,
		Dst:       selectionDeviceMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   encodeIPv4Packet(t, src, dst, 64, payload),
	}, true)
}

// flowSpread returns a source and destination pair per flow, wide enough to reach every
// region of a three-candidate set.
func flowSpread() [][2]netip.Addr {
	var out [][2]netip.Addr
	for host := 1; host <= 40; host++ {
		for dst := 1; dst <= 10; dst++ {
			out = append(out, [2]netip.Addr{
				netip.MustParseAddr(fmt.Sprintf("10.0.10.%d", host)),
				netip.MustParseAddr(fmt.Sprintf("10.200.0.%d", dst)),
			})
		}
	}
	return out
}

func TestFlowHashReachesEveryCandidate(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, ecmpConfig(gatewayA, gatewayB, gatewayC))

	reached := make(map[string]int)
	for _, flow := range flowSpread() {
		res := routeFlow(t, l, flow[0], flow[1], []byte("data"))
		if res.Reason != "" {
			t.Fatalf("reason = %q for %s -> %s, want empty", res.Reason, flow[0], flow[1])
		}
		reached[res.Interface]++
	}
	for _, iface := range []string{"vlan10", "vlan20", "vlan30"} {
		if reached[iface] == 0 {
			t.Errorf("no flow of %d reached %s; reached = %v", len(flowSpread()), iface, reached)
		}
	}
}

func TestFlowHashIgnoresTransportPorts(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, ecmpConfig(gatewayA, gatewayB, gatewayC))

	src := netip.MustParseAddr("10.0.10.7")
	dst := netip.MustParseAddr("10.200.0.1")
	low := routeFlow(t, l, src, dst, []byte{0x04, 0x01, 0x00, 0x35, 'd'})
	high := routeFlow(t, l, src, dst, []byte{0xc3, 0x50, 0x00, 0x35, 'd'})
	if len(low.Candidates) != 3 {
		t.Fatalf("candidates = %d, want 3; a single candidate proves nothing here", len(low.Candidates))
	}
	if low.Interface != high.Interface {
		t.Errorf("source port 1025 left by %s and 50000 by %s; ports do not enter the hash", low.Interface, high.Interface)
	}
}

func routeFragment(t *testing.T, l *routing.Layer, src, dst netip.Addr, v4 *ip.V4) routing.Result {
	t.Helper()
	hdr := ip.Header{Src: src, Dst: dst, HopLimit: 64, Protocol: 17, V4: v4}
	pkt, err := hdr.Encode([]byte("payload"))
	if err != nil {
		t.Fatalf("encode fragment: %v", err)
	}
	return l.Route(testNow, "vlan10", ethernet.Frame{
		Src:       hostMAC,
		Dst:       selectionDeviceMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}, true)
}

func TestFragmentsOfOneDatagramShareANextHop(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, ecmpConfig(gatewayA, gatewayB, gatewayC))

	src := netip.MustParseAddr("10.0.10.7")
	dst := netip.MustParseAddr("10.200.0.1")
	first := routeFragment(t, l, src, dst, &ip.V4{ID: 7, Flags: 0x1})
	later := routeFragment(t, l, src, dst, &ip.V4{ID: 7, FragmentOffset: 185})
	if len(first.Candidates) != 3 {
		t.Fatalf("candidates = %d, want 3", len(first.Candidates))
	}
	if first.Interface != later.Interface {
		t.Errorf("first fragment left by %s and a later one by %s", first.Interface, later.Interface)
	}
}

func TestFlowSelectionRepeatsItself(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, ecmpConfig(gatewayA, gatewayB, gatewayC))

	src := netip.MustParseAddr("10.0.10.7")
	dst := netip.MustParseAddr("10.200.0.1")
	first := routeFlow(t, l, src, dst, []byte("data"))
	if len(first.Candidates) != 3 {
		t.Fatalf("candidates = %d, want 3", len(first.Candidates))
	}
	for i := range 10 {
		again := routeFlow(t, l, src, dst, []byte("data"))
		if again.Interface != first.Interface || again.Frame.Dst != first.Frame.Dst {
			t.Fatalf("repetition %d left by %s towards %s, want %s towards %s", i, again.Interface, again.Frame.Dst, first.Interface, first.Frame.Dst)
		}
	}
}

// TestRemovedCandidateLeavesTheOthersInPlace is the property the RFC 2992 reduction buys:
// a flow only ever slides down into the region below it, so the whole first region keeps
// its next hop when a candidate goes. Modulo-N would scatter it.
func TestRemovedCandidateLeavesTheOthersInPlace(t *testing.T) {
	t.Parallel()

	three := mustNewRouting(t, ecmpConfig(gatewayA, gatewayB, gatewayC))
	two := mustNewRouting(t, ecmpConfig(gatewayA, gatewayB))

	var onA, heldByB int
	for _, flow := range flowSpread() {
		before := routeFlow(t, three, flow[0], flow[1], []byte("data")).Interface
		after := routeFlow(t, two, flow[0], flow[1], []byte("data")).Interface
		switch before {
		case "vlan10":
			onA++
			if after != "vlan10" {
				t.Errorf("%s -> %s was on vlan10 and moved to %s when vlan30 went away", flow[0], flow[1], after)
			}
		case "vlan20":
			if after == "vlan20" {
				heldByB++
			}
		}
	}
	if onA == 0 || heldByB == 0 {
		t.Fatalf("flows on vlan10 = %d, flows held by vlan20 = %d; the spread proves nothing", onA, heldByB)
	}
}

func TestOriginateSelectsOverTheCandidateSet(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, ecmpConfig(gatewayA, gatewayB, gatewayC))

	// The originated source address is the same for every destination here, so the
	// destinations differ in an octet the hash mixes rather than in the last one, which a
	// run of consecutive values would move by too little to leave one region.
	reached := make(map[string]int)
	for i := 1; i <= 40; i++ {
		dst := netip.MustParseAddr(fmt.Sprintf("10.200.%d.1", i))
		first := l.Originate(testNow, routing.DefaultVRF, dst, 17, []byte("data"), true)
		if first.Reason != "" {
			t.Fatalf("reason = %q for %s, want empty", first.Reason, dst)
		}
		if len(first.Candidates) != 3 {
			t.Fatalf("candidates = %d for %s, want 3", len(first.Candidates), dst)
		}
		reached[first.Interface]++
		for range 10 {
			again := l.Originate(testNow, routing.DefaultVRF, dst, 17, []byte("data"), true)
			if again.Interface != first.Interface || again.Frame.Dst != first.Frame.Dst {
				t.Fatalf("%s left by %s towards %s, want %s towards %s", dst, again.Interface, again.Frame.Dst, first.Interface, first.Frame.Dst)
			}
		}
	}
	if len(reached) < 2 {
		t.Errorf("originated flows reached %v, want more than one candidate", reached)
	}
}

// selectionConfigV6 mirrors selectionConfig on IPv6, so a flow label has somewhere to
// change the answer.
func selectionConfigV6() routing.Config {
	return routing.Config{
		VRFs: map[string]routing.VRF{
			routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"vlan10": {VLAN: 10, MAC: selectionDeviceMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("2001:db8:10::1/64")}},
					"vlan20": {VLAN: 20, MAC: selectionDeviceMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("2001:db8:20::1/64")}},
					"vlan30": {VLAN: 30, MAC: selectionDeviceMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("2001:db8:30::1/64")}},
				},
				Routes: []routing.Route{
					{Prefix: ecmpPrefixV6, NextHop: gatewayA6, Preference: 1, Metric: 10},
					{Prefix: ecmpPrefixV6, NextHop: gatewayB6, Preference: 1, Metric: 10},
					{Prefix: ecmpPrefixV6, NextHop: gatewayC6, Preference: 1, Metric: 10},
				},
				Neighbors: []routing.Neighbor{
					{Interface: "vlan10", Addr: gatewayA6, MAC: gatewayMACA},
					{Interface: "vlan20", Addr: gatewayB6, MAC: gatewayMACB},
					{Interface: "vlan30", Addr: gatewayC6, MAC: gatewayMACC},
				},
			},
		},
	}
}

func routeFlow6(t *testing.T, l *routing.Layer, src, dst netip.Addr, flowLabel uint32, protocol uint8) routing.Result {
	t.Helper()
	hdr := ip.Header{Src: src, Dst: dst, HopLimit: 64, Protocol: protocol, V6: &ip.V6{FlowLabel: flowLabel}}
	pkt, err := hdr.Encode([]byte("payload"))
	if err != nil {
		t.Fatalf("encode IPv6 packet: %v", err)
	}
	return l.Route(testNow, "vlan10", ethernet.Frame{
		Src:       hostMAC,
		Dst:       selectionDeviceMAC,
		EtherType: ethernet.EtherTypeIPv6,
		Payload:   pkt,
	}, true)
}

func TestFlowLabelSeparatesIPv6Flows(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, selectionConfigV6())

	src := netip.MustParseAddr("2001:db8:10::7")
	dst := netip.MustParseAddr("2001:db8:200::1")
	reached := make(map[string]uint32)
	for label := range uint32(64) {
		res := routeFlow6(t, l, src, dst, label, 17)
		if res.Reason != "" {
			t.Fatalf("reason = %q at flow label %d, want empty", res.Reason, label)
		}
		if _, seen := reached[res.Interface]; !seen {
			reached[res.Interface] = label
		}
	}
	if len(reached) < 2 {
		t.Fatalf("flows differing only in flow label reached %v, want two next hops", reached)
	}
}

// TestIPv6ExtensionHeaderKeepsTheNextHop holds because the protocol octet stays out of the
// hash. ip.Decode does not walk the extension header chain, so for a segment behind one the
// octet names the first extension header rather than the transport protocol, and hashing it
// would split a flow on the headers its packets happen to carry.
func TestIPv6ExtensionHeaderKeepsTheNextHop(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, selectionConfigV6())

	src := netip.MustParseAddr("2001:db8:10::7")
	dst := netip.MustParseAddr("2001:db8:200::1")
	plain := routeFlow6(t, l, src, dst, 0, 6)     // TCP directly after the fixed header.
	extended := routeFlow6(t, l, src, dst, 0, 43) // TCP behind a routing header.
	if len(plain.Candidates) != 3 {
		t.Fatalf("candidates = %d, want 3", len(plain.Candidates))
	}
	if plain.Interface != extended.Interface {
		t.Errorf("bare TCP left by %s and TCP behind a routing header by %s", plain.Interface, extended.Interface)
	}
}

func TestEqualCostRoutesOnOnePrefixAreCapped(t *testing.T) {
	t.Parallel()

	const paths = 65
	ifaces := make(map[string]routing.Interface, paths)
	neighbors := make([]routing.Neighbor, 0, paths)
	routes := make([]routing.Route, 0, paths)
	for i := 1; i <= paths; i++ {
		name := fmt.Sprintf("vlan%d", i)
		hop := netip.MustParseAddr(fmt.Sprintf("172.%d.0.254", i))
		ifaces[name] = routing.Interface{
			VLAN:     vlan.ID(i),
			MAC:      selectionDeviceMAC,
			Prefixes: []netip.Prefix{netip.MustParsePrefix(fmt.Sprintf("172.%d.0.1/24", i))},
		}
		neighbors = append(neighbors, routing.Neighbor{Interface: name, Addr: hop, MAC: gatewayMACA})
		routes = append(routes, routing.Route{Prefix: ecmpPrefix, NextHop: hop, Preference: 1, Metric: 10})
	}

	l := mustNewRouting(t, routing.Config{VRFs: map[string]routing.VRF{
		routing.DefaultVRF: {Interfaces: ifaces, Routes: routes, Neighbors: neighbors},
	}})

	res := l.Route(testNow, "vlan1", ethernet.Frame{
		Src:       hostMAC,
		Dst:       selectionDeviceMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   encodeIPv4Packet(t, netip.MustParseAddr("172.1.0.7"), netip.MustParseAddr("10.200.0.1"), 64, []byte("data")),
	}, true)
	if len(res.Candidates) != 64 {
		t.Fatalf("candidates = %d, want the cap of 64", len(res.Candidates))
	}

	withdrawn := l.WithdrawnRoutes(routing.DefaultVRF)
	if len(withdrawn) != 1 {
		t.Fatalf("withdrawn = %+v, want the one route past the cap", withdrawn)
	}
	if withdrawn[0].Reason != routing.WithdrawnMaxPaths || withdrawn[0].Prefix != ecmpPrefix {
		t.Errorf("withdrawn = %+v, want %s as %q", withdrawn[0], ecmpPrefix, routing.WithdrawnMaxPaths)
	}
}

// TestPeekAndCommitAgreeOnSelection holds because the selection reads the packet and nothing
// else: no bucket table remembers where a flow went, so looking cannot move it, and a commit
// chooses the same candidate a preceding peek already reported.
func TestPeekAndCommitAgreeOnSelection(t *testing.T) {
	t.Parallel()
	l := mustNewRouting(t, ecmpConfig(gatewayA, gatewayB, gatewayC))

	for _, flow := range flowSpread()[:20] {
		frame := ethernet.Frame{
			Src:       hostMAC,
			Dst:       selectionDeviceMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   encodeIPv4Packet(t, flow[0], flow[1], 64, []byte("data")),
		}
		first := l.Route(testNow, "vlan10", frame, false)
		second := l.Route(testNow, "vlan10", frame, false)
		committed := l.Route(testNow, "vlan10", frame, true)
		if first.Frame.Dst != second.Frame.Dst || first.Frame.Dst != committed.Frame.Dst {
			t.Fatalf("%s -> %s: peeks chose %s and %s, commit chose %s", flow[0], flow[1], first.Frame.Dst, second.Frame.Dst, committed.Frame.Dst)
		}
	}
}

// TestOriginateRefusesAnUnencodableDatagramBeforeQueuingIt pins that a datagram
// ip.Header.Encode refuses never reaches a hold queue. finishHeld re-encodes the header once the
// neighbor resolves, and an encode failure there omits the frame with no report at all, so a
// datagram Encode will never accept has to be refused while its caller is still there to be told.
func TestOriginateRefusesAnUnencodableDatagramBeforeQueuingIt(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{ResolutionTimeout: time.Second})

	// A 20-octet IPv4 header over 65516 octets of payload totals 65536, one past what the
	// total-length field can carry.
	res := l.Originate(testNow, routing.DefaultVRF, lifecycleDstV4, 17, make([]byte, 65516), true)
	if res.Reason != routing.ReasonBadHeader {
		t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonBadHeader)
	}
	last := res.Steps[len(res.Steps)-1]
	if last.Op != trace.OpDrop || last.RuleID != trace.RuleID(routing.ReasonBadHeader) {
		t.Errorf("last step = %s/%s, want %s/%s", last.Op, last.RuleID, trace.OpDrop, routing.ReasonBadHeader)
	}

	// A queued frame surfaces here: a Wake past the resolution deadline fails an Incomplete
	// entry and reports every frame it was holding.
	eff := l.Wake(testNow.Add(2 * time.Second))
	if len(eff.Released) != 0 || len(eff.Failed) != 0 {
		t.Fatalf("wake reported %d released and %d failed, want none of either", len(eff.Released), len(eff.Failed))
	}
}

var (
	hostMAC      = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	ecmpPrefix   = netip.MustParsePrefix("10.0.0.0/8")
	ecmpPrefixV6 = netip.MustParsePrefix("2001:db8:200::/48")
	gatewayA6    = netip.MustParseAddr("2001:db8:10::fe")
	gatewayB6    = netip.MustParseAddr("2001:db8:20::fe")
	gatewayC6    = netip.MustParseAddr("2001:db8:30::fe")
)
