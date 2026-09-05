package recovery

import "sync"

// Hold is the per-device latch an abandonment engages: while active, the
// lane admits no further mutation for that device (a read is unaffected —
// reads are not mutations) until an explicit [Hold.Resolve] call, never a
// retry. The zero value is an inactive Hold, ready to use.
type Hold struct {
	mu     sync.Mutex
	active bool
}

// Engage activates the hold. Idempotent.
func (h *Hold) Engage() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active = true
}

// Active reports whether the hold currently blocks mutation admission.
func (h *Hold) Active() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.active
}

// Resolve clears the hold: an operator's accept-observed or
// restore-expected decision, or a reconciliation intent, has addressed the
// abandoned mutation's effect. Building the concrete resolution (which
// intent to admit next, if any) is the caller's job — a Hold only tracks
// whether the device's lane may admit mutations again.
func (h *Hold) Resolve() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active = false
}
