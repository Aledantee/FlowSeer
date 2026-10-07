package routing_test

import (
	"bytes"
	"io"
	"net/netip"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/pcap"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func neighborCapture(t *testing.T) [][]byte {
	t.Helper()
	f, err := os.Open("testdata/neighbor.pcap")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	r, err := pcap.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var frames [][]byte
	for {
		record, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if record.LinkType != 1 {
			t.Fatalf("link type = %d, want Ethernet", record.LinkType)
		}
		frames = append(frames, record.Data)
	}
	if len(frames) != 5 {
		t.Fatalf("capture frames = %d, want 5", len(frames))
	}
	return frames
}

func TestNeighborSolicitationMatchesCapture(t *testing.T) {
	frames := neighborCapture(t)
	for _, test := range []struct {
		name, target string
		record       int
	}{
		{"ARP", "192.0.2.7", 0},
		{"NDP", "2001:db8::1:800:200e:8c6c", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := neighborLifecycleConfig(routing.NeighborPolicy{})
			vrf := cfg.VRFs[routing.DefaultVRF]
			iface := vrf.Interfaces["vlan10"]
			iface.MAC = netaddr.MAC{0, 0, 0x5e, 0, 0x53, 1}
			iface.Prefixes = []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24"), netip.MustParsePrefix("2001:db8::1/64")}
			vrf.Interfaces["vlan10"] = iface
			l := mustNewRouting(t, cfg)
			dst := netip.MustParseAddr(test.target)
			peek := l.Originate(testNow, routing.DefaultVRF, dst, 17, []byte{1}, false)
			if len(peek.Emissions) != 0 || len(l.Neighbors()) != 0 {
				t.Fatal("preview mutates neighbor resolution")
			}
			res := l.Originate(testNow, routing.DefaultVRF, dst, 17, []byte{1}, true)
			if res.Reason != routing.ReasonNeighborPending || res.Hold == 0 || len(res.Emissions) != 1 {
				t.Fatalf("result = %+v, want held frame and one solicitation", res)
			}
			em := res.Emissions[0]
			if em.Port != "" || em.VID != 10 {
				t.Errorf("egress = %q/%d, want VLAN 10", em.Port, em.VID)
			}
			wire, err := em.Frame.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(wire, frames[test.record]) {
				t.Errorf("wire = % x, want capture % x", wire, frames[test.record])
			}
			if wake, ok := l.NextWake(); !ok || !wake.Equal(testNow.Add(time.Second)) {
				t.Errorf("wake = %s/%t, want one second", wake, ok)
			}
		})
	}
}

func TestNeighborRetransmissionsDeleteAndRestart(t *testing.T) {
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
	res := routeToV4(t, l, testNow, lifecycleDstV4, []byte{1}, true)
	if len(res.Emissions) != 1 {
		t.Errorf("first emissions = %d, want 1", len(res.Emissions))
	}
	for second := 1; second <= 2; second++ {
		fx := l.Advance(testNow.Add(time.Duration(second) * time.Second))
		if len(fx.Emissions) != 1 || len(l.DrainExits()) != 0 {
			t.Fatalf("second %d effects = %+v, want retransmission and no exit", second, fx)
		}
		if len(l.Advance(testNow.Add(time.Duration(second)*time.Second)).Emissions) != 0 {
			t.Fatal("duplicate retransmission at same time")
		}
	}
	fx := l.Advance(testNow.Add(3 * time.Second))
	exits := l.DrainExits()
	if len(fx.Emissions) != 0 || len(exits) != 1 || exits[0].Cause != routing.HeldTimedOut || exits[0].Token != res.Hold {
		t.Fatalf("timeout effects = %+v, exits = %+v", fx, exits)
	}
	if len(l.Neighbors()) != 0 {
		t.Fatal("failed resolution retains neighbor")
	}
	if _, ok := l.NextWake(); ok {
		t.Fatal("failed resolution retains timer")
	}
	retry := routeToV4(t, l, testNow.Add(3*time.Second), lifecycleDstV4, []byte{2}, true)
	if retry.Reason != routing.ReasonNeighborPending || len(retry.Emissions) != 1 {
		t.Fatalf("retry = %+v, want a new solicitation", retry)
	}
}

func TestNeighborLazyReachabilityAndProbes(t *testing.T) {
	for _, family := range []string{"IPv4", "IPv6"} {
		t.Run(family, func(t *testing.T) {
			l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
			dst := lifecycleDstV4
			route := routeToV4
			if family == "IPv6" {
				dst = lifecycleDstV6
				route = routeToV6
			}
			route(t, l, testNow, dst, []byte{1}, true)
			mac := lifecycleHostMAC
			l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: dst, MAC: mac, HasMAC: true, Solicited: true, Override: true})
			l.Advance(testNow)
			l.DrainExits()
			aged := l.Clone()
			aged.Advance(testNow.Add(31 * time.Second))
			peek := route(t, l, testNow.Add(31*time.Second), dst, []byte{2}, false)
			wantState(t, peek, "stale")
			if l.Neighbors()[0].State != routing.NeighborReachable {
				t.Fatal("preview ages stored entry")
			}
			for _, engine := range []*routing.Layer{l, aged} {
				res := route(t, engine, testNow.Add(31*time.Second), dst, []byte{2}, true)
				if res.Frame.Dst != mac || string(engine.Neighbors()[0].State) != "delay" {
					t.Fatalf("route = %+v, neighbors = %+v, want cached forwarding and delay", res, engine.Neighbors())
				}
				if wake, ok := engine.NextWake(); !ok || !wake.Equal(testNow.Add(36*time.Second)) {
					t.Fatalf("delay wake = %s/%t", wake, ok)
				}
			}
			for second := 36; second <= 38; second++ {
				fx := l.Advance(testNow.Add(time.Duration(second) * time.Second))
				if len(fx.Emissions) != 1 || fx.Emissions[0].Frame.Dst != mac || string(l.Neighbors()[0].State) != "probe" {
					t.Fatalf("probe effects = %+v, neighbors = %+v", fx, l.Neighbors())
				}
				if family == "IPv6" {
					hdr, _, err := ip.Decode(fx.Emissions[0].Frame.Payload)
					if err != nil || hdr.Dst != dst {
						t.Fatalf("probe header = %+v/%v, want target destination", hdr, err)
					}
				}
				res := route(t, l, testNow.Add(time.Duration(second)*time.Second), dst, []byte{3}, true)
				if res.Frame.Dst != mac || res.Reason != "" {
					t.Fatal("probe does not forward cached MAC")
				}
			}
			l.Advance(testNow.Add(39 * time.Second))
			if len(l.Neighbors()) != 0 {
				t.Fatal("exhausted probes retain entry")
			}
		})
	}
}

