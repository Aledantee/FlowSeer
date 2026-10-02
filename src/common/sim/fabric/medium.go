package fabric

import (
	"math"
	"time"
)

// Medium identifies the physical transmission medium of a network cable,
// determining its velocity factor and reach per link speed. The zero value is
// [MediumUnspecified].
type Medium string

const (
	// MediumUnspecified is a medium no source reported, as with an unresolved
	// transceiver. Its velocity factor and its reach at every speed are unknown.
	MediumUnspecified Medium = ""

	// TwistedPair represents copper twisted-pair cabling.
	TwistedPair Medium = "TwistedPair"

	// MultimodeFiber represents optical multimode fiber cabling.
	MultimodeFiber Medium = "MultimodeFiber"

	// SinglemodeFiber represents optical singlemode fiber cabling.
	SinglemodeFiber Medium = "SinglemodeFiber"

	// Twinax represents direct-attach copper twinaxial cabling.
	Twinax Medium = "Twinax"
)

// TypeID returns the fact type identifier for Medium.
func (m Medium) TypeID() string {
	return "fabric.medium"
}

// Canonical returns the string value of the medium, or "Unspecified" for
// [MediumUnspecified].
func (m Medium) Canonical() string {
	if m == MediumUnspecified {
		return "Unspecified"
	}

	return string(m)
}

// VelocityFactor returns the ratio of propagation speed through the medium
// relative to the speed of light in vacuum. It returns 0 for
// [MediumUnspecified] and for any value Validate refuses, whose factor is unknown.
func (m Medium) VelocityFactor() float64 {
	switch m {
	case TwistedPair:
		return 0.64
	case MultimodeFiber, SinglemodeFiber:
		return 0.67
	case Twinax:
		return 0.77
	default:
		return 0
	}
}

// ReachState is the outcome of comparing a cable length against a medium's
// reach at one speed. The zero value is [ReachUnknown].
type ReachState uint8

const (
	// ReachUnknown means the medium is unspecified or has no reach row for the speed.
	ReachUnknown ReachState = iota

	// ReachInRange means the length is at or below the medium's reach.
	ReachInRange

	// ReachExceeded means the length is above the medium's reach.
	ReachExceeded
)

// String returns the name of the reach state.
func (s ReachState) String() string {
	switch s {
	case ReachInRange:
		return "InRange"
	case ReachExceeded:
		return "Exceeded"
	default:
		return "Unknown"
	}
}

// Reach compares lengthMeters against the medium's reach at speedBPS and
// returns the state with the reach in meters. A stated medium with a row for
// the speed is [ReachInRange] when the length is at or below the row and
// [ReachExceeded] above it. [MediumUnspecified], or a medium with no row for
// the speed, is [ReachUnknown] with 0 meters, whatever the length.
func (m Medium) Reach(lengthMeters float64, speedBPS uint64) (ReachState, float64) {
	meters := m.reachMeters(speedBPS)
	if meters == 0 {
		return ReachUnknown, 0
	}
	if lengthMeters > meters {
		return ReachExceeded, meters
	}

	return ReachInRange, meters
}

func (m Medium) reachMeters(speedBPS uint64) float64 {
	switch m {
	case TwistedPair:
		switch speedBPS {
		case 10_000_000, 100_000_000, 1_000_000_000, 10_000_000_000:
			return 100
		}
	case MultimodeFiber:
		switch speedBPS {
		case 1_000_000_000:
			return 550
		case 10_000_000_000:
			return 300
		}
	case SinglemodeFiber:
		switch speedBPS {
		case 1_000_000_000:
			return 5000
		case 10_000_000_000:
			return 10000
		}
	case Twinax:
		if speedBPS == 10_000_000_000 {
			return 15
		}
	}

	return 0
}

// Propagation computes the physical signal propagation delay for a cable of the given
// length in meters and medium, rounded to the nearest nanosecond. It returns 0 for lengths <= 0
// and for a medium whose velocity factor is unknown; a fabric reports the latter as a
// propagation-unknown issue on a link that carries frames.
func Propagation(lengthMeters float64, m Medium) time.Duration {
	factor := m.VelocityFactor()
	if lengthMeters <= 0 || factor == 0 {
		return 0
	}

	const lightSpeedMPS = 299792458.0
	seconds := lengthMeters / (factor * lightSpeedMPS)
	nanoseconds := math.Round(seconds * 1e9)

	return time.Duration(nanoseconds) * time.Nanosecond
}
