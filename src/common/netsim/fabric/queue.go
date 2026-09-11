package fabric

import (
	"cmp"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
)

// ArrivalKind identifies work scheduled on the simulation queue.
type ArrivalKind uint8

const (
	// ArrivalWake advances a virtual switch timer.
	ArrivalWake ArrivalKind = iota
	// ArrivalFrame delivers a frame to a virtual switch port.
	ArrivalFrame
	// ArrivalDequeue serves an endpoint's pending egress frames.
	ArrivalDequeue
)

// Arrival represents a frame, timer wake, or egress dequeue scheduled for processing at a specific device and time.
type Arrival struct {
	At      time.Time
	Kind    ArrivalKind
	Seq     uint64
	Device  string
	Port    string
	FrameID FrameID
	Frame   ethernet.Frame
	Corrupt bool
}

func compareArrival(a, b Arrival) int {
	if r := a.At.Compare(b.At); r != 0 {
		return r
	}
	if r := cmp.Compare(a.Kind, b.Kind); r != 0 {
		return r
	}
	if r := cmp.Compare(a.Seq, b.Seq); r != 0 {
		return r
	}
	if r := cmp.Compare(a.Device, b.Device); r != 0 {
		return r
	}

	return cmp.Compare(a.Port, b.Port)
}

func (f *Fabric) enqueue(arr Arrival) {
	idx, _ := slices.BinarySearchFunc(f.queue, arr, compareArrival)
	f.queue = slices.Insert(f.queue, idx, arr)
}
