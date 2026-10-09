package fabric

import (
	"cmp"
	"container/heap"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

// ArrivalKind identifies work scheduled on the simulation queue. Zero is invalid.
type ArrivalKind uint8

const (
	// ArrivalHost transmits an injected frame before later actions at the same time.
	ArrivalHost ArrivalKind = iota + 1
	// ArrivalAction executes a scenario action before wakes and stream frames at its time.
	ArrivalAction
	// ArrivalWake advances a device timer.
	ArrivalWake
	// ArrivalStream introduces one attached source frame and schedules its successor.
	ArrivalStream
	// ArrivalFrame delivers a frame to a device port.
	ArrivalFrame
	// ArrivalDequeue serves an endpoint's pending egress frames.
	ArrivalDequeue
)

// Arrival represents an input, frame, timer wake, or egress dequeue scheduled at a specific time.
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

	index  int
	pcp    vlan.PCP
	source int
	action *Action
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

// arrivalQueue owns scheduled events and their keyed indexes.
//
// compareArrival is the order. A heap is not stable, so its pop order matches
// the sorted order only because distinct members compare total: copies of one
// frame share Seq and differ in Device or Port. Every swap rewrites the
// endpoint and device indexes, so a removal by key never acts on a stale
// position.
type arrivalQueue struct {
	queue        []Arrival
	wakes        map[string]time.Time
	dequeueItems map[Endpoint]int
	wakeItems    map[string]int
}

func (q *arrivalQueue) Len() int { return len(q.queue) }

func (q *arrivalQueue) Less(i, j int) bool {
	return compareArrival(q.queue[i], q.queue[j]) < 0
}

func (q *arrivalQueue) Swap(i, j int) {
	items := q.queue
	items[i], items[j] = items[j], items[i]
	items[i].index, items[j].index = i, j
	q.reindex(items[i])
	q.reindex(items[j])
}

func (q *arrivalQueue) Push(x any) {
	arr := x.(Arrival)
	arr.index = len(q.queue)
	q.queue = append(q.queue, arr)
	q.reindex(arr)
}

func (q *arrivalQueue) Pop() any {
	items := q.queue
	last := len(items) - 1
	arr := items[last]
	items[last] = Arrival{}
	q.queue = items[:last]
	q.unindex(arr)

	return arr
}

func (f *Fabric) enqueue(arr Arrival) {
	if arr.Kind == 0 {
		f.recordFault(errs.Msg("arrival kind is zero"))
		return
	}
	f.initQueueIndexes()
	heap.Push(&f.arrivalQueue, arr)
	if arr.Kind == ArrivalFrame || arr.Kind == ArrivalHost {
		f.inflightAdd(arr.FrameID)
	}
}

func (f *Fabric) popArrival() Arrival {
	return heap.Pop(&f.arrivalQueue).(Arrival)
}

func (q *arrivalQueue) removeDequeueArrival(txEnd Endpoint) {
	if idx, ok := q.dequeueItems[txEnd]; ok {
		heap.Remove(q, idx)
	}
}

func (q *arrivalQueue) removeWakeArrival(device string) {
	if idx, ok := q.wakeItems[device]; ok {
		heap.Remove(q, idx)
	}
}

// initQueueIndexes ensures the key indexes exist before a keyed arrival is
// pushed. Fork leaves empty indexes unallocated, so copying an unused index
// costs nothing until a keyed arrival needs it.
func (q *arrivalQueue) initQueueIndexes() {
	if q.dequeueItems == nil {
		q.dequeueItems = make(map[Endpoint]int)
	}
	if q.wakeItems == nil {
		q.wakeItems = make(map[string]int)
	}
}

// reindex records arr's heap position under its key so a later removal finds
// it. Only a wake and a dequeue are keyed, and the queue holds at most one of
// each per device and endpoint.
func (q *arrivalQueue) reindex(arr Arrival) {
	switch arr.Kind {
	case ArrivalWake:
		if q.wakeItems != nil {
			q.wakeItems[arr.Device] = arr.index
		}
	case ArrivalDequeue:
		if q.dequeueItems != nil {
			q.dequeueItems[Endpoint{Node: arr.Device, Port: arr.Port}] = arr.index
		}
	}
}

func (q *arrivalQueue) unindex(arr Arrival) {
	switch arr.Kind {
	case ArrivalWake:
		delete(q.wakeItems, arr.Device)
	case ArrivalDequeue:
		delete(q.dequeueItems, Endpoint{Node: arr.Device, Port: arr.Port})
	}
}

func (f *Fabric) enqueueActions(actions []Action) {
	for _, action := range actions {
		clone := action.Clone()
		f.enqueue(Arrival{At: clone.At, Kind: ArrivalAction, Seq: f.nextSeq, action: &clone})
		f.nextSeq++
	}
}

// removeActionArrivals drops every scenario action from the queue and
// restores the heap and the key indexes over what remains. A run enqueues its
// actions and owns them, so none outlives the run that scheduled it.
func (q *arrivalQueue) removeActionArrivals() {
	kept := q.queue[:0]
	for _, arr := range q.queue {
		if arr.Kind != ArrivalAction {
			kept = append(kept, arr)
		}
	}
	if len(kept) == len(q.queue) {
		return
	}
	clear(q.queue[len(kept):])
	q.queue = kept
	for i := range q.queue {
		q.queue[i].index = i
		q.reindex(q.queue[i])
	}
	heap.Init(q)
}
