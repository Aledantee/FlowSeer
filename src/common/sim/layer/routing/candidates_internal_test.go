package routing

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

var (
	testCandidateDeviceMAC    = netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	testCandidateHostMAC      = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	testCandidateGatewayA     = netip.MustParseAddr("10.0.10.254")
	testCandidateGatewayB     = netip.MustParseAddr("10.0.20.254")
	testCandidateGatewayC     = netip.MustParseAddr("10.0.30.254")
	testCandidateGatewayMACA  = netaddr.MAC{0x00, 0x22, 0x33, 0x44, 0x55, 0x0a}
	testCandidateGatewayMACB  = netaddr.MAC{0x00, 0x22, 0x33, 0x44, 0x55, 0x0b}
	testCandidateGatewayMACC  = netaddr.MAC{0x00, 0x22, 0x33, 0x44, 0x55, 0x0c}
	testCandidateECMPPrefix   = netip.MustParsePrefix("10.200.0.0/16")
	testCandidateRecursivePfx = netip.MustParsePrefix("10.200.1.0/24")
	testCandidateViaPrefix    = netip.MustParsePrefix("192.0.2.0/24")
	testCandidateViaAddr      = netip.MustParseAddr("192.0.2.1")
	testCandidateRecursiveDst = netip.MustParseAddr("10.200.1.1")
	testCandidateTime         = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

func mustNewRoutingInternal(t *testing.T, cfg Config) *Layer {
	t.Helper()
	l, err := New(cfg, layer.Env{NodeID: "sw1"})
	if err != nil {
		t.Fatalf("routing.New: %v", err)
	}
	return l
}

func testCandidateSelectionConfig(routes ...Route) Config {
	return Config{
		VRFs: map[string]VRF{
			DefaultVRF: {
				Interfaces: map[string]Interface{
					"vlan10": {VLAN: 10, MAC: testCandidateDeviceMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
					"vlan20": {VLAN: 20, MAC: testCandidateDeviceMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					"vlan30": {VLAN: 30, MAC: testCandidateDeviceMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
				},
				Routes: routes,
				Neighbors: []Neighbor{
					{Interface: "vlan10", Addr: testCandidateGatewayA, MAC: testCandidateGatewayMACA},
					{Interface: "vlan20", Addr: testCandidateGatewayB, MAC: testCandidateGatewayMACB},
					{Interface: "vlan30", Addr: testCandidateGatewayC, MAC: testCandidateGatewayMACC},
					{Interface: "vlan10", Addr: netip.MustParseAddr("10.0.10.7"), MAC: testCandidateGatewayMACA},
				},
			},
		},
	}
}

func testCandidateECMPConfig(gateways ...netip.Addr) Config {
	routes := make([]Route, 0, len(gateways))
	for _, gw := range gateways {
		routes = append(routes, Route{Prefix: testCandidateECMPPrefix, NextHop: gw, Preference: 1, Metric: 10})
	}
	return testCandidateSelectionConfig(routes...)
}

func encodeTestCandidateIPv4(t *testing.T, src, dst netip.Addr, payload []byte) []byte {
	t.Helper()
	hdr := ip.Header{Src: src, Dst: dst, HopLimit: 64, Protocol: 17, V4: &ip.V4{}}
	pkt, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv4: %v", err)
	}
	return pkt
}

func testRouteFromVLAN10(t *testing.T, l *Layer, dst netip.Addr) Result {
	t.Helper()
	return l.Route(testCandidateTime, "vlan10", ethernet.Frame{
		Src:       testCandidateHostMAC,
		Dst:       testCandidateDeviceMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   encodeTestCandidateIPv4(t, netip.MustParseAddr("10.0.10.7"), dst, []byte("data")),
	}, true)
}

func testRouteFlow(t *testing.T, l *Layer, src, dst netip.Addr, payload []byte) Result {
	t.Helper()
	return l.Route(testCandidateTime, "vlan10", ethernet.Frame{
		Src:       testCandidateHostMAC,
		Dst:       testCandidateDeviceMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   encodeTestCandidateIPv4(t, src, dst, payload),
	}, true)
}

func testCandidateFlowSpread() [][2]netip.Addr {
	spread := make([][2]netip.Addr, 0, 100)
	for i := 1; i <= 100; i++ {
		spread = append(spread, [2]netip.Addr{
			netip.MustParseAddr(fmt.Sprintf("10.0.10.%d", (i%200)+1)),
			netip.MustParseAddr(fmt.Sprintf("10.200.%d.1", i)),
		})
	}
	return spread
}

func TestRouteCandidateSet(t *testing.T) {
	t.Parallel()

	t.Run("every equal-cost route is a candidate in canonical order", func(t *testing.T) {
		t.Parallel()
		l := mustNewRoutingInternal(t, testCandidateSelectionConfig(
			Route{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: testCandidateGatewayC, Preference: 1, Metric: 10},
			Route{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: testCandidateGatewayA, Preference: 1, Metric: 10},
			Route{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: testCandidateGatewayB, Preference: 1, Metric: 10},
		))

		res := testRouteFromVLAN10(t, l, netip.MustParseAddr("10.0.1.1"))
		wantNextHops := []netip.Addr{testCandidateGatewayA, testCandidateGatewayB, testCandidateGatewayC}
		if len(res.candidates) != len(wantNextHops) {
			t.Fatalf("candidates = %+v, want %d", res.candidates, len(wantNextHops))
		}
		for i, want := range wantNextHops {
			if res.candidates[i].NextHop != want {
				t.Errorf("candidate %d next hop = %s, want %s", i, res.candidates[i].NextHop, want)
			}
		}
		if res.Interface != "vlan20" {
			t.Errorf("interface = %q, want vlan20, the candidate this flow's hash lands on", res.Interface)
		}
	})

	t.Run("equal-length prefix not containing the destination is no candidate", func(t *testing.T) {
		t.Parallel()
		l := mustNewRoutingInternal(t, testCandidateSelectionConfig(
			Route{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: testCandidateGatewayA, Preference: 1, Metric: 10},
			Route{Prefix: netip.MustParsePrefix("11.0.0.0/8"), NextHop: testCandidateGatewayB, Preference: 1, Metric: 10},
		))

		res := testRouteFromVLAN10(t, l, netip.MustParseAddr("10.0.1.1"))
		if len(res.candidates) != 1 || res.candidates[0].NextHop != testCandidateGatewayA {
			t.Fatalf("candidates = %+v, want the 10.0.0.0/8 route alone", res.candidates)
		}
	})

	t.Run("single candidate keeps the plain route shape", func(t *testing.T) {
		t.Parallel()
		l := mustNewRoutingInternal(t, testCandidateSelectionConfig(
			Route{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: testCandidateGatewayB, Preference: 1, Metric: 10},
		))

		res := testRouteFromVLAN10(t, l, netip.MustParseAddr("10.0.1.1"))
		want := candidate{
			Prefix:     netip.MustParsePrefix("10.0.0.0/8"),
			NextHop:    testCandidateGatewayB,
			Interface:  "vlan20",
			Preference: 1,
			Metric:     10,
		}
		if len(res.candidates) != 1 || res.candidates[0] != want {
			t.Fatalf("candidates = %+v, want [%+v]", res.candidates, want)
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
			l := mustNewRoutingInternal(t, testCandidateSelectionConfig(Route{
				Prefix:     netip.MustParsePrefix("10.0.10.0/24"),
				NextHop:    testCandidateGatewayB,
				Preference: preference,
			}))

			res := testRouteFromVLAN10(t, l, netip.MustParseAddr("10.0.10.7"))
			if res.Reason != "" {
				t.Fatalf("reason = %q, want empty", res.Reason)
			}
			if len(res.candidates) != 1 {
				t.Fatalf("candidates = %+v, want the connected route alone", res.candidates)
			}
			if res.candidates[0].Preference != 0 || res.candidates[0].Interface != "vlan10" {
				t.Errorf("candidate = %+v, want the connected route on vlan10 at preference 0", res.candidates[0])
			}
		})
	}
}

func TestRecursiveRouteInheritsCandidateSet(t *testing.T) {
	t.Parallel()

	res := testRouteFromVLAN10(t, mustNewRoutingInternal(t, testCandidateSelectionConfig(
		Route{Prefix: testCandidateRecursivePfx, NextHop: testCandidateViaAddr},
		Route{Prefix: testCandidateViaPrefix, NextHop: testCandidateGatewayB},
		Route{Prefix: testCandidateViaPrefix, NextHop: testCandidateGatewayC},
	)), testCandidateRecursiveDst)

	if len(res.candidates) != 2 {
		t.Fatalf("candidates = %+v, want both members of the resolving set", res.candidates)
	}
	for _, c := range res.candidates {
		if c.Prefix != testCandidateRecursivePfx || c.NextHop != testCandidateViaAddr {
			t.Errorf("candidate = %+v, want the configured %s via %s", c, testCandidateRecursivePfx, testCandidateViaAddr)
		}
	}
	if res.candidates[0].Interface != "vlan20" || res.candidates[1].Interface != "vlan30" {
		t.Errorf("candidate egresses = %q and %q, want vlan20 and vlan30", res.candidates[0].Interface, res.candidates[1].Interface)
	}
	if res.Interface != "vlan20" || res.Frame.Dst != testCandidateGatewayMACB {
		t.Errorf("egress = %q via %s, want the first candidate on vlan20 via %s", res.Interface, res.Frame.Dst, testCandidateGatewayMACB)
	}
}

func TestRecursiveRouteCapsInheritedCandidates(t *testing.T) {
	t.Parallel()

	const paths = 65
	ifaces := make(map[string]Interface, paths)
	neighbors := make([]Neighbor, 0, paths)
	routes := []Route{{Prefix: testCandidateRecursivePfx, NextHop: testCandidateViaAddr}}
	for i := 1; i <= paths; i++ {
		name := fmt.Sprintf("vlan%d", i)
		hop := netip.MustParseAddr(fmt.Sprintf("10.%d.0.254", i))
		ifaces[name] = Interface{
			VLAN:     vlan.ID(i),
			MAC:      testCandidateDeviceMAC,
			Prefixes: []netip.Prefix{netip.MustParsePrefix(fmt.Sprintf("10.%d.0.1/24", i))},
		}
		neighbors = append(neighbors, Neighbor{Interface: name, Addr: hop, MAC: testCandidateGatewayMACA})
		routes = append(routes, Route{Prefix: testCandidateViaPrefix, NextHop: hop})
	}

	l := mustNewRoutingInternal(t, Config{VRFs: map[string]VRF{
		DefaultVRF: {Interfaces: ifaces, Routes: routes, Neighbors: neighbors},
	}})

	res := l.Route(testCandidateTime, "vlan1", ethernet.Frame{
		Src:       testCandidateHostMAC,
		Dst:       testCandidateDeviceMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   encodeTestCandidateIPv4(t, netip.MustParseAddr("10.1.0.7"), netip.MustParseAddr("10.200.1.1"), []byte("data")),
	}, true)
	if len(res.candidates) != 64 {
		t.Fatalf("candidates = %d, want the cap of 64", len(res.candidates))
	}

	withdrawn := l.WithdrawnRoutes(DefaultVRF)
	if len(withdrawn) != 2 {
		t.Fatalf("withdrawn = %+v, want the inherited path and the equal route past the cap", withdrawn)
	}
	if withdrawn[0].Prefix != testCandidateRecursivePfx || withdrawn[0].Reason != WithdrawnMaxPaths {
		t.Errorf("withdrawn = %+v, want %s as %q", withdrawn[0], testCandidateRecursivePfx, WithdrawnMaxPaths)
	}
	if withdrawn[1].Prefix != testCandidateViaPrefix || withdrawn[1].Reason != WithdrawnMaxPaths {
		t.Errorf("withdrawn = %+v, want %s as %q", withdrawn[1], testCandidateViaPrefix, WithdrawnMaxPaths)
	}
	for _, w := range withdrawn {
		if w.Interface != fmt.Sprintf("vlan%d", paths) {
			t.Errorf("withdrawn egress = %q, want the last path in canonical order", w.Interface)
		}
	}
}

func TestEqualCostRoutesOnOnePrefixAreCapped(t *testing.T) {
	t.Parallel()

	const paths = 65
	ifaces := make(map[string]Interface, paths)
	neighbors := make([]Neighbor, 0, paths)
	routes := make([]Route, 0, paths)
	for i := 1; i <= paths; i++ {
		name := fmt.Sprintf("vlan%d", i)
		hop := netip.MustParseAddr(fmt.Sprintf("172.%d.0.254", i))
		ifaces[name] = Interface{
			VLAN:     vlan.ID(i),
			MAC:      testCandidateDeviceMAC,
			Prefixes: []netip.Prefix{netip.MustParsePrefix(fmt.Sprintf("172.%d.0.1/24", i))},
		}
		neighbors = append(neighbors, Neighbor{Interface: name, Addr: hop, MAC: testCandidateGatewayMACA})
		routes = append(routes, Route{Prefix: testCandidateECMPPrefix, NextHop: hop, Preference: 1, Metric: 10})
	}

	l := mustNewRoutingInternal(t, Config{VRFs: map[string]VRF{
		DefaultVRF: {Interfaces: ifaces, Routes: routes, Neighbors: neighbors},
	}})

	res := l.Route(testCandidateTime, "vlan1", ethernet.Frame{
		Src:       testCandidateHostMAC,
		Dst:       testCandidateDeviceMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   encodeTestCandidateIPv4(t, netip.MustParseAddr("172.1.0.7"), netip.MustParseAddr("10.200.0.1"), []byte("data")),
	}, true)
	if len(res.candidates) != 64 {
		t.Fatalf("candidates = %d, want the cap of 64", len(res.candidates))
	}

	withdrawn := l.WithdrawnRoutes(DefaultVRF)
	if len(withdrawn) != 1 {
		t.Fatalf("withdrawn = %+v, want the one route past the cap", withdrawn)
	}
	if withdrawn[0].Reason != WithdrawnMaxPaths || withdrawn[0].Prefix != testCandidateECMPPrefix {
		t.Errorf("withdrawn = %+v, want %s as %q", withdrawn[0], testCandidateECMPPrefix, WithdrawnMaxPaths)
	}
}

func TestFlowHashReachesEveryCandidate(t *testing.T) {
	t.Parallel()
	l := mustNewRoutingInternal(t, testCandidateECMPConfig(testCandidateGatewayA, testCandidateGatewayB, testCandidateGatewayC))

	reached := make(map[string]int)
	for _, flow := range testCandidateFlowSpread() {
		res := testRouteFlow(t, l, flow[0], flow[1], []byte("data"))
		if res.Reason != "" {
			t.Fatalf("reason = %q for %s -> %s, want empty", res.Reason, flow[0], flow[1])
		}
		reached[res.Interface]++
	}
	for _, iface := range []string{"vlan10", "vlan20", "vlan30"} {
		if reached[iface] == 0 {
			t.Errorf("no flow reached %s; reached = %v", iface, reached)
		}
	}
}

func TestOriginateSelectsOverTheCandidateSet(t *testing.T) {
	t.Parallel()
	l := mustNewRoutingInternal(t, testCandidateECMPConfig(testCandidateGatewayA, testCandidateGatewayB, testCandidateGatewayC))

	reached := make(map[string]int)
	for i := 1; i <= 40; i++ {
		dst := netip.MustParseAddr(fmt.Sprintf("10.200.%d.1", i))
		first := l.Originate(testCandidateTime, DefaultVRF, dst, 17, []byte("data"), true)
		if first.Reason != "" {
			t.Fatalf("reason = %q for %s, want empty", first.Reason, dst)
		}
		if len(first.candidates) != 3 {
			t.Fatalf("candidates = %d for %s, want 3", len(first.candidates), dst)
		}
		reached[first.Interface]++
		for range 10 {
			again := l.Originate(testCandidateTime, DefaultVRF, dst, 17, []byte("data"), true)
			if again.Interface != first.Interface || again.Frame.Dst != first.Frame.Dst {
				t.Fatalf("%s left by %s towards %s, want %s towards %s", dst, again.Interface, again.Frame.Dst, first.Interface, first.Frame.Dst)
			}
		}
	}
	if len(reached) < 2 {
		t.Errorf("originated flows reached %v, want more than one candidate", reached)
	}
}
