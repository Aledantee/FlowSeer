package stream_test

import (
	"bytes"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/sim/stream"
)

func TestBurstGapOffsets(t *testing.T) {
	spec := stream.Spec{Frame: ethernet.Frame{Payload: make([]byte, 46)}, Rate: stream.Rate{BitsPerSecond: 1_000_000_000}, Burst: 10, Gap: time.Millisecond, Count: 20}
	source, err := spec.Source()
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	for n := 0; n < 20; n++ {
		at, _, ok := source.Next()
		want := time.Duration(n)*672*time.Nanosecond + time.Duration(n/10)*time.Millisecond
		if !ok || at != want {
			t.Fatalf("frame %d = (%s, %t), want (%s, true)", n, at, ok, want)
		}
	}
}

func TestNondecreasingOffsets(t *testing.T) {
	spec := stream.Spec{Rate: stream.Rate{FramesPerSecond: 2_000_000_000}, Count: 5}
	source, err := spec.Source()
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	previous := time.Duration(-1)
	for n := 0; n < 5; n++ {
		at, _, ok := source.Next()
		if !ok || at < previous {
			t.Fatalf("frame %d = (%s, %t), previous %s", n, at, ok, previous)
		}
		previous = at
	}
}

func TestSourceExhaustion(t *testing.T) {
	source, err := (stream.Spec{Rate: stream.Rate{FramesPerSecond: 1}, Count: 1}).Source()
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	if _, _, ok := source.Next(); !ok {
		t.Fatal("first Next exhausted, want frame")
	}
	for i := 0; i < 2; i++ {
		if _, _, ok := source.Next(); ok {
			t.Errorf("Next after exhaustion %d yielded a frame", i)
		}
	}
}

func TestSourceCloneCursor(t *testing.T) {
	source, err := (stream.Spec{Rate: stream.Rate{FramesPerSecond: 10}, Count: 3}).Source()
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	if _, _, ok := source.Next(); !ok {
		t.Fatal("first Next exhausted")
	}
	clone := source.Clone()
	at, _, ok := clone.Next()
	if !ok || at != 100*time.Millisecond {
		t.Fatalf("clone next = (%s, %t), want (100ms, true)", at, ok)
	}
	at, _, ok = source.Next()
	if !ok || at != 100*time.Millisecond {
		t.Errorf("original next = (%s, %t), want (100ms, true)", at, ok)
	}
}

func TestSourceFramesIndependent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		variations []stream.Variation
	}{
		{name: "none"},
		{name: "MAC", variations: []stream.Variation{stream.MACVariation{Field: stream.MACSource, Count: 1}}},
		{name: "size", variations: []stream.Variation{stream.SizeVariation{Sizes: []int{68}}}},
		{name: "UDP", variations: []stream.Variation{stream.UDPPortVariation{Dst: true, Count: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := udpFrame(t)
			frame.Tags = []vlan.Tag{{VID: 7}}
			frame.Payload = append(frame.Payload, make([]byte, 46-len(frame.Payload))...)
			spec := variationSpec(frame, 4, tc.variations...)
			source := variationSource(t, spec)
			clone := source.Clone()
			_, first, _ := source.Next()
			_, second, _ := source.Next()
			want := bytes.Clone(first.Payload)
			first.Tags[0].VID = 8
			first.Payload[0] ^= 0xff
			if second.Tags[0].VID != 7 || !bytes.Equal(second.Payload, want) {
				t.Fatal("second yield shares first frame")
			}
			second.Tags[0].VID = 9
			second.Payload[0] ^= 0xff
			if spec.Frame.Tags[0].VID != 7 || spec.Frame.Payload[0] != want[0] {
				t.Fatal("yield changed template")
			}
			for _, cursor := range []stream.Source{source, clone} {
				_, got, ok := cursor.Next()
				if !ok || got.Tags[0].VID != 7 || !bytes.Equal(got.Payload, want) {
					t.Fatal("cursor shares yielded frame")
				}
			}
		})
	}
}
