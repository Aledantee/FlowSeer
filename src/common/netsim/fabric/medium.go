package fabric

import (
	"math"
	"time"
)

// Medium identifies the physical transmission medium of a network cable,
// determining its velocity factor and reach per link speed.
// An empty medium behaves as [TwistedPair].
type Medium string

const (
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

// Canonical returns the string value of the medium.
func (m Medium) Canonical() string {
	return string(m)
}

// VelocityFactor returns the ratio of propagation speed through the medium
// relative to the speed of light in vacuum. An empty medium is [TwistedPair];
// so is any value Validate refuses.
func (m Medium) VelocityFactor() float64 {
	switch m {
	case MultimodeFiber, SinglemodeFiber:
		return 0.67
	case Twinax:
		return 0.77
	default:
		return 0.64
	}
}

// Reach returns the maximum reach in meters supported by the medium at the given link speed
// in bits per second, or 0 when the medium has no specification row for that speed.
// An empty medium behaves as [TwistedPair].
func (m Medium) Reach(speedBPS uint64) float64 {
	switch m {
	case "", TwistedPair:
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
// length in meters and medium, rounded to the nearest nanosecond. It returns 0 for lengths <= 0.
func Propagation(lengthMeters float64, m Medium) time.Duration {
	if lengthMeters <= 0 {
		return 0
	}

	const lightSpeedMPS = 299792458.0
	seconds := lengthMeters / (m.VelocityFactor() * lightSpeedMPS)
	nanoseconds := math.Round(seconds * 1e9)

	return time.Duration(nanoseconds) * time.Nanosecond
}
