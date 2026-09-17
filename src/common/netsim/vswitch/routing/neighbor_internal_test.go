package routing

import (
	"net/netip"
	"testing"
	"time"
)

// TestResolveNeighborStoredZeroStateIsAMiss is finding 7: [NeighborState]'s zero value,
// [NeighborUnobserved], is meaningful only as a lookup answer, but [neighborEntry]{} is a legal
// Go zero value too. No exported path stores one — this reaches into the package to reproduce it
// directly and pins that [vrfState.resolveNeighbor] treats a stored zero state as a miss rather
// than silently starting to hold frames for a neighbor nothing ever looked up.
func TestResolveNeighborStoredZeroStateIsAMiss(t *testing.T) {
	t.Parallel()

	vs := &vrfState{
		neighbors: map[neighborKey]*neighborEntry{},
		policy:    NeighborPolicy{}.normalize(),
	}
	key := neighborKey{iface: "vlan10", addr: netip.MustParseAddr("10.0.10.9")}
	vs.neighbors[key] = &neighborEntry{}

	calls := 0
	lookup := vs.resolveNeighbor(time.Now(), key, true, func() heldEntry {
		calls++
		return heldEntry{}
	})

	if lookup.state != NeighborUnobserved {
		t.Errorf("state = %q, want %q", lookup.state, NeighborUnobserved)
	}
	if lookup.ok {
		t.Error("ok = true, want false for a stored zero-state entry")
	}
	if calls != 0 {
		t.Errorf("held() called %d times, want 0: a zero-state entry must not start holding frames", calls)
	}
	if len(vs.neighbors[key].queue) != 0 {
		t.Errorf("queue length = %d, want 0", len(vs.neighbors[key].queue))
	}
}