func TestNeighborDelayedWakeDoesNotBurst(t *testing.T) {
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
	routeToV4(t, l, testNow, lifecycleDstV4, []byte{1}, true)
	fx := l.Advance(testNow.Add(time.Hour))
	if len(fx.Emissions) != 1 || len(l.DrainExits()) != 0 {
		t.Fatalf("late wake = %+v, want one retransmission", fx)
	}
	if wake, ok := l.NextWake(); !ok || !wake.Equal(testNow.Add(time.Hour+time.Second)) {
		t.Fatalf("late wake timer = %s/%t", wake, ok)
	}
}

var (
	lifecycleDeviceMAC = netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	lifecycleHostMAC   = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	lifecycleDstV4     = netip.MustParseAddr("10.0.10.99")
	lifecycleDstV6     = netip.MustParseAddr("2001:db8:10::99")
)

// neighborLifecycleConfig builds a single-VRF, single-interface config on both address
// families, with policy as the VRF's NeighborPolicy so a test can tune the timers and depth
// it needs.
func neighborLifecycleConfig(policy routing.NeighborPolicy) routing.Config {
	return routing.Config{
		VRFs: map[string]routing.VRF{
			routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"vlan10": {
						VLAN: 10,
						MAC:  lifecycleDeviceMAC,
						Prefixes: []netip.Prefix{
							netip.MustParsePrefix("10.0.10.1/24"),
							netip.MustParsePrefix("2001:db8:10::1/64"),
						},
					},
				},
				NeighborPolicy: policy,
			},
		},
	}
}

func routeToV4(t *testing.T, l *routing.Layer, now time.Time, dst netip.Addr, payload []byte, commit bool) routing.Result {
	t.Helper()
	frame := ethernet.Frame{
		Src:       lifecycleHostMAC,
		Dst:       lifecycleDeviceMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   encodeIPv4Packet(t, netip.MustParseAddr("10.0.10.7"), dst, 64, payload),
	}
	return l.Route(now, "vlan10", frame, commit)
}

func routeToV6(t *testing.T, l *routing.Layer, now time.Time, dst netip.Addr, payload []byte, commit bool) routing.Result {
	t.Helper()
	frame := ethernet.Frame{
		Src:       lifecycleHostMAC,
		Dst:       lifecycleDeviceMAC,
		EtherType: ethernet.EtherTypeIPv6,
		Payload:   encodeIPv6Packet(t, netip.MustParseAddr("2001:db8:10::7"), dst, payload),
	}
	return l.Route(now, "vlan10", frame, commit)
}

// neighborFact returns the canonical string of the routing.neighbor_decision fact carried by
// res.Steps, searching from the last step backward so it finds whichever step (pending, miss,
// or the success rewrite) carries it.
func neighborFact(t *testing.T, res routing.Result) string {
	t.Helper()
	for i := len(res.Steps) - 1; i >= 0; i-- {
		for _, f := range res.Steps[i].Outputs {
			if f.TypeID() == "routing.neighbor_decision" {
				return f.Canonical()
			}
		}
		for _, f := range res.Steps[i].Inputs {
			if f.TypeID() == "routing.neighbor_decision" {
				return f.Canonical()
			}
		}
	}
	t.Fatalf("no neighbor decision fact found in steps: %+v", res.Steps)
	return ""
}

func wantState(t *testing.T, res routing.Result, state string) {
	t.Helper()
	fact := neighborFact(t, res)
	if !strings.Contains(fact, `state="`+state+`"`) {
		t.Errorf("neighbor fact = %s, want state %q", fact, state)
	}
}

func wantMACInFact(t *testing.T, res routing.Result, mac netaddr.MAC) {
	t.Helper()
	fact := neighborFact(t, res)
	if !strings.Contains(fact, `mac="`+mac.String()+`"`) {
		t.Errorf("neighbor fact = %s, want mac %q", fact, mac)
	}
}

func wantOrigin(t *testing.T, res routing.Result, origin string) {
	t.Helper()
	fact := neighborFact(t, res)
	if !strings.Contains(fact, `origin="`+origin+`"`) {
		t.Errorf("neighbor fact = %s, want origin %q", fact, origin)
	}
}

// TestNeighborFactReportsOrigin verifies that a configured binding's decision fact names it
// "configured" and an entry NeighborObserved resolution creates
// names it "observed", the vocabulary [bridge.Origin] and [mcast.Origin] also use.
func TestNeighborFactReportsOrigin(t *testing.T) {
	t.Parallel()

	cfg := neighborLifecycleConfig(routing.NeighborPolicy{})
	vrf := cfg.VRFs[routing.DefaultVRF]
	vrf.Neighbors = []routing.Neighbor{{Interface: "vlan10", Addr: lifecycleDstV4, MAC: lifecycleHostMAC}}
	cfg.VRFs[routing.DefaultVRF] = vrf
	configured := mustNewRouting(t, cfg)
	wantOrigin(t, routeToV4(t, configured, testNow, lifecycleDstV4, []byte("data"), true), "configured")

	observed := mustNewLifecycleLayer(t, routing.NeighborPolicy{Mode: routing.NeighborObserved})
	wantOrigin(t, routeToV4(t, observed, testNow, lifecycleDstV4, []byte("data"), true), "observed")
}

// TestRouteMissCreatesIncompleteEntryAndQueuesTheFrame tests that a
// destination nothing resolved yet does not vanish as a plain drop.
func TestRouteMissCreatesIncompleteEntryAndQueuesTheFrame(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})

	res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
	if res.Reason != routing.ReasonNeighborPending {
		t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonNeighborPending)
	}
	wantState(t, res, "incomplete")
}

