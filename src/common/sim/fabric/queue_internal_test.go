package fabric_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
)

func TestPastArrivalRunsAtClock(t *testing.T) {
	fab := newTestFabricForFork(t)
	clock := fab.Snapshot().Clock
	fabric.ExportFabricEnqueue(fab, fabric.MakeTestArrival(fabric.ExportArrivalFields{At: clock.Add(-time.Second), Kind: fabric.ArrivalWake, Device: "unused"}))
	entry, ok := fab.Step()
	if !ok || !entry.At.Equal(clock) || !fab.Snapshot().Clock.Equal(clock) {
		t.Fatalf("Step = %+v, %t, clock = %s, want %s", entry, ok, fab.Snapshot().Clock, clock)
	}
}

func TestZeroArrivalKindRefused(t *testing.T) {
	fab := newTestFabricForFork(t)
	before := len(fab.Snapshot().Queue)
	fabric.ExportFabricEnqueue(fab, fabric.Arrival{At: fab.Snapshot().Clock})
	if fab.Err() == nil || len(fab.Snapshot().Queue) != before {
		t.Fatalf("Err = %v, queue = %d, want fault and %d arrivals", fab.Err(), len(fab.Snapshot().Queue), before)
	}
}

// TestQueueStepsWakeFrameDequeueInOrder enqueues one of each arrival kind at
// the same instant in reverse priority order and checks that Step pops them
// wake, frame, dequeue, and that Snapshot reports the queue sorted by
// compareArrival rather than in heap order.
func TestQueueStepsWakeFrameDequeueInOrder(t *testing.T) {
	fab := newTestFabricForFork(t)
	(*fabric.ExportFabricQueue(fab)) = nil
	(*fabric.ExportFabricDequeueItems(fab)) = make(map[fabric.Endpoint]int)
	(*fabric.ExportFabricWakeItems(fab)) = make(map[string]int)

	at := fabric.ExportFabricClock(fab).Add(time.Second)
	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/2"}
	if _, err := fab.Inject(fabric.Injection{
		At:     at,
		Origin: ep,
		Frame:  ethernet.Frame{Src: netaddr.MAC{0x02}, Payload: []byte("q")},
	}); err != nil {
		t.Fatalf("inject frame: %v", err)
	}
	fabric.ExportFabricEnqueue(fab, fabric.MakeTestArrival(fabric.ExportArrivalFields{At: at, Kind: fabric.ArrivalDequeue, Device: ep.Node, Port: ep.Port}))
	fabric.ExportFabricEnqueue(fab, fabric.MakeTestArrival(fabric.ExportArrivalFields{At: at, Kind: fabric.ArrivalWake, Device: ep.Node}))

	sorted := fab.Snapshot().Queue
	if !slices.IsSortedFunc(sorted, fabric.ExportCompareArrival) {
		t.Fatalf("Snapshot queue not sorted by compareArrival: %+v", sorted)
	}
	wantKinds := []fabric.ArrivalKind{fabric.ArrivalWake, fabric.ArrivalFrame, fabric.ArrivalDequeue}
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
		got := fabric.EntryDequeue
		switch want {
		case fabric.ArrivalWake:
			got = fabric.EntryWake
		case fabric.ArrivalFrame:
			got = fabric.EntryHop
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
	fab := fabric.MakeTestFabricPointer(fabric.ExportFabricFields{})
	fabric.ExportFabricInitRunState(fab)

	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/1"}
	base := time.Unix(1000, 0)
	(*fabric.ExportFabricEgress(fab))[ep] = fabric.MakeTestEgressQueuePointer(fabric.ExportEgressQueueFields{})
	fabric.ExportFabricScheduleDequeue(fab, ep, base.Add(2*time.Second))
	fabric.ExportFabricRemoveDequeue(fab, ep)
	fabric.ExportFabricScheduleDequeue(fab, ep, base.Add(time.Second))

	if len((*fabric.ExportFabricQueue(fab))) != 1 {
		t.Fatalf("queue length = %d, want 1 dequeue", len((*fabric.ExportFabricQueue(fab))))
	}
	if got, want := (*fabric.ExportFabricQueue(fab))[0].At, base.Add(time.Second); !got.Equal(want) {
		t.Errorf("dequeue at %s, want %s", got, want)
	}
	idx, ok := (*fabric.ExportFabricDequeueItems(fab))[ep]
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
	fab := fabric.MakeTestFabricPointer(fabric.ExportFabricFields{})
	fabric.ExportFabricInitRunState(fab)

	ep := fabric.Endpoint{Node: "sw2", Port: "1/1/1"}
	fabric.ExportFabricEnqueue(fab, fabric.MakeTestArrival(fabric.ExportArrivalFields{At: time.Unix(10, 0), Kind: fabric.ArrivalDequeue, Device: ep.Node, Port: ep.Port}))
	fabric.ExportFabricEnqueue(fab, fabric.MakeTestArrival(fabric.ExportArrivalFields{At: time.Unix(20, 0), Kind: fabric.ArrivalWake, Device: "sw2"}))

	if _, ok := (*fabric.ExportFabricDequeueItems(fab))[ep]; !ok {
		t.Fatalf("dequeueItems missing %s", ep)
	}
	if _, ok := (*fabric.ExportFabricWakeItems(fab))["sw2"]; !ok {
		t.Fatalf("wakeItems missing sw2")
	}
	fabric.ExportFabricPopArrival(fab)
	if _, ok := (*fabric.ExportFabricDequeueItems(fab))[ep]; ok {
		t.Errorf("popped dequeue left its index behind")
	}
	fabric.ExportFabricPopArrival(fab)
	if _, ok := (*fabric.ExportFabricWakeItems(fab))["sw2"]; ok {
		t.Errorf("popped wake left its index behind")
	}
	if len((*fabric.ExportFabricQueue(fab))) != 0 {
		t.Errorf("queue length = %d after popping both, want 0", len((*fabric.ExportFabricQueue(fab))))
	}
}

// TestQueueHeapOrderMatchesCompareArrival checks the total order under
// interleaved pushes, indexed removals, and pops. Unique Seq values make
// compareArrival a total order even when timestamps and kinds tie.
func TestQueueHeapOrderMatchesCompareArrival(t *testing.T) {
	fab := fabric.MakeTestFabricPointer(fabric.ExportFabricFields{})
	fabric.ExportFabricInitRunState(fab)

	const total = 10_000
	const removals = 1_000

	base := time.Unix(2_000_000, 0)
	state := uint64(0x2545f4914f6cdd1d)
	next := func() uint64 {
		state = state*6364136223846793005 + 1442695040888963407
		return state >> 33
	}

	pushed := make([]fabric.Arrival, 0, total)
	active := make([]bool, 0, total)
	dequeueCandidates := make([]int, 0, total/3)
	wakeCandidates := make([]int, 0, total/3)
	usedDequeue := make(map[fabric.Endpoint]bool)
	usedWake := make(map[string]bool)

	sortedRemaining := func() []fabric.Arrival {
		expected := make([]fabric.Arrival, 0, len(pushed))
		for i, arr := range pushed {
			if active[i] {
				expected = append(expected, arr)
			}
		}
		slices.SortFunc(expected, fabric.ExportCompareArrival)
		return expected
	}
	checkPop := func(want fabric.Arrival) {
		got := fabric.ExportFabricPopArrival(fab)
		if fabric.ExportCompareArrival(got, want) != 0 {
			t.Fatalf("pop = %+v, want %+v", got, want)
		}
		active[int(want.Seq)-1] = false
		switch got.Kind {
		case fabric.ArrivalDequeue:
			if _, ok := (*fabric.ExportFabricDequeueItems(fab))[fabric.Endpoint{Node: got.Device, Port: got.Port}]; ok {
				t.Fatalf("popped dequeue left its index behind")
			}
		case fabric.ArrivalWake:
			if _, ok := (*fabric.ExportFabricWakeItems(fab))[got.Device]; ok {
				t.Fatalf("popped wake left its index behind")
			}
		}
	}

	removedDequeues := 0
	removedWakes := 0
	nextDequeue := 0
	nextWake := 0
	for i := range total {
		arr := fabric.MakeTestArrival(fabric.ExportArrivalFields{
			At:     base.Add(time.Duration(next()%1000) * time.Microsecond),
			Seq:    uint64(i) + 1,
			Device: fmt.Sprintf("sw%d", next()%4096),
			Port:   fmt.Sprintf("1/1/%d", next()%64),
			Kind:   fabric.ArrivalFrame,
		})
		switch next() % 3 {
		case 0:
			ep := fabric.Endpoint{Node: arr.Device, Port: arr.Port}
			if !usedDequeue[ep] {
				usedDequeue[ep] = true
				arr.Kind = fabric.ArrivalDequeue
				dequeueCandidates = append(dequeueCandidates, i)
			}
		case 1:
			if !usedWake[arr.Device] {
				usedWake[arr.Device] = true
				arr.Kind = fabric.ArrivalWake
				wakeCandidates = append(wakeCandidates, i)
			}
		}

		pushed = append(pushed, arr)
		active = append(active, true)
		fabric.ExportFabricEnqueue(fab, arr)

		if i > 0 && i%50 == 0 {
			checkPop(sortedRemaining()[0])
		}

		switch i % 12 {
		case 0:
			if removedDequeues == removals/2 {
				continue
			}
			for nextDequeue < len(dequeueCandidates) && !active[dequeueCandidates[nextDequeue]] {
				nextDequeue++
			}
			if nextDequeue == len(dequeueCandidates) {
				continue
			}
			idx := dequeueCandidates[nextDequeue]
			arr := pushed[idx]
			before := len((*fabric.ExportFabricQueue(fab)))
			fabric.ExportFabricRemoveDequeueArrival(fab, fabric.Endpoint{Node: arr.Device, Port: arr.Port})
			if len((*fabric.ExportFabricQueue(fab))) != before-1 {
				t.Fatalf("removeDequeueArrival removed %d arrivals, want 1", before-len((*fabric.ExportFabricQueue(fab))))
			}
			active[idx] = false
			removedDequeues++
			nextDequeue++
		case 6:
			if removedWakes == removals/2 {
				continue
			}
			for nextWake < len(wakeCandidates) && !active[wakeCandidates[nextWake]] {
				nextWake++
			}
			if nextWake == len(wakeCandidates) {
				continue
			}
			idx := wakeCandidates[nextWake]
			arr := pushed[idx]
			before := len((*fabric.ExportFabricQueue(fab)))
			fabric.ExportFabricRemoveWakeArrival(fab, arr.Device)
			if len((*fabric.ExportFabricQueue(fab))) != before-1 {
				t.Fatalf("removeWakeArrival removed %d arrivals, want 1", before-len((*fabric.ExportFabricQueue(fab))))
			}
			active[idx] = false
			removedWakes++
			nextWake++
		}
	}

	if removedDequeues != removals/2 || removedWakes != removals/2 {
		t.Fatalf("removed %d dequeues and %d wakes, want %d each", removedDequeues, removedWakes, removals/2)
	}

	// An absent key must leave the queue unchanged.
	before := len((*fabric.ExportFabricQueue(fab)))
	fabric.ExportFabricRemoveDequeueArrival(fab, fabric.Endpoint{Node: "absent", Port: "absent"})
	fabric.ExportFabricRemoveWakeArrival(fab, "absent")
	if len((*fabric.ExportFabricQueue(fab))) != before {
		t.Fatalf("removing absent keys changed the queue length")
	}

	expected := sortedRemaining()
	if len((*fabric.ExportFabricQueue(fab))) != len(expected) {
		t.Fatalf("queue length = %d after removals, want %d", len((*fabric.ExportFabricQueue(fab))), len(expected))
	}
	for _, want := range expected {
		checkPop(want)
	}
}
