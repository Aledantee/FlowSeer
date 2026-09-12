package fabric

import (
	"math"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
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
