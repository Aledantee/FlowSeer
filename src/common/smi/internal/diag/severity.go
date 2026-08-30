package diag

import (
	"fmt"

	"go.aledante.io/FlowSeer/src/common/smi/internal/catalog"
)

// Severity is how badly a diagnostic reflects on the source, on libsmi's
// 0-6 scale. Zero is the most severe level and six the least, which is
// backwards from most severity types and is kept anyway: every MIB author
// who has run libsmi with -l already reads these numbers, and inventing a
// second ordering for the same conditions would cost more than it saves.
//
// The parser assigns a severity and stops there. It has no abort
// threshold, never stops early because a level was reached, and offers no
// knob to make it do so. Deciding that a given file's diagnostics are
// acceptable is the caller's judgment, recorded in the caller's baseline.
type Severity uint8

// The severity scale. Each level says what a caller should conclude, not
// how the parser behaves, because the parser behaves the same way at
// every level.
const (
	// SeverityInternal is a bug in this parser rather than a fault in the
	// source. A MIB cannot provoke one; if a corpus run reports one, the
	// parser is wrong.
	SeverityInternal Severity = 0

	// SeverityFatal is a condition that costs the whole file. These are
	// the only conditions that abandon a source: an unterminated string
	// or comment, a missing module header, and a resource limit. A file
	// graded fatal yields no usable module.
	SeverityFatal Severity = 1

	// SeverityError is a definition the RFCs forbid, in a file that is
	// otherwise usable. The affected declaration is dropped or partially
	// resolved; everything around it survives.
	SeverityError Severity = 2

	// SeverityMinor is a violation whose intent is unambiguous, so the
	// parser recovers the meaning and records that it had to.
	SeverityMinor Severity = 3

	// SeverityChange marks a definition that is legal but will not stay
	// legal, or that a later RFC re-spells. Nothing is lost today.
	SeverityChange Severity = 4

	// SeverityWarning marks a suspicious but legal construct: the kind of
	// thing worth a second look in a MIB under review and worth ignoring
	// wholesale in a vendor corpus.
	SeverityWarning Severity = 5

	// SeverityInfo is an observation about how the file was read, such as
	// which comment-termination mode produced the parse. It implies no
	// fault at all.
	SeverityInfo Severity = 6
)

// severityTags are the stable strings a severity renders and parses as.
// They are indexed by the level, so the array order is the scale order
// and must not be shuffled. These strings reach committed baseline files,
// which makes them as append-only as the codes are.
var severityTags = [...]string{
	SeverityInternal: "internal",
	SeverityFatal:    "fatal",
	SeverityError:    "error",
	SeverityMinor:    "minor",
	SeverityChange:   "change",
	SeverityWarning:  "warning",
	SeverityInfo:     "info",
}

// Severities returns the scale from most to least severe. A corpus
// snapshot groups by severity and wants every level present in a fixed
// order, including the levels that drew no diagnostics, so that the
// snapshot's shape does not change with its content.
func Severities() []Severity {
	out := make([]Severity, 0, len(severityTags))
	for level := range severityTags {
		out = append(out, Severity(level))
	}

	return out
}

// Valid reports whether s is on the scale.
func (s Severity) Valid() bool {
	return int(s) < len(severityTags)
}

// String returns the severity's stable tag, such as "error". An off-scale
// value renders as "severity(N)" rather than panicking, because rendering
// a diagnostic must never be the thing that fails.
func (s Severity) String() string {
	if !s.Valid() {
		return fmt.Sprintf("severity(%d)", uint8(s))
	}

	return severityTags[s]
}

// ParseSeverity returns the severity written as tag, which is the inverse
// of [Severity.String]. It exists so a baseline file committed by one
// release is still readable by the next.
func ParseSeverity(tag string) (Severity, error) {
	for level, name := range severityTags {
		if name == tag {
			return Severity(level), nil
		}
	}

	return 0, fmt.Errorf("smi: %q is not a severity", tag)
}

// NeedsBaselineReason reports whether accepting this severity in a
// baseline has to be justified in writing. Fatal and error mean a MIB is
// unusable or a definition is lost, so waving one through is a decision
// somebody should have to defend; the lighter levels are noise a vendor
// corpus produces by the thousand and are accepted silently.
func (s Severity) NeedsBaselineReason() bool {
	return s <= SeverityError
}

// The scale here and the bound the catalog validates rows against are
// the same scale, so a change to one that misses the other fails to
// compile rather than letting a row through with no constant to name it.
var _ [catalog.MaxSeverity + 1]struct{} = [len(severityTags)]struct{}{}
