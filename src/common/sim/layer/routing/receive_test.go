package routing_test

import (
	"bytes"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/ndp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
)

func receiveChecked(t *testing.T, l *routing.Layer, now time.Time, iface string, frame ethernet.Frame) layer.Effects {
	t.Helper()
	admitted := l.Receives(iface, frame)
	before := l.Neighbors()
	effects := l.Receive(now, iface, frame)
	if (len(effects.Emissions) != 0 || !reflect.DeepEqual(l.Neighbors(), before)) && !admitted {
		t.Fatal("Receive answered or learned a frame Receives refused")
	}
	return effects
}

func captureReceiver(t *testing.T, peer bool, mode routing.Mode) *routing.Layer {
	t.Helper()
	mac := netaddr.MAC{0, 0, 0x5e, 0, 0x53, 1}
	prefixes := []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24"), netip.MustParsePrefix("2001:db8::1/64")}
	if peer {
		mac[5] = 7
		prefixes = []netip.Prefix{netip.MustParsePrefix("192.0.2.7/24"), netip.MustParsePrefix("2001:db8::1:800:200e:8c6c/64")}
	}
	return mustNewRouting(t, routing.Config{VRFs: map[string]routing.VRF{routing.DefaultVRF: {
		Interfaces:     map[string]routing.Interface{"vlan10": {VLAN: 10, MAC: mac, Prefixes: prefixes}},
		NeighborPolicy: routing.NeighborPolicy{Mode: mode},
	}}})
}

func TestReceivesNeighborDestinationAndNextHeader(t *testing.T) {
	l := captureReceiver(t, false, routing.NeighborObserved)
	local := netaddr.MAC{0, 0, 0x5e, 0, 0x53, 1}
	nd := receiveNDPFrame(t, "2001:db8::7", "2001:db8::1", local,
		ndp.Message{
			Type: ndp.NeighborSolicitation, Target: netip.MustParseAddr("2001:db8::1"),
			HasLinkLayerAddr: true, LinkLayerAddr: netaddr.MAC{2, 0, 0, 0, 0, 7},
		})
	hop := nd
	hop.Payload = append([]byte(nil), nd.Payload...)
	hdr, body, err := ip.Decode(nd.Payload)
	if err != nil {
		t.Fatal(err)
	}
	hdr.Protocol = 0
	hop.Payload, err = hdr.Encode(append([]byte{58, 0, 0, 0, 0, 0, 0, 0}, body...))
	if err != nil {
		t.Fatal(err)
	}
	udp := nd
	hdr.Protocol = 17
	udp.Payload, err = hdr.Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	if udp.Payload[ip.V6HeaderLen] != byte(ndp.NeighborSolicitation) {
		t.Fatalf("UDP fixture first transport octet = %d, want %d", udp.Payload[ip.V6HeaderLen], ndp.NeighborSolicitation)
	}
	cases := []struct {
		name, iface string
		frame       ethernet.Frame
		want        bool
	}{
		{"ARP own malformed", "vlan10", ethernet.Frame{Dst: local, EtherType: ethernet.EtherTypeARP}, true},
		{"ARP broadcast", "vlan10", ethernet.Frame{Dst: netaddr.MAC{255, 255, 255, 255, 255, 255}, EtherType: ethernet.EtherTypeARP}, true},
		{"ARP other station", "vlan10", ethernet.Frame{Dst: netaddr.MAC{2, 0, 0, 0, 0, 9}, EtherType: ethernet.EtherTypeARP}, false},
		{"unknown interface", "absent", nd, false},
		{"NDP own", "vlan10", nd, true},
		{"hop by hop before NDP", "vlan10", hop, false},
		{"UDP with a solicitation-shaped first octet", "vlan10", udp, false},
		{"non neighbor", "vlan10", ethernet.Frame{Dst: local, EtherType: ethernet.EtherTypeIPv4}, false},
	}
	concrete := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := l.Receives(tc.iface, tc.frame); got != tc.want {
				t.Fatalf("Receives = %v, want %v", got, tc.want)
			}
			if !tc.want && tc.frame.EtherType == ethernet.EtherTypeIPv6 && tc.iface == "vlan10" {
				before := l.Neighbors()
				if fx := receiveChecked(t, l, testNow, tc.iface, tc.frame); len(fx.Emissions) != 0 || !reflect.DeepEqual(l.Neighbors(), before) {
					t.Fatalf("refused frame received: %+v", fx)
				}
			}
			concrete++
		})
	}
	if concrete != 8 {
		t.Fatalf("concrete cases = %d, want 8", concrete)
	}
}

