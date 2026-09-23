package stream_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/stream"
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