// TestObserveIncompleteMovesToReachableOnSolicitedAdvertisement and its IPv6 subtest cover the
// RFC 4861 section 7.2.5 INCOMPLETE branch: a solicited advertisement with a link-layer address
// moves the entry to Reachable and records the MAC, for both families the state machine serves.
func TestObserveIncompleteMovesToReachableOnSolicitedAdvertisement(t *testing.T) {
	t.Parallel()

	t.Run("IPv4", func(t *testing.T) {
		t.Parallel()
		l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
		if res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true); res.Reason != routing.ReasonNeighborPending {
			t.Fatalf("setup reason = %q, want pending", res.Reason)
		}

		observedMAC := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}
		l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: observedMAC, HasMAC: true, Solicited: true, Override: true})

		res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
		if res.Reason != "" {
			t.Fatalf("reason = %q, want empty", res.Reason)
		}
		if res.Frame.Dst != observedMAC {
			t.Errorf("frame dst = %s, want %s", res.Frame.Dst, observedMAC)
		}
		wantState(t, res, "reachable")
	})

	t.Run("IPv6", func(t *testing.T) {
		t.Parallel()
		l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
		if res := routeToV6(t, l, testNow, lifecycleDstV6, []byte("data"), true); res.Reason != routing.ReasonNeighborPending {
			t.Fatalf("setup reason = %q, want pending", res.Reason)
		}

		observedMAC := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x66}
		l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV6, MAC: observedMAC, HasMAC: true, Solicited: true, Override: true})

		res := routeToV6(t, l, testNow, lifecycleDstV6, []byte("data"), true)
		if res.Reason != "" {
			t.Fatalf("reason = %q, want empty", res.Reason)
		}
		if res.Frame.Dst != observedMAC {
			t.Errorf("frame dst = %s, want %s", res.Frame.Dst, observedMAC)
		}
		wantState(t, res, "reachable")
	})
}

// TestObserveIncompleteMovesToStaleOnUnsolicitedAdvertisement covers the INCOMPLETE branch's
// unsolicited half: the entry still records the address but starts Stale rather than Reachable.
func TestObserveIncompleteMovesToStaleOnUnsolicitedAdvertisement(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
	routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)

	observedMAC := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x77}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: observedMAC, HasMAC: true, Solicited: false, Override: true})

	res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
	if res.Reason != "" {
		t.Fatalf("reason = %q, want empty", res.Reason)
	}
	if res.Frame.Dst != observedMAC {
		t.Errorf("frame dst = %s, want %s", res.Frame.Dst, observedMAC)
	}
	wantState(t, res, "stale")
}

// TestObserveIncompleteWithoutLinkLayerAddressIsDiscarded covers RFC 4861 section 7.2.5's
// INCOMPLETE rule that a target link-layer address option is required: "the receiving node
// SHOULD silently discard the received advertisement."
func TestObserveIncompleteWithoutLinkLayerAddressIsDiscarded(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
	routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)

	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, HasMAC: false, Solicited: true, Override: true})

	res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
	if res.Reason != routing.ReasonNeighborPending {
		t.Fatalf("reason = %q, want pending (entry must stay Incomplete)", res.Reason)
	}
	wantState(t, res, "incomplete")
}

// TestObserveOverrideClearedReachableMovesToStale is RFC 4861 section 7.2.5 rule I.a: Override
// clear and a differing link-layer address moves a Reachable entry to Stale without adopting
// the new address.
func TestObserveOverrideClearedReachableMovesToStale(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
	routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)

	originalMAC := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x01}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: originalMAC, HasMAC: true, Solicited: true, Override: true})

	differentMAC := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x02}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: differentMAC, HasMAC: true, Solicited: false, Override: false})

	res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
	if res.Reason != "" {
		t.Fatalf("reason = %q, want empty", res.Reason)
	}
	if res.Frame.Dst != originalMAC {
		t.Errorf("frame dst = %s, want the unchanged original MAC %s", res.Frame.Dst, originalMAC)
	}
	wantState(t, res, "stale")
}

// TestObserveOverrideClearedNonReachableStateIsUnchanged is RFC 4861 section 7.2.5 rule I.b:
// "the received advertisement should be ignored and MUST NOT update the cache" when the entry
// is not Reachable.
func TestObserveOverrideClearedNonReachableStateIsUnchanged(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
	routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)

	staleMAC := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x03}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: staleMAC, HasMAC: true, Solicited: false, Override: true})

	before := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
	wantState(t, before, "stale")

	differentMAC := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x04}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: differentMAC, HasMAC: true, Solicited: false, Override: false})

	after := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
	if after.Reason != "" {
		t.Fatalf("reason = %q, want empty", after.Reason)
	}
	if after.Frame.Dst != staleMAC {
		t.Errorf("frame dst = %s, want the unchanged MAC %s", after.Frame.Dst, staleMAC)
	}
	wantState(t, after, "stale")
}

// TestObserveOverrideSetUnsolicitedUpdateMovesToStale is RFC 4861 section 7.2.5 rule II: "If
// the Solicited flag is zero and the link-layer address was updated with a different address,
// the state MUST be set to STALE."
func TestObserveOverrideSetUnsolicitedUpdateMovesToStale(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
	routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)

	firstMAC := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x05}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: firstMAC, HasMAC: true, Solicited: true, Override: true})

	secondMAC := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x06}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: secondMAC, HasMAC: true, Solicited: false, Override: true})

	res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
	if res.Frame.Dst != secondMAC {
		t.Errorf("frame dst = %s, want the updated MAC %s", res.Frame.Dst, secondMAC)
	}
	wantState(t, res, "stale")
}

// TestObserveOverrideSetUnsolicitedNoChangeLeavesStateUnchanged is RFC 4861 section 7.2.5
// rule II's last sentence: "There is no need to update the state for unsolicited advertisements
// that do not change the contents of the cache."
func TestObserveOverrideSetUnsolicitedNoChangeLeavesStateUnchanged(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
	routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)

	mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x07}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: mac, HasMAC: true, Solicited: true, Override: true})
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: mac, HasMAC: true, Solicited: false, Override: true})

	res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
	wantState(t, res, "reachable")
	wantMACInFact(t, res, mac)
}

// TestObserveARPFlagMappings pins the two ARP-to-RFC-4861 mappings the package README
// documents: a reply's {Solicited: true, Override: true} confirms the forward path, and a
// request's sender fields carry {Solicited: false, Override: true}.
func TestObserveARPFlagMappings(t *testing.T) {
	t.Parallel()

	t.Run("reply reaches Reachable", func(t *testing.T) {
		t.Parallel()
		l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
		routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)

		mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x08}
		l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: mac, HasMAC: true, Solicited: true, Override: true, Router: false})

		res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
		wantState(t, res, "reachable")
	})

	t.Run("request reaches Stale", func(t *testing.T) {
		t.Parallel()
		l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
		routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)

		mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x09}
		l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: mac, HasMAC: true, Solicited: false, Override: true, Router: false})

		res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
		wantState(t, res, "stale")
	})
}

