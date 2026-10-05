package stp

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// runningTrees lists, in treeOrder, the trees whose timer on the named port
// runs at now.
func runningTrees(l *Layer, port string, now time.Time) []treeID {
	var out []treeID
	for _, id := range l.treeOrder {
		if p, ok := l.trees[id].ports[port]; ok && p.tcWhile.After(now) {
			out = append(out, id)
		}
	}

	return out
}

// TestReceivedTopologyChangeStartsTheTimersTheStandardNames pins which trees a
// received change reaches on the next active port (IEEE Std 802.1Q-2003
// 13.26.19): a TCN counts for the CIST and every MSTI, a flag from outside
// the region for every tree, and a flag from inside for the trees that set it.
func TestReceivedTopologyChangeStartsTheTimersTheStandardNames(t *testing.T) {
	t.Parallel()

	foreign := func() bpdu.BPDU {
		other := MST{Name: "region-1", Revision: 2}
		id := other.ConfigID()
		root := bpdu.BridgeID{Priority: 1024}
		b := bpdu.BPDU{
			Version:        3,
			Type:           bpdu.TypeRapid,
			RootID:         root,
			BridgeID:       root,
			PortID:         0x8001,
			HelloTime:      2 * time.Second,
			MaxAge:         20 * time.Second,
			ForwardDelay:   15 * time.Second,
			ConfigID:       &id,
			RegionalRootID: root,
			RemainingHops:  20,
		}
		b.SetTopologyChange(true)

		return b
	}
	// inside is A's next hello, with the flags the row asks for.
	inside := func(cist bool, msti ...bpdu.MSTID) func(*testing.T, *Layer, time.Time) bpdu.BPDU {
		return func(t *testing.T, a *Layer, now time.Time) bpdu.BPDU {
			t.Helper()

			fx := a.Advance(now)
			if len(fx.Emissions) == 0 {
				t.Fatal("A sent no hello")
			}
			b, err := bpdu.Decode(fx.Emissions[0].Frame)
			if err != nil {
				t.Fatalf("decode A's hello: %v", err)
			}
			b.SetTopologyChange(cist)
			for i := range b.MSTIs {
				flags := bpdu.BPDU{Flags: b.MSTIs[i].Flags}
				flags.SetTopologyChange(false)
				for _, id := range msti {
					if b.MSTIs[i].MSTID == id {
						flags.SetTopologyChange(true)
					}
				}
				b.MSTIs[i].Flags = flags.Flags
			}

			return b
		}
	}

	for _, tc := range []struct {
		name string
		msg  func(t *testing.T, a *Layer, now time.Time) bpdu.BPDU
		// onP2 and onP1 list the trees with a running timer on the other
		// active port and on the arrival port. A notification also starts the
		// arrival port's CIST timer (Figure 13-19 NOTIFIED_TCN).
		onP2, onP1 []treeID
	}{
		{"TCN", func(*testing.T, *Layer, time.Time) bpdu.BPDU {
			return bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification}
		}, []treeID{0, 1, 2}, []treeID{0}},
		{"flag from outside the region", func(*testing.T, *Layer, time.Time) bpdu.BPDU { return foreign() }, []treeID{0, 1, 2}, nil},
		{"flag inside the region on the CIST only", inside(true), []treeID{0}, nil},
		{"flag inside the region on one instance only", inside(false, 2), []treeID{2}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, b, now := settledRegion(t)
			now = now.Add(2 * time.Second)
			b.Receive(now, "p1", tc.msg(t, a, now))

			if got := runningTrees(b, "p2", now); !slices.Equal(got, tc.onP2) {
				t.Errorf("trees with a running timer on p2 = %v, want %v", got, tc.onP2)
			}
			if got := runningTrees(b, "p1", now); !slices.Equal(got, tc.onP1) {
				t.Errorf("trees with a running timer on the arrival port p1 = %v, want %v", got, tc.onP1)
			}
		})
	}
}

// TestPortThatBecomesAnEdgeLeavesTheActiveTopology pins that an active port
// that turns into an edge port is flushed, stops its timer, and raises no
// change (Figure 13-19, operEdge).
func TestPortThatBecomesAnEdgeLeavesTheActiveTopology(t *testing.T) {
	t.Parallel()

	_, b, now := settledRegion(t)
	cist := b.cist()
	p := cist.ports["p2"]
	if !p.tcActive {
		t.Fatal("p2 is not active before it becomes an edge port")
	}
	p.tcWhile = now.Add(10 * time.Second)
	before := cist.topologyChangeCount

	b.link("p2").edge = true
	var flushes []layer.FlushTarget
	b.settleTopology(cist, now, &flushes)

	if p.tcActive || !p.tcWhile.IsZero() {
		t.Errorf("p2 active = %v, timer = %v, want inactive with no timer", p.tcActive, p.tcWhile)
	}
	if len(flushes) != 1 || flushes[0].Port != "p2" {
		t.Errorf("flushes = %v, want p2 alone", flushes)
	}
	if cist.topologyChangeCount != before {
		t.Errorf("topology changes = %d, want %d", cist.topologyChangeCount, before)
	}
}

// TestRunningTopologyChangeTimerIsNotRestarted pins that a port whose timer
// runs keeps its end time when another change reaches it, and that a stopped
// one counts HelloTime plus one second on an RSTP port and Max Age plus
// Forward Delay of the root's times on one that sends STP (13.26.6, P802.1aq/D1.5
// 13.29.11).
func TestRunningTopologyChangeTimerIsNotRestarted(t *testing.T) {
	t.Parallel()

	_, b, now := settledRegion(t)
	cist := b.cist()
	p := cist.ports["p2"]

	p.tcWhile = now.Add(time.Second)
	b.startTc(cist, p, now.Add(500*time.Millisecond))
	if want := now.Add(time.Second); !p.tcWhile.Equal(want) {
		t.Errorf("running timer ends %v, want it left at %v", p.tcWhile, want)
	}

	p.tcWhile = time.Time{}
	b.startTc(cist, p, now)
	if want := now.Add(b.helloTime + time.Second); !p.tcWhile.Equal(want) {
		t.Errorf("RSTP timer ends %v, want HelloTime plus one second, %v", p.tcWhile, want)
	}

	p.tcWhile = time.Time{}
	b.link("p2").sendRSTP = false
	b.startTc(cist, p, now)
	maxAge, _, forwardDelay := b.times(cist)
	if want := now.Add(maxAge + forwardDelay); !p.tcWhile.Equal(want) {
		t.Errorf("STP timer ends %v, want Max Age plus Forward Delay, %v", p.tcWhile, want)
	}
}

// TestNextWakeReportsAPortTopologyChangeTimer pins that the layer asks to be
// woken when a port's topology change timer runs out, so the flag leaves the
// BPDUs at the right hello.
func TestNextWakeReportsAPortTopologyChangeTimer(t *testing.T) {
	t.Parallel()

	_, b, now := settledRegion(t)

	due := now.Add(time.Millisecond)
	b.trees[treeID(1)].ports["p2"].tcWhile = due

	if got, ok := b.NextWake(); !ok || !got.Equal(due) {
		t.Errorf("NextWake = %v, %v; want the timer at %v", got, ok, due)
	}
}
