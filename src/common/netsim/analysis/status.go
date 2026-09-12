package analysis

// InputValidity reports whether an input can be analyzed at all. It is separate
// from [Status], which describes how much trust to place in a completed analysis.
// The zero value is [InputValid]. InputValidity is safe for concurrent use.
type InputValidity uint8

const (
	// InputValid means the input satisfies the producer's construction rules.
	InputValid InputValidity = iota

	// InputInvalid means the producer must reject the input without an analysis result.
	InputInvalid
)

// String returns the stable lowercase name of the input validity.
func (v InputValidity) String() string {
	if v == InputValid {
		return "valid"
	}
	return "invalid"
}

// Status describes the trustworthiness of an analysis over a declared [Scope].
// Higher-valued constants take conservative precedence when statuses combine.
// The zero value is [Complete]. Status is safe for concurrent use.
type Status uint8

const (
	// Complete means the analysis covered its declared scope without a trust issue.
	Complete Status = iota

	// Incomplete means part of the declared scope could not be evaluated.
	Incomplete

	// Exhausted means an explicit analysis resource or search bound was reached.
	Exhausted

	// Unstable means the inputs or observations were not stable enough for a reliable result.
	Unstable

	// Unsupported means the analyzer cannot evaluate a required part of the declared scope.
	Unsupported
)

// String returns the stable lowercase name of the status. Unknown values render
// as unsupported so invalid producer input cannot overstate trust.
func (s Status) String() string {
	switch s {
	case Complete:
		return "complete"
	case Incomplete:
		return "incomplete"
	case Exhausted:
		return "exhausted"
	case Unstable:
		return "unstable"
	case Unsupported:
		return "unsupported"
	default:
		return "unsupported"
	}
}

func normalizeStatus(status Status) Status {
	if status > Unsupported {
		return Unsupported
	}
	return status
}