// TestObserveWithNoEntryChangesNothing is RFC 4861 section 7.2.5: "If no entry exists, the
// advertisement SHOULD be silently discarded." An advertisement for an address nothing was ever
// routed to must not leave any trace on a later lookup.
func TestObserveWithNoEntryChangesNothing(t *testing.T) {
	t.Parallel()
	observed := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
	baseline := mustNewLifecycleLayer(t, routing.NeighborPolicy{})

	observed.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x0a}, HasMAC: true, Solicited: true, Override: true})

	gotRes := routeToV4(t, observed, testNow, lifecycleDstV4, []byte("data"), true)
	wantRes := routeToV4(t, baseline, testNow, lifecycleDstV4, []byte("data"), true)
	if gotRes.Reason != wantRes.Reason {
		t.Fatalf("reason = %q, want %q", gotRes.Reason, wantRes.Reason)
	}
	if neighborFact(t, gotRes) != neighborFact(t, wantRes) {
		t.Errorf("neighbor fact = %s, want %s (observing a never-routed address must be a no-op)", neighborFact(t, gotRes), neighborFact(t, wantRes))
	}
}

// TestHoldQueueDropsOldestAtDepth is RFC 4861 section 7.2.2: "When a queue overflows, the new
// arrival SHOULD replace the oldest entry." At HoldDepth 3, four queued frames release as
// frames two through four in arrival order, and the evicted first frame is reported failed
// rather than dropped without a trace.
func TestHoldQueueDropsOldestAtDepth(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{HoldDepth: 3})

	for i := 1; i <= 4; i++ {
		res := routeToV4(t, l, testNow, lifecycleDstV4, []byte{byte(i)}, true)
		if res.Reason != routing.ReasonNeighborPending {
			t.Fatalf("frame %d reason = %q, want pending", i, res.Reason)
		}
	}

	mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x0b}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: mac, HasMAC: true, Solicited: true, Override: true})

	eff := advanceAndDrain(l, testNow)
	evicted := exitsWithCause(eff, routing.HeldEvicted)
	if len(evicted) != 1 {
		t.Fatalf("evicted = %+v, want 1 (frame 1, the one appendHeld pushed out)", evicted)
	}
	if got := lastPayloadByte(t, evicted[0].Frame); got != 1 {
		t.Errorf("evicted[0] payload marker = %d, want 1", got)
	}
	// The eviction resolved on the same Advance, so nothing here is a resolution that failed.
	if timedOut := exitsWithCause(eff, routing.HeldTimedOut); len(timedOut) != 0 {
		t.Errorf("timed out = %+v, want none: the neighbor resolved", timedOut)
	}
	released := exitsWithCause(eff, routing.HeldReleased)
	if len(released) != 3 {
		t.Fatalf("released = %d frames, want 3", len(released))
	}
	for i, want := range []byte{2, 3, 4} {
		got := lastPayloadByte(t, released[i].Frame)
		if got != want {
			t.Errorf("released[%d] payload marker = %d, want %d (arrival order after dropping the oldest)", i, got, want)
		}
		if released[i].Frame.Dst != mac {
			t.Errorf("released[%d] dst = %s, want %s", i, released[i].Frame.Dst, mac)
		}
		if released[i].Interface != "vlan10" {
			t.Errorf("released[%d] interface = %q, want vlan10", i, released[i].Interface)
		}
	}
}

func TestStalePeekLeavesStoredStateUnchanged(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
	routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)

	mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x0c}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: mac, HasMAC: true, Solicited: false, Override: true})

	for i := range 3 {
		res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), false)
		if res.Reason != "" {
			t.Fatalf("attempt %d reason = %q, want empty", i, res.Reason)
		}
		if res.Frame.Dst != mac {
			t.Errorf("attempt %d dst = %s, want %s", i, res.Frame.Dst, mac)
		}
		wantState(t, res, "stale")
	}
	if l.Neighbors()[0].State != routing.NeighborStale {
		t.Fatal("peek writes stale state")
	}
}

// TestCloneIsolatesNeighborState is evidence Clone copies the runtime table rather than sharing
// it: waking the original after the clone must not release or fail the clone's held frame, and
// observing on the clone must not resolve the original's entry.
func TestCloneIsolatesNeighborState(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{RetransTimer: time.Second})
	routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)

	clone := l.Clone()

	mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x0e}
	clone.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: mac, HasMAC: true, Solicited: true, Override: true})

	// A peek (commit false) so checking the original's state does not itself queue a second
	// frame on it.
	if res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), false); res.Reason != routing.ReasonNeighborPending {
		t.Errorf("original reason = %q after observing the clone, want still pending", res.Reason)
	}

	eff := advanceAndDrain(l, testNow.Add(time.Second))
	if timedOut := exitsWithCause(eff, routing.HeldTimedOut); len(timedOut) != 1 {
		t.Fatalf("original timed out = %d, want 1 (unaffected by the clone's Observe)", len(timedOut))
	}

	cloneRes := routeToV4(t, clone, testNow, lifecycleDstV4, []byte("data"), true)
	if cloneRes.Reason != "" || cloneRes.Frame.Dst != mac {
		t.Errorf("clone reason = %q, dst = %s, want resolved to %s", cloneRes.Reason, cloneRes.Frame.Dst, mac)
	}
}

// orderingTrials is how many times an ordering test rebuilds and re-runs its scenario. DrainExits
// drains, so a layer cannot be reused; a single run of the two-address case passes about half
// the time with the sort reverted, which is not a gate. Fifty independent Go map iterations
// agreeing on one order is.
const orderingTrials = 50

