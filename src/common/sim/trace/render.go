package trace

import (
	"fmt"
	"strconv"
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

	switch {
	case t.Outcome == "":
		lines = append(lines, "Outcome: unspecified")
	case t.Reason != "":
		lines = append(lines, "Outcome: "+strconv.Quote(string(t.Outcome))+" reason="+strconv.Quote(string(t.Reason)))
	default:
		lines = append(lines, "Outcome: "+strconv.Quote(string(t.Outcome)))
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

	var headerParts []string
	if canon.Layer != "" {
		headerParts = append(headerParts, "layer="+strconv.Quote(string(canon.Layer)))
	}
	if canon.Op != "" {
		headerParts = append(headerParts, "op="+strconv.Quote(string(canon.Op)))
	}
	header := "[-]"
	if len(headerParts) > 0 {
		header = "[" + strings.Join(headerParts, " ") + "]"
	}

	parts := []string{header}

	if canon.RuleID != "" {
		parts = append(parts, "rule="+strconv.Quote(string(canon.RuleID)))
	}
	if canon.Subject.Kind != "" || canon.Subject.Key != "" {
		parts = append(parts,
			"subject.kind="+strconv.Quote(canon.Subject.Kind),
			"subject.key="+strconv.Quote(canon.Subject.Key),
		)
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
			evs = append(evs, strconv.Quote(string(e)))
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

	parts := []string{"[-]"}
	if canon.Layer != "" {
		parts[0] = "[layer=" + strconv.Quote(string(canon.Layer)) + "]"
	}
	if canon.Subject.Kind != "" || canon.Subject.Key != "" {
		parts = append(parts,
			"subject.kind="+strconv.Quote(canon.Subject.Kind),
			"subject.key="+strconv.Quote(canon.Subject.Key),
		)
	}
	if canon.Field != "" {
		parts = append(parts, "field="+strconv.Quote(canon.Field))
	}
	parts = append(parts, "from="+renderFact(canon.From), "to="+renderFact(canon.To))

	if len(canon.Evidence) > 0 {
		var evs []string
		for _, e := range canon.Evidence {
			evs = append(evs, strconv.Quote(string(e)))
		}
		parts = append(parts, fmt.Sprintf("evidence=[%s]", strings.Join(evs, ", ")))
	}

	return strings.Join(parts, " ")
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
	return renderFact(f)
}

func renderFact(f Fact) string {
	if f == nil {
		return "<nil>"
	}

	return "{type=" + strconv.Quote(f.TypeID()) + " value=" + strconv.Quote(f.Canonical()) + "}"
}
