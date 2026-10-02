package analysis

import (
	"go.aledante.io/FlowSeer/src/common/errs"
)

// Disposition represents the honest verdict of an exact behavioral comparison.
// It has no valid zero value: an uncompared or uninitialized result is not a disposition.
// Disposition is immutable and safe for concurrent use.
type Disposition string

const (
	// Equivalent means both evaluated results have [Complete] status and match across
	// all behavioral observables.
	Equivalent Disposition = "equivalent"

	// Different means a behavioral observable mismatch was proven between the results.
	Different Disposition = "different"

	// Inconclusive means equivalence could not be proven because at least one result had
	// non-Complete status (Incomplete, Exhausted, Unstable, or Unsupported) or stopped with
	// pending work.
	Inconclusive Disposition = "inconclusive"
)

// Validate returns an error if d is not one of the valid non-empty disposition constants.
func (d Disposition) Validate() error {
	switch d {
	case Equivalent, Different, Inconclusive:
		return nil
	case "":
		return errs.Msg("analysis: disposition zero value is invalid")
	default:
		return errs.Msgf("analysis: unknown disposition %q", string(d))
	}
}

// String returns the string representation of the disposition.
func (d Disposition) String() string {
	return string(d)
}