func decodeCapture(t *testing.T, wire []byte) ethernet.Frame {
	t.Helper()
	f, err := ethernet.Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func receiveNDPFrame(t *testing.T, src, dst string, mac netaddr.MAC, msg ndp.Message) ethernet.Frame {
	t.Helper()
	hdr := ip.Header{Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr(dst), Protocol: 58, HopLimit: 255, V6: &ip.V6{}}
	body, err := ndp.Encode(hdr, msg)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hdr.Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	return ethernet.Frame{Src: msg.LinkLayerAddr, Dst: mac, EtherType: ethernet.EtherTypeIPv6, Payload: payload}
}

func TestReceiveAnswersCaptureAndReleasesHeldFrame(t *testing.T) {
	frames := neighborCapture(t)
	for _, test := range []struct {
		name, target, source string
		request, answer      int
	}{
		{"ARP", "192.0.2.7", "192.0.2.1", 0, 1},
		{"NDP", "2001:db8::1:800:200e:8c6c", "2001:db8::1", 2, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			peer := captureReceiver(t, true, routing.NeighborObserved)
			router := captureReceiver(t, false, routing.NeighborObserved)
			res := router.Originate(testNow, routing.DefaultVRF, netip.MustParseAddr(test.target), 17, []byte{1}, true)
			if res.Hold == 0 || len(res.Emissions) != 1 {
				t.Fatalf("setup = %+v, want held and solicited", res)
			}
			request := decodeCapture(t, frames[test.request])
			if !peer.Receives("vlan10", request) {
				t.Fatal("predicate refused a request Receive answers and learns")
			}
			fx := receiveChecked(t, peer, testNow, "vlan10", request)
			if len(fx.Emissions) != 1 {
				t.Fatalf("answers = %+v, want one", fx)
			}
			wire, err := fx.Emissions[0].Frame.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(wire, frames[test.answer]) {
				t.Fatalf("answer = % x, want capture % x", wire, frames[test.answer])
			}
			entries := peer.Neighbors()
			if len(entries) != 1 || entries[0].Addr.String() != test.source || entries[0].State != routing.NeighborStale {
				t.Fatalf("learned = %+v, want stale source", entries)
			}
			if forwarded := peer.Originate(testNow, routing.DefaultVRF, netip.MustParseAddr(test.source), 17, []byte{2}, true); forwarded.Reason != "" || forwarded.Hold != 0 {
				t.Fatalf("source route = %+v, want forwarded", forwarded)
			}
			if !router.Receives("vlan10", fx.Emissions[0].Frame) {
				t.Fatal("predicate refused an answer Receive learns")
			}
			receiveChecked(t, router, testNow, "vlan10", fx.Emissions[0].Frame)
			if entries := router.Neighbors(); len(entries) != 1 || entries[0].State != routing.NeighborReachable {
				t.Fatalf("router neighbors = %+v, want reachable peer", entries)
			}
			if exits := router.DrainExits(); len(exits) != 0 {
				t.Fatalf("early exits = %+v, want none before Advance", exits)
			}
			router.Advance(testNow)
			if exits := router.DrainExits(); len(exits) != 1 || exits[0].Token != res.Hold || exits[0].Cause != routing.HeldReleased {
				t.Fatalf("exits = %+v, want held token released", exits)
			}
		})
	}
	peer := captureReceiver(t, true, routing.NeighborObserved)
	request := receiveNDPFrame(t, "::", "ff02::1:ff0e:8c6c", netaddr.MAC{0x33, 0x33, 0xff, 0x0e, 0x8c, 0x6c}, ndp.Message{Type: ndp.NeighborSolicitation, Target: netip.MustParseAddr("2001:db8::1:800:200e:8c6c")})
	if !peer.Receives("vlan10", request) {
		t.Fatal("predicate refused DAD request Receive answers")
	}
	fx := receiveChecked(t, peer, testNow, "vlan10", request)
	if len(fx.Emissions) != 1 {
		t.Fatalf("DAD answers = %+v, want one", fx)
	}
	wire, err := fx.Emissions[0].Frame.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wire, frames[4]) || len(peer.Neighbors()) != 0 {
		t.Fatalf("DAD answer = % x, neighbors = %+v, want capture and no entry", wire, peer.Neighbors())
	}
}

