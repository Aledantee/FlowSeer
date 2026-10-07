package stp

import (
	"fmt"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

// TestRecentRootCutIsIndependentOfAdvance covers a port that has already left
// the Root role: p1 leaves it before the Advance under test, so both runs
// share one deadline. It does not cover the call a port leaves the role in,
// which depends on Advance when the Root port's information has aged.
func TestRecentRootCutIsIndependentOfAdvance(t *testing.T) {
	t.Parallel()
	const fixedDelay = 15 * time.Second
	for _, mode := range []string{"CIST", "MSTI", "PVST"} {
		for _, nextDelay := range []time.Duration{4 * time.Second, fixedDelay, 30 * time.Second} {
			for _, fresh := range []bool{false, true} {
				for _, elapsed := range []time.Duration{fixedDelay - time.Second, fixedDelay + time.Second} {
					t.Run(fmt.Sprintf("%s/next=%s/fresh=%v/elapsed=%s", mode, nextDelay, fresh, elapsed), func(t *testing.T) {
						t.Parallel()
						var region *MST
						var pvst *PVST
						switch mode {
						case "MSTI":
							region = &MST{Name: "region", Instances: map[bpdu.MSTID]Instance{1: {VLANs: []vlan.ID{10}}}}
						case "PVST":
							pvst = &PVST{Trees: map[vlan.ID]Tree{1: {}, 10: {}}}
						}
						id := cistID
						if region != nil {
							id = treeID(1)
						} else if pvst != nil {
							id = treeID(10)
						}
						receive := func(l *Layer, at time.Time, port string, priority uint16, delay time.Duration) {
							b := rerootBPDU(priority, 0)
							if region != nil {
								b = syncMSTBPDU(region.ConfigID(), syncMAC(2), 0, 0, bpdu.RoleDesignated, syncMSTFlags{})
								b.RootID.Priority = priority
								b.RegionalRootID.Priority = priority
								b.MSTIs[0].RegionalRootID.Priority = priority | 1
							}
							b.ForwardDelay = delay
							if pvst != nil {
								b.RootID.Priority |= 10
								if _, outcome := l.ReceiveSSTP(at, port, syncSSTP(10), b); outcome != SSTPApplied {
									t.Fatalf("ReceiveSSTP = %v, want applied", outcome)
								}
							} else {
								l.Receive(at, port, b)
							}
						}
						var states [2]State
						for i := range states {
							l, now := rerootBridge(region, pvst)
							receive(l, now, "p1", 4096, 30*time.Second)
							for range 60 {
								now = now.Add(time.Second)
								receive(l, now, "p1", 4096, 30*time.Second)
								l.Advance(now)
							}
							left := now.Add(time.Second)
							l.LinkChange(left, "p3", false, true, 0)
							l.LinkChange(left, "p3", true, true, 1_000_000_000)
							rerootState(t, l.trees[id], "p2", bpdu.RoleDesignated, StateForwarding)
							receive(l, left, "p2", 2048, fixedDelay)
							rerootState(t, l.trees[id], "p1", bpdu.RoleDesignated, StateForwarding)
							rerootState(t, l.trees[id], "p2", bpdu.RoleRoot, StateForwarding)
							_, _, delay := l.times(l.trees[id])
							if delay != fixedDelay {
								t.Fatalf("delay at end of departure = %v, want %v", delay, fixedDelay)
							}
							at := left.Add(elapsed)
							receive(l, left.Add(time.Second), "p2", 2048, 30*time.Second)
							if fresh {
								receive(l, at.Add(-time.Second), "p2", 2048, 30*time.Second)
							}
							p := l.trees[id].ports["p2"]
							if got := p.rcvTime.Add(3 * p.rcvHelloTime).After(at); got != fresh {
								t.Fatalf("root information fresh = %v, want %v", got, fresh)
							}
							if i == 1 {
								l.Advance(at)
							}
							if l.trees[id].ports["p3"].state == StateForwarding {
								t.Fatal("next Root already forwards before reroot")
							}
							receive(l, at, "p3", 0, nextDelay)
							rerootState(t, l.trees[id], "p3", bpdu.RoleRoot, StateForwarding)
							states[i] = l.trees[id].ports["p1"].state
						}
						want := StateForwarding
						if elapsed < fixedDelay {
							want = StateDiscarding
						}
						if states[0] != states[1] || states[0] != want {
							t.Errorf("former Root states without/with Advance = %v/%v, want %v from fixed deadline", states[0], states[1], want)
						}
					})
				}
			}
		}
	}
}
