package ssh

import (
	"bytes"
	"testing"
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