func TestReceiveARPNonLocalMergesOnlyExistingSenderAndProbeAddsNothing(t *testing.T) {
	l := captureReceiver(t, true, routing.NeighborObserved)
	routerMAC := netaddr.MAC{0, 0, 0x5e, 0, 0x53, 1}
	makeRequest := func(source, target string) ethernet.Frame {
		frame, err := arp.Encode(arp.Message{
			HardwareType: 1, ProtocolType: 0x0800, Operation: arp.Request,
			SenderMAC: routerMAC, SenderAddr: netip.MustParseAddr(source), TargetAddr: netip.MustParseAddr(target),
		}, netaddr.MAC{255, 255, 255, 255, 255, 255})
		if err != nil {
			t.Fatal(err)
		}
		return frame
	}
	receiveChecked(t, l, testNow, "vlan10", makeRequest("192.0.2.1", "192.0.2.8"))
	if len(l.Neighbors()) != 0 {
		t.Fatal("non-local request created a sender entry")
	}
	l.Originate(testNow, routing.DefaultVRF, netip.MustParseAddr("192.0.2.1"), 17, []byte{1}, true)
	if n := l.Neighbors(); len(n) != 1 || n[0].State != routing.NeighborIncomplete {
		t.Fatalf("setup = %+v, want incomplete", n)
	}
	if fx := receiveChecked(t, l, testNow, "vlan10", makeRequest("192.0.2.1", "192.0.2.8")); len(fx.Emissions) != 0 {
		t.Fatalf("non-local replies = %+v, want none", fx)
	}
	if n := l.Neighbors(); len(n) != 1 || n[0].State != routing.NeighborStale || n[0].MAC != routerMAC {
		t.Fatalf("merged = %+v, want stale sender", n)
	}
	before := l.Neighbors()
	if fx := receiveChecked(t, l, testNow, "vlan10", makeRequest("0.0.0.0", "192.0.2.7")); len(fx.Emissions) != 1 {
		t.Fatalf("probe answers = %+v, want one", fx)
	}
	if !reflect.DeepEqual(l.Neighbors(), before) {
		t.Fatal("ARP probe changed neighbor table")
	}
}

func TestReceiveRefusesOnlyTheNamedInvalidInput(t *testing.T) {
	localMAC := netaddr.MAC{0, 0, 0x5e, 0, 0x53, 7}
	peerMAC := netaddr.MAC{0, 0, 0x5e, 0, 0x53, 1}
	local := netip.MustParseAddr("2001:db8::1:800:200e:8c6c")
	for _, test := range []struct {
		name    string
		frame   ethernet.Frame
		pending bool
	}{
		{"non-local target", receiveNDPFrame(t, "2001:db8::1", local.String(), localMAC, ndp.Message{Type: ndp.NeighborSolicitation, Target: netip.MustParseAddr("2001:db8::8"), LinkLayerAddr: peerMAC, HasLinkLayerAddr: true}), false},
		{"unspecified to unicast", receiveNDPFrame(t, "::", local.String(), localMAC, ndp.Message{Type: ndp.NeighborSolicitation, Target: local}), false},
		{"solicited multicast advertisement", receiveNDPFrame(t, "2001:db8::1", "ff02::1", netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, ndp.Message{Type: ndp.NeighborAdvertisement, Target: netip.MustParseAddr("2001:db8::1"), LinkLayerAddr: peerMAC, HasLinkLayerAddr: true, Solicited: true, Override: true}), true},
		{"other station MAC", receiveNDPFrame(t, "2001:db8::1", local.String(), netaddr.MAC{2, 0, 0, 0, 0, 9}, ndp.Message{Type: ndp.NeighborAdvertisement, Target: netip.MustParseAddr("2001:db8::1"), LinkLayerAddr: peerMAC, HasLinkLayerAddr: true, Solicited: true, Override: true}), true},
		{"incomplete without option", receiveNDPFrame(t, "2001:db8::1", local.String(), localMAC, ndp.Message{Type: ndp.NeighborAdvertisement, Target: netip.MustParseAddr("2001:db8::1"), Solicited: true, Override: true}), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			l := captureReceiver(t, true, routing.NeighborObserved)
			if test.pending {
				l.Originate(testNow, routing.DefaultVRF, netip.MustParseAddr("2001:db8::1"), 17, []byte{1}, true)
			}
			before := l.Neighbors()
			if got := l.Receives("vlan10", test.frame); got != (test.name != "other station MAC") {
				t.Fatalf("predicate = %v, want destination admission before decoding", got)
			}
			if fx := receiveChecked(t, l, testNow, "vlan10", test.frame); len(fx.Emissions) != 0 {
				t.Fatalf("effects = %+v, want none", fx)
			}
			if !reflect.DeepEqual(l.Neighbors(), before) {
				t.Fatalf("neighbors = %+v, want %+v", l.Neighbors(), before)
			}
		})
	}
}

