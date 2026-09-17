package fabric

import (
	"math"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

func TestRateIntervalHandlesArithmeticBoundaries(t *testing.T) {
	t.Parallel()

	if got := rateInterval(704, math.MaxUint64); got != time.Nanosecond {
		t.Errorf("rateInterval at maximum rate = %v, want 1ns", got)
	}
	if got := rateInterval(math.MaxUint64, 1); got != time.Duration(math.MaxInt64) {
		t.Errorf("overflowing rateInterval = %v, want maximum duration", got)
	}
}

func TestReportDeepClonesNestedJourneyData(t *testing.T) {
	tag := vlan.Tag{VID: 10}
	frame := ethernet.Frame{
		Dst:     netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Tags:    []vlan.Tag{tag},
		Payload: []byte{1},
	}
	f := &Fabric{journeys: map[FrameID]*Journey{
		1: {
			FrameID: 1,
			Injection: Injection{
				Frame: frame,
				Packet: &Packet{
					Payload: []byte{2},
				},
			},
			Entries: []Entry{{
				Result: &vswitch.ForwardResult{Result: bridge.Result{
					Trace: trace.Trace{Steps: []trace.Step{{
						Inputs:   []trace.Fact{LengthFact(1)},
						Outputs:  []trace.Fact{TopSpeedFact(2)},
						Evidence: []trace.EvidenceRef{"source"},
					}}},
					Egress: []bridge.Egress{{Frame: frame}},
				}},
			}},
		},
	}}

	report := f.Report()
	report[0].Injection.Frame.Tags[0].VID = 20
	report[0].Injection.Frame.Payload[0] = 10
	report[0].Injection.Packet.Payload[0] = 20
	report[0].Entries[0].Result.Steps[0].Inputs[0] = LengthFact(10)
	report[0].Entries[0].Result.Steps[0].Outputs[0] = TopSpeedFact(20)
	report[0].Entries[0].Result.Steps[0].Evidence[0] = "mutated"
	report[0].Entries[0].Result.Egress[0].Frame.Tags[0].VID = 30
	report[0].Entries[0].Result.Egress[0].Frame.Payload[0] = 30

	fresh := f.Report()[0]
	if got := fresh.Injection.Frame.Tags[0].VID; got != 10 {
		t.Errorf("fresh injection frame VLAN = %d, want 10", got)
	}
	if got := fresh.Injection.Frame.Payload[0]; got != 1 {
		t.Errorf("fresh injection frame payload = %d, want 1", got)
	}
	if got := fresh.Injection.Packet.Payload[0]; got != 2 {
		t.Errorf("fresh injection packet payload = %d, want 2", got)
	}
	step := fresh.Entries[0].Result.Steps[0]
	if got := step.Inputs[0].Canonical(); got != "1" {
		t.Errorf("fresh step input = %q, want 1", got)
	}
	if got := step.Outputs[0].Canonical(); got != "2" {
		t.Errorf("fresh step output = %q, want 2", got)
	}
	if got := step.Evidence[0]; got != "source" {
		t.Errorf("fresh step evidence = %q, want source", got)
	}
	egress := fresh.Entries[0].Result.Egress[0].Frame
	if got := egress.Tags[0].VID; got != 10 {
		t.Errorf("fresh egress frame VLAN = %d, want 10", got)
	}
	if got := egress.Payload[0]; got != 1 {
		t.Errorf("fresh egress frame payload = %d, want 1", got)
	}
}

// TestScheduleDequeueRecordsFaultInsteadOfPanicking drives scheduleDequeue's
// two internal invariants directly — the only way to reach them, since no
// topology can — and checks each records a sticky fault instead of panicking,
// that the queue is left unscheduled, that the first fault is the one Err
// reports, and that Step and Run both stop and surface it. The Err assertions
// are the caller-side read the sticky field needs: without them a recorded
// fault would go unnoticed.
func TestScheduleDequeueRecordsFaultInsteadOfPanicking(t *testing.T) {
	base := time.Unix(1, 0)
	ep := Endpoint{Node: "sw1", Port: "e0"}

	t.Run("dequeue before the clock", func(t *testing.T) {
		f := &Fabric{
			egress:  map[Endpoint]*egressQueue{ep: {}},
			clock:   base,
			stepped: true,
		}

		f.scheduleDequeue(ep, base.Add(-time.Second))

		if f.Err() == nil {
			t.Fatal("Err() = nil; want a fault for a dequeue preceding the clock")
		}
		if !strings.Contains(f.Err().Error(), base.String()) {
			t.Errorf("Err() = %q, want it to name the clock %s", f.Err(), base)
		}
		if len(f.queue) != 0 {
			t.Errorf("queue = %d entries, want 0 (nothing scheduled)", len(f.queue))
		}
		if f.egress[ep].dequeuePending {
			t.Error("dequeuePending = true, want false after a rejected schedule")
		}
		if _, ok := f.Step(); ok {
			t.Error("Step() ok = true after a fault, want false")
		}
	})

	t.Run("second dequeue while one is pending", func(t *testing.T) {
		f := &Fabric{
			egress: map[Endpoint]*egressQueue{ep: {dequeuePending: true}},
		}

		f.scheduleDequeue(ep, base)

		if f.Err() == nil {
			t.Fatal("Err() = nil; want a fault for a second pending dequeue")
		}
		if !strings.Contains(f.Err().Error(), ep.Node) {
			t.Errorf("Err() = %q, want it to name endpoint %s", f.Err(), ep.Node)
		}
	})

	t.Run("Run halts and Err keeps the first fault", func(t *testing.T) {
		f := &Fabric{
			egress:  map[Endpoint]*egressQueue{ep: {}},
			clock:   base,
			stepped: true,
		}

		f.scheduleDequeue(ep, base.Add(-time.Second))
		first := f.Err()
		f.scheduleDequeue(ep, base.Add(-2*time.Second))

		if f.Err() != first {
			t.Errorf("Err() = %q after a second fault, want the first %q", f.Err(), first)
		}
		if n := f.Run(10); n != 0 {
			t.Errorf("Run(10) = %d after a fault, want 0", n)
		}
		if f.Err() == nil {
			t.Fatal("Err() = nil after Run over a faulted fabric, want the fault")
		}
	})
}

// TestRecordNeighborFailureCountsOnlyAgainstAPort reaches into the package because the invariant
// is about a counter nothing can read: snapshotCounters walks a device's real ports, so an
// Endpoint whose Port names a VLAN interface never appears in a Snapshot and is invisible from
// outside — a later reader finding it would believe it meant something. A drop that names no
// port has to create nothing at all, which only the counter map itself can show.
func TestRecordNeighborFailureCountsOnlyAgainstAPort(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)

	t.Run("a drop naming no port creates no endpoint", func(t *testing.T) {
		t.Parallel()
		f := &Fabric{clock: base}
		f.recordNeighborFailure(base, "sw1", vswitch.NeighborDrop{
			Step:   trace.Step{Subject: trace.Subject{Kind: "interface", Key: "vlan20"}},
			Reason: routing.ReasonNeighborMiss,
		})

		if len(f.counters) != 0 {
			t.Errorf("counters = %+v, want none: the drop named no port", f.counters)
		}
	})

	t.Run("a drop naming a port counts against it", func(t *testing.T) {
		t.Parallel()
		f := &Fabric{clock: base}
		f.recordNeighborFailure(base, "sw1", vswitch.NeighborDrop{
			Step:   trace.Step{Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
			Port:   "1/1/2",
			Reason: routing.ReasonNeighborHoldOverflow,
		})

		c, ok := f.counters[Endpoint{Node: "sw1", Port: "1/1/2"}]
		if !ok {
			t.Fatalf("no counters for sw1/1/1/2: %+v", f.counters)
		}
		if got := c.Discards[routing.ReasonNeighborHoldOverflow]; got != 1 {
			t.Errorf("Discards[%s] = %d, want 1", routing.ReasonNeighborHoldOverflow, got)
		}
		if c.Discards[routing.ReasonNeighborMiss] != 0 {
			t.Error("the drop was counted as a neighbor miss, want its own reason")
		}
	})
}
