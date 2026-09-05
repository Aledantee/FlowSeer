package testtest

import "testing"

// AssertBytesEqual reports, through t, the first length or byte difference
// between got and want, labeling the failure with label. Behavior suites use
// it to compare a recorded TX frame against its fixture-mirrored expectation.
// It marks itself a test helper so failures point at the caller.
func AssertBytesEqual(t *testing.T, label string, got, want []byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: length mismatch: got %d, want %d", label, len(got), len(want))
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s: byte %d: got 0x%02x, want 0x%02x", label, i, got[i], want[i])
			return
		}
	}
}
