package stream_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/stream"
)

func TestSpecValidate(t *testing.T) {
	valid := stream.Spec{Frame: ethernet.Frame{}, Rate: stream.Rate{FramesPerSecond: 10}, Count: 1}
	cases := []struct {
		name string
		spec stream.Spec
	}{
		{"neither rate", stream.Spec{Count: 1}},
		{"both rates", stream.Spec{Rate: stream.Rate{FramesPerSecond: 10, BitsPerSecond: 100}, Count: 1}},
		{"negative burst", stream.Spec{Rate: valid.Rate, Burst: -1, Count: 1}},
		{"negative gap", stream.Spec{Rate: valid.Rate, Gap: -time.Nanosecond, Count: 1}},
		{"neither end", stream.Spec{Rate: valid.Rate}},
		{"both ends", stream.Spec{Rate: valid.Rate, Count: 1, Duration: time.Second}},
		{"invalid frame", stream.Spec{Frame: ethernet.Frame{Tags: []vlan.Tag{{TPID: 1}}}, Rate: valid.Rate, Count: 1}},
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("valid spec: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.spec.Validate(); err == nil {
				t.Errorf("Validate() = nil, want error")
			}
		})
	}
}

func TestSpecNormalize(t *testing.T) {
	spec := stream.Spec{Rate: stream.Rate{FramesPerSecond: 3}, Duration: time.Second}
	normalized, err := spec.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if normalized.Burst != 1 || normalized.Count != 3 || normalized.Duration != 0 {
		t.Errorf("normalized burst/count/duration = %d/%d/%s, want 1/3/0", normalized.Burst, normalized.Count, normalized.Duration)
	}
	if spec.Burst != 0 || spec.Count != 0 {
		t.Errorf("original burst/count = %d/%d, want 0/0", spec.Burst, spec.Count)
	}

	normalized, err = (stream.Spec{Rate: stream.Rate{FramesPerSecond: 3}, Duration: 500 * time.Millisecond}).Normalize()
	if err != nil {
		t.Fatalf("Normalize fractional count: %v", err)
	}
	if normalized.Count != 2 {
		t.Errorf("Count = %d, want 2", normalized.Count)
	}
}

func TestFrameRateOffsets(t *testing.T) {
	spec := stream.Spec{Frame: ethernet.Frame{Payload: make([]byte, 64)}, Rate: stream.Rate{FramesPerSecond: 10_000}, Count: 1000, Start: time.Second}
	source, err := spec.Source()
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	for n := 0; n < 1000; n++ {
		at, frame, ok := source.Next()
		if !ok || at != time.Duration(n)*100*time.Microsecond || len(frame.Payload) != 64 {
			t.Fatalf("frame %d = (%s, payload %d, %t), want (%s, 64, true)", n, at, len(frame.Payload), ok, time.Duration(n)*100*time.Microsecond)
		}
	}
	if _, _, ok := source.Next(); ok {
		t.Error("frame 1001 yielded, want exhaustion")
	}
}

func TestSpecClone(t *testing.T) {
	spec := stream.Spec{Frame: ethernet.Frame{Payload: []byte{7}}, Rate: stream.Rate{FramesPerSecond: 1}, Count: 1}
	clone := spec.Clone()
	if clone.Count != spec.Count || clone.Rate != spec.Rate || &clone.Frame.Payload[0] != &spec.Frame.Payload[0] {
		t.Errorf("Clone() = %+v, want value copy sharing immutable frame", clone)
	}
}
