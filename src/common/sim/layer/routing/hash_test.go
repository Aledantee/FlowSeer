package routing

import (
	"fmt"
	"net/netip"
	"testing"
)

// moduloIndex is the reduction this package refuses: RFC 2992 measures it as the most
// disruptive of the algorithms, at (N-1)/N of the flows moving when a next hop goes. The
// tests below keep it so the difference from [hashThreshold] is asserted rather than
// claimed.
func moduloIndex(hash uint32, n int) int {
	return int(hash % uint32(n))
}

// spreadHashes returns the hashes of a spread of IPv4 flows, wide enough that every region
// of a small candidate set holds some of them.
func spreadHashes() []uint32 {
	var out []uint32
	for host := 1; host <= 40; host++ {
		for dst := 1; dst <= 10; dst++ {
			src := netip.MustParseAddr(fmt.Sprintf("10.0.10.%d", host))
			dest := netip.MustParseAddr(fmt.Sprintf("10.200.0.%d", dst))
			out = append(out, flowHash(src, dest, 0))
		}
	}
	return out
}

func TestFlowHashCoversLayer3Only(t *testing.T) {
	t.Parallel()

	v6Src := netip.MustParseAddr("2001:db8:10::7")
	v6Dst := netip.MustParseAddr("2001:db8:200::1")
	if flowHash(v6Src, v6Dst, 0) == flowHash(v6Src, v6Dst, 1) {
		t.Error("the IPv6 flow label does not reach the hash")
	}

	v4Src := netip.MustParseAddr("10.0.10.7")
	v4Dst := netip.MustParseAddr("10.200.0.1")
	if flowHash(v4Src, v4Dst, 0) != flowHash(v4Src, v4Dst, 9) {
		t.Error("an IPv4 flow hashes over a flow label it has no field for")
	}
	if flowHash(v4Src, v4Dst, 0) == flowHash(v4Dst, v4Src, 0) {
		t.Error("the hash is symmetric in source and destination")
	}
}

func TestHashThresholdRegionsAreEqualAndContiguous(t *testing.T) {
	t.Parallel()

	for _, n := range []int{1, 2, 3, 5, 64} {
		width := uint64(1<<32) / uint64(n)
		for i := range n {
			first := uint32(uint64(i) * width)
			last := uint32(uint64(i+1)*width - 1)
			if i == n-1 {
				last = ^uint32(0)
			}
			if got := hashThreshold(first, n); got != i {
				t.Errorf("hashThreshold(%d, %d) = %d, want %d at the region's first hash", first, n, got, i)
			}
			if got := hashThreshold(last, n); got != i {
				t.Errorf("hashThreshold(%d, %d) = %d, want %d at the region's last hash", last, n, got, i)
			}
		}
	}
}

// TestHashThresholdKeepsMostFlowsWhenACandidateLeaves is the reason the reduction is not
// modulo-N. RFC 2992 puts hash-threshold's disruption between 1/4 and 1/2 of the flows, so
// not every flow stays; what holds exactly is that a flow only ever slides down into the
// region below it, which leaves the whole first region untouched.
func TestHashThresholdKeepsMostFlowsWhenACandidateLeaves(t *testing.T) {
	t.Parallel()

	hashes := spreadHashes()
	var onFirst, stayedOnSecond int
	for _, h := range hashes {
		before, after := hashThreshold(h, 3), hashThreshold(h, 2)
		if after > before || before-after > 1 {
			t.Fatalf("hash %d moved from candidate %d to %d, want at most one place down", h, before, after)
		}
		switch {
		case before == 0:
			onFirst++
			if after != 0 {
				t.Errorf("hash %d left the first candidate when the third was removed", h)
			}
		case before == 1 && after == 1:
			stayedOnSecond++
		}
	}
	if onFirst == 0 || stayedOnSecond == 0 {
		t.Fatalf("flows on the first candidate = %d, flows held by the second = %d; the spread proves nothing", onFirst, stayedOnSecond)
	}

	var moduloMoved int
	for _, h := range hashes {
		if moduloIndex(h, 3) == 0 && moduloIndex(h, 2) != 0 {
			moduloMoved++
		}
	}
	if moduloMoved == 0 {
		t.Fatal("no flow on the first candidate moves under modulo-N, so this spread does not tell the two reductions apart")
	}
}

func TestSelectRouteIsAPureFunctionOfTheFlow(t *testing.T) {
	t.Parallel()

	candidates := []routeEntry{
		{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: netip.MustParseAddr("10.0.10.254"), Interface: "vlan10"},
		{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: netip.MustParseAddr("10.0.20.254"), Interface: "vlan20"},
		{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: netip.MustParseAddr("10.0.30.254"), Interface: "vlan30"},
	}
	src := netip.MustParseAddr("10.0.10.7")
	dst := netip.MustParseAddr("10.200.0.1")

	first := selectRoute(candidates, src, dst, 0)
	for range 10 {
		again := selectRoute(candidates, src, dst, 0)
		if again.chosen != first.chosen || again.hash != first.hash {
			t.Fatalf("selection = %d at hash %d, want %d at hash %d", again.chosen, again.hash, first.chosen, first.hash)
		}
	}
	if selectRoute(nil, src, dst, 0) != nil {
		t.Error("an empty candidate set selected a route")
	}
}