func TestReceiveConfiguredEntriesSurviveEveryMessage(t *testing.T) {
	for _, test := range []struct {
		name, family string
		mode         routing.Mode
	}{
		{"observed ARP", "ARP", routing.NeighborObserved},
		{"observed NDP", "NDP", routing.NeighborObserved},
		{"disabled ARP", "ARP", routing.NeighborDisabled},
		{"disabled NDP", "NDP", routing.NeighborDisabled},
	} {
		t.Run(test.name, func(t *testing.T) {
			family := test.family
			cfg := neighborLifecycleConfig(routing.NeighborPolicy{Mode: test.mode})
			vrf := cfg.VRFs[routing.DefaultVRF]
			addr := lifecycleDstV4
			if family == "NDP" {
				addr = lifecycleDstV6
			}
			vrf.Neighbors = []routing.Neighbor{{Interface: "vlan10", Addr: addr, MAC: lifecycleHostMAC}}
			cfg.VRFs[routing.DefaultVRF] = vrf
			l := mustNewRouting(t, cfg)
			before := l.Neighbors()
			wrongMAC := netaddr.MAC{2, 0, 0, 0, 0, 9}
			for _, request := range []bool{false, true} {
				var frame ethernet.Frame
				if family == "ARP" {
					op := arp.Reply
					if request {
						op = arp.Request
					}
					var err error
					frame, err = arp.Encode(arp.Message{
						HardwareType: 1, ProtocolType: 0x0800, Operation: op, SenderAddr: addr,
						SenderMAC: wrongMAC, TargetAddr: netip.MustParseAddr("10.0.10.1"), TargetMAC: lifecycleDeviceMAC,
					}, lifecycleDeviceMAC)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					msg := ndp.Message{Type: ndp.NeighborAdvertisement, Target: addr, HasLinkLayerAddr: true, LinkLayerAddr: wrongMAC, Solicited: true, Override: true}
					if request {
						msg = ndp.Message{Type: ndp.NeighborSolicitation, Target: netip.MustParseAddr("2001:db8:10::1"), HasLinkLayerAddr: true, LinkLayerAddr: wrongMAC}
					}
					frame = receiveNDPFrame(t, addr.String(), "2001:db8:10::1", lifecycleDeviceMAC, msg)
				}
				fx := receiveChecked(t, l, testNow, "vlan10", frame)
				if request && (len(fx.Emissions) != 1 || fx.Emissions[0].Frame.Dst != wrongMAC) {
					t.Fatalf("request answer = %+v, want option or sender MAC", fx)
				}
				if !reflect.DeepEqual(l.Neighbors(), before) {
					t.Fatalf("configured entries = %+v, want %+v", l.Neighbors(), before)
				}
			}
		})
	}
}

func TestReceiveAnswerUsesOptionThenCacheAndDisabledNeverLearns(t *testing.T) {
	for _, mode := range []routing.Mode{routing.NeighborObserved, routing.NeighborDisabled} {
		t.Run(string(mode), func(t *testing.T) {
			l := captureReceiver(t, true, mode)
			mac := netaddr.MAC{2, 0, 0, 0, 0, 1}
			localMAC := netaddr.MAC{0, 0, 0x5e, 0, 0x53, 7}
			msg := ndp.Message{Type: ndp.NeighborSolicitation, Target: netip.MustParseAddr("2001:db8::1:800:200e:8c6c")}
			noOption := receiveNDPFrame(t, "2001:db8::1", msg.Target.String(), localMAC, msg)
			if fx := receiveChecked(t, l, testNow, "vlan10", noOption); len(fx.Emissions) != 0 {
				t.Fatalf("uncached answer = %+v, want none", fx)
			}
			msg.HasLinkLayerAddr = true
			msg.LinkLayerAddr = mac
			fx := receiveChecked(t, l, testNow, "vlan10", receiveNDPFrame(t, "2001:db8::1", msg.Target.String(), localMAC, msg))
			if len(fx.Emissions) != 1 || fx.Emissions[0].Frame.Dst != mac {
				t.Fatalf("option answer = %+v, want option MAC", fx)
			}
			fx = receiveChecked(t, l, testNow.Add(time.Millisecond), "vlan10", noOption)
			if mode == routing.NeighborDisabled {
				if len(fx.Emissions) != 0 || len(l.Neighbors()) != 0 {
					t.Fatalf("disabled = %+v/%+v, want no cache", fx, l.Neighbors())
				}
				arpFx := receiveChecked(t, l, testNow, "vlan10", decodeCapture(t, neighborCapture(t)[0]))
				if len(arpFx.Emissions) != 1 || len(l.Neighbors()) != 0 {
					t.Fatalf("disabled ARP = %+v/%+v", arpFx, l.Neighbors())
				}
			} else if len(fx.Emissions) != 1 || fx.Emissions[0].Frame.Dst != mac {
				t.Fatalf("cached answer = %+v, want cached MAC", fx)
			}
		})
	}
}

