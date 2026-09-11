package fabric

import (
	"cmp"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
)

// Arrival represents a frame or timer wake scheduled for processing at a specific device and time.
type Arrival struct {
	At      time.Time
	Seq     uint64
	Device  string
	Port    string
	FrameID FrameID
	Frame   ethernet.Frame
	Corrupt bool
	Wake    bool
}

func compareArrival(a, b Arrival) int {
	if r := a.At.Compare(b.At); r != 0 {
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
