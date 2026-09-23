package fabric

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

// TestQueueStepsWakeFrameDequeueInOrder enqueues one of each arrival kind at
// the same instant in reverse priority order and checks that Step pops them
// wake, frame, dequeue, and that Snapshot reports the queue sorted by
// compareArrival rather than in heap order.
func TestQueueStepsWakeFrameDequeueInOrder(t *testing.T) {
	fab := newTestFabricForFork(t)
	fab.queue = nil
	fab.dequeueItems = make(map[Endpoint]int)
	fab.wakeItems = make(map[string]int)

	at := fab.clock.Add(time.Second)
	ep := Endpoint{Node: "sw1", Port: "1/1/2"}
	if _, err := fab.Inject(Injection{
		At:     at,
		Origin: ep,
		Frame:  ethernet.Frame{Src: netaddr.MAC{0x02}, Payload: []byte("q")},
	}); err != nil {
		t.Fatalf("inject frame: %v", err)
	}
	fab.enqueue(Arrival{At: at, Kind: ArrivalDequeue, Device: ep.Node, Port: ep.Port})
	fab.enqueue(Arrival{At: at, Kind: ArrivalWake, Device: ep.Node})

	sorted := fab.Snapshot().Queue
	if !slices.IsSortedFunc(sorted, compareArrival) {
		t.Fatalf("Snapshot queue not sorted by compareArrival: %+v", sorted)
	}
	wantKinds := []ArrivalKind{ArrivalWake, ArrivalFrame, ArrivalDequeue}
	if len(sorted) != len(wantKinds) {
		t.Fatalf("Snapshot queue length = %d, want %d", len(sorted), len(wantKinds))
	}
	for i, want := range wantKinds {
		if sorted[i].Kind != want {
			t.Errorf("Snapshot queue[%d].Kind = %v, want %v", i, sorted[i].Kind, want)
		}
	}

	for i, want := range wantKinds {
		entry, ok := fab.Step()
		if !ok {
			t.Fatalf("Step %d returned false", i)
		}
		got := EntryDequeue
		switch want {
		case ArrivalWake:
			got = EntryWake
		case ArrivalFrame:
			got = EntryHop
		}
		if entry.Kind != got {
			t.Errorf("Step %d processed %v, want %v", i, entry.Kind, got)
		}
	}
}

// TestQueueRescheduleKeepsOneDequeuePerEndpoint schedules a dequeue, removes
// it, and reschedules it earlier: the queue must hold exactly one dequeue for
// the endpoint, at the new time, and the endpoint index must name it.
func TestQueueRescheduleKeepsOneDequeuePerEndpoint(t *testing.T) {
	fab := &Fabric{}
	fab.initRunState()

	ep := Endpoint{Node: "sw1", Port: "1/1/1"}
	base := time.Unix(1000, 0)
	fab.egress[ep] = &egressQueue{}
	fab.scheduleDequeue(ep, base.Add(2*time.Second))
	fab.removeDequeue(ep)
	fab.scheduleDequeue(ep, base.Add(time.Second))

	if len(fab.queue) != 1 {
		t.Fatalf("queue length = %d, want 1 dequeue", len(fab.queue))
	}
	if got, want := fab.queue[0].At, base.Add(time.Second); !got.Equal(want) {
		t.Errorf("dequeue at %s, want %s", got, want)
	}
	idx, ok := fab.dequeueItems[ep]
	if !ok {
		t.Fatalf("dequeueItems has no entry for %s/%s", ep.Node, ep.Port)
	}
	if idx != 0 {
		t.Errorf("dequeueItems[%s] = %d, want 0", ep, idx)
	}
}

// TestQueuePopClearsKeyIndexes enqueues a wake and a dequeue, asserts both
// keys are indexed, pops them, and asserts each key left its index when its
// arrival was popped.
func TestQueuePopClearsKeyIndexes(t *testing.T) {
	fab := &Fabric{}
	fab.initRunState()

	ep := Endpoint{Node: "sw2", Port: "1/1/1"}
	fab.enqueue(Arrival{At: time.Unix(10, 0), Kind: ArrivalDequeue, Device: ep.Node, Port: ep.Port})
	fab.enqueue(Arrival{At: time.Unix(20, 0), Kind: ArrivalWake, Device: "sw2"})

	if _, ok := fab.dequeueItems[ep]; !ok {
		t.Fatalf("dequeueItems missing %s", ep)
	}
	if _, ok := fab.wakeItems["sw2"]; !ok {
		t.Fatalf("wakeItems missing sw2")
	}

	fab.popArrival()
	if _, ok := fab.dequeueItems[ep]; ok {
		t.Errorf("popped dequeue left its index behind")
	}
	fab.popArrival()
	if _, ok := fab.wakeItems["sw2"]; ok {
		t.Errorf("popped wake left its index behind")
	}
	if len(fab.queue) != 0 {
		t.Errorf("queue length = %d after popping both, want 0", len(fab.queue))
	}
}

