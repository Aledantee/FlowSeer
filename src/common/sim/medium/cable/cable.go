// Package cable resolves physical links and applies faults at cable crossings.
package cable

import (
	"cmp"
	"slices"
	"time"
)

// Cable holds the physical facts used to resolve and cross a two-ended link.
// A zero length states a zero-meter cable. Delay overrides propagation when set.
type Cable struct {
	LengthMeters float64
	TopSpeedBPS  uint64
	Fault        Fault
	Medium       Medium
	Delay        *time.Duration
}

// Clone copies the mutable fault sequence and delay.
func (c Cable) Clone() Cable {
	cp := c
	cp.Fault = c.Fault.Clone()
	if c.Delay != nil {
		d := *c.Delay
		cp.Delay = &d
	}
	return cp
}

// Equal compares the physical configuration, including ordered fault positions.
func (c Cable) Equal(other Cable) bool {
	if cmp.Compare(c.LengthMeters, other.LengthMeters) != 0 || c.TopSpeedBPS != other.TopSpeedBPS || c.Medium != other.Medium {
		return false
	}
	if (c.Delay == nil) != (other.Delay == nil) {
		return false
	}
	if c.Delay != nil && *c.Delay != *other.Delay {
		return false
	}
	return c.Fault.Kind == other.Fault.Kind && c.Fault.N == other.Fault.N && slices.Equal(c.Fault.Sequence, other.Fault.Sequence)
}

// Propagation returns the stated delay or the delay computed from length and medium.
func (c Cable) Propagation() time.Duration {
	if c.Delay != nil {
		return *c.Delay
	}
	return Propagation(c.LengthMeters, c.Medium)
}

// Crossing applies the fault at a one-based position shared by both directions.
// The caller counts every transmitted frame, including protocol frames.
func (c Cable) Crossing(count uint, aToB bool) (lost, corrupt bool) {
	switch c.Fault.Kind {
	case FaultLoseEveryNth:
		lost = c.Fault.N > 0 && count%c.Fault.N == 0
	case FaultLoseSequence:
		lost = slices.Contains(c.Fault.Sequence, count)
	case FaultCorruptEveryNth:
		corrupt = c.Fault.N > 0 && count%c.Fault.N == 0
	case FaultDeadAToB:
		lost = aToB
	case FaultDeadBToA:
		lost = !aToB
	}
	return lost, corrupt
}
