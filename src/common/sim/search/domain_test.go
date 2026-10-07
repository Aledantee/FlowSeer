package search

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
)

var (
	_ Domain = (*L2TrafficDomain)(nil)
	_ Domain = (*TimedFaultDomain)(nil)
)

func TestTupleString(t *testing.T) {
	t.Parallel()

	tuple := Tuple("test-tuple-123")
	if tuple.String() != "test-tuple-123" {
		t.Errorf("Tuple.String() = %q, want test-tuple-123", tuple.String())
	}
}

func TestCandidateCloneIsolation(t *testing.T) {
	t.Parallel()

	orig := Candidate{
		Tuple: "t1",
		Actions: []fabric.Action{
			{Kind: fabric.ActionInject, At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Inject: &fabric.Injection{
				At:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				Origin: fabric.Endpoint{Node: "h1", Port: "eth0"},
				Frame: ethernet.Frame{
					Dst:       netaddr.MAC{0x02, 0, 0, 0, 0, 2},
					Src:       netaddr.MAC{0x02, 0, 0, 0, 0, 1},
					Tags:      []vlan.Tag{{VID: 10}},
					EtherType: ethernet.EtherTypeIPv4,
					Payload:   []byte{1, 2, 3},
				},
				Packet: &fabric.Packet{
					Payload: []byte{4, 5, 6},
				},
			}},
			{
				At: time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC), Kind: fabric.ActionFault, Fault: &fabric.FaultAction{
					A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
					B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
					Fault: fabric.Fault{
						Kind:     fabric.FaultLoseSequence,
						Sequence: []uint{1, 3},
					},
				},
			},
		},
	}

	cloned := orig.Clone()

	// Mutate clone
	cloned.Tuple = "t2"
	cloned.Actions[0].Inject.Frame.Tags[0].VID = 20
	cloned.Actions[0].Inject.Frame.Payload[0] = 99
	cloned.Actions[0].Inject.Packet.Payload[0] = 88
	cloned.Actions[1].Fault.Fault.Sequence[0] = 77

	// Verify original untouched
	if orig.Tuple != "t1" {
		t.Errorf("orig.Tuple was mutated to %s", orig.Tuple)
	}
	if orig.Actions[0].Inject.Frame.Tags[0].VID != 10 {
		t.Errorf("orig.Actions tag VID was mutated: %d", orig.Actions[0].Inject.Frame.Tags[0].VID)
	}
	if orig.Actions[0].Inject.Frame.Payload[0] != 1 {
		t.Errorf("orig.Actions payload was mutated: %d", orig.Actions[0].Inject.Frame.Payload[0])
	}
	if orig.Actions[0].Inject.Packet.Payload[0] != 4 {
		t.Errorf("orig.Actions packet was mutated: %d", orig.Actions[0].Inject.Packet.Payload[0])
	}
	if orig.Actions[1].Fault.Fault.Sequence[0] != 1 {
		t.Errorf("orig.Faults sequence was mutated: %d", orig.Actions[1].Fault.Fault.Sequence[0])
	}
}

func TestCandidateValidationAndStableIndices(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	injection := fabric.Action{At: at, Index: 7, Kind: fabric.ActionInject, Inject: &fabric.Injection{Origin: fabric.Endpoint{Node: "a"}}}
	candidate := Candidate{Actions: []fabric.Action{injection, injection}}
	if err := candidate.Validate(); err == nil {
		t.Fatal("duplicate At/Index accepted")
	}
	candidate.Actions[1].At = at.Add(time.Second)
	if err := candidate.Validate(); err != nil {
		t.Fatalf("reused index at another time: %v", err)
	}
	candidate.Actions[1].Index = 0
	if err := candidate.Validate(); err == nil {
		t.Fatal("mixed indices accepted")
	}
	candidate.Actions = []fabric.Action{{At: at, Kind: fabric.ActionConfigure, Configure: &fabric.ConfigureAction{Node: "a"}}}
	if err := candidate.Validate(); err == nil {
		t.Fatal("configure action accepted")
	}
	candidate.Actions = []fabric.Action{injection}
	scenario := candidate.ToScenario(10)
	if scenario.Actions[0].Index != 7 {
		t.Fatalf("Index=%d, want 7", scenario.Actions[0].Index)
	}
	scenario.Actions[0].Inject.Origin.Node = "mutated"
	if candidate.Actions[0].Inject.Origin.Node != "a" {
		t.Fatal("scenario shares candidate injection")
	}
}

func TestProductSizeBounds(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if size, err := productSize(maxInt, 1); err != nil || size != maxInt {
		t.Fatalf("boundary=%d,%v", size, err)
	}
	if _, err := productSize(maxInt, 2); err == nil {
		t.Fatal("overflow accepted")
	}
	if size, err := productSize(maxInt, 2, 0); err != nil || size != 0 {
		t.Fatalf("empty=%d,%v", size, err)
	}
}