// twoVRFOrderingConfig builds two VRFs of two VLAN interfaces each, so an ordering test can
// cover the two sort keys ahead of the address. Interface names and VLAN IDs are unique across
// VRFs because [routing.Layer.Observe] resolves a VRF from the interface name alone and the
// config refuses a VLAN claimed twice. The higher-numbered pair sits in the lower-named VRF on
// purpose: sorting by interface or address alone then produces a different order from sorting by
// VRF first, so the test can tell the two apart.
func twoVRFOrderingConfig() routing.Config {
	iface := func(vid vlan.ID, prefix string) routing.Interface {
		return routing.Interface{VLAN: vid, MAC: lifecycleDeviceMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix(prefix)}}
	}
	return routing.Config{VRFs: map[string]routing.VRF{
		"vrf-a": {Interfaces: map[string]routing.Interface{
			"vlan30": iface(30, "10.0.30.1/24"),
			"vlan40": iface(40, "10.0.40.1/24"),
		}},
		"vrf-b": {Interfaces: map[string]routing.Interface{
			"vlan10": iface(10, "10.0.10.1/24"),
			"vlan20": iface(20, "10.0.20.1/24"),
		}},
	}}
}

// TestWakeReleasesInDeterministicOrder checks that held-frame releases range l.vrfs and a VRF's neighbors,
// both Go maps, in sorted order so the release order does not vary run to run. Next hops resolved
// together must release in (VRF, interface, address) order every time.
func TestWakeReleasesInDeterministicOrder(t *testing.T) {
	t.Parallel()
	mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x20}

	t.Run("by address on one interface", func(t *testing.T) {
		t.Parallel()
		addr98 := netip.MustParseAddr("10.0.10.98")
		addr99 := netip.MustParseAddr("10.0.10.99")

		for trial := range orderingTrials {
			l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
			if res := routeToV4(t, l, testNow, addr98, []byte{98}, true); res.Reason != routing.ReasonNeighborPending {
				t.Fatalf("trial %d: addr98 reason = %q, want pending", trial, res.Reason)
			}
			if res := routeToV4(t, l, testNow, addr99, []byte{99}, true); res.Reason != routing.ReasonNeighborPending {
				t.Fatalf("trial %d: addr99 reason = %q, want pending", trial, res.Reason)
			}
			l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: addr98, MAC: mac, HasMAC: true, Solicited: true, Override: true})
			l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: addr99, MAC: mac, HasMAC: true, Solicited: true, Override: true})

			wantReleaseOrder(t, trial, advanceAndDrain(l, testNow), 98, 99)
		}
	})

	// The VRF and interface keys sort ahead of the address, and no other test drives a layer
	// whose held frames differ by either. Each entry is queued in the map's own iteration order,
	// so the queuing order varies run to run as well as the table's.
	t.Run("by VRF then interface", func(t *testing.T) {
		t.Parallel()
		// Markers descend in the expected release order, so sorting by interface name or by
		// address instead would produce 2, 1, 4, 3.
		queued := map[string]struct {
			addr   netip.Addr
			marker byte
		}{
			"vlan30": {netip.MustParseAddr("10.0.30.50"), 4},
			"vlan40": {netip.MustParseAddr("10.0.40.50"), 3},
			"vlan10": {netip.MustParseAddr("10.0.10.50"), 2},
			"vlan20": {netip.MustParseAddr("10.0.20.50"), 1},
		}
		ifaceVRF := map[string]string{"vlan30": "vrf-a", "vlan40": "vrf-a", "vlan10": "vrf-b", "vlan20": "vrf-b"}

		for trial := range orderingTrials {
			l := mustNewRouting(t, twoVRFOrderingConfig())
			for iface, q := range queued {
				if res := l.Originate(testNow, ifaceVRF[iface], q.addr, 17, []byte{q.marker}, true); res.Reason != routing.ReasonNeighborPending {
					t.Fatalf("trial %d: %s reason = %q, want pending", trial, iface, res.Reason)
				}
				l.Observe(testNow, routing.Advertisement{Interface: iface, Addr: q.addr, MAC: mac, HasMAC: true, Solicited: true, Override: true})
			}

			wantReleaseOrder(t, trial, advanceAndDrain(l, testNow), 4, 3, 2, 1)
		}
	})
}

// wantReleaseOrder asserts that eff's released exits carry want's payload markers in that order.
func wantReleaseOrder(t *testing.T, trial int, exits []routing.HeldFrame, want ...byte) {
	t.Helper()
	released := exitsWithCause(exits, routing.HeldReleased)
	if len(released) != len(want) {
		t.Fatalf("trial %d: released = %d frames, want %d", trial, len(released), len(want))
	}
	got := make([]byte, len(released))
	for i, hf := range released {
		got[i] = lastPayloadByte(t, hf.Frame)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("trial %d: release order by payload marker = %v, want %v", trial, got, want)
	}
}

// TestOriginatePendingResolutionKeepsHopLimit64 asserts that Originate's pending held frames keep
// hop limit 64 upon release, matching the direct non-held path.
func TestOriginatePendingResolutionKeepsHopLimit64(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})

	res := l.Originate(testNow, routing.DefaultVRF, lifecycleDstV4, 17, []byte("data"), true)
	if res.Reason != routing.ReasonNeighborPending {
		t.Fatalf("reason = %q, want pending", res.Reason)
	}

	mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x21}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: mac, HasMAC: true, Solicited: true, Override: true})

	eff := advanceAndDrain(l, testNow)
	released := exitsWithCause(eff, routing.HeldReleased)
	if len(released) != 1 {
		t.Fatalf("released = %d frames, want 1", len(released))
	}
	if released[0].Frame.Dst != mac {
		t.Fatalf("released dst = %s, want %s", released[0].Frame.Dst, mac)
	}
	hdr, _, err := ip.Decode(released[0].Frame.Payload)
	if err != nil {
		// ip.Decode itself verifies the header checksum and errors on a mismatch, so a
		// successful decode is also evidence the checksum matches.
		t.Fatalf("decode released frame payload: %v", err)
	}
	if hdr.HopLimit != 64 {
		t.Errorf("hop limit = %d, want 64", hdr.HopLimit)
	}
}

