package routing_test

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

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

// TestRouteMissCreatesIncompleteEntryAndQueuesTheFrame is R20a's first acceptance example: a
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
// rather than dropped without a trace (finding 3).
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

	eff := l.Wake(testNow)
	if len(eff.Failed) != 1 {
		t.Fatalf("failed = %+v, want 1 (frame 1, the one appendHeld evicted)", eff.Failed)
	}
	if got := lastPayloadByte(t, eff.Failed[0].Frame); got != 1 {
		t.Errorf("failed[0] payload marker = %d, want 1", got)
	}
	if len(eff.Released) != 3 {
		t.Fatalf("released = %d frames, want 3", len(eff.Released))
	}
	for i, want := range []byte{2, 3, 4} {
		got := lastPayloadByte(t, eff.Released[i].Frame)
		if got != want {
			t.Errorf("released[%d] payload marker = %d, want %d (arrival order after dropping the oldest)", i, got, want)
		}
		if eff.Released[i].Frame.Dst != mac {
			t.Errorf("released[%d] dst = %s, want %s", i, eff.Released[i].Frame.Dst, mac)
		}
		if eff.Released[i].Interface != "vlan10" {
			t.Errorf("released[%d] interface = %q, want vlan10", i, eff.Released[i].Interface)
		}
	}
}

// TestWakeFailsAnIncompleteEntryAfterResolutionTimeout is R21b's timeout half: nothing observed
// within ResolutionTimeout moves the entry to Failed and reports its held frames as failed
// rather than discarding them silently.
func TestWakeFailsAnIncompleteEntryAfterResolutionTimeout(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{ResolutionTimeout: 3 * time.Second})

	res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("held"), true)
	if res.Reason != routing.ReasonNeighborPending {
		t.Fatalf("reason = %q, want pending", res.Reason)
	}

	// Before the deadline, nothing happens.
	eff := l.Wake(testNow.Add(2 * time.Second))
	if len(eff.Failed) != 0 || len(eff.Released) != 0 {
		t.Fatalf("effects before the deadline = %+v, want none", eff)
	}

	eff = l.Wake(testNow.Add(3 * time.Second))
	if len(eff.Released) != 0 {
		t.Fatalf("released = %+v, want none", eff.Released)
	}
	if len(eff.Failed) != 1 {
		t.Fatalf("failed = %d frames, want 1", len(eff.Failed))
	}
	if eff.Failed[0].Interface != "vlan10" {
		t.Errorf("failed interface = %q, want vlan10", eff.Failed[0].Interface)
	}

	after := routeToV4(t, l, testNow.Add(3*time.Second), lifecycleDstV4, []byte("data"), true)
	if after.Reason != routing.ReasonNeighborMiss {
		t.Fatalf("reason after timeout = %q, want %q", after.Reason, routing.ReasonNeighborMiss)
	}
	wantState(t, after, "failed")
}

// TestStaleEntryForwardsWithoutChangingState is the package README's rule: netsim forwards a
// Stale entry's cached address without probing, and Stale stays Stale.
func TestStaleEntryForwardsWithoutChangingState(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})
	routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)

	mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x0c}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: mac, HasMAC: true, Solicited: false, Override: true})

	for i := range 3 {
		res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
		if res.Reason != "" {
			t.Fatalf("attempt %d reason = %q, want empty", i, res.Reason)
		}
		if res.Frame.Dst != mac {
			t.Errorf("attempt %d dst = %s, want %s", i, res.Frame.Dst, mac)
		}
		wantState(t, res, "stale")
	}
}

// TestFailedEntryResolvesAgainOnLaterObservation covers the hop the package README's
// "Neighbor lifecycle" section and layer_test.go's switch-facing tests both rely on: a Failed
// entry is not a dead end, because a later advertisement can still move it to Reachable.
func TestFailedEntryResolvesAgainOnLaterObservation(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{ResolutionTimeout: time.Second})

	routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
	l.Wake(testNow.Add(time.Second))
	if res := routeToV4(t, l, testNow.Add(time.Second), lifecycleDstV4, []byte("data"), true); res.Reason != routing.ReasonNeighborMiss {
		t.Fatalf("reason after timeout = %q, want %q", res.Reason, routing.ReasonNeighborMiss)
	}

	mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x0d}
	l.Observe(testNow.Add(time.Second), routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: mac, HasMAC: true, Solicited: true, Override: true})

	res := routeToV4(t, l, testNow.Add(time.Second), lifecycleDstV4, []byte("data"), true)
	if res.Reason != "" {
		t.Fatalf("reason = %q, want empty", res.Reason)
	}
	if res.Frame.Dst != mac {
		t.Errorf("dst = %s, want %s", res.Frame.Dst, mac)
	}
	wantState(t, res, "reachable")
}

