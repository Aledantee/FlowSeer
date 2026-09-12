package trace

import (
	"fmt"
	"strings"
)

// Render formats a [Trace] as deterministic human-readable text.
// The output lists numbered steps followed by the outcome and optional reason.
// An empty trace renders as "Outcome: unspecified".
func Render(t Trace) string {
	var lines []string

	for i, s := range t.Steps {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, RenderStep(s)))
	}

	outcome := string(t.Outcome)
	if outcome == "" {
		outcome = "unspecified"
	}

	if t.Reason != "" {
		lines = append(lines, fmt.Sprintf("Outcome: %s (%s)", outcome, t.Reason))
	} else {
		lines = append(lines, fmt.Sprintf("Outcome: %s", outcome))
	}

	return strings.Join(lines, "\n")
}

// RenderStep formats a [Step] as deterministic human-readable text.
// The step is canonicalized before formatting so facts and evidence appear in stable order.
// A zero-value step renders as "[unspecified]".
func RenderStep(s Step) string {
	canon := s.Canonical()

	isZero := canon.Layer == "" && canon.Op == "" && canon.RuleID == "" &&
		canon.Subject.Kind == "" && canon.Subject.Key == "" &&
		len(canon.Inputs) == 0 && len(canon.Outputs) == 0 && len(canon.Evidence) == 0
	if isZero {
		return "[unspecified]"
	}

	var header string
	switch {
	case canon.Layer != "" && canon.Op != "":
		header = fmt.Sprintf("[%s:%s]", canon.Layer, canon.Op)
	case canon.Layer != "":
		header = fmt.Sprintf("[%s]", canon.Layer)
	case canon.Op != "":
		header = fmt.Sprintf("[%s]", canon.Op)
	default:
		header = "[-]"
	}

	parts := []string{header}

	if canon.RuleID != "" {
		parts = append(parts, fmt.Sprintf("rule=%s", canon.RuleID))
	}
	if canon.Subject.Kind != "" || canon.Subject.Key != "" {
		parts = append(parts, fmt.Sprintf("subject=%s", canon.Subject.String()))
	}
	if len(canon.Inputs) > 0 {
		var facts []string
		for _, f := range canon.Inputs {
			facts = append(facts, renderStepFact(f))
		}
		parts = append(parts, fmt.Sprintf("in=[%s]", strings.Join(facts, ", ")))
	}
	if len(canon.Outputs) > 0 {
		var facts []string
		for _, f := range canon.Outputs {
			facts = append(facts, renderStepFact(f))
		}
		parts = append(parts, fmt.Sprintf("out=[%s]", strings.Join(facts, ", ")))
	}
	if len(canon.Evidence) > 0 {
		var evs []string
		for _, e := range canon.Evidence {
			evs = append(evs, string(e))
		}
		parts = append(parts, fmt.Sprintf("evidence=[%s]", strings.Join(evs, ", ")))
	}

	return strings.Join(parts, " ")
}

// RenderChange formats a [Change] as deterministic human-readable text.
// A zero-value change renders as "[unspecified]".
func RenderChange(c Change) string {
	canon := c.Canonical()

	isZero := canon.Layer == "" && canon.Field == "" &&
		canon.Subject.Kind == "" && canon.Subject.Key == "" &&
		canon.From == nil && canon.To == nil && len(canon.Evidence) == 0
	if isZero {
		return "[unspecified]"
	}

	layer := string(canon.Layer)
	if layer == "" {
		layer = "-"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s]", layer)

	if subj := canon.Subject.String(); subj != "" {
		sb.WriteString(" ")
		sb.WriteString(subj)
	}

	if canon.Field != "" {
		sb.WriteString(" ")
		sb.WriteString(canon.Field)
		sb.WriteString(":")
	}

	fmt.Fprintf(&sb, " %s -> %s", renderChangeFact(canon.From), renderChangeFact(canon.To))

	if len(canon.Evidence) > 0 {
		var evs []string
		for _, e := range canon.Evidence {
			evs = append(evs, string(e))
		}
		fmt.Fprintf(&sb, " (evidence: %s)", strings.Join(evs, ", "))
	}

	return sb.String()
}

// RenderChanges formats a slice of [Change] records as newline-delimited text.
func RenderChanges(changes []Change) string {
	if len(changes) == 0 {
		return ""
	}
	lines := make([]string, len(changes))
	for i, c := range changes {
		lines[i] = RenderChange(c)
	}
	return strings.Join(lines, "\n")
}

func renderStepFact(f Fact) string {
	if f == nil {
		return "<nil>"
	}
	if f.TypeID() == "" {
		return f.Canonical()
	}
	return f.TypeID() + "=" + f.Canonical()
}

func renderChangeFact(f Fact) string {
	if f == nil {
		return "<nil>"
	}
	if f.Canonical() != "" {
		return f.Canonical()
	}
	return f.TypeID()
}