// TestObserveDoesNotOverwriteConfiguredBinding asserts that a configured binding never
// adopts an observed MAC or gains an expiry. A solicited, overriding advertisement
// leaves a configured entry unchanged, because static bindings never age out.
func TestObserveDoesNotOverwriteConfiguredBinding(t *testing.T) {
	t.Parallel()
	configuredMAC := netaddr.MAC{0x00, 0x22, 0x33, 0x44, 0x55, 0x66}
	observedMAC := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x30}

	cfg := neighborLifecycleConfig(routing.NeighborPolicy{ReachableTime: 30 * time.Second})
	vrf := cfg.VRFs[routing.DefaultVRF]
	vrf.Neighbors = []routing.Neighbor{{Interface: "vlan10", Addr: lifecycleDstV4, MAC: configuredMAC}}
	cfg.VRFs[routing.DefaultVRF] = vrf
	l := mustNewRouting(t, cfg)

	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: observedMAC, HasMAC: true, Solicited: true, Override: true})

	res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
	if res.Frame.Dst != configuredMAC {
		t.Errorf("frame dst = %s, want the unchanged configured MAC %s", res.Frame.Dst, configuredMAC)
	}
	wantState(t, res, "reachable")

	// A configured entry was never given an expiry, so aging well past the policy's
	// ReachableTime must not move it to Stale.
	l.Advance(testNow.Add(time.Hour))
	after := routeToV4(t, l, testNow.Add(time.Hour), lifecycleDstV4, []byte("data"), true)
	wantState(t, after, "reachable")
	if after.Frame.Dst != configuredMAC {
		t.Errorf("frame dst after Advance = %s, want %s", after.Frame.Dst, configuredMAC)
	}
}

// TestAgeMovesReachableToStaleAfterReachableTime asserts that Layer.Advance moves Reachable
// entries whose expiry has passed to Stale according to NeighborPolicy.ReachableTime.
func TestAgeMovesReachableToStaleAfterReachableTime(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{ReachableTime: 30 * time.Second})
	routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)

	mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x31}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: mac, HasMAC: true, Solicited: true, Override: true})

	res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
	wantState(t, res, "reachable")

	l.Advance(testNow.Add(30 * time.Second))
	after := routeToV4(t, l, testNow.Add(30*time.Second), lifecycleDstV4, []byte("data"), true)
	wantState(t, after, "stale")
	if after.Frame.Dst != mac {
		t.Errorf("frame dst = %s, want %s (a Stale entry still forwards on its cached MAC)", after.Frame.Dst, mac)
	}
}

// advanceAndDrain advances l to at and drains its hold-queue exits.
func advanceAndDrain(l *routing.Layer, at time.Time) []routing.HeldFrame {
	l.Advance(at)
	return l.DrainExits()
}

// exitsWithCause returns the exits in exits carrying cause, keeping the order they were reported
// in, so a test can name the one exit shape it is about without losing the ordering guarantee.
func exitsWithCause(exits []routing.HeldFrame, cause routing.HeldCause) []routing.HeldFrame {
	var out []routing.HeldFrame
	for _, hf := range exits {
		if hf.Cause == cause {
			out = append(out, hf)
		}
	}
	return out
}

func lastPayloadByte(t *testing.T, f ethernet.Frame) byte {
	t.Helper()
	if len(f.Payload) == 0 {
		t.Fatal("released frame has no payload")
	}
	return f.Payload[len(f.Payload)-1]
}

// TestWakeReportsPortForARoutedPortButNotAVLANInterface pins [HeldFrame.Port]: empty for a VLAN
// interface, which has no single port until the bridge picks one, and the egress port name for a
// routed-port interface, which has exactly one. The interface is named "routed1" and its Port
// "port1" so the two differ: an implementation that filled Port with the interface name instead
// of the interface's configured Port would still pass a routed-port arm named the same as its
// port.
func TestWakeReportsPortForARoutedPortButNotAVLANInterface(t *testing.T) {
	t.Parallel()

	portMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x02, 0x01}
	portDst := netip.MustParseAddr("10.0.50.99")
	portNeighborMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66}

	cfg := neighborLifecycleConfig(routing.NeighborPolicy{RetransTimer: time.Second})
	vrf := cfg.VRFs[routing.DefaultVRF]
	vrf.Interfaces["routed1"] = routing.Interface{
		Port:     "port1",
		MAC:      portMAC,
		Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")},
	}
	cfg.VRFs[routing.DefaultVRF] = vrf

	ports, err := port.NewBuilder().Add(port.Port{Name: "port1", Kind: port.Physical}).Build()
	if err != nil {
		t.Fatalf("port.NewBuilder: %v", err)
	}
	l := mustNewRoutingWithPorts(t, cfg, ports)

	portFrame := ethernet.Frame{
		Src:       lifecycleHostMAC,
		Dst:       portMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   encodeIPv4Packet(t, netip.MustParseAddr("10.0.50.7"), portDst, 64, []byte("data")),
	}
	if res := l.Route(testNow, "routed1", portFrame, true); res.Reason != routing.ReasonNeighborPending {
		t.Fatalf("reason = %q, want %q", res.Reason, routing.ReasonNeighborPending)
	}
	l.Observe(testNow, routing.Advertisement{Interface: "routed1", Addr: portDst, MAC: portNeighborMAC, HasMAC: true, Solicited: true, Override: true})
	portEff := advanceAndDrain(l, testNow)
	if len(portEff) != 1 {
		t.Fatalf("port exits = %+v, want exactly one", portEff)
	}
	if portEff[0].Port != "port1" {
		t.Errorf("Port = %q, want %q for a routed-port interface", portEff[0].Port, "port1")
	}

	routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x0a}, HasMAC: true, Solicited: true, Override: true})
	vlanEff := advanceAndDrain(l, testNow)
	if len(vlanEff) != 1 {
		t.Fatalf("vlan exits = %+v, want exactly one", vlanEff)
	}
	if vlanEff[0].Port != "" {
		t.Errorf("Port = %q, want empty for a VLAN interface", vlanEff[0].Port)
	}
}

// mustNewLifecycleLayer builds a fresh layer over [neighborLifecycleConfig], reusing
// mustNewRouting from layer_test.go.
func mustNewLifecycleLayer(t *testing.T, policy routing.NeighborPolicy) *routing.Layer {
	t.Helper()
	return mustNewRouting(t, neighborLifecycleConfig(policy))
}

