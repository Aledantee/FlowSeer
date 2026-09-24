package netsimload

import (
	"slices"
	"sort"
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
)

// LatencyStats summarizes nonnegative submission-to-capture latency samples.
type LatencyStats struct {
	Min   time.Duration `json:"min"`
	Max   time.Duration `json:"max"`
	Sum   time.Duration `json:"sum"`
	Count uint64        `json:"count"`
}

// FlowObservation reports the receive-side observation domain for one flow.
// Missing is computed from successful sends and the unique received sequence
// numbers, so a tail gap is included.
type FlowObservation struct {
	Sent           uint64       `json:"sent"`
	UniqueReceived uint64       `json:"unique_received"`
	Missing        uint64       `json:"missing"`
	Duplicates     uint64       `json:"duplicates"`
	Reordered      uint64       `json:"reordered"`
	Malformed      uint64       `json:"malformed"`
	LateAfterClose uint64       `json:"late_after_close"`
	Latency        LatencyStats `json:"latency"`
}

// Observation is the complete lab observation, including traffic that did
// not identify a configured flow and the capture interface-drop counter.
type Observation struct {
	Flows          map[fabric.FlowID]FlowObservation `json:"flows"`
	Malformed      uint64                            `json:"malformed"`
	InterfaceDrops uint64                            `json:"interface_drops"`
}

type flowAccumulator struct {
	observation FlowObservation
	sent        map[uint64]struct{}
	received    map[uint64]struct{}
	highest     uint64
	haveHighest bool
}

// Accumulator collects send and capture events for a finite run. It is not
// safe for concurrent use; the runner owns it and serializes receiver events.
type Accumulator struct {
	flows          map[fabric.FlowID]*flowAccumulator
	malformed      uint64
	interfaceDrops uint64
	closed         bool
}

// NewAccumulator creates an accumulator for flow IDs. Duplicate IDs are
// accepted once, while zero is ignored because it names no flow.
func NewAccumulator(flowIDs ...fabric.FlowID) *Accumulator {
	a := &Accumulator{flows: make(map[fabric.FlowID]*flowAccumulator, len(flowIDs))}
	for _, id := range flowIDs {
		if id != 0 {
			a.ensureFlow(id)
		}
	}

	return a
}

// RecordSend records a frame only after its sender has accepted the complete
// wire frame. A nonzero, previously unseen flow is added for test and library
// callers that build the accumulator incrementally.
func (a *Accumulator) RecordSend(flowID fabric.FlowID, sequence uint64) {
	if flowID == 0 {
		return
	}
	flow := a.ensureFlow(flowID)
	if _, exists := flow.sent[sequence]; exists {
		return
	}
	flow.sent[sequence] = struct{}{}
	flow.observation.Sent++
}

// RecordReceive decodes one complete captured Ethernet frame and records its
// observation. Unrelated or malformed traffic increments the aggregate
// malformed counter without entering a flow.
func (a *Accumulator) RecordReceive(wire []byte, capturedAt time.Time) {
	signature, err := DecodeWireSignature(wire)
	if err != nil {
		a.malformed++
		return
	}

	flow, ok := a.flows[signature.FlowID]
	if !ok || signature.FlowID == 0 {
		a.malformed++
		return
	}
	if a.closed {
		flow.observation.LateAfterClose++
	}

	if _, duplicate := flow.received[signature.Sequence]; duplicate {
		flow.observation.Duplicates++
		return
	}
	flow.received[signature.Sequence] = struct{}{}
	flow.observation.UniqueReceived++
	if flow.haveHighest && signature.Sequence < flow.highest {
		flow.observation.Reordered++
	}
	if !flow.haveHighest || signature.Sequence > flow.highest {
		flow.highest = signature.Sequence
		flow.haveHighest = true
	}

	latency := capturedAt.Sub(signature.SubmittedAt)
	if signature.SubmittedAt.UnixNano() < 0 || latency < 0 {
		flow.observation.Malformed++
		return
	}
	if flow.observation.Latency.Count == 0 || latency < flow.observation.Latency.Min {
		flow.observation.Latency.Min = latency
	}
	if latency > flow.observation.Latency.Max {
		flow.observation.Latency.Max = latency
	}
	flow.observation.Latency.Sum += latency
	flow.observation.Latency.Count++
}

// RecordFrame records a rawsocket frame and ignores a terminal receive error;
// the runner reports that error as the run failure separately.
func (a *Accumulator) RecordFrame(frame rawsocket.Frame) {
	if frame.Err == nil {
		a.RecordReceive(frame.Data, frame.CapturedAt)
	}
}

// Close marks the end of the send and drain window. Later valid frames remain
// observable and are counted as LateAfterClose for their flow.
func (a *Accumulator) Close() {
	a.closed = true
}

// SetInterfaceDrops stores the cumulative capture drop count for the report.
func (a *Accumulator) SetInterfaceDrops(drops uint64) {
	a.interfaceDrops = drops
}

// Snapshot returns an independent, deterministically ordered copy of the
// observation. The map is copied for caller ownership; JSON report code sorts
// IDs before emitting arrays.
func (a *Accumulator) Snapshot() Observation {
	flows := make(map[fabric.FlowID]FlowObservation, len(a.flows))
	for id, flow := range a.flows {
		observation := flow.observation
		for sequence := range flow.sent {
			if _, received := flow.received[sequence]; !received {
				observation.Missing++
			}
		}
		flows[id] = observation
	}

	return Observation{Flows: flows, Malformed: a.malformed, InterfaceDrops: a.interfaceDrops}
}

func (a *Accumulator) ensureFlow(id fabric.FlowID) *flowAccumulator {
	if flow := a.flows[id]; flow != nil {
		return flow
	}
	flow := &flowAccumulator{
		sent:     make(map[uint64]struct{}),
		received: make(map[uint64]struct{}),
	}
	a.flows[id] = flow

	return flow
}

// SortedFlowIDs returns observation flow IDs in ascending order for report
// and command consumers that need stable output.
func SortedFlowIDs(observation Observation) []fabric.FlowID {
	ids := make([]fabric.FlowID, 0, len(observation.Flows))
	for id := range observation.Flows {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	return slices.Clone(ids)
}
