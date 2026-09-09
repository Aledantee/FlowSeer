package recovery

import "sync"

// Hold is the per-device latch an abandonment engages: while active, the
// lane admits no further mutation for that device (a read is unaffected —
// reads are not mutations) until an explicit [Hold.Resolve] call, never a
// retry. The zero value is an inactive Hold, ready to use.
//
// A Hold records which mutation engaged it, because a resolution names one
// and the two must agree. Central owes the resolution until the edge
// acknowledges it, and the acknowledgement travels an asynchronous queue, so
// a re-sent resolution for an older sequence is ordinary rather than
// exceptional. Cleared on the strength of its own sequence, a stale one would
// lift the hold a later abandonment engaged and admit a mutation over a
// device state nobody resolved — the one thing the hold exists to prevent.
type Hold struct {
	mu       sync.Mutex
	active   bool
	sequence uint64
}

// Engage activates the hold for sequence. Idempotent for the same sequence;
// a later abandonment takes the hold over, since its own resolution is the
// one still owed.
func (h *Hold) Engage(sequence uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active = true
	h.sequence = sequence
}

// Active reports whether the hold currently blocks mutation admission.
func (h *Hold) Active() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.active
}

// Sequence is the mutation the active hold was engaged for, or zero when it
// is inactive.
func (h *Hold) Sequence() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.active {
		return 0
	}
	return h.sequence
}

// Resolve clears the hold when sequence is the one that engaged it, and
// reports whether it did: an operator's accept-observed or restore-expected
// decision, or a reconciliation intent, has addressed that mutation's
// effect. Building the concrete resolution (which intent to admit next, if
// any) is the caller's job — a Hold only tracks whether the device's lane
// may admit mutations again.
//
// A false return means the resolution named a mutation this hold is not
// held for. The caller still acknowledges it — central needs to stop owing
// the row — but the lane stays closed, which is correct: the abandonment
// that is actually holding it has a resolution of its own still to come.
func (h *Hold) Resolve(sequence uint64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.active || h.sequence != sequence {
		return false
	}
	h.active = false
	h.sequence = 0
	return true
}
