package fabric

import (
	"slices"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// TraceLocation names the first entry whose trace facts differ in the original
// reports. A missing journey has index -1. EntryIndex is an original entry index
// on the current side, or on the expected side when the current side has no such entry.
// Treat Lineage as immutable during concurrent reads.
type TraceLocation struct {
	Injection       InjectionLocation
	Lineage         []LineageStep
	CurrentJourney  int
	ExpectedJourney int
	EntryIndex      int
}

// Alignment carries the behavioral target and an optional trace location.
// Trace can name a different journey than Location.
// Treat contained slices and pointers as immutable during concurrent reads.
type Alignment struct {
	Location DifferenceLocation
	Trace    *TraceLocation
}

// Align always retains a Different comparison's behavioral location. Inconclusive
// comparisons can name a diagnostic trace without asserting behavioral difference.
// Trace follows pairJourneys order: injection ordinal, with one-sided journeys
// before paired journeys within each injection. Entries compare at their original
// positions, so a queue diagnostic present on only one side differs at its own index.
func Align(comparison Comparison) (Alignment, bool) {
	if comparison.Disposition == analysis.Equivalent {
		return Alignment{}, false
	}
	result := Alignment{}
	if comparison.Disposition == analysis.Different {
		result.Location = comparison.Location
	}
	roots := comparison.roots
	if roots[0] == nil && roots[1] == nil {
		for side, journeys := range [][]Journey{comparison.Current, comparison.Expected} {
			for _, j := range journeys {
				if j.Origin.Kind == OriginInjection && !j.Protocol {
					roots[side] = append(roots[side], j.FrameID)
				}
			}
		}
	}
	for _, pair := range pairJourneys(comparison.Replay[0].Scenario.Actions, roots[0], roots[1], comparison.Current, comparison.Expected) {
		location := TraceLocation{Injection: *pair.location.Injection, Lineage: slices.Clone(pair.location.Lineage), CurrentJourney: pair.current, ExpectedJourney: pair.expected}
		if pair.current < 0 || pair.expected < 0 {
			result.Trace = &location
			return result, true
		}
		a, b := comparison.Current[pair.current].Entries, comparison.Expected[pair.expected].Entries
		for i := 0; i < min(len(a), len(b)); i++ {
			if !sameEntryTraceFacts(a[i], b[i]) {
				location.EntryIndex = i
				result.Trace = &location
				return result, true
			}
		}
		if len(a) != len(b) {
			location.EntryIndex = min(len(a), len(b))
			result.Trace = &location
			return result, true
		}
	}
	return result, comparison.Disposition == analysis.Different
}

func sameEntryTraceFacts(a, b Entry) bool {
	if a.Kind != b.Kind || a.Device != b.Device || a.Port != b.Port || a.Reason != b.Reason {
		return false
	}
	stepsA, stepsB := collectEntrySteps(a), collectEntrySteps(b)
	if len(stepsA) != len(stepsB) {
		return false
	}
	for i := range stepsA {
		if !trace.EqualStep(stepsA[i], stepsB[i]) {
			return false
		}
	}
	return true
}

func collectEntrySteps(e Entry) []trace.Step {
	var out []trace.Step
	if e.Step != nil {
		out = append(out, *e.Step)
	}
	if e.Result != nil {
		out = append(out, e.Result.Steps...)
	}
	return out
}
