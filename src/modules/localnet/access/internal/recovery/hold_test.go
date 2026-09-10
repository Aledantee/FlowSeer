package recovery_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/recovery"
)

// TestAStaleResolutionLeavesALaterHoldEngaged is why a Hold carries the
// sequence it was engaged for.
//
// Central owes a resolution until the edge acknowledges it, and that
// acknowledgement travels an asynchronous queue, so a re-sent resolution for
// an older sequence is ordinary rather than exceptional. Cleared on the
// strength of arriving at all, a stale one lifts the hold a later
// abandonment engaged, and the next mutation is admitted over a device state
// nobody resolved.
func TestAStaleResolutionLeavesALaterHoldEngaged(t *testing.T) {
	var h recovery.Hold

	h.Engage(7)
	if !h.Resolve(7) {
		t.Fatal("the resolution naming the held sequence did not clear it")
	}
	if h.Active() {
		t.Fatal("the hold is still active after its own resolution")
	}

	// A later abandonment takes the hold, and central re-sends the old row.
	h.Engage(8)
	if h.Resolve(7) {
		t.Error("a resolution for sequence 7 reported clearing a hold held for 8")
	}
	if !h.Active() {
		t.Fatal("a stale resolution lifted a hold engaged by a later abandonment")
	}
	if got := h.Sequence(); got != 8 {
		t.Errorf("Sequence() = %d, want 8", got)
	}
	if !h.Resolve(8) {
		t.Error("the resolution naming the held sequence did not clear it")
	}
}
