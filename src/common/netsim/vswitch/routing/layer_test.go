package routing_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
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

func encodeIPv6Packet(t *testing.T, src, dst netip.Addr, hopLimit uint8, payload []byte) []byte {
	t.Helper()
	hdr := ip.Header{
		Src:      src,
		Dst:      dst,
		HopLimit: hopLimit,
		Protocol: 17, // UDP
		V6:       &ip.V6{},
	}
	b, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv6 packet: %v", err)
	}
	return b
}

func TestRouteIPv4Connected(t *testing.T) {
	t.Parallel()
	l := routing.New(standardSwitchConfig())

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

	res := l.Route(ifaceName, frame)
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
			Layer:  port.LayerRouting,
			Op:     trace.OpClassify,
			Detail: "vrf default interface vlan10",
		},
		{
			Layer:  port.LayerRouting,
			Op:     trace.OpLookup,
			Detail: "10.0.20.0/24 connected vlan20",
		},
		{
			Layer:  port.LayerRouting,
			Op:     trace.OpRewrite,
			Detail: "hop limit 64 to 63, src 00:00:5e:00:01:01, dst 00:11:22:33:44:77",
		},
	}
	if len(res.Steps) != len(expectedSteps) {
		t.Fatalf("steps count = %d, want %d: %+v", len(res.Steps), len(expectedSteps), res.Steps)
	}
	for i, step := range expectedSteps {
		if res.Steps[i] != step {
			t.Errorf("step %d = %+v, want %+v", i, res.Steps[i], step)
		}
	}
}

func TestRouteNeighborMiss(t *testing.T) {
	t.Parallel()
	cfg := standardSwitchConfig()
	vrf := cfg.VRFs[routing.DefaultVRF]
	vrf.Neighbors = nil
	cfg.VRFs[routing.DefaultVRF] = vrf

	l := routing.New(cfg)
	pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.20.7"), 64, []byte("payload"))
	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := l.Route("vlan10", frame)
	if res.Reason != routing.ReasonNeighborMiss {
		t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonNeighborMiss)
	}
	if len(res.Frame.Payload) != 0 {
		t.Errorf("expected no egress frame, got payload len %d", len(res.Frame.Payload))
	}

	expectedSteps := []trace.Step{
		{
			Layer:  port.LayerRouting,
			Op:     trace.OpClassify,
			Detail: "vrf default interface vlan10",
		},
		{
			Layer:  port.LayerRouting,
			Op:     trace.OpLookup,
			Detail: "10.0.20.0/24 connected vlan20",
		},
		{
			Layer:  port.LayerRouting,
			Op:     trace.OpDrop,
			Detail: "vrf default interface vlan20 address 10.0.20.7",
		},
	}
	if len(res.Steps) != len(expectedSteps) {
		t.Fatalf("steps count = %d, want %d: %+v", len(res.Steps), len(expectedSteps), res.Steps)
	}
	for i, step := range expectedSteps {
		if res.Steps[i] != step {
			t.Errorf("step %d = %+v, want %+v", i, res.Steps[i], step)
		}
	}
}

func TestRouteDropsAndLocalAddress(t *testing.T) {
	t.Parallel()
	l := routing.New(standardSwitchConfig())
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
		res := l.Route("vlan10", frame)
		if res.Reason != routing.ReasonTTLExpired {
			t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonTTLExpired)
		}
		if len(res.Frame.Payload) != 0 {
			t.Errorf("expected no egress payload, got %d bytes", len(res.Frame.Payload))
		}
		if last := res.Steps[len(res.Steps)-1]; last.Op != trace.OpDrop || last.Detail != "ttl-expired" {
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
		res := l.Route("vlan10", frame)
		if res.Reason != routing.ReasonNotRouted {
			t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonNotRouted)
		}
		for _, s := range res.Steps {
			if s.Op == trace.OpDrop {
				t.Errorf("unexpected drop step in not-routed result: %+v", s)
			}
		}
		if last := res.Steps[len(res.Steps)-1]; last.Op != trace.OpLookup || last.Detail != "10.0.10.1" {
			t.Errorf("last step = %+v, want lookup 10.0.10.1", last)
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
		res := l.Route("vlan10", frame)
		if res.Reason != routing.ReasonBadHeader {
			t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonBadHeader)
		}
		if last := res.Steps[len(res.Steps)-1]; last.Op != trace.OpDrop || last.Detail != "bad-header" {
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
		res := l.Route("vlan10", frame)
		if res.Reason != routing.ReasonNoRoute {
			t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonNoRoute)
		}
		if last := res.Steps[len(res.Steps)-1]; last.Op != trace.OpDrop || last.Detail != "vrf default" {
			t.Errorf("last step = %+v, want drop vrf default", last)
		}
	})
}