// TestQueueHeapOrderMatchesCompareArrival drives 10,000 arrivals whose fields
// come from a linear congruential step, with a unique Seq and at most one
// dequeue per endpoint and one wake per device, then removes 1,000 by key. The
// heap pops in exactly the order compareArrival sorts: the heap is not stable,
// so that holds only because unique Seq values make the order total.
func TestQueueHeapOrderMatchesCompareArrival(t *testing.T) {
	fab := &Fabric{}
	fab.initRunState()

	const total = 10_000
	const removals = 1_000

	base := time.Unix(2_000_000, 0)
	state := uint64(0x2545f4914f6cdd1d)
	next := func() uint64 {
		state = state*6364136223846793005 + 1442695040888963407
		return state >> 33
	}

	pushed := make([]Arrival, 0, total)
	usedDequeue := make(map[Endpoint]bool)
	usedWake := make(map[string]bool)
	for i := range total {
		x := next()
		arr := Arrival{
			At:  base.Add(time.Duration(x%1000) * time.Microsecond),
			Seq: uint64(i) + 1,
		}
		switch x % 3 {
		case 0:
			ep := Endpoint{Node: fmt.Sprintf("sw%d", x%40), Port: fmt.Sprintf("1/1/%d", x%8)}
			if usedDequeue[ep] {
				arr.Kind = ArrivalFrame
			} else {
				usedDequeue[ep] = true
				arr.Kind = ArrivalDequeue
				arr.Device, arr.Port = ep.Node, ep.Port
			}
		case 1:
			dev := fmt.Sprintf("sw%d", x%40)
			if usedWake[dev] {
				arr.Kind = ArrivalFrame
			} else {
				usedWake[dev] = true
				arr.Kind = ArrivalWake
				arr.Device = dev
			}
		default:
			arr.Kind = ArrivalFrame
			arr.Device = fmt.Sprintf("sw%d", x%40)
			arr.Port = fmt.Sprintf("1/1/%d", x%8)
		}
		pushed = append(pushed, arr)
		fab.enqueue(arr)
	}

	want := slices.Clone(pushed)
	slices.SortFunc(want, compareArrival)

	// Remove 1,000 items by key, interleaved in a fixed order, and drop them
	// from the expected sequence.
	removed := make(map[int]bool)
	removedDequeues := 0
	removedWakes := 0
	for i := 0; i < total && (removedDequeues < removals/2 || removedWakes < removals/2); i++ {
		arr := pushed[i]
		switch {
		case arr.Kind == ArrivalDequeue && removedDequeues < removals/2:
			before := len(fab.queue)
			fab.removeDequeueArrival(Endpoint{Node: arr.Device, Port: arr.Port})
			if len(fab.queue) != before-1 {
				t.Fatalf("removeDequeueArrival removed %d arrivals, want 1", before-len(fab.queue))
			}
			removed[i] = true
			removedDequeues++
		case arr.Kind == ArrivalWake && removedWakes < removals/2:
			before := len(fab.queue)
			fab.removeWakeArrival(arr.Device)
			if len(fab.queue) != before-1 {
				t.Fatalf("removeWakeArrival removed %d arrivals, want 1", before-len(fab.queue))
			}
			removed[i] = true
			removedWakes++
		}
	}

	// Removing a key that is not queued changes nothing.
	before := len(fab.queue)
	fab.removeDequeueArrival(Endpoint{Node: "absent", Port: "absent"})
	fab.removeWakeArrival("absent")
	if len(fab.queue) != before {
		t.Fatalf("removing absent keys changed the queue length")
	}

	var expected []Arrival
	for i, arr := range pushed {
		if !removed[i] {
			expected = append(expected, arr)
		}
	}
	slices.SortFunc(expected, compareArrival)

	if len(fab.queue) != len(expected) {
		t.Fatalf("queue length = %d after removals, want %d", len(fab.queue), len(expected))
	}
	for i := range expected {
		got := fab.popArrival()
		if compareArrival(got, expected[i]) != 0 {
			t.Fatalf("pop %d = %+v, want %+v", i, got, expected[i])
		}
	}
}