func TestAdvancePastReachableExpiryAndIncompleteDeadlineLeavesStaleAndFailed(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{
		ReachableTime: 30 * time.Second,
		RetransTimer:  10 * time.Second,
	})

	reachableDst := netip.MustParseAddr("10.0.10.88")
	incompleteDst := lifecycleDstV4

	mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}
	routeToV4(t, l, testNow, reachableDst, []byte("reachable-frame"), true)
	l.Observe(testNow, routing.Advertisement{
		Interface: "vlan10",
		Addr:      reachableDst,
		MAC:       mac,
		HasMAC:    true,
		Solicited: true,
		Override:  true,
	})
	l.Advance(testNow)
	_ = l.DrainExits()

	routeToV4(t, l, testNow, incompleteDst, []byte("incomplete-frame"), true)

	tExpiry := testNow.Add(35 * time.Second)
	l.Advance(tExpiry)

	reachableRes := routeToV4(t, l, tExpiry, reachableDst, []byte("probe"), false)
	wantState(t, reachableRes, "stale")

	incompleteRes := routeToV4(t, l, tExpiry, incompleteDst, []byte("probe"), false)
	wantState(t, incompleteRes, "failed")

	clone := l.Clone()

	exits := l.DrainExits()
	if len(exits) != 1 {
		t.Fatalf("DrainExits() = %d exits, want 1", len(exits))
	}
	if exits[0].Cause != routing.HeldTimedOut {
		t.Errorf("exit cause = %q, want %q", exits[0].Cause, routing.HeldTimedOut)
	}
	if secondDrain := l.DrainExits(); len(secondDrain) != 0 {
		t.Errorf("second DrainExits() = %+v, want none", secondDrain)
	}

	cloneExits := clone.DrainExits()
	if len(cloneExits) != 1 {
		t.Fatalf("clone.DrainExits() = %d exits, want 1", len(cloneExits))
	}
	if cloneExits[0].Cause != routing.HeldTimedOut {
		t.Errorf("clone exit cause = %q, want %q", cloneExits[0].Cause, routing.HeldTimedOut)
	}
	if secondCloneDrain := clone.DrainExits(); len(secondCloneDrain) != 0 {
		t.Errorf("second clone.DrainExits() = %+v, want none", secondCloneDrain)
	}
}

func TestFailHeldReturnsWaitingExitsFirstAndClearsThem(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{
		RetransTimer: 2 * time.Second,
	})

	dst1 := netip.MustParseAddr("10.0.10.100")
	dst2 := netip.MustParseAddr("10.0.10.99")

	// Queue frame 1 for dst1.
	routeToV4(t, l, testNow, dst1, []byte{0xaa}, true)

	// Advance past dst1's resolution deadline without draining exits.
	// This parks a HeldTimedOut exit in l.exits.
	l.Advance(testNow.Add(3 * time.Second))

	// Queue frame 2 for dst2, leaving it incomplete in the hold queue.
	routeToV4(t, l, testNow.Add(3*time.Second), dst2, []byte{0xbb}, true)

	// FailHeld drains held frames across all VRFs and returns waiting exits first.
	exits := l.FailHeld()
	if len(exits) != 2 {
		t.Fatalf("FailHeld() returned %d exits, want 2", len(exits))
	}
	if got := lastPayloadByte(t, exits[0].Frame); got != 0xaa {
		t.Errorf("first exit payload marker = %#x, want 0xaa (waiting exit from Advance)", got)
	}
	if got := lastPayloadByte(t, exits[1].Frame); got != 0xbb {
		t.Errorf("second exit payload marker = %#x, want 0xbb (freshly failed held frame)", got)
	}
	if exits[0].Cause != routing.HeldTimedOut || exits[1].Cause != routing.HeldTimedOut {
		t.Errorf("causes = (%q, %q), want both %q", exits[0].Cause, exits[1].Cause, routing.HeldTimedOut)
	}

	// Subsequent DrainExits must be empty: FailHeld already consumed the waiting exit.
	if drained := l.DrainExits(); len(drained) != 0 {
		t.Errorf("DrainExits() after FailHeld returned %d exits, want 0", len(drained))
	}
}

func TestHeldTokensIdentifySelectiveRelease(t *testing.T) {
	t.Parallel()
	cfg := neighborLifecycleConfig(routing.NeighborPolicy{})
	vrf := cfg.VRFs[routing.DefaultVRF]
	vrf.Interfaces["vlan99"] = routing.Interface{VLAN: 99, MAC: lifecycleDeviceMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.99.1/24")}}
	l := mustNewRouting(t, cfg)
	firstAddr := netip.MustParseAddr("10.0.99.2")
	secondAddr := netip.MustParseAddr("10.0.99.6")
	first := routeToV4(t, l, testNow, firstAddr, []byte{1}, true)
	second := routeToV4(t, l, testNow, secondAddr, []byte{2}, true)
	if first.Hold != 1 || second.Hold != 2 {
		t.Fatalf("hold tokens = %d, %d, want 1, 2", first.Hold, second.Hold)
	}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan99", Addr: secondAddr, MAC: lifecycleHostMAC, HasMAC: true, Solicited: true, Override: true})
	exits := advanceAndDrain(l, testNow)
	if len(exits) != 1 || exits[0].Token != 2 || exits[0].Ingress != "vlan10" || exits[0].Cause != routing.HeldReleased {
		t.Fatalf("exits = %+v, want token 2 released from vlan10", exits)
	}
	_, payload, err := ip.Decode(exits[0].Frame.Payload)
	if err != nil || len(payload) != 1 || payload[0] != 2 || exits[0].Frame.Dst != lifecycleHostMAC {
		t.Fatalf("released frame = %+v, payload = %v, error = %v, want frame 2 for resolved MAC", exits[0], payload, err)
	}
	for _, entry := range l.Neighbors() {
		if entry.Addr == firstAddr && entry.HoldDepth == 1 && entry.State == routing.NeighborIncomplete {
			return
		}
	}
	t.Fatal("first neighbor has no incomplete entry holding frame 1")
}

func TestHeldTokensSurviveEvictionAndTimeout(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{HoldDepth: 1, RetransTimer: time.Second})
	first := routeToV4(t, l, testNow, lifecycleDstV4, []byte{1}, true)
	second := routeToV4(t, l, testNow, lifecycleDstV4, []byte{2}, true)
	third := l.Originate(testNow, routing.DefaultVRF, lifecycleDstV6, 17, []byte{3}, true)
	exits := advanceAndDrain(l, testNow.Add(time.Second))
	if first.Hold != 1 || second.Hold != 2 || third.Hold != 3 || len(exits) != 3 {
		t.Fatalf("tokens = %d, %d, %d, exits = %+v, want tokens 1..3 and three exits", first.Hold, second.Hold, third.Hold, exits)
	}
	for i, want := range []struct {
		token   routing.HoldToken
		ingress string
		cause   routing.HeldCause
	}{
		{1, "vlan10", routing.HeldEvicted}, {2, "vlan10", routing.HeldTimedOut}, {3, "", routing.HeldTimedOut},
	} {
		if got := exits[i]; got.Token != want.token || got.Ingress != want.ingress || got.Cause != want.cause {
			t.Errorf("exit %d = %+v, want %+v", i, got, want)
		}
	}
	fourth := routeToV4(t, l, testNow.Add(time.Second), netip.MustParseAddr("10.0.10.98"), []byte{4}, true)
	if fourth.Hold != 4 {
		t.Errorf("token after exits = %d, want 4", fourth.Hold)
	}
	failed := l.FailHeld()
	if len(failed) != 1 || failed[0].Token != 4 || failed[0].Ingress != "vlan10" || failed[0].Cause != routing.HeldTimedOut {
		t.Errorf("FailHeld = %+v, want token 4 timed out from vlan10", failed)
	}
}