func TestRouteIPv6(t *testing.T) {
	t.Parallel()
	l := routing.New(standardSwitchConfig())
	deviceMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}

	pkt := encodeIPv6Packet(t, netip.MustParseAddr("2001:db8:10::7"), netip.MustParseAddr("2001:db8:20::7"), 64, []byte("ipv6 data"))
	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       deviceMAC,
		EtherType: ethernet.EtherTypeIPv6,
		Payload:   pkt,
	}

	res := l.Route("vlan10", frame)
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
	unroutedPkt := encodeIPv6Packet(t, netip.MustParseAddr("2001:db8:10::7"), netip.MustParseAddr("2001:db8:30::7"), 64, []byte("ipv6 data"))
	unroutedFrame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       deviceMAC,
		EtherType: ethernet.EtherTypeIPv6,
		Payload:   unroutedPkt,
	}
	unroutedRes := l.Route("vlan10", unroutedFrame)
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

	l := routing.New(cfg)

	// Injected on vlan30 to tenant MAC.
	pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.20.7"), 64, []byte("tenant traffic"))
	frameTenant := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       tenantMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	resTenant := l.Route("vlan30", frameTenant)
	if resTenant.Reason != "" {
		t.Fatalf("tenant route reason = %q, want empty", resTenant.Reason)
	}
	if resTenant.Interface != "vlan40" {
		t.Errorf("tenant interface = %q, want vlan40", resTenant.Interface)
	}
	if resTenant.Frame.Dst != tenantNeighborMAC {
		t.Errorf("tenant frame dst = %s, want %s", resTenant.Frame.Dst, tenantNeighborMAC)
	}
	if resTenant.Steps[0].Detail != "vrf tenant interface vlan30" {
		t.Errorf("tenant classify detail = %q, want vrf tenant interface vlan30", resTenant.Steps[0].Detail)
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
	resDefault := l.Route("vlan10", frameDefault)
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
	l2 := routing.New(cfgWithDefaultRoute)

	pkt99 := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.99.7"), 64, []byte("data"))
	frameTenant99 := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
		Dst:       tenantMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt99,
	}
	resTenant99 := l2.Route("vlan30", frameTenant99)
	if resTenant99.Reason != routing.ReasonNoRoute {
		t.Fatalf("tenant99 reason = %q, want %q", resTenant99.Reason, routing.ReasonNoRoute)
	}
	if last := resTenant99.Steps[len(resTenant99.Steps)-1]; last.Op != trace.OpDrop || last.Detail != "vrf tenant" {
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

	l := routing.New(cfg)

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
	res := l.Route("vlan10", frame)
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
	resFromPort := l.Route("1/1/5", frameOnPort)
	if resFromPort.Reason != "" {
		t.Fatalf("route from port reason = %q, want empty", resFromPort.Reason)
	}
	if resFromPort.Interface != "vlan20" {
		t.Errorf("interface = %q, want vlan20", resFromPort.Interface)
	}
	if resFromPort.Steps[0].Detail != "vrf default interface 1/1/5" {
		t.Errorf("classify detail = %q, want vrf default interface 1/1/5", resFromPort.Steps[0].Detail)
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

	l := routing.New(cfg)

	t.Run("static route to next hop resolved on interface whose prefix contains it", func(t *testing.T) {
		t.Parallel()
		pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("192.168.1.5"), 64, []byte("data"))
		frame := ethernet.Frame{
			Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
			Dst:       baseMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := l.Route("vlan10", frame)
		if res.Reason != "" {
			t.Fatalf("reason = %q, want empty", res.Reason)
		}
		if res.Interface != "vlan20" {
			t.Errorf("interface = %q, want vlan20", res.Interface)
		}
		if res.Frame.Dst != gwMAC {
			t.Errorf("destination MAC = %s, want %s", res.Frame.Dst, gwMAC)
		}
		if res.Steps[1].Detail != "192.168.1.0/24 static vlan20" {
			t.Errorf("lookup step = %q, want 192.168.1.0/24 static vlan20", res.Steps[1].Detail)
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
		res := l.Route("vlan10", frame)
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
		res := l.Route("vlan10", frame)
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
		resDefault := l.Route("vlan10", frameDefault)
		if resDefault.Interface != "vlan10" {
			t.Errorf("default route interface = %q, want vlan10", resDefault.Interface)
		}
	})
}

func TestStaticRouteVRFBoundaries(t *testing.T) {
	t.Parallel()
	ports := newTestPortTable(t)

	// Route naming next hop that only another VRF holds is rejected by Validate.
	invalidCfg := routing.Config{
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
	if err := invalidCfg.Validate(ports); err == nil {
		t.Fatalf("expected error for next hop reachable only in another VRF, got nil")
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

	l := routing.New(validExplicitCfg)
	pkt := encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("192.168.1.5"), 64, []byte("data"))
	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77},
		Dst:       netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}
	res := l.Route("vlan10", frame)
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
	l := routing.New(standardSwitchConfig())
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
			},
		},
	}

	l := routing.New(cfg)

	t.Run("longest matching source selection", func(t *testing.T) {
		t.Parallel()
		// 10.0.10.7 matches 10.0.10.1/24 (/24 > /16), so source should be 10.0.10.1.
		res := l.Originate(routing.DefaultVRF, netip.MustParseAddr("10.0.10.7"), 17, []byte("originate payload"))
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
		res := l.Originate(routing.DefaultVRF, netip.MustParseAddr("10.0.50.7"), 17, []byte("direct payload"))
		if res.Reason != "" {
			t.Fatalf("reason = %q, want empty", res.Reason)
		}
		if res.Frame.Dst != hostMAC {
			t.Errorf("dst MAC = %s, want host MAC %s", res.Frame.Dst, hostMAC)
		}
	})

	t.Run("default route gateway for off-prefix destination", func(t *testing.T) {
		t.Parallel()
		res := l.Originate(routing.DefaultVRF, netip.MustParseAddr("198.51.100.1"), 17, []byte("wan payload"))
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
		resMiss := l.Originate(routing.DefaultVRF, netip.MustParseAddr("10.0.20.7"), 17, []byte("data"))
		if resMiss.Reason != routing.ReasonNeighborMiss {
			t.Fatalf("reason = %q, want %q", resMiss.Reason, routing.ReasonNeighborMiss)
		}

		// No route in unknown VRF or family with no route.
		resNoRoute := l.Originate("nonexistent", netip.MustParseAddr("10.0.10.7"), 17, []byte("data"))
		if resNoRoute.Reason != routing.ReasonNoRoute {
			t.Fatalf("reason = %q, want %q", resNoRoute.Reason, routing.ReasonNoRoute)
		}
	})
}