func TestReceiveSolicitedReplyInDelayRestoresReachability(t *testing.T) {
	l := captureReceiver(t, false, routing.NeighborObserved)
	dst := netip.MustParseAddr("2001:db8::7")
	mac := netaddr.MAC{2, 0, 0, 0, 0, 7}
	l.Originate(testNow, routing.DefaultVRF, dst, 17, nil, true)
	msg := ndp.Message{Type: ndp.NeighborAdvertisement, Target: dst, LinkLayerAddr: mac, HasLinkLayerAddr: true, Solicited: true, Override: true}
	frame := receiveNDPFrame(t, dst.String(), "2001:db8::1", netaddr.MAC{0, 0, 0x5e, 0, 0x53, 1}, msg)
	receiveChecked(t, l, testNow, "vlan10", frame)
	l.Advance(testNow)
	l.Originate(testNow.Add(30*time.Second), routing.DefaultVRF, dst, 17, nil, true)
	if l.Neighbors()[0].State != routing.NeighborDelay {
		t.Fatalf("neighbors %v, want Delay", l.Neighbors())
	}
	receiveChecked(t, l, testNow.Add(31*time.Second), "vlan10", frame)
	if l.Neighbors()[0].State != routing.NeighborReachable {
		t.Fatalf("solicited reply leaves %v, want Reachable", l.Neighbors())
	}
}

func TestReceiveSolicitationWithoutOptionFromIncompleteGetsNoAnswer(t *testing.T) {
	l := captureReceiver(t, false, routing.NeighborObserved)
	src := netip.MustParseAddr("2001:db8::7")
	l.Originate(testNow, routing.DefaultVRF, src, 17, nil, true)
	frame := receiveNDPFrame(t, src.String(), "2001:db8::1", netaddr.MAC{0, 0, 0x5e, 0, 0x53, 1}, ndp.Message{Type: ndp.NeighborSolicitation, Target: netip.MustParseAddr("2001:db8::1")})
	if fx := receiveChecked(t, l, testNow, "vlan10", frame); len(fx.Emissions) != 0 {
		t.Fatalf("Incomplete source without option got answer %+v", fx)
	}
	if l.Neighbors()[0].State != routing.NeighborIncomplete {
		t.Fatalf("source changed: %v", l.Neighbors())
	}
}

func TestReceiveAllNodesAdvertisementUpdatesExistingEntry(t *testing.T) {
	l := captureReceiver(t, false, routing.NeighborObserved)
	dst := netip.MustParseAddr("2001:db8::7")
	l.Originate(testNow, routing.DefaultVRF, dst, 17, nil, true)
	mac := netaddr.MAC{2, 0, 0, 0, 0, 7}
	msg := ndp.Message{Type: ndp.NeighborAdvertisement, Target: dst, LinkLayerAddr: mac, HasLinkLayerAddr: true, Solicited: true, Override: true}
	receiveChecked(t, l, testNow, "vlan10", receiveNDPFrame(t, dst.String(), "2001:db8::1", netaddr.MAC{0, 0, 0x5e, 0, 0x53, 1}, msg))
	msg.Solicited = false
	msg.LinkLayerAddr[5] = 8
	receiveChecked(t, l, testNow, "vlan10", receiveNDPFrame(t, dst.String(), "ff02::1", netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, msg))
	entries := l.Neighbors()
	if len(entries) != 1 || entries[0].State != routing.NeighborStale || entries[0].MAC != msg.LinkLayerAddr {
		t.Fatalf("all-nodes update: %v, want Stale at %v", entries, msg.LinkLayerAddr)
	}
}

func TestReceiveOtherStationsSolicitedNodeMACIsIgnored(t *testing.T) {
	l := captureReceiver(t, false, routing.NeighborObserved)
	msg := ndp.Message{Type: ndp.NeighborSolicitation, Target: netip.MustParseAddr("2001:db8::1"), LinkLayerAddr: netaddr.MAC{2, 0, 0, 0, 0, 7}, HasLinkLayerAddr: true}
	frame := receiveNDPFrame(t, "2001:db8::7", "ff02::1:ff00:1", netaddr.MAC{0x33, 0x33, 0xff, 0, 0, 8}, msg)
	fx := receiveChecked(t, l, testNow, "vlan10", frame)
	if len(fx.Emissions) != 0 || len(l.Neighbors()) != 0 {
		t.Fatalf("other station group learned or answered: %+v/%v", fx, l.Neighbors())
	}
}
