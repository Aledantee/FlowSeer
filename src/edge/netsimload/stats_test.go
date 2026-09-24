package netsimload

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

func TestAccumulatorCountsGapsDuplicatesReorderingAndLateFrames(t *testing.T) {
	base := time.Unix(1700000000, 0)
	accumulator := NewAccumulator(fabric.FlowID(10))
	for sequence := uint64(0); sequence < 5; sequence++ {
		accumulator.RecordSend(10, sequence)
	}
	for _, sequence := range []uint64{0, 2, 2, 1} {
		recordSignedReceive(t, accumulator, 10, sequence, base, base.Add(time.Microsecond))
	}
	accumulator.Close()
	recordSignedReceive(t, accumulator, 10, 3, base, base.Add(2*time.Microsecond))

	observation := accumulator.Snapshot()
	flow := observation.Flows[10]
	if flow.Sent != 5 || flow.UniqueReceived != 4 || flow.Missing != 1 {
		t.Fatalf("flow counts = %+v, want sent 5, unique 4, missing 1", flow)
	}
	if flow.Duplicates != 1 || flow.Reordered != 1 || flow.LateAfterClose != 1 {
		t.Fatalf("flow ordering counts = %+v", flow)
	}
	if flow.Latency.Count != 4 || flow.Latency.Min != time.Microsecond || flow.Latency.Max != 2*time.Microsecond {
		t.Fatalf("latency = %+v", flow.Latency)
	}
}

func TestAccumulatorSeparatesMalformedAndInterfaceDrops(t *testing.T) {
	accumulator := NewAccumulator(1)
	accumulator.RecordReceive([]byte{1, 2, 3}, time.Unix(1700000000, 0))
	accumulator.SetInterfaceDrops(8)
	observation := accumulator.Snapshot()
	if observation.Malformed != 1 || observation.InterfaceDrops != 8 {
		t.Fatalf("observation = %+v", observation)
	}
}

func TestAccumulatorRejectsNegativeAndFutureLatency(t *testing.T) {
	base := time.Unix(1700000000, 0)
	accumulator := NewAccumulator(1)
	accumulator.RecordSend(1, 0)
	recordSignedReceive(t, accumulator, 1, 0, time.Unix(-1, 0), base)
	accumulator.RecordSend(1, 1)
	recordSignedReceive(t, accumulator, 1, 1, base.Add(time.Second), base)

	flow := accumulator.Snapshot().Flows[1]
	if flow.UniqueReceived != 2 || flow.Malformed != 2 || flow.Latency.Count != 0 {
		t.Fatalf("flow = %+v, want two malformed timestamp observations", flow)
	}
}

func recordSignedReceive(t *testing.T, accumulator *Accumulator, flowID fabric.FlowID, sequence uint64, submittedAt, capturedAt time.Time) {
	t.Helper()
	frame, err := Sign(ethernet.Frame{Payload: make([]byte, SignatureSize)}, flowID, sequence, submittedAt)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	wire, err := frame.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	accumulator.RecordReceive(wire, capturedAt)
}