// TestCloneIsolatesNeighborState is evidence Clone copies the runtime table rather than sharing
// it: waking the original after the clone must not release or fail the clone's held frame, and
// observing on the clone must not resolve the original's entry.
func TestCloneIsolatesNeighborState(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{ResolutionTimeout: time.Second})
	routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)

	clone := l.Clone()

	mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x0e}
	clone.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: mac, HasMAC: true, Solicited: true, Override: true})

	// A peek (commit false) so checking the original's state does not itself queue a second
	// frame on it.
	if res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), false); res.Reason != routing.ReasonNeighborPending {
		t.Errorf("original reason = %q after observing the clone, want still pending", res.Reason)
	}

	eff := l.Wake(testNow.Add(time.Second))
	if len(eff.Failed) != 1 {
		t.Fatalf("original failed = %d, want 1 (unaffected by the clone's Observe)", len(eff.Failed))
	}

	cloneRes := routeToV4(t, clone, testNow, lifecycleDstV4, []byte("data"), true)
	if cloneRes.Reason != "" || cloneRes.Frame.Dst != mac {
		t.Errorf("clone reason = %q, dst = %s, want resolved to %s", cloneRes.Reason, cloneRes.Frame.Dst, mac)
	}
}

// TestWakeReleasesInDeterministicOrder is finding 1: Wake ranges l.vrfs and a VRF's neighbors,
// both Go maps, so without a sort the release order varies run to run. Two next hops on one VRF,
// each holding one frame and resolved together, must release in (iface, addr) order every time.
func TestWakeReleasesInDeterministicOrder(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})

	addr98 := netip.MustParseAddr("10.0.10.98")
	addr99 := netip.MustParseAddr("10.0.10.99")

	if res := routeToV4(t, l, testNow, addr98, []byte{98}, true); res.Reason != routing.ReasonNeighborPending {
		t.Fatalf("addr98 reason = %q, want pending", res.Reason)
	}
	if res := routeToV4(t, l, testNow, addr99, []byte{99}, true); res.Reason != routing.ReasonNeighborPending {
		t.Fatalf("addr99 reason = %q, want pending", res.Reason)
	}

	mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x20}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: addr98, MAC: mac, HasMAC: true, Solicited: true, Override: true})
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: addr99, MAC: mac, HasMAC: true, Solicited: true, Override: true})

	eff := l.Wake(testNow)
	if len(eff.Released) != 2 {
		t.Fatalf("released = %d frames, want 2", len(eff.Released))
	}
	if got := lastPayloadByte(t, eff.Released[0].Frame); got != 98 {
		t.Errorf("released[0] payload marker = %d, want 98 (10.0.10.98 sorts before 10.0.10.99)", got)
	}
	if got := lastPayloadByte(t, eff.Released[1].Frame); got != 99 {
		t.Errorf("released[1] payload marker = %d, want 99", got)
	}
}

// TestDiscardHeldThenWakePastDeadlineFailsTheEntry is finding 2: the queue-emptiness guard in
// Wake used to sit above the Incomplete-expiry check, so an entry DiscardHeld emptied never
// reached Failed and NextWake kept naming a deadline that would never arrive. DiscardHeld and
// NextWake had no test in this package before this one.
func TestDiscardHeldThenWakePastDeadlineFailsTheEntry(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{ResolutionTimeout: time.Second})

	res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
	if res.Reason != routing.ReasonNeighborPending {
		t.Fatalf("reason = %q, want pending", res.Reason)
	}
	if _, ok := l.NextWake(); !ok {
		t.Fatal("NextWake reports no timer before DiscardHeld, want one")
	}

	l.DiscardHeld()

	// DiscardHeld leaves state and expiry alone: the entry is still Incomplete, still pending.
	after := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), false)
	if after.Reason != routing.ReasonNeighborPending {
		t.Fatalf("reason after DiscardHeld = %q, want still pending", after.Reason)
	}

	eff := l.Wake(testNow.Add(time.Second))
	if len(eff.Failed) != 0 || len(eff.Released) != 0 {
		t.Fatalf("effects = %+v, want none: DiscardHeld left no frames to report", eff)
	}
	if _, ok := l.NextWake(); ok {
		t.Error("NextWake still reports a timer once the entry failed, want none")
	}

	final := routeToV4(t, l, testNow.Add(time.Second), lifecycleDstV4, []byte("data"), true)
	if final.Reason != routing.ReasonNeighborMiss {
		t.Fatalf("reason = %q, want %q (the entry reached Failed on its own)", final.Reason, routing.ReasonNeighborMiss)
	}
	wantState(t, final, "failed")
}

