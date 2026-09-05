package lane

import (
	"container/heap"
	"sync"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeOverload identifies a Submit call rejected because the queue is at
// its configured capacity.
var ErrCodeOverload = errs.NewCode("lane/overload")

// Priority orders admission only. A Queue never reorders an admitted Item
// relative to another once both are admitted with the same Priority; two
// different Priorities are ordered high before low regardless of admission
// order, per the direction record's decision 3.
type Priority int

const (
	// PriorityUnspecified is never a caller's real priority; Submit rejects
	// it so a missing choice cannot silently become the lowest priority.
	PriorityUnspecified Priority = iota
	// PriorityLow is an ordinary poll.
	PriorityLow
	// PriorityNormal is an ordinary mutation.
	PriorityNormal
	// PriorityHigh is a re-admission after a freeze release, an operator
	// abandonment resolution, or an authoritative reconciliation intent.
	PriorityHigh
)

// String returns the priority's name for logging and diagnostics.
func (p Priority) String() string {
	switch p {
	case PriorityLow:
		return "low"
	case PriorityNormal:
		return "normal"
	case PriorityHigh:
		return "high"
	default:
		return "unspecified"
	}
}

// Item is one admitted unit of work. Position is assigned at admission and
// never changes; it is the tiebreaker among equal-Priority items and the
// FIFO key poll coalescing and telemetry cite.
type Item struct {
	// Position is this device's local admission-order counter. It has no
	// relation to a central MutationState.sequence.
	Position uint64
	// Priority is this item's priority as submitted; immutable after
	// admission.
	Priority Priority
	// Payload is the caller-supplied work: an *integrationdevicev1.
	// ExecuteRequest in production, anything comparable in a test.
	Payload any
}

// heapItem is Item plus the insertion order container/heap needs to break
// ties between equal priorities; Position already provides that order, so
// this is Item's own Position reused as the heap key.
type itemHeap []*Item

func (h itemHeap) Len() int { return len(h) }

func (h itemHeap) Less(i, j int) bool {
	if h[i].Priority != h[j].Priority {
		return h[i].Priority > h[j].Priority
	}

	return h[i].Position < h[j].Position
}

func (h itemHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *itemHeap) Push(x any) { *h = append(*h, x.(*Item)) }

func (h *itemHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]

	return item
}

// Queue is one device's bounded admission FIFO. The zero value is not
// usable; construct one with [NewQueue].
type Queue struct {
	capacity int

	mu           sync.Mutex
	nextPosition uint64
	heap         itemHeap
}

// NewQueue builds a Queue that admits at most capacity items before
// rejecting further submissions with [ErrCodeOverload]. capacity must be at
// least 1.
func NewQueue(capacity int) *Queue {
	q := &Queue{capacity: capacity}
	heap.Init(&q.heap)

	return q
}

// Submit admits payload at priority, assigning it the next local Position.
// It returns [ErrCodeOverload] without admitting anything, and without
// disturbing any previously admitted item, once the queue holds capacity
// items still awaiting [Queue.Next].
func (q *Queue) Submit(priority Priority, payload any) (*Item, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.heap) >= q.capacity {
		return nil, errs.New().Code(ErrCodeOverload).Msgf("lane queue is at capacity %d", q.capacity)
	}

	q.nextPosition++
	item := &Item{Position: q.nextPosition, Priority: priority, Payload: payload}
	heap.Push(&q.heap, item)

	return item, nil
}

// Next removes and returns the highest-priority, earliest-admitted item
// still queued. ok is false when the queue is empty.
func (q *Queue) Next() (item *Item, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.heap) == 0 {
		return nil, false
	}

	return heap.Pop(&q.heap).(*Item), true
}

// Len reports how many items are currently admitted and not yet dequeued.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return len(q.heap)
}
