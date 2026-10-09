package cable

import (
	"go.aledante.io/FlowSeer/src/common/errs"
)

// FaultKind identifies the nature of a cable impairment, distinguishing physical defects
// from frame-level transmission losses and corruptions.
type FaultKind string

const (
	// FaultNone indicates an unimpaired, fully operational cable.
	FaultNone FaultKind = "None"

	// FaultCut models complete physical severance, disabling transmission in both directions.
	FaultCut FaultKind = "Cut"

	// FaultDeadAToB models unidirectional failure blocking transmission from endpoint A to endpoint B.
	FaultDeadAToB FaultKind = "DeadAToB"

	// FaultDeadBToA models unidirectional failure blocking transmission from endpoint B to endpoint A.
	FaultDeadBToA FaultKind = "DeadBToA"

	// FaultLoseEveryNth models periodic packet loss dropping every Nth transmitted frame.
	FaultLoseEveryNth FaultKind = "LoseEveryNth"

	// FaultLoseSequence models deterministic packet loss at designated 1-based frame positions.
	FaultLoseSequence FaultKind = "LoseSequence"

	// FaultCorruptEveryNth models periodic frame corruption marking every Nth transmitted frame damaged.
	FaultCorruptEveryNth FaultKind = "CorruptEveryNth"
)

// Fault specifies physical defects or frame-level loss and corruption policies applied to a cable.
type Fault struct {
	Kind     FaultKind
	N        uint
	Sequence []uint
}

// Clone returns an independent deep copy of the fault configuration.
func (f Fault) Clone() Fault {
	cp := f
	if len(f.Sequence) > 0 {
		cp.Sequence = make([]uint, len(f.Sequence))
		copy(cp.Sequence, f.Sequence)
	}

	return cp
}

// ValidateFault rejects missing active parameters and parameters the kind does not use.
func ValidateFault(f Fault) error {
	switch f.Kind {
	case "", FaultNone, FaultCut, FaultDeadAToB, FaultDeadBToA:
		if f.N != 0 || len(f.Sequence) != 0 {
			return errs.New().
				Attr("kind", f.Kind).
				Msg("fault parameters are set for a kind that does not use them")
		}
		return nil
	case FaultLoseEveryNth, FaultCorruptEveryNth:
		if f.N == 0 {
			return errs.New().Attr("kind", f.Kind).Msg("fault parameter N must be greater than zero")
		}
		if len(f.Sequence) != 0 {
			return errs.New().Attr("kind", f.Kind).Msg("fault sequence is set for an every-Nth kind")
		}

		return nil
	case FaultLoseSequence:
		if f.N != 0 {
			return errs.New().Attr("kind", f.Kind).Msg("fault parameter N is set for a sequence kind")
		}
		if len(f.Sequence) == 0 {
			return errs.New().Attr("kind", f.Kind).Msg("fault sequence cannot be empty")
		}
		for _, position := range f.Sequence {
			if position == 0 {
				return errs.New().Attr("kind", f.Kind).Msg("fault sequence positions must be greater than zero")
			}
		}

		return nil
	default:
		return errs.New().Attr("kind", f.Kind).Msgf("unsupported fault kind %q", f.Kind)
	}
}