func TestClonePreservesHoldSequence(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
	first := routeToV4(t, l, testNow, lifecycleDstV4, []byte{1}, true)
	clone := l.Clone()
	for _, fork := range []*routing.Layer{l, clone} {
		second := routeToV4(t, fork, testNow, lifecycleDstV4, []byte{2}, true)
		if first.Hold != 1 || second.Hold != 2 {
			t.Fatalf("tokens = %d, %d, want 1, 2", first.Hold, second.Hold)
		}
		exits := fork.FailHeld()
		if len(exits) != 2 || exits[0].Token != 1 || exits[1].Token != 2 {
			t.Fatalf("fork exits = %+v, want tokens 1 and 2", exits)
		}
	}
}

func TestClonePreservesIncompleteWake(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{RetransTimer: 2 * time.Second})
	routeToV4(t, l, testNow, lifecycleDstV4, []byte{1}, true)
	clone := l.Clone()
	wake, ok := l.NextWake()
	cloneWake, cloneOK := clone.NextWake()
	if !ok || !cloneOK || wake != testNow.Add(2*time.Second) || cloneWake != wake {
		t.Fatalf("wake = %v, %t, clone wake = %v, %t, want both at +2s", wake, ok, cloneWake, cloneOK)
	}
}

func TestRoutingResultOutcome(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		reason  trace.Reason
		outcome trace.Outcome
	}{
		{"forwarded", "", trace.Forwarded},
		{"consumed", routing.ReasonNotRouted, trace.Consumed},
		{"held", routing.ReasonNeighborPending, trace.Held},
		{"dropped", routing.ReasonNoRoute, trace.Dropped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := routing.Result{Reason: tc.reason}
			if res.Outcome() != tc.outcome || res.Status() != analysis.Complete {
				t.Errorf("outcome = %v, status = %v, want %v, Complete", res.Outcome(), res.Status(), tc.outcome)
			}
		})
	}
}

func TestRoutingResultMetadata(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
	cfg := standardSwitchConfig()
	vrf := cfg.VRFs[routing.DefaultVRF]
	vrf.Routes = []routing.Route{
		{Prefix: netip.MustParsePrefix("192.0.2.0/24"), NextHop: netip.MustParseAddr("198.51.100.1")},
		{Prefix: netip.MustParsePrefix("198.51.100.0/24"), NextHop: netip.MustParseAddr("10.0.20.7")},
	}
	cfg.VRFs[routing.DefaultVRF] = vrf
	forwarding := mustNewRouting(t, cfg)
	disabled := mustNewLifecycleLayer(t, routing.NeighborPolicy{Mode: routing.NeighborDisabled})
	for _, tc := range []struct {
		name    string
		result  routing.Result
		vrf     string
		nextHop netip.Addr
	}{
		{"bad header", l.Route(testNow, "vlan10", ethernet.Frame{}, false), routing.DefaultVRF, netip.Addr{}},
		{"local", routeToV4(t, l, testNow, netip.MustParseAddr("10.0.10.1"), nil, false), routing.DefaultVRF, netip.Addr{}},
		{"no route", routeToV4(t, l, testNow, netip.MustParseAddr("192.0.2.1"), nil, false), routing.DefaultVRF, netip.Addr{}},
		{"pending", routeToV4(t, l, testNow, lifecycleDstV4, nil, false), routing.DefaultVRF, lifecycleDstV4},
		{"originated", l.Originate(testNow, routing.DefaultVRF, lifecycleDstV6, 17, nil, false), routing.DefaultVRF, lifecycleDstV6},
		{"unknown interface", l.Route(testNow, "unknown", ethernet.Frame{}, false), "", netip.Addr{}},
		{"unknown VRF", l.Originate(testNow, "tenant", lifecycleDstV4, 17, nil, false), "tenant", netip.Addr{}},
		{"forwarded", routeToV4(t, forwarding, testNow, netip.MustParseAddr("10.0.20.7"), nil, true), routing.DefaultVRF, netip.MustParseAddr("10.0.20.7")},
		{"recursive", routeToV4(t, forwarding, testNow, netip.MustParseAddr("192.0.2.9"), nil, true), routing.DefaultVRF, netip.MustParseAddr("10.0.20.7")},
		{"recursive originated", forwarding.Originate(testNow, routing.DefaultVRF, netip.MustParseAddr("192.0.2.9"), 17, nil, true), routing.DefaultVRF, netip.MustParseAddr("10.0.20.7")},
		{"neighbor miss", routeToV4(t, disabled, testNow, lifecycleDstV4, nil, true), routing.DefaultVRF, lifecycleDstV4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.result.VRF != tc.vrf || tc.result.NextHop != tc.nextHop {
				t.Errorf("VRF = %q, next hop = %v, want %q, %v", tc.result.VRF, tc.result.NextHop, tc.vrf, tc.nextHop)
			}
			if tc.result.Hold != 0 {
				t.Errorf("hold = %d, want zero for a preview or unqueued result", tc.result.Hold)
			}
		})
	}
}

func TestRoutePeekDoesNotMintHold(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
	for range 2 {
		peek := routeToV4(t, l, testNow, lifecycleDstV4, nil, false)
		if peek.Hold != 0 || peek.Outcome() != trace.Held || len(l.Neighbors()) != 0 {
			t.Fatalf("peek = %+v, neighbors = %+v, want Held with zero token and no entry", peek, l.Neighbors())
		}
	}
	committed := routeToV4(t, l, testNow, lifecycleDstV4, nil, true)
	if committed.Hold != 1 {
		t.Errorf("first committed hold = %d, want 1", committed.Hold)
	}
}
