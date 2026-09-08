package host

import (
	"testing"
	"time"
)

// The lane reads the time through whatever the deployment supplied, and
// through the wall clock when it supplied nothing.
//
// The default is asserted as a value rather than as "not nil". A clock() that
// returned nil would satisfy every "is a default set" check and then be read
// by the lane as its own nil default, which is the same wall clock — so the
// bug would be invisible until something else read it, and the assertion that
// caught nothing would look like the one that proved it.
func TestTheLaneClockIsSubstitutedOrTheWallClock(t *testing.T) {
	t.Parallel()

	fixed := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	substituted := (&assembly{opts: Options{Clock: func() time.Time { return fixed }}}).clock()
	if got := substituted(); !got.Equal(fixed) {
		t.Errorf("the substituted clock read %v, want %v", got, fixed)
	}

	before := time.Now()
	fallback := (&assembly{}).clock()
	if fallback == nil {
		t.Fatal("an agent given no clock got none; the lane would be reading a nil this package failed to resolve")
	}
	got := fallback()
	if got.Before(before) || got.After(time.Now()) {
		t.Errorf("the default clock read %v, want a reading between %v and now", got, before)
	}
}
