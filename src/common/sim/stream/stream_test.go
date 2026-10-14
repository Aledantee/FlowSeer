package stream_test

import (
	"bytes"
	"math"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/udp"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/stream"
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
	spec := stream.Spec{Rate: stream.Rate{FramesPerSecond: 10}, Count: 2, Start: time.Second}
	source, err := spec.Source()
	if err != nil {
		t.Fatal(err)
	}
	at, _, ok := source.Next()
	if !ok || at != time.Second {
		t.Fatalf("first offset = %s, %t, want 1s", at, ok)
	}
	clone := source.Clone()
	for _, cursor := range []stream.Source{source, clone} {
		at, _, ok := cursor.Next()
		if !ok || at != 1100*time.Millisecond {
			t.Fatalf("next offset = %s, %t, want 1.1s", at, ok)
		}
		if _, _, ok := cursor.Next(); ok {
			t.Fatal("source not exhausted")
		}
	}
}

func TestSpecStartValidation(t *testing.T) {
	for _, spec := range []stream.Spec{
		{Rate: stream.Rate{FramesPerSecond: 10}, Count: 1, Start: -1},
		{Rate: stream.Rate{FramesPerSecond: 10}, Count: 2, Start: math.MaxInt64 - 100*time.Millisecond + 1},
		{Rate: stream.Rate{FramesPerSecond: 10}, Count: 2, Gap: time.Millisecond, Start: math.MaxInt64 - 101*time.Millisecond + 1},
	} {
		if err := spec.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil", spec)
		}
	}
	spec := stream.Spec{Rate: stream.Rate{FramesPerSecond: 10}, Count: 2, Start: math.MaxInt64 - 100*time.Millisecond}
	source, err := spec.Source()
	if err != nil {
		t.Fatal(err)
	}
	source.Next()
	at, _, ok := source.Next()
	if !ok || at != math.MaxInt64 {
		t.Fatalf("last offset = %d, %t, want MaxInt64", at, ok)
	}
}

func TestSpecRejectsNegativeStartWithTwoFrames(t *testing.T) {
	spec := stream.Spec{Rate: stream.Rate{FramesPerSecond: 10}, Count: 2, Start: -1}
	if err := spec.Validate(); err == nil || err.Error() != "stream start must be nonnegative" {
		t.Errorf("Validate() = %v, want stream start must be nonnegative", err)
	}
}

func TestUDPSizeValidationInEitherOrder(t *testing.T) {
	frame := udpFrame(t)
	frame.Payload = append(frame.Payload, bytes.Repeat([]byte{0xa5}, 46-len(frame.Payload))...)
	const packetSize = 18 + 20 + 8 + 3
	cases := 0
	yields := 0
	for _, pointers := range []bool{false, true} {
		for _, udpFirst := range []bool{false, true} {
			for _, size := range []int{packetSize - 1, packetSize} {
				cases++
				var sizeVariation stream.Variation = stream.SizeVariation{Sizes: []int{size}}
				var udpVariation stream.Variation = stream.UDPPortVariation{Dst: true, Step: 1, Count: 2}
				if pointers {
					sizeVariation = &stream.SizeVariation{Sizes: []int{size}}
					udpVariation = &stream.UDPPortVariation{Dst: true, Step: 1, Count: 2}
				}
				variations := []stream.Variation{sizeVariation, udpVariation}
				if udpFirst {
					variations[0], variations[1] = variations[1], variations[0]
				}
				spec := variationSpec(frame, 2, variations...)
				source, err := spec.Source()
				if size < packetSize {
					if err == nil {
						t.Errorf("size %d udpFirst=%t pointers=%t accepted", size, udpFirst, pointers)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				for n := range 2 {
					yields++
					_, yielded, ok := source.Next()
					if !ok {
						t.Fatal("exhausted")
					}
					header, datagram, err := ip.Decode(yielded.Payload)
					if err != nil {
						t.Fatal(err)
					}
					udpHeader, payload, err := udp.Decode(datagram)
					if err != nil {
						t.Fatal(err)
					}
					if len(yielded.Payload) != 31 || len(datagram) != 11 || udpHeader.Length != 11 || udpHeader.DstPort != uint16(1000+n) || !bytes.Equal(payload, []byte{1, 2, 3}) || !udp.Verify(datagram, header.Src, header.Dst) {
						t.Errorf("yielded packet lengths/port/payload changed: %+v", yielded)
					}
				}
			}
		}
	}
	if cases != 8 || yields != 8 {
		t.Fatalf("cases/yields = %d/%d, want 8/8", cases, yields)
	}
}

func TestSpecClone(t *testing.T) {
	spec := stream.Spec{Frame: ethernet.Frame{Payload: []byte{7}}, Rate: stream.Rate{FramesPerSecond: 1}, Count: 1}
	clone := spec.Clone()
	if clone.Count != spec.Count || clone.Rate != spec.Rate || &clone.Frame.Payload[0] != &spec.Frame.Payload[0] {
		t.Errorf("Clone() = %+v, want value copy sharing immutable frame", clone)
	}
}
