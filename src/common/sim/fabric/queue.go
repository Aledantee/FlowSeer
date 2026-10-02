package fabric

import (
	"cmp"
	"container/heap"
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
//
// index is its position in the fabric's arrival heap. The heap rewrites it on
// every move so a dequeue can be removed by endpoint and a wake by device
// without scanning the queue. It carries no meaning outside the queue.
type Arrival struct {
	At      time.Time
	Kind    ArrivalKind
	Seq     uint64
	Device  string
	Port    string
	FrameID FrameID
	Frame   ethernet.Frame
	Corrupt bool

	index int
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

// arrivalHeap adapts a fabric's arrival slice to [heap.Interface].
//
// compareArrival is the order. A heap is not stable, so its pop order matches
// the sorted order only because distinct members compare total: copies of one
// frame share Seq and differ in Device or Port. Every swap rewrites the
// endpoint and device indexes, so a removal by key never acts on a stale
// position.
type arrivalHeap struct {
	fabric *Fabric
}

func (h arrivalHeap) Len() int { return len(h.fabric.queue) }

func (h arrivalHeap) Less(i, j int) bool {
	return compareArrival(h.fabric.queue[i], h.fabric.queue[j]) < 0
}

func (h arrivalHeap) Swap(i, j int) {
	q := h.fabric.queue
	q[i], q[j] = q[j], q[i]
	q[i].index, q[j].index = i, j
	h.fabric.reindex(q[i])
	h.fabric.reindex(q[j])
}

func (h arrivalHeap) Push(x any) {
	arr := x.(Arrival)
	arr.index = len(h.fabric.queue)
	h.fabric.queue = append(h.fabric.queue, arr)
	h.fabric.reindex(arr)
}

func (h arrivalHeap) Pop() any {
	q := h.fabric.queue
	last := len(q) - 1
	arr := q[last]
	q[last] = Arrival{}
	h.fabric.queue = q[:last]
	h.fabric.unindex(arr)

	return arr
}

func (f *Fabric) enqueue(arr Arrival) {
	f.initQueueIndexes()
	heap.Push(arrivalHeap{fabric: f}, arr)
	if arr.Kind == ArrivalFrame {
		f.inflightAdd(arr.FrameID)
	}
}

func (f *Fabric) popArrival() Arrival {
	return heap.Pop(arrivalHeap{fabric: f}).(Arrival)
}

func (f *Fabric) removeDequeueArrival(txEnd Endpoint) {
	if idx, ok := f.dequeueItems[txEnd]; ok {
		heap.Remove(arrivalHeap{fabric: f}, idx)
	}
}

func (f *Fabric) removeWakeArrival(device string) {
	if idx, ok := f.wakeItems[device]; ok {
		heap.Remove(arrivalHeap{fabric: f}, idx)
	}
}

// initQueueIndexes ensures the key indexes exist before a keyed arrival is
// pushed. A fabric built by New or Fork holds them already; a hand-built one
// does not.
func (f *Fabric) initQueueIndexes() {
	if f.dequeueItems == nil {
		f.dequeueItems = make(map[Endpoint]int)
	}
	if f.wakeItems == nil {
		f.wakeItems = make(map[string]int)
	}
}

// reindex records arr's heap position under its key so a later removal finds
// it. Only a wake and a dequeue are keyed, and the queue holds at most one of
// each per device and endpoint.
func (f *Fabric) reindex(arr Arrival) {
	switch arr.Kind {
	case ArrivalWake:
		if f.wakeItems != nil {
			f.wakeItems[arr.Device] = arr.index
		}
	case ArrivalDequeue:
		if f.dequeueItems != nil {
			f.dequeueItems[Endpoint{Node: arr.Device, Port: arr.Port}] = arr.index
		}
	}
}

func (f *Fabric) unindex(arr Arrival) {
	switch arr.Kind {
	case ArrivalWake:
		delete(f.wakeItems, arr.Device)
	case ArrivalDequeue:
		delete(f.dequeueItems, Endpoint{Node: arr.Device, Port: arr.Port})
	}
}
