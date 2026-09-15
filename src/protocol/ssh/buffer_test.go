package ssh

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/spawn"
)

// TestNewRingRetainsTailWithinLimit checks that a ring built with the
// defaulted (positive) limit its only callers pass keeps the most recent
// limit bytes and reports the truncation, so deleting the constructor's
// unreachable non-positive guard changed nothing on the live path.
func TestNewRingRetainsTailWithinLimit(t *testing.T) {
	r := newRing(4)

	r.write([]byte("abcdef"))

	total, truncated := r.stats()
	if total != 6 {
		t.Errorf("totalWritten = %d, want 6", total)
	}
	if !truncated {
		t.Error("truncated = false, want true after writing past the limit")
	}
	if got := r.drain(); !bytes.Equal(got, []byte("cdef")) {
		t.Errorf("retained tail = %q, want %q", got, "cdef")
	}
}

// TestRingClosesWithPanicErrorWhenReported mirrors newSession's drain
// wiring: spawn.Go(ctx, label, func() { s.drain(...) }, spawn.ReportTo(ring.closeWithErr)).
// A drain goroutine that panics before its own closeWithErr call would
// otherwise leave the ring open, and a caller blocked in waitFor with no
// deadline of its own would wait forever for a stream that already died.
// waitFor here carries its own bounded ctx only so a regression reports as
// a failed assertion instead of hanging the test run.
func TestRingClosesWithPanicErrorWhenReported(t *testing.T) {
	r := newRing(64)

	spawn.Go(context.Background(), "test drain", func() {
		panic("boom")
	}, spawn.ReportTo(r.closeWithErr))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := r.waitFor(ctx, func([]byte) (int, bool) { return 0, false })
	if err == nil {
		t.Fatal("waitFor returned a nil error after the drain goroutine panicked")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitFor timed out instead of observing the panic: %v", err)
	}
}
