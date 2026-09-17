package vswitch_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
)

// TestCompareAgreesOverConsecutiveCallsOnUnresolvedDestination covers R20c
// across two switches: [vswitch.Compare] runs [Switch.Peek] on both sides, and
// a pending destination must not perturb either one. Two consecutive Compare
// calls over the same pair of switches and the same unresolved destination
// must therefore return equal results; if a peek queued a frame or created an
// entry, the second call would see a table the first left different from the
// one it found.
func TestCompareAgreesOverConsecutiveCallsOnUnresolvedDestination(t *testing.T) {
	a := buildBaseRoutingSwitch(t)
	b := buildBaseRoutingSwitch(t)

	dst := netip.MustParseAddr("10.0.20.77")
	pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}

	first := vswitch.Compare(a, b, fixedTime, "1/1/1", frame)
	if !first.Same {
		t.Fatalf("first compare: current = %+v, expected = %+v, want equal", first.Current.Result, first.Expected.Result)
	}

	second := vswitch.Compare(a, b, fixedTime, "1/1/1", frame)
	if !second.Same {
		t.Fatalf("second compare: current = %+v, expected = %+v, want equal", second.Current.Result, second.Expected.Result)
	}
	if second.Current.Outcome != first.Current.Outcome || second.Current.Reason != first.Current.Reason {
		t.Errorf("second Compare disagreed with first: %v/%v vs %v/%v",
			second.Current.Outcome, second.Current.Reason, first.Current.Outcome, first.Current.Reason)
	}

	// Same only compares outcome, reason, FID and egress, so it would agree
	// across two calls even if mutate were not threaded through to Peek: it
	// takes no position on whether either switch's state changed. NextWake
	// does: a peek that queued a frame or created an Incomplete entry would
	// leave a resolution timer behind, so its absence here is what actually
	// proves neither switch was mutated.
	if _, ok := a.NextWake(); ok {
		t.Errorf("a.NextWake() reported a timer after two Compare calls, want none: Peek must not have queued a frame")
	}
	if _, ok := b.NextWake(); ok {
		t.Errorf("b.NextWake() reported a timer after two Compare calls, want none: Peek must not have queued a frame")
	}
}