// TestOriginatePendingResolutionKeepsHopLimit64 is finding 4: finishHeld used to decrement the
// hop limit of every held frame, but Originate's direct (non-held) path never decrements, so a
// datagram queued while its neighbor resolved left one hop limit lower than an identical one that
// resolved immediately. Originate's doc comment promises hop limit 64.
func TestOriginatePendingResolutionKeepsHopLimit64(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{})

	res := l.Originate(testNow, routing.DefaultVRF, lifecycleDstV4, 17, []byte("data"), true)
	if res.Reason != routing.ReasonNeighborPending {
		t.Fatalf("reason = %q, want pending", res.Reason)
	}

	mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x21}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: mac, HasMAC: true, Solicited: true, Override: true})

	eff := l.Wake(testNow)
	if len(eff.Released) != 1 {
		t.Fatalf("released = %d frames, want 1", len(eff.Released))
	}
	if eff.Released[0].Frame.Dst != mac {
		t.Fatalf("released dst = %s, want %s", eff.Released[0].Frame.Dst, mac)
	}
	hdr, _, err := ip.Decode(eff.Released[0].Frame.Payload)
	if err != nil {
		// ip.Decode itself verifies the header checksum and errors on a mismatch, so a
		// successful decode is also evidence the checksum matches.
		t.Fatalf("decode released frame payload: %v", err)
	}
	if hdr.HopLimit != 64 {
		t.Errorf("hop limit = %d, want 64", hdr.HopLimit)
	}
}

// TestObserveDoesNotOverwriteConfiguredBinding is finding 6: a configured binding must never
// adopt an observed MAC or gain an expiry. Without the origin check, a solicited, overriding
// advertisement moves a configured entry to Reachable with an expiry, and aging past it demotes
// the binding to Stale even though config.go documents that a static binding never ages out.
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
	l.Age(testNow.Add(time.Hour))
	after := routeToV4(t, l, testNow.Add(time.Hour), lifecycleDstV4, []byte("data"), true)
	wantState(t, after, "reachable")
	if after.Frame.Dst != configuredMAC {
		t.Errorf("frame dst after Age = %s, want %s", after.Frame.Dst, configuredMAC)
	}
}

// TestAgeMovesReachableToStaleAfterReachableTime is Layer.Age's first behavioral test in this
// package, and so also the first test that exercises NeighborPolicy.ReachableTime: deleting the
// expiry assignment from both Observe branches leaves every existing test passing.
func TestAgeMovesReachableToStaleAfterReachableTime(t *testing.T) {
	t.Parallel()
	l := mustNewLifecycleLayer(t, routing.NeighborPolicy{ReachableTime: 30 * time.Second})
	routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)

	mac := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x31}
	l.Observe(testNow, routing.Advertisement{Interface: "vlan10", Addr: lifecycleDstV4, MAC: mac, HasMAC: true, Solicited: true, Override: true})

	res := routeToV4(t, l, testNow, lifecycleDstV4, []byte("data"), true)
	wantState(t, res, "reachable")

	l.Age(testNow.Add(30 * time.Second))
	after := routeToV4(t, l, testNow.Add(30*time.Second), lifecycleDstV4, []byte("data"), true)
	wantState(t, after, "stale")
	if after.Frame.Dst != mac {
		t.Errorf("frame dst = %s, want %s (a Stale entry still forwards on its cached MAC)", after.Frame.Dst, mac)
	}
}

func lastPayloadByte(t *testing.T, f ethernet.Frame) byte {
	t.Helper()
	if len(f.Payload) == 0 {
		t.Fatal("released frame has no payload")
	}
	return f.Payload[len(f.Payload)-1]
}

// mustNewLifecycleLayer builds a fresh layer over [neighborLifecycleConfig], reusing
// mustNewRouting from layer_test.go.
func mustNewLifecycleLayer(t *testing.T, policy routing.NeighborPolicy) *routing.Layer {
	t.Helper()
	return mustNewRouting(t, neighborLifecycleConfig(policy))
}
